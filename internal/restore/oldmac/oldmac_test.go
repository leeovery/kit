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
	"time"

	"github.com/leeovery/kit/internal/restore"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// The fixtures in testdata are dns-sd's own output on a network of three
// Macs offering Remote Login, trimmed and renamed: browse.txt, dns-sd -B
// _ssh._tcp local. run on the Mac Studio, which sees itself on lo0 (if 1)
// as well; and resolve.txt, dns-sd -L of the MacBook Pro. rsync's
// --version output is the restore package's fixtures'.

const (
	user = "someone"
	home = "/Users/someone"
)

// The Macs of browse.txt but the one it ran on, and the one it ran on.
const (
	laptop  = "Someone’s MacBook Pro"
	mini    = "Someone’s Mac mini"
	thisMac = "Someone’s Mac Studio"
)

// Where each answers, without the dot dns-sd ends a host's name with.
var (
	laptopAt = strings.TrimSuffix("Someones-MacBook-Pro.local.", ".")
	miniAt   = strings.TrimSuffix("Someones-Mac-mini.local.", ".")
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

// says is the probe's answer from a Mac called kit whose ~/Documents SSH
// reads, with Homebrew's rsync 3.5.2: "" for a Mac without kit.
func says(t *testing.T, kit string) string {
	t.Helper()
	out := "user=" + user + "\nhome=" + home + "\n"
	if kit != "" {
		out += "kit=" + kit + "\n"
	}
	return out + "documents=read\n" + rsyncSays(t, "rsync-version.txt")
}

// browsing is dns-sd -B's output finding names, each on interface 13.
func browsing(names ...string) string {
	out := "Browsing for _ssh._tcp.local.\nTimestamp     A/R    Flags  if Domain               Service Type         Instance Name\n"
	for _, n := range names {
		out += fmt.Sprintf("21:38:59.104  Add        3  13 local.               _ssh._tcp.           %s\n", n)
	}
	return out
}

// reaching is dns-sd -L's output for the Mac offering Remote Login as name
// at host and port.
func reaching(name, host string, port int) string {
	full := strings.ReplaceAll(name, " ", `\032`)
	return fmt.Sprintf("Lookup %s._ssh._tcp.local.\n21:39:08.624  %s._ssh._tcp.local. can be reached at %s.:%d (interface 13) Flags: 1\n", name, full, host, port)
}

func browses(fake *runnertest.Fake, output string) *runnertest.Script {
	return fake.On("dns-sd", "-B", "_ssh._tcp", "local.").Prints(output)
}

func resolves(fake *runnertest.Fake, name, output string) *runnertest.Script {
	return fake.On("dns-sd", "-L", name, "_ssh._tcp", "local.").Prints(output)
}

// asked scripts ssh's probe of host.
func asked(fake *runnertest.Fake, host string) *runnertest.Script {
	return fake.OnMatch("ssh", func(args []string) bool {
		return len(args) > 2 && args[len(args)-2] == host && strings.Contains(args[len(args)-1], "kit/machine")
	})
}

// finder is a Finder through fake, its state in a folder of its own.
func finder(t *testing.T, fake *runnertest.Fake) Finder {
	return Finder{Run: fake, StateDir: t.TempDir()}
}

// refused is ssh's exit when the Mac refuses the key.
func refused(s *runnertest.Script) {
	s.PrintsToStderr("someone@" + miniAt + ": Permission denied (publickey,password,keyboard-interactive).\n").Exits(255)
}

func TestFindsTheMacByItsKitName(t *testing.T) {
	fake := runnertest.New(t)
	browses(fake, fixture(t, "testdata/browse.txt"))
	resolves(fake, laptop, fixture(t, "testdata/resolve.txt"))
	resolves(fake, mini, reaching(mini, miniAt, 22))
	asked(fake, laptopAt).Prints(says(t, "laptop"))
	asked(fake, miniAt).Prints(says(t, "server"))
	f := finder(t, fake)

	m, err := f.Find(t.Context(), "laptop")
	if err != nil {
		t.Fatal(err)
	}
	got := []any{m.Name, m.Instance, m.Host, m.Port, m.User, m.Home, m.Rsync}
	want := []any{"laptop", laptop, laptopAt, 22, user, home, restore.Found{Version: "3.5.2", Usable: true}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Find() = %v, want %v", got, want)
	}
	if err := m.Check(user, home); err != nil {
		t.Errorf("Check() = %v, want nil", err)
	}
	if info, err := os.Stat(filepath.Dir(KnownHosts(f.StateDir))); err != nil || !info.IsDir() {
		t.Errorf("kit's known-hosts folder isn't made: %v", err)
	}
}

func TestFindsNeverThisMac(t *testing.T) {
	fake := runnertest.New(t)
	browses(fake, fixture(t, "testdata/browse.txt"))
	resolves(fake, laptop, fixture(t, "testdata/resolve.txt"))
	resolves(fake, mini, reaching(mini, miniAt, 22))
	asked(fake, laptopAt).Prints(says(t, "laptop"))
	asked(fake, miniAt).Prints(says(t, "server"))

	if _, err := finder(t, fake).Find(t.Context(), "laptop"); err != nil {
		t.Fatal(err)
	}
	for _, call := range fake.Calls() {
		if strings.Contains(call, thisMac) {
			t.Errorf("ran %s: this Mac, seen on lo0, is never asked", call)
		}
	}
}

func TestFindsNoneSayingWhyOfEach(t *testing.T) {
	const pro, air, mac = "Someone’s Mac Pro", "Someone’s MacBook Air", "Someone’s iMac"
	proAt, macAt := strings.TrimSuffix("Someones-Mac-Pro.local.", "."), strings.TrimSuffix("Someones-iMac.local.", ".")
	fake := runnertest.New(t)
	browses(fake, browsing(mac, mini, pro, laptop, air, mini))
	resolves(fake, mac, reaching(mac, macAt, 22))
	resolves(fake, mini, reaching(mini, miniAt, 22))
	resolves(fake, pro, reaching(pro, proAt, 22))
	resolves(fake, laptop, reaching(laptop, laptopAt, 22))
	resolves(fake, air, "Lookup "+air+"._ssh._tcp.local.\n")
	asked(fake, macAt).Prints(says(t, "studio"))
	refused(asked(fake, miniAt))
	asked(fake, proAt).Prints(says(t, ""))
	asked(fake, laptopAt).PrintsToStderr("ssh: connect to host " + laptopAt + " port 22: Operation timed out\n").Exits(255)

	_, err := finder(t, fake).Find(t.Context(), "laptop")
	if !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("Find() = %v, want ErrNoAnswer", err)
	}
	want := "laptop doesn't answer over SSH: " +
		"Someone’s Mac Pro has no kit; " +
		"Someone’s Mac mini refused this Mac's key, which the old Mac's ~/.ssh/authorized_keys must hold; " +
		"Someone’s MacBook Air didn't answer; " +
		"Someone’s MacBook Pro didn't answer; " +
		"Someone’s iMac answered, but it's studio"
	if err.Error() != want {
		t.Errorf("Find() = %q\nwant %q", err, want)
	}
}

