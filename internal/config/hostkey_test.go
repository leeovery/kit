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

// A Mac's host key is set in its own table in kit.toml: where the table has
// one, in its place; else after its last setting, before the blank lines
// and comments leading to the next table; the rest as it was. kit reads it
// back as the Mac's.
func TestSetHostKey(t *testing.T) {
	const head = "format = 1\nprimary = \"laptop\"\n\n"
	tests := []struct {
		name, mac, toml, want string
	}{
		{
			"added after the table's settings", "laptop",
			head + "[macs.laptop]\ndescription = \"MacBook Pro\"\n\n# the new one\n[macs.studio]\n",
			head + "[macs.laptop]\ndescription = \"MacBook Pro\"\nhost_key = \"" + fakeKey + "\"\n\n# the new one\n[macs.studio]\n",
		},
		{
			"set in its place", "laptop",
			head + "[macs.laptop]\nhost_key = \"ssh-rsa b2xkIGtleQ==\" # set up in 2025\ndescription = \"MacBook Pro\"\n[macs.studio]\n",
			head + "[macs.laptop]\nhost_key = \"" + fakeKey + "\"\ndescription = \"MacBook Pro\"\n[macs.studio]\n",
		},
		{
			"the last table, with nothing in it and no line break", "studio",
			head + "[macs.laptop]\n[macs.studio]",
			head + "[macs.laptop]\n[macs.studio]\nhost_key = \"" + fakeKey + "\"\n",
		},
		{
			"a header with a comment", "studio",
			head + "[macs.laptop]\n[macs.studio] # Mac Studio\nfrom = \"laptop\"\n",
			head + "[macs.laptop]\n[macs.studio] # Mac Studio\nfrom = \"laptop\"\nhost_key = \"" + fakeKey + "\"\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeRepo(t, map[string]string{config.File: tt.toml})
			cfg, err := config.Load(dir, kinds.Sections())
			if err != nil {
				t.Fatal(err)
			}
			if err := cfg.SetHostKey(tt.mac, fakeKey); err != nil {
				t.Fatalf("SetHostKey() = %v", err)
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
			if got := again.Macs[tt.mac].HostKey; got != fakeKey {
				t.Errorf("%s's host key read back = %q, want %q", tt.mac, got, fakeKey)
			}
		})
	}
}

// A Mac kit.toml doesn't know, or one without a table of its own to write
// in, is refused, kit.toml left as it was.
func TestSetHostKeyRefuses(t *testing.T) {
	tests := []struct {
		name, mac, toml, want string
	}{
		{"an unknown Mac", "other", twoMacs, "no Mac named other in kit.toml"},
		{"no table of its own", "laptop", "format = 1\nprimary = \"laptop\"\nmacs.laptop.description = \"MacBook Pro\"\n", "kit.toml has no [macs.laptop] table to record its host key in"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeRepo(t, map[string]string{config.File: tt.toml})
			cfg, err := config.Load(dir, kinds.Sections())
			if err != nil {
				t.Fatal(err)
			}
			if err := cfg.SetHostKey(tt.mac, fakeKey); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("SetHostKey() = %v, want %q", err, tt.want)
			}
			if data, _ := os.ReadFile(filepath.Join(dir, config.File)); string(data) != tt.toml {
				t.Errorf("kit.toml =\n%s\nwant it as it was", data)
			}
		})
	}
}
