package oldmac

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/apps"
	"github.com/leeovery/kit/internal/restore"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// rsync's --version output is the restore package's fixtures'.

const (
	user = "someone"
	home = "/Users/someone"
)

// laptopName is the old Mac's local hostname, as kit-config records it;
// laptopAt, where it answers on the local network.
const (
	laptopName = "Someones-MacBook-Pro"
	laptopAt   = laptopName + ".local"
)

func fixture(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// rsyncSays is what rsync, as the named fixture of the restore package's,
// says of itself, as the probe prints it.
func rsyncSays(t *testing.T, name string) string {
	t.Helper()
	var b strings.Builder
	for line := range strings.Lines(fixture(t, filepath.Join("..", "testdata", name))) {
		b.WriteString("rsync=" + line)
	}
	return b.String()
}

// laptopKey is the old Mac's SSH host key, its type and key.
const laptopKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl"

// says is the probe's answer from a Mac called kit whose ~/Documents SSH
// reads, with Homebrew's rsync 3.5.2: "" for a Mac without kit.
func says(t *testing.T, kit string) string {
	t.Helper()
	out := "user=" + user + "\nhome=" + home + "\n"
	if kit != "" {
		out += "kit=" + kit + "\n"
	}
	return out + "protected=read Documents\n" + rsyncSays(t, "rsync-version.txt")
}

// asked scripts ssh's probe of host.
func asked(fake *runnertest.Fake, host string) *runnertest.Script {
	return fake.OnMatch("ssh", func(args []string) bool {
		return len(args) > 2 && args[len(args)-2] == host && strings.Contains(args[len(args)-1], "kit/machine")
	})
}

// finder is a Finder through fake, its state in a folder of its own,
// reaching laptop at laptopName, presenting laptopKey.
func finder(t *testing.T, fake *runnertest.Fake) Finder {
	return Finder{Run: fake, StateDir: t.TempDir(), HostKey: laptopKey, LocalHostName: laptopName}
}

// refused is ssh's exit when the Mac refuses this Mac's key.
func refused(s *runnertest.Script) {
	s.PrintsToStderr("someone@" + laptopAt + ": Permission denied (publickey,password,keyboard-interactive).\n").Exits(255)
}

// pinnedOpts are ssh's options trusting the host key in file alone, under
// laptop's kit name.
func pinnedOpts(file string) []string {
	return []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=5", "-o", "StrictHostKeyChecking=yes", "-o", "UserKnownHostsFile=" + file, "-o", "GlobalKnownHostsFile=/dev/null", "-o", "KnownHostsCommand=none", "-o", "VerifyHostKeyDNS=no", "-o", "HostKeyAlias=laptop", "-o", "LogLevel=ERROR"}
}

// The old Mac is reached at the local hostname kit-config records for it,
// then .local, alone, with the host key kit-config records pinned, under
// its kit name, and nowhere else's trusted: the probe, the look and the
// copy all through those options.
func TestFindsTheMacAtItsRecordedName(t *testing.T) {
	tests := []struct {
		name, state string
		wantShell   func(file string) string
	}{
		{"the usual", "state", func(file string) string { return "ssh " + strings.Join(pinnedOpts(file), " ") }},
		{"a state folder with a space", "kit state", func(file string) string {
			opts := slices.Clone(pinnedOpts(`"` + file + `"`))
			opts[7] = "'" + opts[7] + "'"
			return "ssh " + strings.Join(opts, " ")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := runnertest.New(t)
			asked(fake, laptopAt).Prints(says(t, "laptop"))
			f := finder(t, fake)
			f.StateDir = filepath.Join(f.StateDir, tt.state)

			m, err := f.Find(t.Context(), "laptop")
			if err != nil {
				t.Fatal(err)
			}
			if got, want := []any{m.Name, m.Host, m.User, m.Home, m.Rsync}, []any{"laptop", laptopAt, user, home, restore.Found{Version: "3.5.2", Usable: true}}; !reflect.DeepEqual(got, want) {
				t.Errorf("Find() = %v, want %v", got, want)
			}
			file := filepath.Join(f.StateDir, "restore", "pinned_host")
			if data, err := os.ReadFile(file); err != nil || string(data) != "laptop "+laptopKey+"\n" {
				t.Errorf("the pinned key's file = %q, %v; want laptop's key", data, err)
			}
			opts := pinnedOpts(file)
			if strings.Contains(tt.state, " ") {
				opts = pinnedOpts(`"` + file + `"`)
			}
			want := []runner.Command{{Name: "ssh", Args: slices.Concat(opts, []string{"--", laptopAt, "/bin/sh -c " + shellQuote(probeScript(RemoteRsync))}), Timeout: probeTimeout}}
			if got := fake.Commands(); len(got) != 1 || !slices.Equal(got[0].Args, want[0].Args) || got[0].Timeout != want[0].Timeout {
				t.Errorf("ran %q\nwant only %q", fake.Calls(), want)
			}
			opened := m.Opened()
			if err := opened.Close(t.Context()); err != nil {
				t.Errorf("Close() = %v", err)
			}
			if opened.Look == nil {
				t.Error("Opened() looks at nothing")
			}
			opened.Close, opened.Look = nil, nil
			wantOpened := &restore.Opened{
				Home: home, Host: laptopAt, Shell: tt.wantShell(file), Rsync: "/opt/homebrew/bin/rsync", Live: true,
				Origin: restore.Origin{Kind: restore.FromMac, Detail: "laptop"},
			}
			if !reflect.DeepEqual(opened, wantOpened) {
				t.Errorf("Opened() = %+v\nwant %+v", opened, wantOpened)
			}
		})
	}
}

// Without what reaching the old Mac takes in kit-config, its host key and
// its local hostname, each as it should be, it isn't reached, never
// trusted on first sight: ErrUnrecorded, saying how kit-config comes to
// record it, nothing run.
func TestFindsNothingUnrecorded(t *testing.T) {
	tests := []struct {
		name, key, named, want string
	}{
		{"no host key", "", laptopName, "laptop can't be restored from: laptop's SSH host key isn't in kit-config: kit apply on laptop records it"},
		{"a host key that isn't one", "ssh-ed25519 not*base64", laptopName, "laptop can't be restored from: laptop's SSH host key in kit-config isn't a valid key: kit apply on laptop records it"},
		{"neither", "", "", "laptop can't be restored from: laptop's SSH host key isn't in kit-config: kit apply on laptop records it"},
		{"no local hostname", laptopKey, "", "laptop can't be restored from: laptop's local hostname isn't in kit-config: kit apply on laptop records it"},
		{"a local hostname that's an option", laptopKey, "-oProxyCommand=x", `laptop can't be restored from: laptop's local hostname in kit-config, "-oProxyCommand=x", isn't one: kit apply on laptop records it`},
		{"a local hostname with .local", laptopKey, laptopAt, `laptop can't be restored from: laptop's local hostname in kit-config, "` + laptopAt + `", isn't one: kit apply on laptop records it`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := runnertest.New(t)
			f := finder(t, fake)
			f.HostKey, f.LocalHostName = tt.key, tt.named

			_, err := f.Find(t.Context(), "laptop")
			if !errors.Is(err, ErrUnrecorded) || errors.Is(err, ErrNoAnswer) || err.Error() != tt.want {
				t.Errorf("Find() = %v\nwant ErrUnrecorded, %q", err, tt.want)
			}
			if calls := fake.Calls(); len(calls) > 0 {
				t.Errorf("ran %q; want nothing", calls)
			}
		})
	}
}

