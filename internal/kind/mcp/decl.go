package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Server is an MCP server as kit declares it, or finds it installed.
type Server struct {
	// Folder is the project folder whose server it is, as written, as in
	// ~/Code/site: "" for a user-level one.
	Folder string
	Name   string
	// Definition is what claude mcp add-json takes.
	Definition map[string]any
	// Note says why it's declared.
	Note string
	// Off is whether it's declared, but not to be installed.
	Off bool
	// Line is the line of its file declaring it, when known.
	Line int
}

// ID is what kit calls the server: its name, or, for a project's, the
// folder and the name, as in ~/Code/site:resend.
func (s Server) ID() string {
	if s.Folder == "" {
		return s.Name
	}
	return s.Folder + ":" + s.Name
}

// splitID splits a server's id into its folder, "" for a user-level
// server, and its name.
func splitID(id string) (folder, name string) {
	if isFolder(id) {
		if i := strings.LastIndex(id, ":"); i > 0 {
			return id[:i], id[i+1:]
		}
	}
	return "", id
}

// isFolder reports whether a key of a declarations file names a project
// folder rather than a server.
func isFolder(key string) bool {
	return strings.HasPrefix(key, "~/") || strings.HasPrefix(key, "/")
}

// FileName is the declarations file for the Mac named mac: "" names the
// file every Mac reads.
func FileName(mac string) string {
	if mac == "" {
		return "mcp.json"
	}
	return "mcp." + mac + ".json"
}

// readFile reads the servers a declarations file declares, in order of
// their ids: none when there's no file. A server holding a key in plain
// text is refused.
func readFile(path string) ([]Server, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	servers, err := parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return servers, nil
}

// parse reads a declarations file's servers: each key a server's name and
// definition, or a project folder's, holding that project's servers.
func parse(data []byte) ([]Server, error) {
	var top map[string]json.RawMessage
	if err := decode(data, &top); err != nil {
		return nil, err
	}
	lines := keyLines(data)
	var servers []Server
	for _, key := range slices.Sorted(maps.Keys(top)) {
		if strings.HasPrefix(key, "_") {
			continue
		}
		if !isFolder(key) {
			s, err := server("", key, top[key])
			if err != nil {
				return nil, err
			}
			s.Line = lines[key]
			servers = append(servers, s)
			continue
		}
		var project map[string]json.RawMessage
		if err := decode(top[key], &project); err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		for _, name := range slices.Sorted(maps.Keys(project)) {
			s, err := server(key, name, project[name])
			if err != nil {
				return nil, err
			}
			s.Line = lines[s.ID()]
			servers = append(servers, s)
		}
	}
	return servers, nil
}

// server reads one server's declaration: its definition, and kit's own
// keys, _note and _enabled.
func server(folder, name string, raw json.RawMessage) (Server, error) {
	s := Server{Folder: folder, Name: name}
	var def map[string]any
	if err := decode(raw, &def); err != nil {
		return s, fmt.Errorf("%s: %w", s.ID(), err)
	}
	if note, ok := def["_note"]; ok {
		if s.Note, ok = note.(string); !ok {
			return s, fmt.Errorf("%s: _note isn't text", s.ID())
		}
	}
	if enabled, ok := def["_enabled"]; ok {
		on, ok := enabled.(bool)
		if !ok {
			return s, fmt.Errorf("%s: _enabled isn't true or false", s.ID())
		}
		s.Off = !on
	}
	s.Definition = definition(def)
	if s.Definition["command"] == nil && s.Definition["url"] == nil {
		return s, fmt.Errorf("%s: a definition needs a command or a url", s.ID())
	}
	if plain := PlainKeys(s.Definition); len(plain) > 0 {
		return s, fmt.Errorf("%s: %s holds a key in plain text: put the key in 1Password, and name it here as ${VAR}", s.ID(), strings.Join(plain, " and "))
	}
	return s, nil
}

