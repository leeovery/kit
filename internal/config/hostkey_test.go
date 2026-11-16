package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kinds"
)

// fakeKey is an SSH host key as kit-config records it, plainly no real one.
const fakeKey = "ssh-ed25519 ZmFrZSBlZDI1NTE5IGhvc3Qga2V5"

// fakeName is a Mac's local hostname as kit-config records it.
const fakeName = "Someones-Mac-Studio"

// A Mac's host key and local hostname are set in its own table in
// kit.toml: where the table has them, in their places, as for a Mac set up
// again or renamed; else after its last setting, before the blank lines
// and comments leading to the next table; the rest as it was. kit reads
// them back as the Mac's.
func TestSetHost(t *testing.T) {
	const head = "format = 1\nprimary = \"laptop\"\n\n"
	set := "host_key = \"" + fakeKey + "\"\nlocal_hostname = \"" + fakeName + "\"\n"
	tests := []struct {
		name, mac, toml, want string
	}{
		{
			"added after the table's settings", "laptop",
			head + "[macs.laptop]\ndescription = \"MacBook Pro\"\n\n# the new one\n[macs.studio]\n",
			head + "[macs.laptop]\ndescription = \"MacBook Pro\"\n" + set + "\n# the new one\n[macs.studio]\n",
		},
		{
			"set in their places", "laptop",
			head + "[macs.laptop]\nlocal_hostname = \"Someones-Old-Name\"\nhost_key = \"ssh-rsa b2xkIGtleQ==\" # set up in 2025\ndescription = \"MacBook Pro\"\n[macs.studio]\n",
			head + "[macs.laptop]\nlocal_hostname = \"" + fakeName + "\"\nhost_key = \"" + fakeKey + "\"\ndescription = \"MacBook Pro\"\n[macs.studio]\n",
		},
		{
			"the key there, the name added", "laptop",
			head + "[macs.laptop]\nhost_key = \"" + fakeKey + "\"\ndescription = \"MacBook Pro\"\n[macs.studio]\n",
			head + "[macs.laptop]\nhost_key = \"" + fakeKey + "\"\ndescription = \"MacBook Pro\"\nlocal_hostname = \"" + fakeName + "\"\n[macs.studio]\n",
		},
		{
			"the last table, with nothing in it and no line break", "studio",
			head + "[macs.laptop]\n[macs.studio]",
			head + "[macs.laptop]\n[macs.studio]\n" + set,
		},
		{
			"a header with a comment", "studio",
			head + "[macs.laptop]\n[macs.studio] # Mac Studio\nfrom = \"laptop\"\n",
			head + "[macs.laptop]\n[macs.studio] # Mac Studio\nfrom = \"laptop\"\n" + set,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeRepo(t, map[string]string{config.File: tt.toml})
			cfg, err := config.Load(dir, kinds.Sections())
			if err != nil {
				t.Fatal(err)
			}
			if err := cfg.SetHost(tt.mac, fakeKey, fakeName); err != nil {
				t.Fatalf("SetHost() = %v", err)
			}
			data, err := os.ReadFile(filepath.Join(dir, config.File))
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != tt.want {
				t.Errorf("kit.toml =\n%s\nwant\n%s", data, tt.want)
			}
			again, err := config.Load(dir, kinds.Sections())
			if err != nil {
				t.Fatal(err)
			}
			if got := again.Macs[tt.mac]; got.HostKey != fakeKey || got.LocalHostName != fakeName {
				t.Errorf("%s read back = %+v, want its host key %q and local hostname %q", tt.mac, got, fakeKey, fakeName)
			}
		})
	}
}

// Adding a Mac and recording its host key write kit.toml beside it, then
// put it in place whole, never in place: a reader of the one there never
// sees it half written.
func TestKitTomlIsWrittenWhole(t *testing.T) {
	for name, write := range map[string]func(*config.Config) error{
		"AddMac":  func(c *config.Config) error { return c.AddMac("studio", "", "") },
		"SetHost": func(c *config.Config) error { return c.SetHost("laptop", fakeKey, fakeName) },
	} {
		t.Run(name, func(t *testing.T) {
			const toml = "format = 1\nprimary = \"laptop\"\n\n[macs.laptop]\n"
			dir := writeRepo(t, map[string]string{config.File: toml})
			cfg, err := config.Load(dir, kinds.Sections())
			if err != nil {
				t.Fatal(err)
			}
			held := filepath.Join(t.TempDir(), "held")
			if err := os.Link(filepath.Join(dir, config.File), held); err != nil {
				t.Fatal(err)
			}
			if err := write(cfg); err != nil {
				t.Fatal(err)
			}
			if data, _ := os.ReadFile(held); string(data) != toml {
				t.Errorf("the kit.toml there before reads\n%s\nwant it as it was: the new one put in its place", data)
			}
		})
	}
}

// A Mac kit.toml doesn't know, or one without a table of its own to write
// in, is refused, kit.toml left as it was.
func TestSetHostRefuses(t *testing.T) {
	tests := []struct {
		name, mac, toml, want string
	}{
		{"an unknown Mac", "other", twoMacs, "no Mac named other in kit.toml"},
		{"no table of its own", "laptop", "format = 1\nprimary = \"laptop\"\nmacs.laptop.description = \"MacBook Pro\"\n", "kit.toml has no [macs.laptop] table to record its host_key in"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeRepo(t, map[string]string{config.File: tt.toml})
			cfg, err := config.Load(dir, kinds.Sections())
			if err != nil {
				t.Fatal(err)
			}
			if err := cfg.SetHost(tt.mac, fakeKey, fakeName); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("SetHost() = %v, want %q", err, tt.want)
			}
			if data, _ := os.ReadFile(filepath.Join(dir, config.File)); string(data) != tt.toml {
				t.Errorf("kit.toml =\n%s\nwant it as it was", data)
			}
		})
	}
}

// A kit.toml's text, as a remote has it, gives a Mac's host key and local
// hostname as Load does: none for a Mac without them, or one it doesn't
// know.
func TestMacIn(t *testing.T) {
	text := []byte(twoMacs + "host_key = \" " + fakeKey + " \"\nlocal_hostname = \" " + fakeName + "\"\n")
	for _, tt := range []struct{ mac, key, name string }{{"studio", fakeKey, fakeName}, {"laptop", "", ""}, {"other", "", ""}} {
		if got, err := config.MacIn(text, tt.mac); err != nil || got.HostKey != tt.key || got.LocalHostName != tt.name {
			t.Errorf("MacIn(%s) = %+v, %v; want %q and %q", tt.mac, got, err, tt.key, tt.name)
		}
	}
	if _, err := config.MacIn([]byte("format = \n"), "laptop"); err == nil {
		t.Error("MacIn() of a kit.toml that isn't TOML = nil error")
	}
}
