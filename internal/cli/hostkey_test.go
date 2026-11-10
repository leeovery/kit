package cli_test

import (
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// otherKey is another SSH host key, plainly no real one: the one a Mac set
// up again under its old name finds kit-config recording.
const otherKey = "ssh-rsa ZmFrZSByc2EgaG9zdCBrZXk="

// configFile is kit.toml, from the home; ownKey, the world's Mac's host
// key's file.
var (
	configFile = filepath.Join(".config", "kit", "kit.toml")
	ownKey     = filepath.Join("etc", "ssh", "ssh_host_ed25519_key.pub")
)

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

// remoteLogin switches w's Mac's Remote Login feature on, as it stands.
func (w *world) remoteLogin(t *testing.T) {
	t.Helper()
	w.writeSection(t, "laptop", "features", "remote-login\n")
	w.fake.On("launchctl", "print-disabled", "system").Prints("disabled services = {\n\t\t\"com.openssh.sshd\" => enabled\n}\n")
}

// noKeyYet has w's Mac without a host key, as macOS leaves it till Remote
// Login's first connection.
func (w *world) noKeyYet(t *testing.T) {
	t.Helper()
	if err := os.Remove(filepath.Join(w.home, ownKey)); err != nil {
		t.Fatal(err)
	}
}

// sshd has w's Remote Login answer, as launchd starts it for a connection:
// sshd-keygen-wrapper makes the host key, then sshd says its banner.
func (w *world) sshd(t *testing.T) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	w.sshAt = l.Addr().String()
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_ = os.WriteFile(filepath.Join(w.home, ownKey), []byte(hostKey+" root@laptop\n"), 0o644)
		_, _ = conn.Write([]byte("SSH-2.0-OpenSSH_9.9\r\n"))
	}()
}

// kit status says whether kit-config records this Mac's SSH host key, the
// one its Remote Login presents: recorded, not recorded, another (a Mac set
// up again has a new one), or none yet, as macOS makes it only at Remote
// Login's first connection; with Remote Login off, there's none to record,
// which is fine. It changes nothing, and connects to nothing.
func TestStatusOfTheHostKey(t *testing.T) {
	for _, tt := range []struct {
		name, recorded string
		off, noKey     bool
		want           string
		status         int
	}{
		{"recorded", hostKey, false, false, "host-key ok in kit-config\n", 0},
		{"not recorded", "", false, false, "host-key attention not in kit-config\nhost-key problem SSH host key not in kit-config (kit apply records it)\n", 1},
		{"another recorded", otherKey, false, false, "host-key attention not kit-config's\nhost-key problem SSH host key not the one kit-config records (kit apply records this Mac's: a Mac set up again has a new one)\n", 1},
		{"none yet", hostKey, false, true, "host-key attention none yet\nhost-key problem no SSH host key yet (macOS makes it at Remote Login's first connection: kit apply connects once, then records it)\n", 1},
		{"Remote Login off", "", true, true, "host-key ok Remote Login is off: no host key to record\n", 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := laptopWorld(t)
			w.recording(t, tt.recorded)
			if !tt.off {
				w.remoteLogin(t)
			}
			if tt.noKey {
				w.noKeyYet(t)
			}
			w.sshd(t)
			was := w.read(t, configFile)
			out, errOut, status := w.run(t, "status", "host-key")
			if !strings.Contains(out, tt.want) || status != tt.status {
				t.Errorf("kit status host-key printed\n%s%s\nexit %d; want it holding\n%s(exit %d)", out, errOut, status, tt.want, tt.status)
			}
			if got := w.read(t, configFile); got != was {
				t.Errorf("kit.toml =\n%s\nwant it as it was", got)
			}
			if _, err := os.Stat(filepath.Join(w.home, ownKey)); tt.noKey && err == nil {
				t.Error("kit status woke Remote Login")
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
// commits and pushes kit.toml; with none yet, it has Remote Login answer
// once, which makes it, then records it.
func TestApplyRecordsTheHostKey(t *testing.T) {
	for _, tt := range []struct {
		name, recorded string
		noKey          bool
	}{
		{"not recorded", "", false},
		{"another recorded", otherKey, false},
		{"none yet", "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := laptopWorld(t)
			w.remoteLogin(t)
			w.recording(t, tt.recorded)
			if tt.noKey {
				w.noKeyYet(t)
				w.sshd(t)
			}
			w.expectSync(t, []string{"kit.toml"}, "kit host-key record laptop (laptop)")

			out, errOut, _ := w.run(t, "apply", "host-key")
			if !strings.Contains(out, "host-key ok committed and pushed kit.toml\n") {
				t.Errorf("kit apply host-key printed\n%s%s\nwant kit.toml committed and pushed", out, errOut)
			}
			if got := w.read(t, configFile); got != twoMacs {
				t.Errorf("kit.toml =\n%s\nwant\n%s", got, twoMacs)
			}
		})
	}
}

// The host key's left for the next apply, saying why, kit.toml as it was:
// git set to sign its commits and unable to sign this one, as the key it
// signs with comes later; Remote Login not answering to make the key;
// kit.toml with edits not committed, which recording would commit with it,
// the person's for kit reconcile, or this Mac's table, as the boot left it,
// for the new-mac step.
func TestApplyLeavesTheHostKeyForTheNextRun(t *testing.T) {
	for _, tt := range []struct {
		name, want string
		set        func(t *testing.T, w *world)
	}{
		{"git can't sign", "git couldn't sign the commit: gpg failed to sign the data; the next kit apply tries again", func(t *testing.T, w *world) {
			c := edited("kit host-key record laptop (laptop)", "kit.toml")
			c.unsigned = true
			w.expectCommit(t, c)
		}},
		{"Remote Login doesn't answer", "this Mac has no SSH host key yet, and Remote Login didn't answer at 127.0.0.1:1 to make one: ", func(t *testing.T, w *world) {
			w.noKeyYet(t)
		}},
		{"kit.toml edited", "kit.toml has edits not committed: kit reconcile settles them, then kit apply records the key", func(t *testing.T, w *world) {
			w.edits(t, " M kit.toml\x00", "1\t0\tkit.toml\x00")
		}},
		{"this Mac's table not committed", "kit.toml holds this Mac's table, not committed yet: the new-mac step commits it, then kit apply records the key", func(t *testing.T, w *world) {
			w.edits(t, " M kit.toml\x00", "1\t0\tkit.toml\x00")
			w.write(t, bootState, `{"new_mac": "laptop", "from": "studio"}`)
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := laptopWorld(t)
			w.remoteLogin(t)
			w.recording(t, "")
			tt.set(t, w)
			was := w.read(t, configFile)

			out, errOut, _ := w.run(t, "apply", "host-key")
			if !strings.Contains(out, "host-key deferred "+tt.want) {
				t.Errorf("kit apply host-key printed\n%s%s\nwant the host key left for the next apply, saying\n%s", out, errOut, tt.want)
			}
			if got := w.read(t, configFile); got != was {
				t.Errorf("kit.toml =\n%s\nwant it as it was", got)
			}
		})
	}
}

// With Remote Login a feature of the Mac, the host key's recorded after
// kit turns it on.
func TestHostKeyAfterRemoteLogin(t *testing.T) {
	w := orderWorld(t)
	w.writeSection(t, "laptop", "features", "prefs\nremote-login\n")
	if before := names(w.order(t, "host-key").Before); !slices.Contains(before, "remote-login") {
		t.Errorf("before host-key = %q; want remote-login", before)
	}
}
