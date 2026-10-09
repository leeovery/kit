package config_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/config"
)

func TestLoad(t *testing.T) {
	dir := writeRepo(t, map[string]string{config.File: `format = 1
minimum_kit = "0.1.0"
primary = "laptop"

[macs.laptop]
description = "MacBook Pro"

[macs.studio]
description = "Mac Studio"

[macs.mac-mini-2]
`})
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Dir != dir || cfg.Format != 1 || cfg.MinimumKit != "0.1.0" || cfg.Primary != "laptop" {
		t.Errorf("Load() = %+v, want the settings as written", cfg)
	}
	if want := []string{"laptop", "mac-mini-2", "studio"}; !slices.Equal(cfg.MacNames(), want) {
		t.Errorf("MacNames() = %q, want %q", cfg.MacNames(), want)
	}
	if got := cfg.Macs["studio"]; got != (config.Mac{Name: "studio", Description: "Mac Studio"}) {
		t.Errorf("Macs[studio] = %+v, want its name and description", got)
	}
	if !cfg.Knows("laptop") || cfg.Knows("other") {
		t.Error("Knows() should know laptop alone of laptop and other")
	}
}

func TestLoadWithoutAConfig(t *testing.T) {
	_, err := config.Load(t.TempDir())
	if !errors.Is(err, config.ErrNoConfig) {
		t.Errorf("Load() of a directory without kit.toml: error = %v, want ErrNoConfig", err)
	}
}

func TestLoadRefuses(t *testing.T) {
	const macs = "[macs.laptop]\n[macs.studio]\n"
	tests := []struct {
		name string
		toml string
		want string
	}{
		{name: "not TOML", toml: "format = \n", want: "kit.toml: toml:"},
		{name: "an unknown setting", toml: "format = 1\nprimary = \"laptop\"\ncolour = \"red\"\n" + macs, want: "kit.toml: unknown setting colour"},
		{name: "an unknown Mac setting", toml: "format = 1\nprimary = \"laptop\"\n[macs.laptop]\nmodel = \"x\"\n", want: "kit.toml: unknown setting macs.laptop.model"},
		{name: "no format", toml: "primary = \"laptop\"\n" + macs, want: "kit.toml: no format: add format = 1"},
		{name: "a later format", toml: "format = 2\nprimary = \"laptop\"\n" + macs, want: "the config is format 2, newer than this kit reads (1): update kit"},
		{name: "a negative format", toml: "format = -1\nprimary = \"laptop\"\n" + macs, want: "format -1 isn't one"},
		{name: "a minimum kit that isn't a version", toml: "format = 1\nminimum_kit = \"soon\"\nprimary = \"laptop\"\n" + macs, want: `minimum_kit "soon" isn't a version`},
		{name: "no Macs", toml: "format = 1\nprimary = \"laptop\"\n", want: "kit.toml: no Macs: add one, as in [macs.laptop]"},
		{name: "a Mac's name with capitals", toml: "format = 1\nprimary = \"Laptop\"\n[macs.Laptop]\n", want: `"Laptop" can't be a Mac's name`},
		{name: "a Mac's name with a dot", toml: "format = 1\nprimary = \"lap.top\"\n[macs.\"lap.top\"]\n", want: `"lap.top" can't be a Mac's name`},
		{name: "no primary", toml: "format = 1\n" + macs, want: "no primary: name the Mac that hears about every Mac's problems, one of laptop, studio"},
		{name: "an unknown primary", toml: "format = 1\nprimary = \"other\"\n" + macs, want: "the primary is other, which isn't one of its Macs (laptop, studio)"},
		{name: "a terminal kit doesn't know", toml: "format = 1\nprimary = \"laptop\"\nterminal = \"teletype\"\n" + macs, want: `kit doesn't know the terminal "teletype": one of ghostty`},
		{name: "a password manager kit doesn't know", toml: "format = 1\nprimary = \"laptop\"\npassword_manager = \"notebook\"\n" + macs, want: `kit doesn't know the password manager "notebook": one of 1password`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.Load(writeRepo(t, map[string]string{config.File: tt.toml}))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Load() error = %v, want one saying %q", err, tt.want)
			}
		})
	}
}