func TestFindsNoMacOfferingRemoteLogin(t *testing.T) {
	fake := runnertest.New(t)
	browses(fake, browsing(thisMac)+strings.Replace(browsing(thisMac), "  13 ", "   1 ", 1))

	_, err := finder(t, fake).Find(t.Context(), "laptop")
	if !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("Find() = %v, want ErrNoAnswer", err)
	}
	if want := "laptop doesn't answer over SSH: no Mac on the network offers Remote Login"; err.Error() != want {
		t.Errorf("Find() = %q, want %q", err, want)
	}
}

func TestFindsOnlyTheKeyRefused(t *testing.T) {
	fake := runnertest.New(t)
	browses(fake, browsing(mini))
	resolves(fake, mini, reaching(mini, miniAt, 22))
	refused(asked(fake, miniAt))

	_, err := finder(t, fake).Find(t.Context(), "laptop")
	if !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("Find() = %v, want ErrNoAnswer", err)
	}
	if want := "laptop doesn't answer over SSH: Someone’s Mac mini refused this Mac's key, which the old Mac's ~/.ssh/authorized_keys must hold"; err.Error() != want {
		t.Errorf("Find() = %q, want %q", err, want)
	}
}

func TestFindsSSHsOwnReason(t *testing.T) {
	fake := runnertest.New(t)
	browses(fake, browsing(mini))
	resolves(fake, mini, reaching(mini, miniAt, 22))
	asked(fake, miniAt).PrintsToStderr("@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@\n@    WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED!     @\nHost key verification failed.\n").Exits(255)

	_, err := finder(t, fake).Find(t.Context(), "laptop")
	if want := "laptop doesn't answer over SSH: Someone’s Mac mini couldn't be reached over SSH: Host key verification failed."; err == nil || err.Error() != want {
		t.Errorf("Find() = %v, want %q", err, want)
	}
}

