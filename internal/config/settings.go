package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/leeovery/kit/internal/apps"
)

// The settings of kit.toml's own, before its tables, that kit's commands
// set.
const (
	TerminalSetting  = "terminal"
	NightlyAtSetting = "nightly_at"
)

// ParseTimeOfDay reads a time of day, as in 03:00, as hours and minutes
// since midnight.
func ParseTimeOfDay(s string) (time.Duration, error) {
	at, err := time.Parse("15:04", s)
	if err != nil {
		return 0, fmt.Errorf("%q isn't a time of day, as in 03:00", s)
	}
	return time.Duration(at.Hour())*time.Hour + time.Duration(at.Minute())*time.Minute, nil
}

// TimeOfDay says d, hours and minutes since midnight, as in 03:00.
func TimeOfDay(d time.Duration) string {
	return fmt.Sprintf("%02d:%02d", int(d/time.Hour), int(d%time.Hour/time.Minute))
}

// NightlyAtSet reports whether kit.toml sets nightly_at: else NightlyAt is
// DefaultNightlyAt.
func (c *Config) NightlyAtSet() bool { return c.nightlyAtSet }

// SetTerminal names the terminal called name, one of apps.Terminals, in
// kit.toml, as the one a new Mac's boot hands over to: "" takes the setting
// out. The Config as loaded is left as it is, as AddMac leaves it.
func (c *Config) SetTerminal(name string) error {
	if _, ok := apps.Terminals[name]; name != "" && !ok {
		return fmt.Errorf("kit doesn't know the terminal %s: one of %s", name, apps.Names(apps.Terminals))
	}
	return c.setOwn(TerminalSetting, name, func(edited *Config) bool { return edited.Terminal == name })
}

// SetNightlyAt sets kit.toml's nightly_at to at, a time of day as in 03:00,
// written as TimeOfDay says it. The Config as loaded is left as it is.
func (c *Config) SetNightlyAt(at string) error {
	d, err := ParseTimeOfDay(at)
	if err != nil {
		return err
	}
	return c.setOwn(NightlyAtSetting, TimeOfDay(d), func(edited *Config) bool { return edited.nightlyAtSet && edited.NightlyAt == d })
}

// setOwn sets kit.toml's own setting key to value, "" taking it out, and
// writes the file whole, once the edit reads back as Load reads it, and
// says reports the setting as wanted.
func (c *Config) setOwn(key, value string, says func(*Config) bool) error {
	path := filepath.Join(c.Dir, File)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read the config: %w", err)
	}
	edited, err := withOwn(string(data), key, value)
	if err != nil {
		return err
	}
	cfg, err := read(c.Dir, edited, nil)
	switch {
	case err != nil:
		return fmt.Errorf("couldn't set %s in %s: %w", key, File, err)
	case !says(cfg):
		return fmt.Errorf("couldn't set %s in %s: its line isn't one kit can write to", key, File)
	}
	if err := writeAtomic(path, []byte(edited)); err != nil {
		return fmt.Errorf("set %s: %w", key, err)
	}
	return nil
}

// withOwn is kit.toml's text with its own setting key set to value, "" for
// none: the setting's line replaced where it stands, its indent, comment and
// line ending kept, else one added after the last setting before the first
// table, ending as the lines before it do; the rest as it was.
func withOwn(text, key, value string) (string, error) {
	lines := strings.SplitAfter(text, "\n")
	end := tableEnd(lines, -1)
	at := slices.IndexFunc(lines[:end], func(line string) bool { return sets(line, key) })
	var encoded bytes.Buffer
	if err := toml.NewEncoder(&encoded).Encode(map[string]string{key: value}); err != nil {
		return "", fmt.Errorf("write %s: %w", key, err)
	}
	setting := strings.TrimSuffix(encoded.String(), "\n")
	switch {
	case value == "" && at >= 0:
		lines = slices.Delete(lines, at, at+1)
	case value == "":
	case at >= 0:
		lines[at] = replaced(lines[at], setting)
	default:
		eol := ending(lines[:end])
		if end > 0 && !strings.HasSuffix(lines[end-1], "\n") {
			lines[end-1] += eol
		}
		lines = slices.Insert(lines, end, setting+eol)
	}
	return strings.Join(lines, ""), nil
}

// replaced is was, a setting's line, saying setting instead: its indent,
// the comment that ends it and its line ending kept.
func replaced(was, setting string) string {
	body := strings.TrimRight(was, "\r\n")
	indent := body[:len(body)-len(strings.TrimLeft(body, " \t"))]
	return indent + setting + comment(body) + was[len(body):]
}

// comment is the comment that ends line, a setting's, with the space before
// it: "" for none. A # inside the value is no comment's.
func comment(line string) string {
	for i := strings.IndexByte(line, '#'); i >= 0; i = nextHash(line, i) {
		if _, err := toml.Decode(line[:i], new(map[string]any)); err == nil {
			return line[len(strings.TrimRight(line[:i], " \t")):]
		}
	}
	return ""
}

// nextHash is where the # after the one at i is in line: -1 for none.
func nextHash(line string, i int) int {
	next := strings.IndexByte(line[i+1:], '#')
	if next < 0 {
		return -1
	}
	return i + 1 + next
}

// ending is the line ending of the last of lines that has one: "\n" when
// none has.
func ending(lines []string) string {
	for _, line := range slices.Backward(lines) {
		if strings.HasSuffix(line, "\r\n") {
			return "\r\n"
		}
		if strings.HasSuffix(line, "\n") {
			return "\n"
		}
	}
	return "\n"
}