func TestSupports(t *testing.T) {
	tests := []struct {
		minimum string
		version string
		ok      bool
	}{
		{minimum: "", version: "0.0.1", ok: true},
		{minimum: "0.2.0", version: "0.2.0", ok: true},
		{minimum: "0.2.0", version: "0.10.0", ok: true},
		{minimum: "0.2.0", version: "v1.0.0", ok: true},
		{minimum: "0.2.0", version: "0.1.9", ok: false},
		{minimum: "1.0.0", version: "0.99.99", ok: false},
		{minimum: "0.2.0", version: "dev", ok: true},
		{minimum: "0.2.0", version: "0.3.0-next", ok: true},
	}
	for _, tt := range tests {
		cfg := &config.Config{MinimumKit: tt.minimum}
		err := cfg.Supports(tt.version)
		if (err == nil) != tt.ok {
			t.Errorf("minimum %q, Supports(%q) error = %v, want ok = %v", tt.minimum, tt.version, err, tt.ok)
		}
		if err != nil && !strings.Contains(err.Error(), "the config needs kit "+tt.minimum+" or later, and this is "+tt.version+": update kit") {
			t.Errorf("Supports(%q) error = %v, want it to say what's needed", tt.version, err)
		}
	}
}

func TestNightlyAt(t *testing.T) {
	cfg := loadRepo(t, map[string]string{})
	if cfg.NightlyAt != 3*time.Hour {
		t.Errorf("NightlyAt unset = %v, want 3h", cfg.NightlyAt)
	}
	cfg = loadRepo(t, map[string]string{config.File: "format = 1\nprimary = \"laptop\"\nnightly_at = \"02:30\"\n\n[macs.laptop]\n"})
	if cfg.NightlyAt != 2*time.Hour+30*time.Minute {
		t.Errorf("NightlyAt = %v, want 2h30m", cfg.NightlyAt)
	}
	dir := writeRepo(t, map[string]string{config.File: "format = 1\nprimary = \"laptop\"\nnightly_at = \"3am\"\n\n[macs.laptop]\n"})
	if _, err := config.Load(dir); err == nil || !strings.Contains(err.Error(), `nightly_at "3am" isn't a time of day`) {
		t.Errorf("Load() = %v", err)
	}
}

// The terminal a new Mac's boot hands over to, and the password manager it
// installs: settings of the person's, none unless kit.toml says.
func TestApps(t *testing.T) {
	if cfg := loadRepo(t, map[string]string{}); cfg.Terminal != "" || cfg.PasswordManager != "" {
		t.Errorf("unset: terminal %q, password manager %q", cfg.Terminal, cfg.PasswordManager)
	}
	cfg := loadRepo(t, map[string]string{config.File: "format = 1\nprimary = \"laptop\"\nterminal = \"ghostty\"\npassword_manager = \"1password\"\n\n[macs.laptop]\n"})
	if cfg.Terminal != "ghostty" || cfg.PasswordManager != "1password" {
		t.Errorf("terminal %q, password manager %q", cfg.Terminal, cfg.PasswordManager)
	}
}

func TestPrefsRepo(t *testing.T) {
	if cfg := loadRepo(t, map[string]string{}); cfg.PrefsRepo != "" {
		t.Errorf("PrefsRepo unset = %q", cfg.PrefsRepo)
	}
	cfg := loadRepo(t, map[string]string{config.File: "format = 1\nprimary = \"laptop\"\nprefs_repo = \"git@github.com:someone/prefs.git\"\n\n[macs.laptop]\n"})
	if cfg.PrefsRepo != "git@github.com:someone/prefs.git" {
		t.Errorf("PrefsRepo = %q", cfg.PrefsRepo)
	}
}