func TestFindsTwoMacsOfOneNameAnError(t *testing.T) {
	fake := runnertest.New(t)
	browses(fake, browsing(laptop, mini))
	resolves(fake, laptop, reaching(laptop, laptopAt, 22))
	resolves(fake, mini, reaching(mini, miniAt, 22))
	asked(fake, laptopAt).Prints(says(t, "laptop"))
	asked(fake, miniAt).Prints(says(t, "laptop"))

	_, err := finder(t, fake).Find(t.Context(), "laptop")
	if errors.Is(err, ErrNoAnswer) || err == nil || !strings.Contains(err.Error(), "more than one Mac answers as laptop, "+miniAt+" and "+laptopAt) {
		t.Errorf("Find() = %v, want both Macs named", err)
	}
}

func TestFindsWhileDNSSDRunsOn(t *testing.T) {
	fake := runnertest.New(t)
	browses(fake, fixture(t, "testdata/browse.txt")).Hangs()
	resolves(fake, laptop, fixture(t, "testdata/resolve.txt")).Hangs()
	resolves(fake, mini, "").Hangs()
	asked(fake, laptopAt).Prints(says(t, "laptop"))
	f := finder(t, fake)
	f.Browse, f.Resolve = 50*time.Millisecond, 50*time.Millisecond

	m, err := f.Find(t.Context(), "laptop")
	if err != nil {
		t.Fatal(err)
	}
	if m.Host != laptopAt {
		t.Errorf("Find() = %s, want %s", m.Host, laptopAt)
	}
}

func TestFindsWhereAMacAnswersOnceItSays(t *testing.T) {
	fake := runnertest.New(t)
	browses(fake, browsing(laptop))
	resolves(fake, laptop, fixture(t, "testdata/resolve.txt")).Hangs()
	asked(fake, laptopAt).Prints(says(t, "laptop"))
	f := finder(t, fake)
	f.Resolve = time.Hour

	started := time.Now()
	if _, err := f.Find(t.Context(), "laptop"); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(started); took > 10*time.Second {
		t.Errorf("Find() took %s: dns-sd -L wasn't ended once it answered", took)
	}
}

