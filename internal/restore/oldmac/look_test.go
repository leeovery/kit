package oldmac

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// paths are listed paths of every kind, one with spaces in it.
var paths = []string{"~/Code", "~/Library/Application Support/Claude", "~/.zsh_history", "~/Gone"}

// looked scripts ssh's look on host.
func looked(fake *runnertest.Fake, host string) *runnertest.Script {
	return fake.OnMatch("ssh", func(args []string) bool {
		return len(args) > 2 && args[len(args)-2] == host && strings.Contains(args[len(args)-1], "\nlook ")
	})
}

// macThrough is laptop, at laptopAt, reached through run.
func macThrough(run runner.Runner) *Mac {
	return &Mac{Name: "laptop", Host: laptopAt, Port: 22, User: user, Home: home, run: run, opts: sshOptions("/state/restore/known_hosts", 22)}
}

func TestLooksInOneGo(t *testing.T) {
	fake := runnertest.New(t)
	looked(fake, laptopAt).Prints("folder\x00./app/node_modules\x00./web [x]/vendor\x00folder\x00file\x00none\x00")

	got, err := macThrough(fake).Look(t.Context(), paths)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Listed{
		"~/Code":                               {Kind: Folder, Skips: []string{"app/node_modules", "web [x]/vendor"}},
		"~/Library/Application Support/Claude": {Kind: Folder},
		"~/.zsh_history":                       {Kind: File},
		"~/Gone":                               {Kind: Missing},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Look() = %v\nwant %v", got, want)
	}
	calls := fake.Commands()
	if len(calls) != 1 {
		t.Fatalf("ran %d commands, want one: %q", len(calls), fake.Calls())
	}
	args := calls[0].Args
	if wantOpts := slices.Concat(sshOptions("/state/restore/known_hosts", 22), []string{laptopAt}); !slices.Equal(args[:len(args)-1], wantOpts) {
		t.Errorf("ssh's arguments = %q, want %q and the command", args[:len(args)-1], wantOpts)
	}
	quoted := `look '\''Library/Application Support/Claude'\''`
	if cmd := args[len(args)-1]; !strings.HasPrefix(cmd, "/bin/sh -c '") || !strings.Contains(cmd, quoted) {
		t.Errorf("the command there = %s\nwant it in sh -c, quoted, holding %s", cmd, quoted)
	}
}

func TestLooksAtNothingWithNothingListed(t *testing.T) {
	got, err := macThrough(runnertest.New(t)).Look(t.Context(), nil)
	if err != nil || len(got) != 0 {
		t.Errorf("Look() = %v, %v, want nothing, without asking", got, err)
	}
}

func TestLooksRefusingAnswersItCantRead(t *testing.T) {
	tests := []struct {
		name, prints, want string
	}{
		{"cut short", "folder\x00file\x00", "it said nothing of ~/.zsh_history"},
		{"nothing", "", "it said nothing of ~/Code"},
		{"a skip before any path", "./app/node_modules\x00", `it said "./app/node_modules", which isn't an answer`},
		{"more than asked", "folder\x00folder\x00file\x00none\x00file\x00", `it said "file", which isn't an answer`},
		{"something else", "folder\x00welcome\x00", `it said "welcome", which isn't an answer`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := runnertest.New(t)
			looked(fake, laptopAt).Prints(tt.prints)
			_, err := macThrough(fake).Look(t.Context(), paths)
			if err == nil || !strings.HasSuffix(err.Error(), tt.want) {
				t.Errorf("Look() = %v, want it ending %q", err, tt.want)
			}
		})
	}
}

func TestLooksRefusingAPathNotFromHome(t *testing.T) {
	_, err := macThrough(runnertest.New(t)).Look(t.Context(), []string{"/Users/someone/Code"})
	if err == nil {
		t.Error("Look() = nil, want an error for a path not from ~")
	}
}

func TestLooksSayingSSHsFailure(t *testing.T) {
	fake := runnertest.New(t)
	refused(looked(fake, laptopAt))
	_, err := macThrough(fake).Look(t.Context(), paths)
	if want := "look at the listed paths on laptop: " + errRefusedKey.Error(); err == nil || err.Error() != want {
		t.Errorf("Look() = %v, want %q", err, want)
	}
}

func TestPatternsAreEscaped(t *testing.T) {
	l := Listed{Kind: Folder, Skips: []string{"app/node_modules", "web [x]/vendor", "a*b?/cache"}}
	want := []string{"app/node_modules", `web \[x]/vendor`, `a\*b\?/cache`}
	if got := l.Patterns(); !slices.Equal(got, want) {
		t.Errorf("Patterns() = %q, want %q", got, want)
	}
}

// A real shell, find and sh, run the look on a home folder made for it, ssh
// a stand-in that runs its command through sh as the old Mac's shell would:
// what's marked left out of backups is skipped, but a folder holding git
// checkouts, and the listed path itself; quoting holds through both shells.
func TestLooksForRealThroughTheShell(t *testing.T) {
	homeDir := t.TempDir()
	marked := func(rel string) string {
		p := filepath.Join(homeDir, rel)
		if err := unix.Setxattr(p, excludedAttr, []byte("bplist"), 0); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write := func(rel string) {
		p := filepath.Join(homeDir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{
		"Code/app/node_modules/left/index.js",
		"Code/app/.claude/worktrees/feature/.git",
		"Code/app/.claude/worktrees/feature/node_modules/kept/index.js",
		"Code/app/main.go",
		"Code/empty/.claude/worktrees/.keep",
		"Code/web [x]/vendor/it's/autoload.php",
		"Code/notes.txt",
		"Library/Application Support/Claude/Cache/blob",
		"Library/Application Support/Claude/sessions/one.json",
		"Listed/inner/file",
		".zsh_history",
	} {
		write(f)
	}
	for _, m := range []string{
		"Code/app/node_modules", "Code/app/.claude/worktrees", "Code/app/.claude/worktrees/feature/node_modules",
		"Code/empty/.claude/worktrees", "Code/web [x]/vendor", "Code/notes.txt",
		"Library/Application Support/Claude/Cache", "Listed",
	} {
		marked(m)
	}
	if err := os.Symlink("Code", filepath.Join(homeDir, "Link")); err != nil {
		t.Fatal(err)
	}

	bin := t.TempDir()
	stub := "#!/bin/sh\nfor a; do last=$a; done\nexec /bin/sh -c \"$last\"\n"
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(stub), 0o700); err != nil {
		t.Fatal(err)
	}
	run := runner.Exec{Path: []string{bin}, Env: []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + homeDir}, Now: time.Now}

	got, err := macThrough(run).Look(t.Context(), []string{"~/Code", "~/Library/Application Support/Claude", "~/Listed", "~/.zsh_history", "~/Link", "~/Gone"})
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range got {
		slices.Sort(l.Skips)
	}
	want := map[string]Listed{
		"~/Code":                               {Kind: Folder, Skips: []string{"app/node_modules", "empty/.claude/worktrees", "notes.txt", "web [x]/vendor"}},
		"~/Library/Application Support/Claude": {Kind: Folder, Skips: []string{"Cache"}},
		"~/Listed":                             {Kind: Folder},
		"~/.zsh_history":                       {Kind: File},
		"~/Link":                               {Kind: File},
		"~/Gone":                               {Kind: Missing},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Look() = %v\nwant %v", got, want)
	}
}
