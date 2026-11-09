package restore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// rsync 3 isn't on the Mac the tests were written on, so the fixtures in
// testdata are rsync 3.4's output as its manual documents it: --version,
// --info=name1,progress2 with --stats, a dry run's --stats, and
// --itemize-changes. Each progress line is as the runner gives it, the last
// a carriage return drew before a new line ended it.

func fixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// base are the options of every copy, dry run and second pass.
var base = []string{"--archive", "--xattrs", "--hard-links", "--partial-dir=.rsync-partial", "--timeout=600"}

// ops are the three ways rsync is run on a copy, with the options each adds.
var ops = []struct {
	name    string
	options []string
	run     func(ctx context.Context, r Rsync, c Copy) error
}{
	{"size", []string{"--dry-run", "--stats"}, func(ctx context.Context, r Rsync, c Copy) error {
		_, err := r.Size(ctx, c)
		return err
	}},
	{"copy", []string{"--info=name1,progress2", "--stats"}, func(ctx context.Context, r Rsync, c Copy) error {
		_, err := r.Copy(ctx, c, nil)
		return err
	}},
	{"compare", []string{"--checksum", "--itemize-changes"}, func(ctx context.Context, r Rsync, c Copy) error {
		_, err := r.Compare(ctx, c)
		return err
	}},
}