// The old Mac not answering at its recorded name as itself is ErrNoAnswer,
// saying why: nothing answering there is ErrUnrecorded too, as a Mac
// renamed since answers elsewhere, and kit apply on it records its name as
// it is, as is its Remote Login off, which kit apply there turns on. ssh
// having no route to it, or not finding its .local name, may be macOS
// asking whether the app kit's running in can find devices on local
// networks, which it asks of the first ssh run there, failing it till it's
// allowed (that app named, or, unknown, said as Unnamed): said alone of no
// route, and beside kit apply there of a name not found; but over SSH,
// where macOS never asks. A Mac presenting another host key isn't it; and
// one answering as another Mac, or without kit, isn't, its words without
// control characters.
func TestFindsWhyTheMacDoesntAnswer(t *testing.T) {
	const (
		nothing    = "laptop doesn't answer over SSH: nothing answered at " + laptopAt + ", its local hostname in kit-config: "
		applyThere = "with laptop awake on this network, kit apply on laptop records the one it has now"
	)
	localNetwork := func(app string) string {
		return "macOS may be asking whether " + app + " can find devices on local networks: choose Allow, then try again, or, if Don't Allow was chosen, turn on " +
			app + " in System Settings › Privacy & Security › Local Network"
	}
	sshSays := func(said string) func(*runnertest.Script) {
		return func(s *runnertest.Script) { s.PrintsToStderr(said).Exits(255) }
	}
	unresolved := sshSays("ssh: Could not resolve hostname " + laptopAt + ": nodename nor servname provided, or not known\n")
	noRoute := sshSays("ssh: connect to host " + laptopAt + " port 22: No route to host\n")
	tests := []struct {
		name       string
		terminal   string
		overSSH    bool
		answers    func(*runnertest.Script)
		want       string
		unrecorded bool
	}{
		{"asleep", "", false, sshSays("ssh: connect to host " + laptopAt + " port 22: Operation timed out\n"), nothing + applyThere, true},
		{"not found, in Ghostty", "Ghostty", false, unresolved, nothing + localNetwork("Ghostty") + "; else, " + applyThere, true},
		{"not found, over SSH", "Ghostty", true, unresolved, nothing + applyThere, true},
		{"Remote Login off", "", false, sshSays("ssh: connect to host " + laptopAt + " port 22: Connection refused\n"),
			"laptop doesn't answer over SSH: " + laptopAt + " answered, but its Remote Login is off: kit apply on laptop turns it on", true},
		{"no route, in Ghostty", "Ghostty", false, noRoute, "laptop doesn't answer over SSH: " + laptopAt + " didn't answer: " + localNetwork("Ghostty"), false},
		{"no route, the terminal unknown", "", false, noRoute, "laptop doesn't answer over SSH: " + laptopAt + " didn't answer: " + localNetwork(apps.Unnamed), false},
		{"no route, over SSH", "Ghostty", true, noRoute, nothing + applyThere, true},
		{"the key refused", "", false, refused,
			"laptop doesn't answer over SSH: " + laptopAt + " refused this Mac's key, which the old Mac's ~/.ssh/authorized_keys must hold", false},
		{"another host key", "", false, sshSays("@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@\n@    WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED!     @\nHost key for laptop has changed and you have requested strict checking.\nHost key verification failed.\n"),
			"laptop doesn't answer over SSH: " + laptopAt + " isn't laptop: its host key doesn't match the one kit-config records (kit apply on laptop records a new one)", false},
		{"ssh's own reason", "", false, sshSays("kex_exchange_identification: Connection closed by remote host\n"),
			"laptop doesn't answer over SSH: " + laptopAt + " couldn't be reached over SSH: kex_exchange_identification: Connection closed by remote host", false},
		{"another Mac", "", false, func(s *runnertest.Script) { s.Prints(says(t, "stu\x1b[2Jdio")) },
			"laptop doesn't answer over SSH: " + laptopAt + " answered, but it's stu[2Jdio", false},
		{"a Mac without kit", "", false, func(s *runnertest.Script) { s.Prints(says(t, "")) },
			"laptop doesn't answer over SSH: " + laptopAt + " answered, but it's a Mac without kit", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := runnertest.New(t)
			tt.answers(asked(fake, laptopAt))
			f := finder(t, fake)
			f.Terminal, f.OverSSH = tt.terminal, tt.overSSH

			_, err := f.Find(t.Context(), "laptop")
			if !errors.Is(err, ErrNoAnswer) || errors.Is(err, ErrUnrecorded) != tt.unrecorded || err.Error() != tt.want {
				t.Errorf("Find() = %v\nwant ErrNoAnswer (ErrUnrecorded %v), %q", err, tt.unrecorded, tt.want)
			}
		})
	}
}

