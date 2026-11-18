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
	terminalSetting  = "terminal"
	nightlyAtSetting = "nightly_at"
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
	return c.setOwn(terminalSetting, name)
}

// SetNightlyAt sets kit.toml's nightly_at to at, a time of day as in 03:00,
// written as TimeOfDay says it. The Config as loaded is left as it is.
func (c *Config) SetNightlyAt(at string) error {
	d, err := ParseTimeOfDay(at)
	if err != nil {
		return err
	}
	return c.setOwn(nightlyAtSetting, TimeOfDay(d))
}

// setOwn sets kit.toml's own setting key to value, "" taking it out, and
// writes the file whole.
func (c *Config) setOwn(key, value string) error {
	path := filepath.Join(c.Dir, File)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read the config: %w", err)
	}
	edited, err := c.withOwn(string(data), key, value)
	if err != nil {
		return err
	}
	if err := writeAtomic(path, []byte(edited)); err != nil {
		return fmt.Errorf("set %s: %w", key, err)
	}
	return nil
}

// withOwn is kit.toml's text with its own setting key set to value, "" for
// none: the setting's line replaced where it stands, its comment kept, else
// one added after the last setting before the first table; the rest as it
// was. It's read back as Load reads it, to be sure it says value.
func (c *Config) withOwn(text, key, value string) (string, error) {
	lines := strings.SplitAfter(text, "\n")
	end := tableEnd(lines, -1)
	at := slices.IndexFunc(lines[:end], func(line string) bool { return sets(line, key) })
	var setting bytes.Buffer
	if err := toml.NewEncoder(&setting).Encode(map[string]string{key: value}); err != nil {
		return "", fmt.Errorf("write %s: %w", key, err)
	}
	switch {
	case value == "" && at >= 0:
		lines = slices.Delete(lines, at, at+1)
	case value == "":
	case at >= 0:
		lines[at] = withComment(setting.String(), lines[at])
	default:
		if end > 0 && !strings.HasSuffix(lines[end-1], "\n") {
			lines[end-1] += "\n"
		}
		lines = slices.Insert(lines, end, setting.String())
	}
	edited := strings.Join(lines, "")
	if _, err := read(c.Dir, edited, nil); err != nil {
		return "", fmt.Errorf("couldn't set %s in %s: %w", key, File, err)
	}
	var own map[string]any
	if _, err := toml.Decode(edited, &own); err != nil {
		return "", fmt.Errorf("couldn't set %s in %s: %w", key, File, err)
	}
	if said, ok := own[key]; value == "" && ok || value != "" && said != value {
		return "", fmt.Errorf("couldn't set %s in %s: its line isn't one kit can write to", key, File)
	}
	return edited, nil
}

// withComment is setting, a line, with the comment that ends was, the line
// it replaces, if any, as far from the value as it was.
func withComment(setting, was string) string {
	was = strings.TrimRight(was, "\r\n")
	for i := strings.IndexByte(was, '#'); i >= 0; i = nextHash(was, i) {
		if _, err := toml.Decode(was[:i], new(map[string]any)); err != nil {
			continue
		}
		value := strings.TrimRight(was[:i], " \t")
		return strings.TrimSuffix(setting, "\n") + was[len(value):] + "\n"
	}
	return setting
}

// nextHash is where the # after the one at i is in line: -1 for none.
func nextHash(line string, i int) int {
	next := strings.IndexByte(line[i+1:], '#')
	if next < 0 {
		return -1
	}
	return i + 1 + next
}