func TestFindsWithTheSSHOptionsExact(t *testing.T) {
	tests := []struct {
		name      string
		state     string
		port      int
		wantOpts  func(state string) []string
		wantShell func(state string) string
	}{
		{
			"the usual", "state", 22,
			func(s string) []string {
				return []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=5", "-o", "StrictHostKeyChecking=accept-new", "-o", "UserKnownHostsFile=" + s + "/restore/known_hosts", "-o", "LogLevel=ERROR"}
			},
			func(s string) string {
				return "ssh -o BatchMode=yes -o ConnectTimeout=5 -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=" + s + "/restore/known_hosts -o LogLevel=ERROR"
			},
		},
		{
			"another port, a state folder with a space", "kit state", 2222,
			func(s string) []string {
				return []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=5", "-o", "StrictHostKeyChecking=accept-new", "-o", `UserKnownHostsFile="` + s + `/restore/known_hosts"`, "-o", "LogLevel=ERROR", "-p", "2222"}
			},
			func(s string) string {
				return `ssh -o BatchMode=yes -o ConnectTimeout=5 -o StrictHostKeyChecking=accept-new -o 'UserKnownHostsFile="` + s + `/restore/known_hosts"' -o LogLevel=ERROR -p 2222`
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := runnertest.New(t)
			browses(fake, browsing(laptop))
			resolves(fake, laptop, reaching(laptop, laptopAt, tt.port))
			asked(fake, laptopAt).Prints(says(t, "laptop"))
			f := finder(t, fake)
			f.StateDir = filepath.Join(f.StateDir, tt.state)

			m, err := f.Find(t.Context(), "laptop")
			if err != nil {
				t.Fatal(err)
			}
			ssh := fake.Commands()[2]
			want := slices.Concat(tt.wantOpts(f.StateDir), []string{laptopAt, "/bin/sh -c " + shellQuote(probeScript)})
			if !slices.Equal(ssh.Args, want) {
				t.Errorf("ssh's arguments = %q\nwant %q", ssh.Args, want)
			}
			if ssh.Timeout != probeTimeout {
				t.Errorf("ssh's timeout = %s, want %s", ssh.Timeout, probeTimeout)
			}
			opened := m.Opened()
			if err := opened.Close(t.Context()); err != nil {
				t.Errorf("Close() = %v", err)
			}
			opened.Close = nil
			wantOpened := &restore.Opened{
				Home: home, Host: laptopAt, Shell: tt.wantShell(f.StateDir), Rsync: "/opt/homebrew/bin/rsync", Live: true,
				Origin: restore.Origin{Kind: restore.FromMac, Detail: "laptop"},
			}
			if !reflect.DeepEqual(opened, wantOpened) {
				t.Errorf("Opened() = %+v\nwant %+v", opened, wantOpened)
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
		{"no ~/Documents to read", func(good string) string {
			return strings.Replace(good, "documents=read", "documents=none", 1)
		}, ""},
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
			return strings.Replace(good, "documents=read", "documents=denied", 1)
		}, fda},
		{"everything wrong, each said", func(good string) string {
			good = strings.Replace(good, "user=someone", "user=other", 1)
			return strings.Replace(good[:strings.Index(good, "rsync=")], "documents=read", "documents=denied", 1)
		}, "laptop's user is other, and this Mac's someone: they must be the same, as Claude keys its sessions by their full paths\n" +
			"laptop's rsync, /opt/homebrew/bin/rsync, can't copy: it isn't installed: brew install rsync there\n" + fda},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := runnertest.New(t)
			browses(fake, browsing(laptop))
			resolves(fake, laptop, reaching(laptop, laptopAt, 22))
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

func TestKnownHostsAreKits(t *testing.T) {
	if got, want := KnownHosts("/Users/someone/.local/state/kit"), "/Users/someone/.local/state/kit/restore/known_hosts"; got != want {
		t.Errorf("KnownHosts() = %s, want %s", got, want)
	}
}

func TestRemoteFailuresSaid(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"the key refused", runner.NewExitError("ssh", 255, []byte("someone@host: Permission denied (publickey).\n")), errRefusedKey.Error()},
		{"refused", runner.NewExitError("ssh", 255, []byte("ssh: connect to host h port 22: Connection refused\n")), "didn't answer"},
		{"no route", runner.NewExitError("ssh", 255, []byte("ssh: connect to host h port 22: No route to host\n")), "didn't answer"},
		{"no such host", runner.NewExitError("ssh", 255, []byte("ssh: Could not resolve hostname h: nodename nor servname provided, or not known\n")), "didn't answer"},
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
