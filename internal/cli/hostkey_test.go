package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// otherKey is another SSH host key, plainly no real one: the one a Mac set
// up again under its old name finds kit-config recording.
const otherKey = "ssh-rsa ZmFrZSByc2EgaG9zdCBrZXk="

// configFile is kit.toml, from the home.
var configFile = filepath.Join(".config", "kit", "kit.toml")

// recording has w's kit.toml record key as laptop's host key: none for "".
func (w *world) recording(t *testing.T, key string) {
	t.Helper()
	line := `host_key = "` + hostKey + `"` + "\n"
	if key != "" {
		w.write(t, configFile, strings.Replace(twoMacs, line, `host_key = "`+key+`"`+"\n", 1))
		return
	}
	w.write(t, configFile, strings.Replace(twoMacs, line, "", 1))
}

// kit status says whether kit-config records this Mac's SSH host key, the
// one its Remote Login presents: recorded, not recorded, another (a Mac set
// up again has a new one), or none yet, as macOS makes it only once Remote
// Login starts. It changes nothing.
func TestStatusOfTheHostKey(t *testing.T) {
	for _, tt := range []struct {
		name, recorded string
		noKey          bool
		want           string
		status         int
	}{
		{"recorded", hostKey, false, "host-key ok in kit-config\n", 0},
		{"not recorded", "", false, "host-key attention not in kit-config\nhost-key problem SSH host key not in kit-config (kit apply records it)\n", 1},
		{"another recorded", otherKey, false, "host-key attention not kit-config's\nhost-key problem SSH host key not the one kit-config records (kit apply records this Mac's: a Mac set up again has a new one)\n", 1},
		{"none yet", hostKey, true, "host-key attention none yet\nhost-key problem no SSH host key yet (macOS makes it the first time Remote Login starts; kit apply records it then)\n", 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := laptopWorld(t)
			w.recording(t, tt.recorded)
			if tt.noKey {
				if err := os.Remove(filepath.Join(w.home, "etc", "ssh", "ssh_host_ed25519_key.pub")); err != nil {
					t.Fatal(err)
				}
			}
			was := w.read(t, configFile)
			out, errOut, status := w.run(t, "status", "host-key")
			if !strings.Contains(out, tt.want) || status != tt.status {
				t.Errorf("kit status host-key printed\n%s%s\nexit %d; want it holding\n%s(exit %d)", out, errOut, status, tt.want, tt.status)
			}
			if got := w.read(t, configFile); got != was {
				t.Errorf("kit.toml =\n%s\nwant it as it was", got)
			}
			for _, c := range w.fake.Calls() {
				if strings.Contains(c, "commit-tree") || strings.HasSuffix(c, "push --quiet") {
					t.Errorf("ran %s; want nothing committed or pushed", c)
				}
			}
		})
	}
}

// kit apply records this Mac's host key in its table in kit.toml, its type
// and key, without the comment its file has, in place of another, and
// commits and pushes kit.toml.
func TestApplyRecordsTheHostKey(t *testing.T) {
	for _, recorded := range []string{"", otherKey} {
		w := laptopWorld(t)
		w.recording(t, recorded)
		w.expectSync(t, []string{"kit.toml"}, "kit host-key record laptop (laptop)")

		out, errOut, _ := w.run(t, "apply", "host-key")
		if !strings.Contains(out, "host-key ok committed and pushed kit.toml\n") {
			t.Errorf("recorded %q: kit apply host-key printed\n%s%s\nwant kit.toml committed and pushed", recorded, out, errOut)
		}
		if got := w.read(t, configFile); got != twoMacs {
			t.Errorf("recorded %q: kit.toml =\n%s\nwant\n%s", recorded, got, twoMacs)
		}
	}
}

// git set to sign its commits, and unable to sign this one, as the key it
// signs with comes later, the host key's left for the next apply, saying
// what git said, kit.toml put back as it was; so is one macOS hasn't made
// yet.
func TestApplyLeavesTheHostKeyForTheNextRun(t *testing.T) {
	t.Run("git can't sign", func(t *testing.T) {
		w := laptopWorld(t)
		w.recording(t, "")
		c := edited("kit host-key record laptop (laptop)", "kit.toml")
		c.unsigned = true
		w.expectCommit(t, c)
		was := w.read(t, configFile)

		out, errOut, _ := w.run(t, "apply", "host-key")
		if !strings.Contains(out, "host-key deferred git couldn't sign the commit: gpg failed to sign the data; the next kit apply tries again\n") {
			t.Errorf("kit apply host-key printed\n%s%s\nwant the host key left for the next apply", out, errOut)
		}
		if got := w.read(t, configFile); got != was {
			t.Errorf("kit.toml =\n%s\nwant it as it was", got)
		}
	})
	t.Run("no key yet", func(t *testing.T) {
		w := laptopWorld(t)
		w.recording(t, "")
		if err := os.Remove(filepath.Join(w.home, "etc", "ssh", "ssh_host_ed25519_key.pub")); err != nil {
			t.Fatal(err)
		}
		out, errOut, _ := w.run(t, "apply", "host-key")
		if !strings.Contains(out, "host-key deferred this Mac has no SSH host key yet: macOS makes it the first time Remote Login starts; the next kit apply records it\n") {
			t.Errorf("kit apply host-key printed\n%s%s\nwant the host key left for the next apply", out, errOut)
		}
	})
}
