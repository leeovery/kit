package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Format is the config format this kit reads. A config of a later format
// needs a later kit; an earlier one, kit migrates (none yet).
const Format = 1

// File is the config repository's settings file.
const File = "kit.toml"

// ErrNoConfig is returned by Load for a directory without a kit.toml.
var ErrNoConfig = errors.New("no config repository")

// Config is a config repository: its settings, and where it is.
type Config struct {
	// Dir is the repository's directory.
	Dir string
	// Format is the config format the repository is written in.
	Format int
	// MinimumKit is the oldest kit release that reads the repository, or ""
	// when any does.
	MinimumKit string
	// Primary is the Mac that hears about every Mac's problems.
	Primary string
	// Macs are the Macs the repository knows, by name.
	Macs map[string]Mac
	// NightlyAt is when each day the nightly run is due, as hours and
	// minutes since midnight: 03:00 unless kit.toml says.
	NightlyAt time.Duration
	// SecretsItem is the 1Password item that holds most secrets, as a
	// reference (op://vault/item), so a secret's line names only its
	// section and field, or its attachment: none unless kit.toml says.
	SecretsItem string
}

// DefaultNightlyAt is when the nightly run is due when kit.toml doesn't say.
const DefaultNightlyAt = 3 * time.Hour

// Mac is a Mac the config repository knows.
type Mac struct {
	Name        string
	Description string
}

// file is kit.toml as written.
type file struct {
	Format     int                `toml:"format"`
	MinimumKit string             `toml:"minimum_kit"`
	Primary    string             `toml:"primary"`
	NightlyAt  string             `toml:"nightly_at"`
	Secrets    string             `toml:"secrets_item"`
	Macs       map[string]macFile `toml:"macs"`
}

type macFile struct {
	Description string `toml:"description"`
}

// macName is what a Mac's name may be: it names the Mac's own declarations
// file, as in laptop.
var macName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Load reads the config repository in dir, checking its settings: a format
// this kit reads, at least one Mac, every Mac's name, and a primary among
// them.
func Load(dir string) (*Config, error) {
	path := filepath.Join(dir, File)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: no %s in %s", ErrNoConfig, File, dir)
	}
	if err != nil {
		return nil, fmt.Errorf("read the config: %w", err)
	}
	var f file
	md, err := toml.Decode(string(data), &f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", File, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, key := range undecoded {
			keys[i] = key.String()
		}
		return nil, fmt.Errorf("%s: unknown setting %s", File, strings.Join(keys, ", "))
	}
	switch {
	case f.Format == 0:
		return nil, fmt.Errorf("%s: no format: add format = %d", File, Format)
	case f.Format > Format:
		return nil, fmt.Errorf("%s: the config is format %d, newer than this kit reads (%d): update kit", File, f.Format, Format)
	case f.Format < 0:
		return nil, fmt.Errorf("%s: format %d isn't one", File, f.Format)
	}
	if f.MinimumKit != "" {
		if _, ok := parseVersion(f.MinimumKit); !ok {
			return nil, fmt.Errorf("%s: minimum_kit %q isn't a version such as 0.1.0", File, f.MinimumKit)
		}
	}
	if len(f.Macs) == 0 {
		return nil, fmt.Errorf("%s: no Macs: add one, as in [macs.laptop]", File)
	}
	cfg := &Config{Dir: dir, Format: f.Format, MinimumKit: f.MinimumKit, Primary: f.Primary, Macs: make(map[string]Mac, len(f.Macs)), NightlyAt: DefaultNightlyAt, SecretsItem: strings.TrimSuffix(f.Secrets, "/")}
	if cfg.SecretsItem != "" && (!strings.HasPrefix(cfg.SecretsItem, "op://") || strings.Count(cfg.SecretsItem, "/") != 3) {
		return nil, fmt.Errorf("%s: secrets_item %q isn't an item's reference, as in op://vault/item", File, f.Secrets)
	}
	if f.NightlyAt != "" {
		at, err := time.Parse("15:04", f.NightlyAt)
		if err != nil {
			return nil, fmt.Errorf("%s: nightly_at %q isn't a time of day, as in 03:00", File, f.NightlyAt)
		}
		cfg.NightlyAt = time.Duration(at.Hour())*time.Hour + time.Duration(at.Minute())*time.Minute
	}
	for name, m := range f.Macs {
		switch {
		case !macName.MatchString(name):
			return nil, fmt.Errorf("%s: %q can't be a Mac's name: use lower-case letters, digits and hyphens", File, name)
		case name == Shared:
			return nil, fmt.Errorf("%s: a Mac can't be called %s: that's the file every Mac reads", File, Shared)
		}
		cfg.Macs[name] = Mac{Name: name, Description: m.Description}
	}
	switch {
	case f.Primary == "":
		return nil, fmt.Errorf("%s: no primary: name the Mac that hears about every Mac's problems, one of %s", File, cfg.macList())
	case !cfg.Knows(f.Primary):
		return nil, fmt.Errorf("%s: the primary is %s, which isn't one of its Macs (%s)", File, f.Primary, cfg.macList())
	}
	if err := cfg.checkFiles(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Knows reports whether the config knows a Mac named name.
func (c *Config) Knows(name string) bool {
	_, ok := c.Macs[name]
	return ok
}

// MacNames are the names of the config's Macs, in order.
func (c *Config) MacNames() []string {
	names := make([]string, 0, len(c.Macs))
	for name := range c.Macs {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// macList is the config's Macs, as a list for a message.
func (c *Config) macList() string {
	return strings.Join(c.MacNames(), ", ")
}

// Supports returns an error when the config needs a later kit than version.
// A version that isn't a release's, such as a build from source, reads any.
func (c *Config) Supports(version string) error {
	if c.MinimumKit == "" {
		return nil
	}
	have, ok := parseVersion(version)
	if !ok {
		return nil
	}
	need, _ := parseVersion(c.MinimumKit)
	if slices.Compare(have, need) < 0 {
		return fmt.Errorf("the config needs kit %s or later, and this is %s: update kit", c.MinimumKit, version)
	}
	return nil
}

// parseVersion reads a release's version, as in 0.1.0 or v0.1.0.
func parseVersion(v string) ([]int, bool) {
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) != 3 {
		return nil, false
	}
	nums := make([]int, 3)
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 || strconv.Itoa(n) != part {
			return nil, false
		}
		nums[i] = n
	}
	return nums, true
}
