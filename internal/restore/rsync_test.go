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
	"time"

	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// The fixtures in testdata are rsync 3.5.2's own output, captured from real
// runs in a scratch folder, renamed /Volumes/Scratch/restore: --version
// (and macOS's openrsync's); a copy's --info=progress2,skip1 --stats, its
// carriage returns kept (copy.raw; vanished.raw, a 200 MB file copied
// slowly, its bandwidth limited, while a later file was deleted;
// copy-newer.raw, a file's copy over one newer there, which --update kept,
// and copy-newer-escaped.raw, the same of a file with a newline in its
// name; copy-newer-folder.raw, a folder's, four files in it newer there,
// two named with what rsync escapes, a newline and a backslash, and a file
// named "x is newer" copied; copy-again.raw, the same folder's copy again,
// of those four alone, set aside, named on its standard input with --from0
// --files-from=-); a dry run's --stats (size.raw);
// a second pass's --itemize-changes (compare.raw, after a file's contents
// were changed, others' times, permissions and attributes, and a file, a
// link, a hard link and an empty folder deleted; compare-skipping.raw,
// with --no-specials and --no-devices, past a socket and a pipe, a file's
// contents changed); and its standard error
// as it exited 23, 24, 30, 35, 127 and 255 (127 and 255 through a stand-in
// for ssh, its own line its words).

func fixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// partial is the folder for files not finished that every copy in these
// tests is given.
const partial = "/Users/someone/.local/state/kit/restore/partial"

// base are the options of every copy, dry run and second pass.
var base = []string{"--archive", "--no-specials", "--no-devices", "--xattrs", "--hard-links", "--crtimes", "--mkpath", "--update", "--partial-dir=" + partial}

// local is a copy of the folder at path on a snapshot to to.
func local(path, to string) Copy {
	return Copy{From: Source{Path: "/Volumes/snapshot" + path}, To: to, Partial: partial}
}

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
	{"copy", []string{"--info=progress2,skip1", "--stats"}, func(ctx context.Context, r Rsync, c Copy) error {
		_, err := r.Copy(ctx, c, nil)
		return err
	}},
	{"compare", []string{"--checksum", "--itemize-changes"}, func(ctx context.Context, r Rsync, c Copy) error {
		_, err := r.Compare(ctx, c)
		return err
	}},
}

// prints has the fake's rsync, run with anything, print stdout.
func prints(t *testing.T, stdout string) *runnertest.Fake {
	fake := runnertest.New(t)
	fake.OnMatch("rsync", func([]string) bool { return true }).Prints(stdout)
	return fake
}