// definition is def without kit's own keys, which start with _.
func definition(def map[string]any) map[string]any {
	out := make(map[string]any, len(def))
	for k, v := range def {
		if !strings.HasPrefix(k, "_") {
			out[k] = v
		}
	}
	return out
}

// decode decodes JSON data into v, numbers kept as they're written, and
// nothing after the value.
func decode(data []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := d.Decode(v); err != nil {
		return fmt.Errorf("not JSON kit reads: %w", err)
	}
	if d.More() {
		return errors.New("not JSON kit reads: something after the object")
	}
	return nil
}

var (
	topKey    = regexp.MustCompile(`^  "([^"]+)": `)
	nestedKey = regexp.MustCompile(`^    "([^"]+)": `)
)

// keyLines finds the line each server is declared at, by id, in a file as
// kit writes them: a server's key indented two spaces, or a project's
// server's four, inside its folder's.
func keyLines(data []byte) map[string]int {
	lines := make(map[string]int)
	top := ""
	for i, line := range strings.Split(string(data), "\n") {
		if m := topKey.FindStringSubmatch(line); m != nil {
			top = m[1]
			if !isFolder(top) {
				lines[top] = i + 1
			}
			continue
		}
		if m := nestedKey.FindStringSubmatch(line); m != nil && isFolder(top) {
			lines[top+":"+m[1]] = i + 1
		}
	}
	return lines
}

// writeFile writes servers as the declarations file at path, whole, or not
// at all: keys sorted, two spaces' indent, a server's own keys first. The
// file keeps its mode.
func writeFile(path string, servers []Server) error {
	top := make(map[string]any)
	for _, s := range servers {
		entry := maps.Clone(s.Definition)
		if s.Note != "" {
			entry["_note"] = s.Note
		}
		if s.Off {
			entry["_enabled"] = false
		}
		if s.Folder == "" {
			top[s.Name] = entry
			continue
		}
		project, _ := top[s.Folder].(map[string]any)
		if project == nil {
			project = make(map[string]any)
			top[s.Folder] = project
		}
		project[s.Name] = entry
	}
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	e.SetIndent("", "  ")
	if err := e.Encode(top); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if _, err := parse(b.Bytes()); err != nil {
		return fmt.Errorf("write %s: it wouldn't read back: %w", filepath.Base(path), err)
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(b.Bytes()); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	return nil
}

// Declare declares s in the declarations file named file in dir, replacing
// a declaration of its id there.
func Declare(dir, file string, s Server) error {
	return edit(dir, file, func(servers []Server) ([]Server, error) {
		servers = slices.DeleteFunc(servers, func(d Server) bool { return d.ID() == s.ID() })
		return append(servers, s), nil
	})
}

// Undeclare takes the server id out of the declarations file named file in
// dir.
func Undeclare(dir, file, id string) error {
	return edit(dir, file, func(servers []Server) ([]Server, error) {
		before := len(servers)
		servers = slices.DeleteFunc(servers, func(d Server) bool { return d.ID() == id })
		if len(servers) == before {
			return nil, fmt.Errorf("%s doesn't declare %s", file, id)
		}
		return servers, nil
	})
}

// SetOff declares the server id off, or on, in the declarations file named
// file in dir.
func SetOff(dir, file, id string, off bool) error {
	return edit(dir, file, func(servers []Server) ([]Server, error) {
		i := slices.IndexFunc(servers, func(d Server) bool { return d.ID() == id })
		if i < 0 {
			return nil, fmt.Errorf("%s doesn't declare %s", file, id)
		}
		servers[i].Off = off
		return servers, nil
	})
}

// edit changes the declarations file named file in dir with change, and
// writes it, whole.
func edit(dir, file string, change func([]Server) ([]Server, error)) error {
	path := filepath.Join(dir, file)
	servers, err := readFile(path)
	if err != nil {
		return err
	}
	if servers, err = change(servers); err != nil {
		return err
	}
	return writeFile(path, servers)
}