func TestFindsRsync3(t *testing.T) {
	tests := []struct {
		name   string
		prints string
		want   Found
	}{
		{"Homebrew's", fixture(t, "rsync-version.txt"), Found{Version: "3.4.1", Usable: true}},
		{"the oldest that copies", "rsync  version 3.2.4  protocol version 31\n", Found{Version: "3.2.4", Usable: true}},
		{"macOS's own", fixture(t, "openrsync-version.txt"), Found{Why: "it's macOS's own, openrsync, not rsync 3"}},
		{"too old", "rsync  version 3.2.3  protocol version 31\n", Found{Version: "3.2.3", Why: "it's rsync 3.2.3, not rsync 3.2.4 or later"}},
		{"macOS's old one", "rsync  version 2.6.9  protocol version 29\n", Found{Version: "2.6.9", Why: "it's rsync 2.6.9, not rsync 3.2.4 or later"}},
		{"something else", "copier 1.0\n", Found{Why: `it says it's "copier 1.0", not rsync 3`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := runnertest.New(t)
			fake.On("rsync", "--version").Prints(tt.prints)
			got, err := NewRsync(fake).Find(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("Find() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestFindsNoRsync(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("rsync", "--version").Fails(runner.ErrNotFound)
	got, err := NewRsync(fake).Find(t.Context())
	if want := (Found{Why: "rsync isn't installed"}); err != nil || got != want {
		t.Errorf("Find() = %+v, %v; want %+v", got, err, want)
	}
}

func TestFindFailsWhenRsyncDoes(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("rsync", "--version").Exits(1).PrintsToStderr("dyld: Library not loaded")
	if _, err := NewRsync(fake).Find(t.Context()); err == nil || !strings.Contains(err.Error(), "Library not loaded") {
		t.Errorf("Find() = %v, want rsync's failure", err)
	}
}

// A folder's contents are copied into its place, the filter anchored at it;
// a file is copied as its place; over SSH, through the shell given, with
// the host's rsync. Each has the folder it's copied into made first.
func TestCopyArguments(t *testing.T) {
	const (
		filter = "/Users/someone/.local/state/kit/restore/Code.filter"
		shell  = "ssh -i /Users/someone/.ssh/id_kit -o UserKnownHostsFile=/Users/someone/.local/state/kit/known_hosts"
		remote = "/opt/homebrew/bin/rsync"
	)
	tests := []struct {
		name     string
		copy     func(home string) Copy
		args     []string
		from, to func(home string) string
	}{
		{
			name: "a folder",
			copy: func(home string) Copy {
				return Copy{From: Source{Path: "/Volumes/snapshot/Users/someone/Code"}, To: home + "/Code", Filter: filter}
			},
			args: []string{"--exclude-from=" + filter},
			from: func(string) string { return "/Volumes/snapshot/Users/someone/Code/" },
			to:   func(home string) string { return home + "/Code/" },
		},
		{
			name: "a folder written with its slash",
			copy: func(home string) Copy {
				return Copy{From: Source{Path: "/Volumes/snapshot/Users/someone/Code/"}, To: home + "/Code/"}
			},
			from: func(string) string { return "/Volumes/snapshot/Users/someone/Code/" },
			to:   func(home string) string { return home + "/Code/" },
		},
		{
			name: "a file",
			copy: func(home string) Copy {
				return Copy{From: Source{Path: "/Volumes/snapshot/Users/someone/.zsh_history"}, To: home + "/.zsh_history", File: true}
			},
			from: func(string) string { return "/Volumes/snapshot/Users/someone/.zsh_history" },
			to:   func(home string) string { return home + "/.zsh_history" },
		},
		{
			name: "a folder deep in the home",
			copy: func(home string) Copy {
				return Copy{From: Source{Path: "/Volumes/snapshot/Users/someone/Library/Application Support/Claude/claude-code-sessions"}, To: home + "/Library/Application Support/Claude/claude-code-sessions"}
			},
			from: func(string) string {
				return "/Volumes/snapshot/Users/someone/Library/Application Support/Claude/claude-code-sessions/"
			},
			to: func(home string) string { return home + "/Library/Application Support/Claude/claude-code-sessions/" },
		},
		{
			name: "a folder over SSH",
			copy: func(home string) Copy {
				return Copy{From: Source{Path: "/Users/someone/Library/Application Support/Claude/claude-code-sessions", Host: "laptop", Shell: shell, Rsync: remote}, To: home + "/Library/Application Support/Claude/claude-code-sessions", Filter: filter}
			},
			args: []string{"--exclude-from=" + filter, "--rsh=" + shell, "--rsync-path=" + remote},
			from: func(string) string {
				return "laptop:/Users/someone/Library/Application Support/Claude/claude-code-sessions/"
			},
			to: func(home string) string { return home + "/Library/Application Support/Claude/claude-code-sessions/" },
		},
		{
			name: "a file over SSH",
			copy: func(home string) Copy {
				return Copy{From: Source{Path: "/Users/someone/.zsh_history", Host: "laptop", Shell: shell, Rsync: remote}, To: home + "/.zsh_history", File: true}
			},
			args: []string{"--rsh=" + shell, "--rsync-path=" + remote},
			from: func(string) string { return "laptop:/Users/someone/.zsh_history" },
			to:   func(home string) string { return home + "/.zsh_history" },
		},
	}
	for _, tt := range tests {
		for _, op := range ops {
			t.Run(tt.name+", "+op.name, func(t *testing.T) {
				home := t.TempDir()
				c := tt.copy(home)
				fake := runnertest.New(t)
				fake.On("rsync", slices.Concat(base, tt.args, op.options, []string{tt.from(home), tt.to(home)})...).Prints(fixture(t, "copy.txt"))
				if err := op.run(t.Context(), NewRsync(fake), c); err != nil {
					t.Fatal(err)
				}
				if cmd := fake.Commands()[0]; cmd.Timeout != runner.Unlimited {
					t.Errorf("timeout = %v, want none: a copy takes as long as it takes, rsync's own --timeout ending one gone quiet", cmd.Timeout)
				}
				if info, err := os.Stat(filepath.Dir(filepath.Clean(c.To))); err != nil || !info.IsDir() {
					t.Errorf("the folder %s is copied into isn't there: %v", c.To, err)
				}
			})
		}
	}
}

// No copy carries ACLs, as Time Machine marks the folders it backs up with
// one denying files added, which a copy then can't finish; and none deletes
// anything.
func TestCopiesNeverCarryACLsOrDelete(t *testing.T) {
	home := t.TempDir()
	copies := []Copy{
		{From: Source{Path: "/Volumes/snapshot/Users/someone/Code"}, To: home + "/Code", Filter: "/tmp/filter"},
		{From: Source{Path: "/Volumes/snapshot/Users/someone/.zsh_history"}, To: home + "/.zsh_history", File: true},
		{From: Source{Path: "/Users/someone/Code", Host: "laptop", Shell: "ssh", Rsync: "/opt/homebrew/bin/rsync"}, To: home + "/Code"},
	}
	fake := runnertest.New(t)
	fake.OnMatch("rsync", func([]string) bool { return true }).Prints(fixture(t, "copy.txt"))
	for _, c := range copies {
		for _, op := range ops {
			if err := op.run(t.Context(), NewRsync(fake), c); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, cmd := range fake.Commands() {
		for _, arg := range cmd.Args {
			short := strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--")
			if arg == "--acls" || short && strings.Contains(arg, "A") || strings.HasPrefix(arg, "--del") || strings.HasPrefix(arg, "--remove-source") {
				t.Errorf("%s: %s, which carries ACLs or deletes", cmd, arg)
			}
		}
	}
}

func TestCopyRefuses(t *testing.T) {
	home := t.TempDir()
	tests := []struct {
		name string
		copy Copy
		want string
	}{
		{"a relative source", Copy{From: Source{Path: "Code"}, To: home + "/Code"}, "the path isn't absolute"},
		{"a relative destination", Copy{From: Source{Path: "/Volumes/snapshot/Code"}, To: "Code"}, "the path isn't absolute"},
		{"a home to expand", Copy{From: Source{Path: "~/Code", Host: "laptop", Shell: "ssh", Rsync: "/opt/homebrew/bin/rsync"}, To: home + "/Code"}, "the path isn't absolute"},
		{"no shell over SSH", Copy{From: Source{Path: "/Users/someone/Code", Host: "laptop", Rsync: "/opt/homebrew/bin/rsync"}, To: home + "/Code"}, "needs its shell and the host's rsync"},
		{"no rsync over SSH", Copy{From: Source{Path: "/Users/someone/Code", Host: "laptop", Shell: "ssh"}, To: home + "/Code"}, "needs its shell and the host's rsync"},
		{"an option for a host", Copy{From: Source{Path: "/Users/someone/Code", Host: "-oProxyCommand=sh", Shell: "ssh", Rsync: "/opt/homebrew/bin/rsync"}, To: home + "/Code"}, "that's no host"},
	}
	for _, tt := range tests {
		for _, op := range ops {
			t.Run(tt.name+", "+op.name, func(t *testing.T) {
				fake := runnertest.New(t)
				if err := op.run(t.Context(), NewRsync(fake), tt.copy); err == nil || !strings.Contains(err.Error(), tt.want) {
					t.Errorf("error = %v, want one saying %q", err, tt.want)
				}
				if calls := fake.Calls(); len(calls) > 0 {
					t.Errorf("ran %q, want nothing run", calls)
				}
			})
		}
	}
}

func TestCopySaysHowFarItsGot(t *testing.T) {
	home := t.TempDir()
	fake := runnertest.New(t)
	fake.OnMatch("rsync", func([]string) bool { return true }).Prints(fixture(t, "copy.txt"))
	var got []Progress
	stats, err := NewRsync(fake).Copy(t.Context(), Copy{From: Source{Path: "/Volumes/snapshot/Users/someone/Code"}, To: home + "/Code"}, func(p Progress) {
		got = append(got, p)
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []Progress{
		{Bytes: 32768, Percent: 0, Files: 1},
		{Bytes: 12345678, Percent: 24, Files: 2},
		// Part way through a file, rsync says nothing of the files: the
		// count so far stands.
		{Bytes: 20000000, Percent: 39, Files: 2},
		{Bytes: 51234567, Percent: 100, Files: 3},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("progress = %+v, want %+v", got, want)
	}
	if want := (Stats{Files: 3, Bytes: 51234567}); stats != want {
		t.Errorf("Copy() = %+v, want %+v", stats, want)
	}
}

func TestSizeSaysWhatsToCome(t *testing.T) {
	tests := []struct {
		name   string
		prints string
		want   Stats
	}{
		{"commas", fixture(t, "dry-run.txt"), Stats{Files: 12345, Bytes: 9876543210}},
		{"points, where decimals take commas", "Number of regular files transferred: 1.234\nTotal transferred file size: 5.678.901 bytes\n", Stats{Files: 1234, Bytes: 5678901}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			fake := runnertest.New(t)
			fake.OnMatch("rsync", func([]string) bool { return true }).Prints(tt.prints)
			got, err := NewRsync(fake).Size(t.Context(), Copy{From: Source{Path: "/Volumes/snapshot/Users/someone/Code"}, To: home + "/Code"})
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("Size() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSizeFailsWithoutStatistics(t *testing.T) {
	home := t.TempDir()
	fake := runnertest.New(t)
	fake.OnMatch("rsync", func([]string) bool { return true }).Prints("sent 12 bytes  received 34 bytes\n")
	if _, err := NewRsync(fake).Size(t.Context(), Copy{From: Source{Path: "/Volumes/snapshot/Code"}, To: home + "/Code"}); err == nil {
		t.Error("Size() = nil, want an error: rsync said nothing of the files")
	}
}

// The second pass counts what it copied again, a file received or a link
// made, and names the first of them; attributes set, or a folder made, are
// no copy.
func TestCompareCountsWhatItCopiedAgain(t *testing.T) {
	many := strings.Builder{}
	var manyPaths []string
	for i := range mostRecopied + 5 {
		fmt.Fprintf(&many, ">f+++++++++ lost/%02d.md\n", i)
		manyPaths = append(manyPaths, fmt.Sprintf("lost/%02d.md", i))
	}
	tests := []struct {
		name   string
		prints string
		want   Recopied
	}{
		{"nothing", "", Recopied{}},
		{"attributes alone", ".d..t...... ./\n.f....og... notes/b.md\n", Recopied{}},
		{"some", fixture(t, "compare.txt"), Recopied{Count: 5, Paths: []string{"notes/a.md", "notes/lost.md", "current", "notes/copy.md", "notes/d -> e.md"}}},
		{"more than it names", many.String(), Recopied{Count: mostRecopied + 5, Paths: manyPaths[:mostRecopied]}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			fake := runnertest.New(t)
			fake.OnMatch("rsync", func([]string) bool { return true }).Prints(tt.prints)
			got, err := NewRsync(fake).Compare(t.Context(), Copy{From: Source{Path: "/Volumes/snapshot/Users/someone/Code"}, To: home + "/Code"})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Compare() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestRsyncsExitsInItsWords(t *testing.T) {
	tests := []struct {
		name   string
		code   int
		stderr string
		want   string
	}{
		{
			name: "a partial transfer",
			code: 23,
			stderr: `rsync: [sender] send_files failed to open "/Volumes/snapshot/Users/someone/Code/locked.db": Permission denied (13)
rsync: [receiver] mkstemp "/Users/someone/Code/.locked.db.Xa1b2c" failed: Permission denied (13)
rsync error: some files/attrs were not transferred (see previous errors) (code 23) at main.c(1338) [sender=3.4.1]
`,
			want: `rsync exited 23, partial transfer due to error: rsync: [sender] send_files failed to open "/Volumes/snapshot/Users/someone/Code/locked.db": Permission denied (13) (and 1 more)`,
		},
		{
			name: "a timeout",
			code: 30,
			stderr: `[receiver] io timeout after 600 seconds -- exiting
rsync error: timeout in data send/receive (code 30) at io.c(197) [receiver=3.4.1]
`,
			want: "rsync exited 30, timeout in data send/receive: [receiver] io timeout after 600 seconds -- exiting",
		},
		{
			name:   "a connection's timeout",
			code:   35,
			stderr: "rsync error: timeout waiting for daemon connection (code 35) at socket.c(278) [sender=3.4.1]\n",
			want:   "rsync exited 35, timeout waiting for daemon connection: rsync error: timeout waiting for daemon connection (code 35) at socket.c(278) [sender=3.4.1]",
		},
		{
			name: "no SSH",
			code: 255,
			stderr: `ssh: connect to host laptop port 22: Connection refused
rsync: connection unexpectedly closed (0 bytes received so far) [Receiver]
rsync error: unexplained error (code 255) at io.c(232) [Receiver=3.4.1]
`,
			want: "rsync exited 255, unexplained error: ssh: connect to host laptop port 22: Connection refused (and 1 more)",
		},
		{
			name:   "no rsync on the host",
			code:   127,
			stderr: "zsh:1: no such file or directory: /opt/homebrew/bin/rsync\nrsync: connection unexpectedly closed (0 bytes received so far) [Receiver]\nrsync error: remote command not found (code 127) at io.c(232) [Receiver=3.4.1]\n",
			want:   "rsync exited 127, remote command not found: zsh:1: no such file or directory: /opt/homebrew/bin/rsync (and 1 more)",
		},
		{
			name: "an exit it doesn't explain",
			code: 99,
			want: "rsync exited 99, an exit rsync doesn't explain",
		},
	}
	for _, tt := range tests {
		for _, op := range ops {
			t.Run(tt.name+", "+op.name, func(t *testing.T) {
				home := t.TempDir()
				fake := runnertest.New(t)
				fake.OnMatch("rsync", func([]string) bool { return true }).Exits(tt.code).PrintsToStderr(tt.stderr)
				err := op.run(t.Context(), NewRsync(fake), Copy{From: Source{Path: "/Volumes/snapshot/Users/someone/Code"}, To: home + "/Code"})
				if err == nil || err.Error() != tt.want {
					t.Fatalf("error = %v, want %s", err, tt.want)
				}
				exit, ok := errors.AsType[*ExitError](err)
				if !ok || exit.Code != tt.code {
					t.Errorf("error = %#v, want rsync's exit %d", err, tt.code)
				}
				if errors.Is(err, ErrVanished) {
					t.Errorf("exit %d is ErrVanished, want only 24 to be", tt.code)
				}
				if full, ok := errors.AsType[*runner.ExitError](err); !ok || full.FullStderr != tt.stderr {
					t.Errorf("error = %#v, want the runner's, with all rsync printed", err)
				}
			})
		}
	}
}

// Files vanished from the source as it was copied are an error of their
// own, ErrVanished, with what was copied all the same: from a Mac in use,
// no failure.
func TestFilesVanishedAreAnErrorOfTheirOwn(t *testing.T) {
	const stderr = `file has vanished: "/Users/someone/.claude/projects/p/s.jsonl.tmp"
rsync warning: some files vanished before they could be transferred (code 24) at main.c(1338) [sender=3.4.1]
`
	const want = `rsync exited 24, partial transfer due to vanished source files: file has vanished: "/Users/someone/.claude/projects/p/s.jsonl.tmp"`
	c := Copy{From: Source{Path: "/Users/someone/.claude", Host: "laptop", Shell: "ssh", Rsync: "/opt/homebrew/bin/rsync"}, To: t.TempDir() + "/.claude"}
	vanishes := func(prints string) Rsync {
		fake := runnertest.New(t)
		fake.OnMatch("rsync", func([]string) bool { return true }).Exits(24).Prints(prints).PrintsToStderr(stderr)
		return NewRsync(fake)
	}
	check := func(op string, err error) {
		t.Helper()
		if !errors.Is(err, ErrVanished) || err.Error() != want {
			t.Errorf("%s: error = %v, want ErrVanished, saying %s", op, err, want)
		}
	}

	size, err := vanishes(fixture(t, "dry-run.txt")).Size(t.Context(), c)
	check("Size", err)
	if want := (Stats{Files: 12345, Bytes: 9876543210}); size != want {
		t.Errorf("Size() = %+v, want %+v", size, want)
	}
	copied, err := vanishes(fixture(t, "copy.txt")).Copy(t.Context(), c, nil)
	check("Copy", err)
	if want := (Stats{Files: 3, Bytes: 51234567}); copied != want {
		t.Errorf("Copy() = %+v, want %+v", copied, want)
	}
	again, err := vanishes(">f.st...... projects/p/s.jsonl\n").Compare(t.Context(), c)
	check("Compare", err)
	if want := (Recopied{Count: 1, Paths: []string{"projects/p/s.jsonl"}}); !reflect.DeepEqual(again, want) {
		t.Errorf("Compare() = %+v, want %+v", again, want)
	}
}

// A copy that doesn't run, or is ended, fails, saying so.
func TestCopyThatDoesntRun(t *testing.T) {
	fake := runnertest.New(t)
	fake.OnMatch("rsync", func([]string) bool { return true }).Fails(context.Canceled)
	_, err := NewRsync(fake).Copy(t.Context(), Copy{From: Source{Path: "/Volumes/snapshot/Code"}, To: t.TempDir() + "/Code"}, nil)
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrVanished) {
		t.Errorf("Copy() = %v, want the run's own error", err)
	}
	if _, ok := errors.AsType[*ExitError](err); ok {
		t.Errorf("Copy() = %#v, want no exit of rsync's", err)
	}
}
