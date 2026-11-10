package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// AddMac adds a Mac named name to the repository: its folder, a copy of
// from's when from isn't "", else a folder with an empty declarations file,
// and its table in kit.toml, after the last Mac's, with from and
// description, if any. It refuses a name that can't be a Mac's or that the
// config knows, a from it doesn't, and a folder there already.
func (c *Config) AddMac(name, from, description string) error {
	switch {
	case !ValidName(name):
		return fmt.Errorf("%q can't be a Mac's name: use lower-case letters, digits and hyphens", name)
	case name == Shared:
		return fmt.Errorf("a Mac can't be called %s: that's the file every Mac reads", Shared)
	case c.Knows(name):
		return fmt.Errorf("%s knows a Mac named %s already", File, name)
	case from != "" && !c.Knows(from):
		return fmt.Errorf("no Mac named %s in %s: one of %s", from, File, c.macList())
	}
	dir := filepath.Join(c.Dir, name)
	if _, err := os.Lstat(dir); !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%s is there already: remove it, or name the Mac otherwise", name)
	}
	data, err := os.ReadFile(filepath.Join(c.Dir, File))
	if err != nil {
		return fmt.Errorf("read the config: %w", err)
	}
	edited, err := withMac(string(data), name, from, description)
	if err != nil {
		return err
	}
	if from != "" {
		err = copyTree(filepath.Join(c.Dir, from), dir)
	} else if err = os.Mkdir(dir, 0o755); err == nil {
		err = os.WriteFile(filepath.Join(c.Dir, DeclFile(name)), nil, 0o644)
	}
	if err == nil {
		err = os.WriteFile(filepath.Join(c.Dir, File), []byte(edited), 0o644)
	}
	if err != nil {
		return errors.Join(fmt.Errorf("add %s: %w", name, err), os.RemoveAll(dir))
	}
	return nil
}

// withMac is kit.toml's text with a table for the Mac named name after the
// last Mac's, before any blank lines and comments that end it, the rest as
// it was.
func withMac(text, name, from, description string) (string, error) {
	lines := strings.SplitAfter(text, "\n")
	last := -1
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "[macs.") {
			last = i
		}
	}
	if last < 0 {
		return "", fmt.Errorf("%s has no [macs.<name>] table to add %s after", File, name)
	}
	end := tableEnd(lines, last)
	var table bytes.Buffer
	if !strings.HasSuffix(lines[end-1], "\n") {
		table.WriteString("\n")
	}
	table.WriteString("\n[macs." + name + "]\n")
	if err := toml.NewEncoder(&table).Encode(macFile{Description: description, From: from}); err != nil {
		return "", fmt.Errorf("write %s's description: %w", name, err)
	}
	return strings.Join(lines[:end], "") + table.String() + strings.Join(lines[end:], ""), nil
}

// tableEnd is where the table whose header is lines[header] ends: after
// its last setting, the blank lines and comments before the next table's
// header left out.
func tableEnd(lines []string, header int) int {
	end := header + 1
	for end < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[end]), "[") {
		end++
	}
	for end > header+1 && (strings.TrimSpace(lines[end-1]) == "" || strings.HasPrefix(strings.TrimSpace(lines[end-1]), "#")) {
		end--
	}
	return end
}

// SetHostKey records key as the SSH host key of the Mac named name, in its
// table in kit.toml: its host_key set where it is, else added after the
// table's last setting, the rest as it was. The Config as loaded is left as
// it is, as AddMac leaves it.
func (c *Config) SetHostKey(name, key string) error {
	if !c.Knows(name) {
		return fmt.Errorf("no Mac named %s in %s: one of %s", name, File, c.macList())
	}
	path := filepath.Join(c.Dir, File)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read the config: %w", err)
	}
	edited, err := withHostKey(string(data), name, key)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		return fmt.Errorf("record %s's host key: %w", name, err)
	}
	return nil
}

// withHostKey is kit.toml's text with key as the host_key of the Mac named
// name, read back to be sure that's what it says.
func withHostKey(text, name, key string) (string, error) {
	lines := strings.SplitAfter(text, "\n")
	header := slices.IndexFunc(lines, func(line string) bool { return tableName(line) == "macs."+name })
	if header < 0 {
		return "", fmt.Errorf("%s has no [macs.%s] table to record its host key in", File, name)
	}
	var setting bytes.Buffer
	if err := toml.NewEncoder(&setting).Encode(macFile{HostKey: key}); err != nil {
		return "", fmt.Errorf("write %s's host key: %w", name, err)
	}
	end := tableEnd(lines, header)
	if at := slices.IndexFunc(lines[header+1:end], isHostKey); at >= 0 {
		lines[header+1+at] = setting.String()
	} else {
		if !strings.HasSuffix(lines[end-1], "\n") {
			lines[end-1] += "\n"
		}
		lines = slices.Insert(lines, end, setting.String())
	}
	edited := strings.Join(lines, "")
	var f file
	if _, err := toml.Decode(edited, &f); err != nil {
		return "", fmt.Errorf("couldn't record %s's host key in %s: %w", name, File, err)
	}
	if f.Macs[name].HostKey != key {
		return "", fmt.Errorf("couldn't record %s's host key in %s: its [macs.%s] table isn't one kit can write to", name, File, name)
	}
	return edited, nil
}

// tableName is the name of the table whose header line is, as macs.laptop
// is [macs.laptop]'s: "" for a line that's no table's header.
func tableName(line string) string {
	text, _, _ := strings.Cut(line, "#")
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "[") || strings.HasPrefix(text, "[[") || !strings.HasSuffix(text, "]") {
		return ""
	}
	return strings.TrimSpace(text[1 : len(text)-1])
}

// isHostKey reports whether line sets host_key.
func isHostKey(line string) bool {
	key, _, ok := strings.Cut(line, "=")
	return ok && strings.TrimSpace(key) == "host_key"
}

// copyTree copies the folder from to to, which isn't there: every file as
// it is, its permissions kept, and links as links.
func copyTree(from, to string) error {
	return filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		target := filepath.Join(to, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.Mkdir(target, info.Mode().Perm())
		case info.Mode()&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case info.Mode().IsRegular():
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if err := os.WriteFile(target, data, info.Mode().Perm()); err != nil {
				return err
			}
			// WriteFile's mode is cut by the umask: the copy's is the original's.
			return os.Chmod(target, info.Mode().Perm())
		}
		return fmt.Errorf("%s isn't a file, a folder or a link", path)
	})
}