func TestChecksTheOldMac(t *testing.T) {
	fda := "SSH can't read ~/Documents on laptop: turn on Allow full disk access for remote users there, in System Settings › General › Sharing › Remote Login › ⓘ"
	tests := []struct {
		name string
		says func(good string) string
		want string
	}{
		{"all as it should be", func(good string) string { return good }, ""},
		{"none of the protected folders to read", func(good string) string {
			return strings.Replace(good, "protected=read Documents", "protected=none", 1)
		}, ""},
		{"no full disk access, ~/Desktop tried", func(good string) string {
			return strings.Replace(good, "protected=read Documents", "protected=denied Desktop", 1)
		}, strings.Replace(fda, "~/Documents", "~/Desktop", 1)},
		{"control characters said without them", func(good string) string {
			return strings.Replace(good, "user=someone", "user=\x1b[31mother\a", 1)
		}, "laptop's user is [31mother, and this Mac's someone: they must be the same, as Claude keys its sessions by their full paths"},
		{"another user", func(good string) string {
			return strings.Replace(good, "user=someone", "user=other", 1)
		}, "laptop's user is other, and this Mac's someone: they must be the same, as Claude keys its sessions by their full paths"},
		{"another home folder", func(good string) string {
			return strings.Replace(good, "home=/Users/someone", "home=/Volumes/Data/someone", 1)
		}, "laptop's home folder is /Volumes/Data/someone, and this Mac's /Users/someone: they must be the same, as Claude keys its sessions by their full paths"},
		{"no Homebrew rsync", func(good string) string {
			return good[:strings.Index(good, "rsync=")]
		}, "laptop's rsync, /opt/homebrew/bin/rsync, can't copy: it isn't installed: brew install rsync there"},
		{"openrsync", func(good string) string {
			return good[:strings.Index(good, "rsync=")] + rsyncSays(t, "openrsync-version.txt")
		}, "laptop's rsync, /opt/homebrew/bin/rsync, can't copy: it's macOS's own, openrsync, not rsync 3: brew install rsync there"},
		{"rsync too old", func(good string) string {
			return strings.Replace(good, "version 3.5.2", "version 3.2.3", 1)
		}, "laptop's rsync, /opt/homebrew/bin/rsync, can't copy: it's rsync 3.2.3, not rsync 3.2.4 or later: brew install rsync there"},
		{"no full disk access", func(good string) string {
			return strings.Replace(good, "protected=read Documents", "protected=denied Documents", 1)
		}, fda},
		{"everything wrong, each said", func(good string) string {
			good = strings.Replace(good, "user=someone", "user=other", 1)
			return strings.Replace(good[:strings.Index(good, "rsync=")], "protected=read Documents", "protected=denied Documents", 1)
		}, "laptop's user is other, and this Mac's someone: they must be the same, as Claude keys its sessions by their full paths\n" +
			"laptop's rsync, /opt/homebrew/bin/rsync, can't copy: it isn't installed: brew install rsync there\n" + fda},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := runnertest.New(t)
			asked(fake, laptopAt).Prints(tt.says(says(t, "laptop")))

			m, err := finder(t, fake).Find(t.Context(), "laptop")
			if err != nil {
				t.Fatal(err)
			}
			err = m.Check(user, home)
			if got := fmt.Sprint(err); err == nil && tt.want != "" || err != nil && got != tt.want {
				t.Errorf("Check() = %v\nwant %q", err, tt.want)
			}
		})
	}
}

