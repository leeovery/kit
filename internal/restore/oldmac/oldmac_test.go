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
	return out + "protected=read Documents\n" + rsyncSays(t, "rsync-version.txt")
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
			want := slices.Concat(tt.wantOpts(f.StateDir), []string{"--", laptopAt, "/bin/sh -c " + shellQuote(probeScript(RemoteRsync))})
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

func TestFindsTheNameTrimmed(t *testing.T) {
	fake := runnertest.New(t)
	browses(fake, browsing(laptop))
	resolves(fake, laptop, reaching(laptop, laptopAt, 22))
	asked(fake, laptopAt).Prints(strings.Replace(says(t, "laptop"), "kit=laptop", "kit= laptop \t", 1))

	if m, err := finder(t, fake).Find(t.Context(), "laptop"); err != nil || m.Name != "laptop" {
		t.Errorf("Find() = %v, %v, want laptop", m, err)
	}
}

func TestFindsNeverAHostThatsAnOption(t *testing.T) {
	const evil = "Someone’s Printer"
	fake := runnertest.New(t)
	browses(fake, browsing(evil))
	// As dns-sd says it, its name ending in a dot, which kit drops.
	evilAt := strings.TrimSuffix("-oProxyCommand=touch${IFS}/tmp/x.local.", ".")
	resolves(fake, evil, reaching(evil, evilAt, 22))

	_, err := finder(t, fake).Find(t.Context(), "laptop")
	want := `laptop doesn't answer over SSH: Someone’s Printer names its host "` + evilAt + `", which isn't a host name kit reaches: letters, digits, - and ., not starting with -`
	if !errors.Is(err, ErrNoAnswer) || err.Error() != want {
		t.Errorf("Find() = %v\nwant %q", err, want)
	}
	for _, call := range fake.Calls() {
		if strings.HasPrefix(call, "ssh") {
			t.Errorf("ran %s", call)
		}
	}
}

func TestFindsWithoutWithdrawnMacs(t *testing.T) {
	const renamed = "Someone’s MacBook Pro (2)"
	fake := runnertest.New(t)
	gone := strings.Replace(browsing(renamed), "  Add  ", "  Rmv  ", 1)
	browses(fake, browsing(laptop, renamed)+gone[strings.Index(gone, "21:"):])
	resolves(fake, laptop, reaching(laptop, laptopAt, 22))
	asked(fake, laptopAt).Prints(says(t, "laptop"))

	if _, err := finder(t, fake).Find(t.Context(), "laptop"); err != nil {
		t.Fatal(err)
	}
	for _, call := range fake.Calls() {
		if strings.Contains(call, "(2)") {
			t.Errorf("ran %s: a withdrawn Mac isn't asked", call)
		}
	}
}

func TestFindsOneMacUnderTwoNames(t *testing.T) {
	const renamed = "Someone’s MacBook Pro (2)"
	fake := runnertest.New(t)
	browses(fake, browsing(laptop, renamed))
	resolves(fake, laptop, reaching(laptop, laptopAt, 22))
	shouted := strings.ToUpper(laptopAt)
	resolves(fake, renamed, reaching(renamed, shouted, 22))
	asked(fake, laptopAt).Prints(says(t, "laptop"))
	asked(fake, shouted).Prints(says(t, "laptop"))

	m, err := finder(t, fake).Find(t.Context(), "laptop")
	if err != nil {
		t.Fatal(err)
	}
	if m.Host != laptopAt {
		t.Errorf("Find() = %s, want %s", m.Host, laptopAt)
	}
}

func TestFindsNamesWithoutControlCharacters(t *testing.T) {
	const odd = "Someone’s \x1b]0;owned\a Mac"
	fake := runnertest.New(t)
	browses(fake, browsing(odd))
	resolves(fake, odd, reaching(odd, miniAt, 22))
	asked(fake, miniAt).Prints(says(t, "stu\x1b[2Jdio"))

	_, err := finder(t, fake).Find(t.Context(), "laptop")
	if want := "laptop doesn't answer over SSH: Someone’s ]0;owned Mac answered, but it's stu[2Jdio"; err == nil || err.Error() != want {
		t.Errorf("Find() = %q, want %q", err, want)
	}
}

// hostKey is a host key as a .pub file holds it.
const hostKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl root@laptop"

func TestFindsThePinnedMac(t *testing.T) {
	fake := runnertest.New(t)
	browses(fake, browsing(laptop, mini))
	resolves(fake, laptop, reaching(laptop, laptopAt, 22))
	resolves(fake, mini, reaching(mini, miniAt, 22))
	asked(fake, laptopAt).Prints(says(t, "laptop"))
	asked(fake, miniAt).PrintsToStderr("@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@\n@    WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED!     @\nHost key for laptop has changed and you have requested strict checking.\nHost key verification failed.\n").Exits(255)
	f := finder(t, fake)
	f.HostKey = hostKey

	m, err := f.Find(t.Context(), "laptop")
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(f.StateDir, "restore", "pinned_host")
	wantOpts := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=5", "-o", "StrictHostKeyChecking=yes", "-o", "UserKnownHostsFile=" + file, "-o", "GlobalKnownHostsFile=/dev/null", "-o", "HostKeyAlias=laptop", "-o", "LogLevel=ERROR"}
	for _, c := range fake.Commands() {
		if c.Name == "ssh" && !slices.Equal(c.Args[:len(wantOpts)], wantOpts) {
			t.Errorf("ssh's options = %q\nwant %q", c.Args, wantOpts)
		}
	}
	if want := "ssh " + strings.Join(wantOpts, " "); m.Shell() != want {
		t.Errorf("Shell() = %s\nwant %s", m.Shell(), want)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if want := "laptop ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl\n"; string(data) != want {
		t.Errorf("the pinned key's file = %q, want %q", data, want)
	}
}

func TestFindsAnImpostorByItsHostKey(t *testing.T) {
	fake := runnertest.New(t)
	browses(fake, browsing(mini))
	resolves(fake, mini, reaching(mini, miniAt, 22))
	asked(fake, miniAt).PrintsToStderr("Host key for laptop has changed and you have requested strict checking.\nHost key verification failed.\n").Exits(255)
	f := finder(t, fake)
	f.HostKey = hostKey

	_, err := f.Find(t.Context(), "laptop")
	want := "laptop doesn't answer over SSH: Someone’s Mac mini isn't laptop: its host key doesn't match the one kit-config records"
	if !errors.Is(err, ErrNoAnswer) || err.Error() != want {
		t.Errorf("Find() = %v\nwant %q", err, want)
	}
}

func TestPinsOnlyAKey(t *testing.T) {
	tests := []struct{ name, key, want string }{
		{"a .pub file's", hostKey, "laptop ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl\n"},
		{"a known-hosts line's", "laptop.example.com,192.0.2.7 ecdsa-sha2-nistp256 AAAAE2VjZHNh", "laptop ecdsa-sha2-nistp256 AAAAE2VjZHNh\n"},
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
	f.HostKey = hostKey
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
