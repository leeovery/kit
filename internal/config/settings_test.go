package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kinds"
)

// kit.toml's own settings are set in place: the line replaced where it
// stands, its comment kept, else added after the last setting before the
// first table; taken out, its line goes; every other line stays as it was.
func TestSetOwnSettings(t *testing.T) {
	const macs = "\n# The Macs.\n[macs.laptop]\ndescription = \"MacBook Pro\" # the old one\n"
	terminal := func(name string) func(*config.Config) error {
		return func(c *config.Config) error { return c.SetTerminal(name) }
	}
	nightlyAt := func(at string) func(*config.Config) error {
		return func(c *config.Config) error { return c.SetNightlyAt(at) }
	}
	tests := []struct {
		name string
		set  func(*config.Config) error
		toml string
		want string
	}{
		{
			"added after the last setting", terminal("ghostty"),
			"# kit-config\nformat = 1\n# Hears of every Mac.\nprimary = \"laptop\"\n" + macs,
			"# kit-config\nformat = 1\n# Hears of every Mac.\nprimary = \"laptop\"\nterminal = \"ghostty\"\n" + macs,
		},
		{
			"replaced where it stands, its comment kept", nightlyAt("4:30"),
			"format = 1\nnightly_at = \"03:00\"   # when the run's due\nprimary = \"laptop\"\n" + macs,
			"format = 1\nnightly_at = \"04:30\"   # when the run's due\nprimary = \"laptop\"\n" + macs,
		},
		{
			"replaced, without a comment", nightlyAt("23:59"),
			"format = 1\nprimary = \"laptop\"\nnightly_at = \"03:00\"\n" + macs,
			"format = 1\nprimary = \"laptop\"\nnightly_at = \"23:59\"\n" + macs,
		},
		{
			"a setting commented out left as it is", nightlyAt("01:00"),
			"format = 1\n# nightly_at = \"05:00\"\nprimary = \"laptop\"\n" + macs,
			"format = 1\n# nightly_at = \"05:00\"\nprimary = \"laptop\"\nnightly_at = \"01:00\"\n" + macs,
		},
		{
			"taken out", terminal(""),
			"format = 1\nterminal = \"ghostty\" # the boot's\nprimary = \"laptop\"\n" + macs,
			"format = 1\nprimary = \"laptop\"\n" + macs,
		},
		{
			"taken out when it's not there", terminal(""),
			"format = 1\nprimary = \"laptop\"\n" + macs,
			"format = 1\nprimary = \"laptop\"\n" + macs,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeRepo(t, map[string]string{config.File: tt.toml})
			cfg, err := config.Load(dir, kinds.Sections())
			if err != nil {
				t.Fatal(err)
			}
			if err := tt.set(cfg); err != nil {
				t.Fatalf("set: %v", err)
			}
			if data, _ := os.ReadFile(filepath.Join(dir, config.File)); string(data) != tt.want {
				t.Errorf("kit.toml =\n%s\nwant\n%s", data, tt.want)
			}
		})
	}
}

// A terminal kit doesn't know, or a time that isn't one, is refused, kit.toml
// left as it was.
func TestSetOwnSettingsRefuses(t *testing.T) {
	tests := []struct {
		name string
		set  func(*config.Config) error
		want string
	}{
		{"an unknown terminal", func(c *config.Config) error { return c.SetTerminal("other") }, "kit doesn't know the terminal other: one of ghostty"},
		{"an hour past the day", func(c *config.Config) error { return c.SetNightlyAt("24:00") }, `"24:00" isn't a time of day, as in 03:00`},
		{"no time at all", func(c *config.Config) error { return c.SetNightlyAt("3am") }, `"3am" isn't a time of day, as in 03:00`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeRepo(t, map[string]string{config.File: twoMacs})
			cfg, err := config.Load(dir, kinds.Sections())
			if err != nil {
				t.Fatal(err)
			}
			if err := tt.set(cfg); err == nil || err.Error() != tt.want {
				t.Errorf("set = %v, want %q", err, tt.want)
			}
			if data, _ := os.ReadFile(filepath.Join(dir, config.File)); string(data) != twoMacs {
				t.Errorf("kit.toml =\n%s\nwant it as it was", data)
			}
		})
	}
}

// An edit that wouldn't read back as Load reads it is refused, the file as
// it was: here, as kit.toml sets nightly_at in a way of its own.
func TestSetOwnSettingsReadsBack(t *testing.T) {
	toml := "format = 1\nprimary = \"laptop\"\n\"nightly_at\" = \"03:00\"\n\n[macs.laptop]\n"
	dir := writeRepo(t, map[string]string{config.File: toml})
	cfg, err := config.Load(dir, kinds.Sections())
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetNightlyAt("04:00"); err == nil || !strings.HasPrefix(err.Error(), "couldn't set nightly_at in kit.toml: ") {
		t.Errorf("SetNightlyAt() = %v, want it refused", err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, config.File)); string(data) != toml {
		t.Errorf("kit.toml =\n%s\nwant it as it was", data)
	}
}

func TestTimeOfDay(t *testing.T) {
	tests := []struct {
		in   string
		at   time.Duration
		says string
	}{
		{"3:00", 3 * time.Hour, "03:00"},
		{"04:30", 4*time.Hour + 30*time.Minute, "04:30"},
		{"00:05", 5 * time.Minute, "00:05"},
		{"23:59", 23*time.Hour + 59*time.Minute, "23:59"},
	}
	for _, tt := range tests {
		at, err := config.ParseTimeOfDay(tt.in)
		if err != nil || at != tt.at || config.TimeOfDay(at) != tt.says {
			t.Errorf("ParseTimeOfDay(%q) = %v, %v, said %q; want %v, said %q", tt.in, at, err, config.TimeOfDay(at), tt.at, tt.says)
		}
	}
}

// Load says whether kit.toml sets nightly_at, as 03:00 it may say, or leaves
// it to the default.
func TestNightlyAtSet(t *testing.T) {
	for toml, want := range map[string]bool{
		twoMacs:                              false,
		"nightly_at = \"03:00\"\n" + twoMacs: true,
	} {
		cfg := loadRepo(t, map[string]string{config.File: toml})
		if cfg.NightlyAtSet() != want || cfg.NightlyAt != config.DefaultNightlyAt {
			t.Errorf("Load(%q): NightlyAtSet() = %v, NightlyAt = %v; want %v, 03:00", toml, cfg.NightlyAtSet(), cfg.NightlyAt, want)
		}
	}
}