func TestReadsTheProbeAmongTheShellsOwnLines(t *testing.T) {
	p := readProbe("Welcome\nuser=someone\r\nhome=/Users/someone\nrsync=a=b\nrsync=\n")
	if p.one(keyUser) != "someone" || p.one(keyHome) != home || !slices.Equal(p[keyRsync], []string{"a=b", ""}) || p.one(keyKit) != "" {
		t.Errorf("readProbe() = %v", p)
	}
}

func TestRemoteFailuresSaid(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"the key refused", runner.NewExitError("ssh", 255, []byte("someone@host: Permission denied (publickey).\n")), errRefusedKey.Error()},
		{"refused", runner.NewExitError("ssh", 255, []byte("ssh: connect to host h port 22: Connection refused\n")), errRemoteLoginOff.Error()},
		{"no route", runner.NewExitError("ssh", 255, []byte("ssh: connect to host h port 22: No route to host\n")), errNoRoute.Error()},
		{"down", runner.NewExitError("ssh", 255, []byte("ssh: connect to host h port 22: Host is down\n")), "didn't answer"},
		{"no such host", runner.NewExitError("ssh", 255, []byte("ssh: Could not resolve hostname h: nodename nor servname provided, or not known\n")), errUnresolved.Error()},
		{"timed out", runner.NewExitError("ssh", 255, []byte("ssh: connect to host h port 22: Operation timed out\n")), "didn't answer"},
		{"another host key", runner.NewExitError("ssh", 255, []byte("Host key verification failed.\n")), "couldn't be reached over SSH: Host key verification failed."},
		{"the command there", runner.NewExitError("ssh", 2, []byte("sh: line 1: find: command not found\n")), "failed to answer, exit 2: sh: line 1: find: command not found"},
		{"not an exit", errors.New("boom"), "boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sshFailed(tt.err).Error(); got != tt.want {
				t.Errorf("sshFailed() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFindsTheNameTrimmed(t *testing.T) {
	fake := runnertest.New(t)
	asked(fake, laptopAt).Prints(strings.Replace(says(t, "laptop"), "kit=laptop", "kit= laptop \t", 1))

	if m, err := finder(t, fake).Find(t.Context(), "laptop"); err != nil || m.Name != "laptop" {
		t.Errorf("Find() = %v, %v, want laptop", m, err)
	}
}

func TestFindsRefusingAStateFolderSSHCantName(t *testing.T) {
	for _, odd := range []string{"100%", "$HOME", "it's", `say "kit"`, `back\slash`} {
		t.Run(odd, func(t *testing.T) {
			f := finder(t, runnertest.New(t))
			f.StateDir = filepath.Join(f.StateDir, odd)
			_, err := f.Find(t.Context(), "laptop")
			if err == nil || !strings.Contains(err.Error(), "kit's state folder, "+f.StateDir+",") {
				t.Errorf("Find() = %v, want the state folder refused, named", err)
			}
		})
	}
}

// hostKey is a host key as a .pub file holds it.
const hostKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl root@laptop"

func TestPinsOnlyAKey(t *testing.T) {
	tests := []struct{ name, key, want string }{
		{"a .pub file's", hostKey, "laptop ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl\n"},
		{"a known-hosts line's", "laptop.example.com,192.0.2.7 ecdsa-sha2-nistp256 AAAAE2VjZHNh", "laptop ecdsa-sha2-nistp256 AAAAE2VjZHNh\n"},
		{"a known-hosts line's, its host like a type", "ssh-gateway.example.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5", "laptop ssh-ed25519 AAAAC3NzaC1lZDI1NTE5\n"},
		{"a .pub file's, its comment like a type", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5 ssh-key", "laptop ssh-ed25519 AAAAC3NzaC1lZDI1NTE5\n"},
		{"no type", "AAAAC3NzaC1lZDI1NTE5", ""},
		{"not base64", "ssh-ed25519 not*base64", ""},
		{"nothing", " ", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := pinLine("laptop", tt.key)
			if got != tt.want || (err == nil) != (tt.want != "") {
				t.Errorf("pinLine() = %q, %v, want %q", got, err, tt.want)
			}
		})
	}
}

func TestPinsOnlyToAPlainName(t *testing.T) {
	f := finder(t, runnertest.New(t))
	if _, err := f.Find(t.Context(), "-oProxyCommand=x"); err == nil {
		t.Error("Find() = nil, want a name that's no plain name refused")
	}
}

// A real shell runs the probe on a home folder made for it, ssh a stand-in
// that runs its command through sh as the old Mac's shell would, rsync a
// stand-in at a path with a space.
func TestProbesForRealThroughTheShell(t *testing.T) {
	tests := []struct {
		name      string
		folders   []string
		locked    string
		state     string
		protected string
	}{
		{"~/Documents read", []string{"Documents", "Desktop"}, "", "", "read Documents"},
		{"~/Documents locked", []string{"Documents"}, "Documents", "", "denied Documents"},
		{"no ~/Documents, ~/Desktop locked", []string{"Desktop", "Downloads"}, "Desktop", "", "denied Desktop"},
		{"no ~/Documents or ~/Desktop", []string{"Downloads"}, "", "", "read Downloads"},
		{"none of them", nil, "", "", "none"},
		{"kit's state where XDG_STATE_HOME says", []string{"Documents"}, "", "elsewhere", "read Documents"},
		{"a relative XDG_STATE_HOME ignored", []string{"Documents"}, "", "relative", "read Documents"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			homeDir := t.TempDir()
			for _, f := range tt.folders {
				if err := os.Mkdir(filepath.Join(homeDir, f), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if tt.locked != "" {
				locked := filepath.Join(homeDir, tt.locked)
				if err := os.Chmod(locked, 0); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
			}
			state, env := filepath.Join(homeDir, ".local", "state"), []string{}
			switch tt.state {
			case "elsewhere":
				state = filepath.Join(homeDir, "elsewhere")
				env = append(env, "XDG_STATE_HOME="+state)
			case "relative":
				env = append(env, "XDG_STATE_HOME=elsewhere")
			}
			if err := os.MkdirAll(filepath.Join(state, "kit"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(state, "kit", "machine"), []byte("laptop\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			rsync := filepath.Join(t.TempDir(), "brew bin", "rsync")
			if err := os.MkdirAll(filepath.Dir(rsync), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(rsync, []byte("#!/bin/sh\necho 'rsync  version 3.5.2  protocol version 33'\necho '    xattrs, crtimes'\n"), 0o700); err != nil {
				t.Fatal(err)
			}

			res, err := shellThrough(t, homeDir, env...).Run(t.Context(), remote(nil, laptopAt, probeScript(rsync), probeTimeout))
			if err != nil {
				t.Fatal(err)
			}
			p := readProbe(string(res.Stdout))
			got := []string{p.one(keyKit), p.one(keyHome), p.one(keyProtected), rsyncThere(p[keyRsync]).Version}
			if want := []string{"laptop", homeDir, tt.protected, "3.5.2"}; !slices.Equal(got, want) || p.one(keyUser) == "" {
				t.Errorf("probed %q (user %q), want %q", got, p.one(keyUser), want)
			}
		})
	}
}

// This Mac's own host key is the one another Mac's probe would say: its
// ed25519 key, else the first other it may read, without the comment; none
// while macOS hasn't made them.
func TestReadsThisMacsHostKey(t *testing.T) {
	const (
		ed25519 = "ssh-ed25519 ZmFrZSBlZDI1NTE5IGhvc3Qga2V5"
		ecdsa   = "ecdsa-sha2-nistp256 ZmFrZSBlY2RzYSBob3N0IGtleQ=="
		rsa     = "ssh-rsa ZmFrZSByc2EgaG9zdCBrZXk="
	)
	tests := []struct {
		name       string
		keys       map[string]string
		unreadable string
		want       string
	}{
		{"ed25519 first", map[string]string{"ssh_host_ecdsa_key.pub": ecdsa, "ssh_host_ed25519_key.pub": ed25519, "ssh_host_rsa_key.pub": rsa}, "", ed25519},
		{"else the first other", map[string]string{"ssh_host_ecdsa_key.pub": ecdsa, "ssh_host_rsa_key.pub": rsa}, "", ecdsa},
		{"one unreadable passed over", map[string]string{"ssh_host_ed25519_key.pub": ed25519, "ssh_host_rsa_key.pub": rsa}, "ssh_host_ed25519_key.pub", rsa},
		{"none yet", nil, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, key := range tt.keys {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(key+" root@laptop\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, strings.TrimSuffix(name, ".pub")), []byte("private"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tt.unreadable != "" {
				if err := os.Chmod(filepath.Join(dir, tt.unreadable), 0); err != nil {
					t.Fatal(err)
				}
			}
			if got, err := HostKey(dir); err != nil || got != tt.want {
				t.Errorf("HostKey() = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
	t.Run("not a key", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "ssh_host_ed25519_key.pub"), []byte("garbled\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := HostKey(dir); err == nil {
			t.Error("HostKey() = nil error; want the file that isn't a key refused")
		}
	})
}