// realRsync is the real runner, with an rsync on its PATH that prints the
// fixture stdout as it is, carriage returns and all, and the fixture stderr
// on its standard error, then exits exit: what rsync printed, through the
// runner's own reading of it.
func realRsync(t *testing.T, stdout, stderr string, exit int) Rsync {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n"
	for _, out := range []struct{ name, to string }{{stdout, ""}, {stderr, " >&2"}} {
		if out.name == "" {
			continue
		}
		path, err := filepath.Abs(filepath.Join("testdata", out.name))
		if err != nil {
			t.Fatal(err)
		}
		script += fmt.Sprintf("/bin/cat '%s'%s\n", path, out.to)
	}
	script += fmt.Sprintf("exit %d\n", exit)
	if err := os.WriteFile(filepath.Join(dir, "rsync"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return NewRsync(runner.Exec{Path: []string{dir}, Env: []string{"PATH=" + dir}, Now: time.Now})
}

func TestFindsRsync3(t *testing.T) {
	tests := []struct {
		name   string
		prints string
		want   Found
	}{
		{"Homebrew's", fixture(t, "rsync-version.txt"), Found{Version: "3.5.2", Usable: true}},
		{"the oldest that copies", "rsync  version 3.2.4  protocol version 31\nCapabilities:\n    xattrs, crtimes\n", Found{Version: "3.2.4", Usable: true}},
		{"macOS's own", fixture(t, "openrsync-version.txt"), Found{Why: "it's macOS's own, openrsync, not rsync 3"}},
		{"too old", "rsync  version 3.2.3  protocol version 31\nCapabilities:\n    xattrs, crtimes\n", Found{Version: "3.2.3", Why: "it's rsync 3.2.3, not rsync 3.2.4 or later"}},
		{"macOS's old one", "rsync  version 2.6.9  protocol version 29\n", Found{Version: "2.6.9", Why: "it's rsync 2.6.9, not rsync 3.2.4 or later"}},
		{"without creation dates", "rsync  version 3.5.2  protocol version 33\nCapabilities:\n    hardlinks, ACLs,\n    xattrs, no crtimes\n", Found{Version: "3.5.2", Why: "it's rsync 3.5.2, built without crtimes"}},
		{"without extended attributes", "rsync  version 3.5.2  protocol version 33\nCapabilities:\n    no xattrs, crtimes\n", Found{Version: "3.5.2", Why: "it's rsync 3.5.2, built without xattrs"}},
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
// a file is copied as its place; files of a folder copied alone are named
// on rsync's standard input, set apart by NULs, as a name can hold a
// newline, and nothing else is; over SSH, through the shell given, with
// the host's rsync, ended if it goes quiet. rsync makes the folders its
// place is in (--mkpath); the driver makes nothing.
func TestCopyArguments(t *testing.T) {
	const (
		filter = "/Users/someone/.local/state/kit/restore/Code.filter"
		shell  = "ssh -i /Users/someone/.ssh/id_kit -o UserKnownHostsFile=/Users/someone/.local/state/kit/known_hosts"
		remote = "/opt/homebrew/bin/rsync"
		app    = "/Users/someone/Library/Application Support/Claude/claude-code-sessions"
	)
	overSSH := []string{"--timeout=600", "--rsh=" + shell, "--rsync-path=" + remote}
	tests := []struct {
		name     string
		copy     func(home string) Copy
		args     []string
		from, to func(home string) string
		input    string
	}{
		{
			name: "a folder",
			copy: func(home string) Copy {
				c := local("/Users/someone/Code", home+"/Code")
				c.Filter = filter
				return c
			},
			args: []string{"--exclude-from=" + filter},
			from: func(string) string { return "/Volumes/snapshot/Users/someone/Code/" },
			to:   func(home string) string { return home + "/Code/" },
		},
		{
			name: "a folder written with its slash",
			copy: func(home string) Copy { return local("/Users/someone/Code/", home+"/Code/") },
			from: func(string) string { return "/Volumes/snapshot/Users/someone/Code/" },
			to:   func(home string) string { return home + "/Code/" },
		},
		{
			name: "files of a folder alone",
			copy: func(home string) Copy {
				c := local("/Users/someone/.claude", home+"/.claude")
				c.Only = []string{"history.jsonl", "projects/site/odd\nname"}
				return c
			},
			args:  []string{"--from0", "--files-from=-"},
			from:  func(string) string { return "/Volumes/snapshot/Users/someone/.claude/" },
			to:    func(home string) string { return home + "/.claude/" },
			input: "history.jsonl\x00projects/site/odd\nname",
		},
		{
			name: "a file",
			copy: func(home string) Copy {
				c := local("/Users/someone/.zsh_history", home+"/.zsh_history")
				c.File = true
				return c
			},
			from: func(string) string { return "/Volumes/snapshot/Users/someone/.zsh_history" },
			to:   func(home string) string { return home + "/.zsh_history" },
		},
		{
			name: "a folder deep in the home",
			copy: func(home string) Copy {
				return local(app, home+"/Library/Application Support/Claude/claude-code-sessions")
			},
			from: func(string) string { return "/Volumes/snapshot" + app + "/" },
			to:   func(home string) string { return home + "/Library/Application Support/Claude/claude-code-sessions/" },
		},
		{
			name: "a folder over SSH",
			copy: func(home string) Copy {
				return Copy{From: Source{Path: app, Host: "laptop", Shell: shell, Rsync: remote}, To: home + "/Library/Application Support/Claude/claude-code-sessions", Filter: filter, Partial: partial}
			},
			args: append([]string{"--exclude-from=" + filter}, overSSH...),
			from: func(string) string { return "laptop:" + app + "/" },
			to:   func(home string) string { return home + "/Library/Application Support/Claude/claude-code-sessions/" },
		},
		{
			name: "a file over SSH",
			copy: func(home string) Copy {
				return Copy{From: Source{Path: "/Users/someone/.zsh_history", Host: "laptop", Shell: shell, Rsync: remote}, To: home + "/.zsh_history", File: true, Partial: partial}
			},
			args: overSSH,
			from: func(string) string { return "laptop:/Users/someone/.zsh_history" },
			to:   func(home string) string { return home + "/.zsh_history" },
		},
	}
	for _, tt := range tests {
		for _, op := range ops {
			t.Run(tt.name+", "+op.name, func(t *testing.T) {
				home := filepath.Join(t.TempDir(), "home")
				c := tt.copy(home)
				fake := runnertest.New(t)
				fake.On("rsync", slices.Concat(base, tt.args, op.options, []string{tt.from(home), tt.to(home)})...).Prints(fixture(t, "copy.raw"))
				if err := op.run(t.Context(), NewRsync(fake), c); err != nil {
					t.Fatal(err)
				}
				cmd := fake.Commands()[0]
				if cmd.Input != tt.input {
					t.Errorf("input = %q, want %q", cmd.Input, tt.input)
				}
				if cmd.Timeout != runner.Unlimited {
					t.Errorf("timeout = %v, want none: a copy takes as long as it takes", cmd.Timeout)
				}
				if streamed := op.name == "copy"; cmd.Redraws != streamed || cmd.LinesOnly != streamed {
					t.Errorf("redraws, lines only = %t, %t; want %t: a copy's progress given as it's drawn, its output not kept", cmd.Redraws, cmd.LinesOnly, streamed)
				}
				if _, err := os.Stat(home); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("the driver made %s, or can't tell: %v; want rsync to make what it needs", home, err)
				}
			})
		}
	}
}

// No copy carries ACLs, as Time Machine marks the folders it backs up with
// one denying files added, which a copy then can't finish; none deletes
// anything; and none replaces a file newer here than at its source, so the
// person's edits outlive a copy run again and the second pass.
func TestCopiesNeverCarryACLsDeleteOrReplaceNewerFiles(t *testing.T) {
	home := t.TempDir()
	file := local("/Users/someone/.zsh_history", home+"/.zsh_history")
	file.File = true
	copies := []Copy{
		local("/Users/someone/Code", home+"/Code"),
		file,
		{From: Source{Path: "/Users/someone/Code", Host: "laptop", Shell: "ssh", Rsync: "/opt/homebrew/bin/rsync"}, To: home + "/Code", Partial: partial},
	}
	fake := prints(t, fixture(t, "copy.raw"))
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
		if !slices.Contains(cmd.Args, "--update") {
			t.Errorf("%s: no --update, so it replaces files newer here than at the source", cmd)
		}
	}
}

// The files the copy kept, newer here than at the source, as --update
// keeps them, are named as rsync 3.5.2 says each under --info=skip1, its
// name put back as it was where rsync escapes it: a file's by its name
// (copy-newer.raw, copy-newer-escaped.raw); a folder's each from the folder
// (copy-newer-folder.raw), never a file copied, though named "x is newer".
// A copy keeping none names none.
func TestCopySaysWhatItKeptAsNewer(t *testing.T) {
	file := Copy{From: Source{Path: "/Volumes/snapshot/Users/someone/.zsh_history"}, To: t.TempDir() + "/.zsh_history", Partial: partial, File: true}
	folder := local("/Users/someone/.claude", t.TempDir()+"/.claude")
	for _, tt := range []struct {
		name  string
		c     Copy
		rsync Rsync
		newer []string
	}{
		{"a file kept", file, realRsync(t, "copy-newer.raw", "", 0), []string{".zsh_history"}},
		{"a file kept, its name escaped", Copy{From: Source{Path: "/Volumes/snapshot/Users/someone/nl\nname"}, To: t.TempDir() + "/nl\nname", Partial: partial, File: true}, realRsync(t, "copy-newer-escaped.raw", "", 0), []string{"nl\nname"}},
		{"a file copied", file, realRsync(t, "copy.raw", "", 0), nil},
		{"files in a folder kept", folder, realRsync(t, "copy-newer-folder.raw", "", 0),
			[]string{"history.jsonl", `projects/site/back\#123slash`, "projects/site/odd\nname", "projects/site/session.jsonl"}},
		{"files in a folder copied again", folder, realRsync(t, "copy-again.raw", "", 0), nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stats, err := tt.rsync.Copy(t.Context(), tt.c, nil)
			if err != nil || !slices.Equal(stats.Newer, tt.newer) {
				t.Errorf("Copy() kept %q as newer, %v; want %q", stats.Newer, err, tt.newer)
			}
		})
	}
}

// Only over SSH is a copy that goes quiet ended: on this Mac, rsync says
// nothing while it checksums a file, which on a slow disk can take longer
// than any limit.
func TestOnlyACopyOverSSHTimesOut(t *testing.T) {
	home := t.TempDir()
	fake := prints(t, fixture(t, "copy.raw"))
	for _, c := range []Copy{local("/Users/someone/Code", home+"/Code"), {From: Source{Path: "/Users/someone/Code", Host: "laptop", Shell: "ssh", Rsync: "/opt/homebrew/bin/rsync"}, To: home + "/Code", Partial: partial}} {
		for _, op := range ops {
			if err := op.run(t.Context(), NewRsync(fake), c); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, cmd := range fake.Commands() {
		overSSH := slices.ContainsFunc(cmd.Args, func(a string) bool { return strings.HasPrefix(a, "--rsh=") })
		if timesOut := slices.ContainsFunc(cmd.Args, func(a string) bool { return strings.HasPrefix(a, "--timeout") }); timesOut != overSSH {
			t.Errorf("%s: times out %t, want %t", cmd, timesOut, overSSH)
		}
	}
}

func TestCopyRefuses(t *testing.T) {
	home := t.TempDir()
	folder := filepath.Join(home, "Code")
	if err := os.Mkdir(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	remote := Source{Path: "/Users/someone/Code", Host: "laptop", Shell: "ssh", Rsync: "/opt/homebrew/bin/rsync"}
	tests := []struct {
		name string
		copy Copy
		want string
	}{
		{"a relative source", Copy{From: Source{Path: "Code"}, To: home + "/Code", Partial: partial}, "the path isn't absolute"},
		{"a relative destination", Copy{From: Source{Path: "/Volumes/snapshot/Code"}, To: "Code", Partial: partial}, "the path isn't absolute"},
		{"a home to expand", Copy{From: Source{Path: "~/Code", Host: "laptop", Shell: "ssh", Rsync: "/opt/homebrew/bin/rsync"}, To: home + "/Code", Partial: partial}, "the path isn't absolute"},
		{"no folder for files not finished", Copy{From: Source{Path: "/Volumes/snapshot/Code"}, To: home + "/Code"}, "isn't absolute"},
		{"a relative folder for files not finished", Copy{From: Source{Path: "/Volumes/snapshot/Code"}, To: home + "/Code", Partial: ".rsync-partial"}, "isn't absolute"},
		{"no shell over SSH", Copy{From: Source{Path: "/Users/someone/Code", Host: "laptop", Rsync: "/opt/homebrew/bin/rsync"}, To: home + "/Code", Partial: partial}, "needs its shell and the host's rsync"},
		{"no rsync over SSH", Copy{From: Source{Path: "/Users/someone/Code", Host: "laptop", Shell: "ssh"}, To: home + "/Code", Partial: partial}, "needs its shell and the host's rsync"},
		{"an option for a host", Copy{From: Source{Path: "/Users/someone/Code", Host: "-oProxyCommand=sh", Shell: "ssh", Rsync: "/opt/homebrew/bin/rsync"}, To: home + "/Code", Partial: partial}, "that's no host"},
		{"a folder here as a file", Copy{From: Source{Path: folder}, To: home + "/elsewhere", File: true, Partial: partial}, "it's a folder"},
		{"a file onto a folder", Copy{From: remote, To: folder, File: true, Partial: partial}, "it's a folder"},
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

// What rsync prints, through the runner's own reading of it, gives the copy's
// progress as rsync draws it, part way through a file included, and its
// statistics, though none of its output is kept.
func TestCopySaysHowFarItsGot(t *testing.T) {
	var got []Progress
	stats, err := realRsync(t, "copy.raw", "", 0).Copy(t.Context(), local("/Users/someone/Code", t.TempDir()+"/Code"), func(p Progress) {
		got = append(got, p)
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []Progress{
		// Part way through the first file, before rsync says it's copied.
		{Bytes: 32768, Percent: 0, Files: 0},
		{Bytes: 50000000, Percent: 98, Files: 1},
		{Bytes: 50300000, Percent: 98, Files: 2},
		{Bytes: 50600000, Percent: 99, Files: 3},
		{Bytes: 50900000, Percent: 99, Files: 4},
		// Drawn four times as it ends: said once.
		{Bytes: 50900003, Percent: 100, Files: 5},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("progress = %+v, want %+v", got, want)
	}
	if want := (Stats{Files: 5, Bytes: 50900003, TotalFiles: 5, TotalBytes: 50900003}); !reflect.DeepEqual(stats, want) {
		t.Errorf("Copy() = %+v, want %+v", stats, want)
	}
}

// rsync's statistics say what it copies, or would, and what the set it
// copies from holds, copied or there already: its regular files, never its
// folders or links, and their bytes.
func TestSizeSaysWhatsToCome(t *testing.T) {
	tests := []struct {
		name   string
		prints string
		want   Stats
	}{
		{"commas", fixture(t, "size.raw"), Stats{Files: 5, Bytes: 50900003, TotalFiles: 5, TotalBytes: 50900003}},
		{"points, where decimals take commas", "Number of files: 1.240 (reg: 1.234, dir: 6)\nNumber of regular files transferred: 1.234\nTotal file size: 5.678.901 bytes\nTotal transferred file size: 5.678.901 bytes\n",
			Stats{Files: 1234, Bytes: 5678901, TotalFiles: 1234, TotalBytes: 5678901}},
		{"part copied already", "Number of files: 12,031 (reg: 10,512, dir: 1,402, link: 117)\nNumber of regular files transferred: 2,040\nTotal file size: 9,120,004,113 bytes\nTotal transferred file size: 1,016,330 bytes\n",
			Stats{Files: 2040, Bytes: 1016330, TotalFiles: 10512, TotalBytes: 9120004113}},
		{"folders alone", "Number of files: 3 (dir: 3)\nNumber of regular files transferred: 0\nTotal file size: 0 bytes\nTotal transferred file size: 0 bytes\n", Stats{}},
		{"nothing", "Number of files: 0\nNumber of regular files transferred: 0\nTotal file size: 0 bytes\nTotal transferred file size: 0 bytes\n", Stats{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewRsync(prints(t, tt.prints)).Size(t.Context(), local("/Users/someone/Code", t.TempDir()+"/Code"))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Size() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// Statistics that don't say what was copied, or what the set holds, fail
// the copy and its size.
func TestStatisticsMissingFail(t *testing.T) {
	for _, said := range []string{
		"sent 12 bytes  received 34 bytes\n",
		"Number of regular files transferred: 5\nTotal transferred file size: 50,900,003 bytes\n",
	} {
		for _, op := range ops[:2] {
			t.Run(op.name, func(t *testing.T) {
				fake := prints(t, said)
				if err := op.run(t.Context(), NewRsync(fake), local("/Code", t.TempDir()+"/Code")); err == nil {
					t.Errorf("rsync saying %q, error = nil, want one: it said nothing of the files", said)
				}
			})
		}
	}
}

// The second pass counts what it copied again, a file received, or a link
// or folder made, and names the first of them; attributes alone set are no
// copy.
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
		{"attributes alone", ".d..t...... ./\n.f...p..... notes/b.md\n.f........x notes/c.md\n.f..t...n.. photos/big.raw\n", Recopied{}},
		{"some", fixture(t, "compare.raw"), Recopied{Count: 6, Paths: []string{"current", "empty/", "notes/a.md", "notes/d -> e.md", "notes/lost.md", "notes/copy.md"}}},
		{"past what isn't a file", fixture(t, "compare-skipping.raw"), Recopied{Count: 1, Paths: []string{"a.txt"}}},
		{"more than it names", many.String(), Recopied{Count: mostRecopied + 5, Paths: manyPaths[:mostRecopied]}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewRsync(prints(t, tt.prints)).Compare(t.Context(), local("/Users/someone/Code", t.TempDir()+"/Code"))
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
		code   int
		stderr string
		want   string
	}{
		{23, fixture(t, "exit23.stderr"), `rsync exited 23, partial transfer due to error: rsync: [sender] get_xattr_names: llistxattr("/Volumes/Scratch/restore/e23/src/./locked",1024) failed: Permission denied (13)`},
		{30, fixture(t, "exit30.stderr"), "rsync exited 30, timeout in data send/receive: [Receiver] io timeout after 1 seconds -- exiting (and 1 more)"},
		{35, fixture(t, "exit35.stderr"), "rsync exited 35, timeout waiting for daemon connection: rsync error: timeout waiting for daemon connection (code 35) at socket.c(437) [Receiver=3.5.2]"},
		{127, fixture(t, "exit127.stderr"), "rsync exited 127, remote command not found: /bin/sh: /nonexistent/rsync: No such file or directory (and 1 more)"},
		{255, fixture(t, "exit255.stderr"), "rsync exited 255, unexplained error: ssh: connect to host laptop port 22: Connection refused (and 1 more)"},
		{15, "", "rsync exited 15, sibling process crashed"},
		{16, "", "rsync exited 16, sibling process was killed by a signal"},
		{19, "", "rsync exited 19, received SIGUSR1"},
		{99, "", "rsync exited 99, an exit rsync doesn't explain"},
	}
	for _, tt := range tests {
		for _, op := range ops {
			t.Run(fmt.Sprintf("%d, %s", tt.code, op.name), func(t *testing.T) {
				fake := runnertest.New(t)
				fake.OnMatch("rsync", func([]string) bool { return true }).Exits(tt.code).PrintsToStderr(tt.stderr)
				err := op.run(t.Context(), NewRsync(fake), local("/Users/someone/Code", t.TempDir()+"/Code"))
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
	const want = `rsync exited 24, partial transfer due to vanished source files: file has vanished: "/Volumes/Scratch/restore/e24/src/z/session.tmp"`
	c := Copy{From: Source{Path: "/Users/someone/.claude", Host: "laptop", Shell: "ssh", Rsync: "/opt/homebrew/bin/rsync"}, To: t.TempDir() + "/.claude", Partial: partial}
	check := func(op string, err error) {
		t.Helper()
		if !errors.Is(err, ErrVanished) || err.Error() != want {
			t.Errorf("%s: error = %v, want ErrVanished, saying %s", op, err, want)
		}
	}
	vanishes := func(prints string) Rsync {
		fake := runnertest.New(t)
		fake.OnMatch("rsync", func([]string) bool { return true }).Exits(24).Prints(prints).PrintsToStderr(fixture(t, "vanished.stderr"))
		return NewRsync(fake)
	}

	size, err := vanishes(fixture(t, "size.raw")).Size(t.Context(), c)
	check("Size", err)
	if want := (Stats{Files: 5, Bytes: 50900003, TotalFiles: 5, TotalBytes: 50900003}); !reflect.DeepEqual(size, want) {
		t.Errorf("Size() = %+v, want %+v", size, want)
	}
	var progress []Progress
	copied, err := realRsync(t, "vanished.raw", "vanished.stderr", 24).Copy(t.Context(), c, func(p Progress) { progress = append(progress, p) })
	check("Copy", err)
	if want := (Stats{Files: 3, Bytes: 209715210, TotalFiles: 3, TotalBytes: 209715210}); !reflect.DeepEqual(copied, want) {
		t.Errorf("Copy() = %+v, want %+v", copied, want)
	}
	wantProgress := []Progress{
		{32768, 0, 0}, {23855104, 11, 0}, {48824320, 23, 0}, {74711040, 35, 0}, {100401152, 47, 0},
		{124944384, 59, 0}, {150896640, 71, 0}, {174292992, 83, 0}, {200245248, 95, 0},
		{209715200, 99, 1}, {209715205, 99, 2}, {209715210, 100, 3},
	}
	if !reflect.DeepEqual(progress, wantProgress) {
		t.Errorf("progress = %+v, want %+v, each drawn part way through the file", progress, wantProgress)
	}
	again, err := vanishes(">fc........ projects/p/s.jsonl\n").Compare(t.Context(), c)
	check("Compare", err)
	if want := (Recopied{Count: 1, Paths: []string{"projects/p/s.jsonl"}}); !reflect.DeepEqual(again, want) {
		t.Errorf("Compare() = %+v, want %+v", again, want)
	}
}

// A copy that doesn't run, or is ended, fails, saying so.
func TestCopyThatDoesntRun(t *testing.T) {
	fake := runnertest.New(t)
	fake.OnMatch("rsync", func([]string) bool { return true }).Fails(context.Canceled)
	_, err := NewRsync(fake).Copy(t.Context(), local("/Code", t.TempDir()+"/Code"), nil)
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrVanished) {
		t.Errorf("Copy() = %v, want the run's own error", err)
	}
	if _, ok := errors.AsType[*ExitError](err); ok {
		t.Errorf("Copy() = %#v, want no exit of rsync's", err)
	}
}
