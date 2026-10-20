package gitrepo_test

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

const dir = "/Users/someone/.config/kit"

func git(args ...string) []string { return append([]string{"-C", dir}, args...) }

// commitCase is a commit of paths, git's answers to it as real git gives
// them, and the commands kit runs: status of the paths, what update-index
// stages (none: it doesn't run), what's staged of the paths once it has,
// and the paths committed (none: no commit).
type commitCase struct {
	name   string
	paths  []string
	status string
	update []string
	staged string
	commit []string
}

// run scripts git's answers to c, runs Commit, and checks the commands it
// ran, in order, and that it committed when, and only when, it ran commit.
func (c commitCase) run(t *testing.T) {
	t.Helper()
	fake := runnertest.New(t)
	want := [][]string{git(slices.Concat([]string{"--literal-pathspecs", "status", "--porcelain", "-z", "--untracked-files=all", "--no-renames", "--"}, c.paths)...)}
	fake.On("git", want[0]...).Prints(c.status)
	if c.update != nil {
		want = append(want, git(slices.Concat([]string{"update-index", "--add", "--remove", "--"}, c.update)...))
		fake.On("git", want[len(want)-1]...)
		want = append(want, git(slices.Concat([]string{"--literal-pathspecs", "diff", "--cached", "--name-only", "-z", "--no-renames", "--"}, c.paths)...))
		fake.On("git", want[len(want)-1]...).Prints(c.staged)
	}
	if c.commit != nil {
		want = append(want, git(slices.Concat([]string{"--literal-pathspecs", "commit", "--quiet", "-m", "kit test", "--"}, c.commit)...))
		fake.On("git", want[len(want)-1]...)
	}
	committed, err := gitrepo.Repo{Dir: dir, Run: fake}.Commit(context.Background(), "kit test", c.paths...)
	if err != nil || committed != (c.commit != nil) {
		t.Errorf("%s: Commit() = %v, %v; want committed %v", c.name, committed, err, c.commit != nil)
	}
	var ran [][]string
	for _, cmd := range fake.Commands() {
		ran = append(ran, cmd.Args)
	}
	if !slices.EqualFunc(ran, want, slices.Equal) {
		t.Errorf("%s: ran\n%s\nwant\n%s", c.name, strings.Join(fake.Calls(), "\n"), want)
	}
}

func TestCommit(t *testing.T) {
	commitCase{
		name: "an edit", paths: []string{"laptop/declarations"},
		status: " M laptop/declarations\x00", update: []string{"laptop/declarations"},
		staged: "laptop/declarations\x00", commit: []string{"laptop/declarations"},
	}.run(t)
}

// Every change under the paths goes in whole, file by file, whatever the
// user's config says: a new folder's every file, a link, a deletion staged
// or not, both sides of a rename, names with spaces, " -> ", unicode or a
// pattern's characters. A path holding no change is left out of the
// commit, as is anything else staged, which status of the paths never
// names. Each status is what real git prints.
func TestCommitTakesEachChangeWhole(t *testing.T) {
	for _, c := range []commitCase{
		{
			name:   "a new folder hidden by status.showUntrackedFiles=no",
			paths:  []string{"laptop/steps/tool"},
			status: "?? laptop/steps/tool/run\x00", update: []string{"laptop/steps/tool/run"},
			staged: "laptop/steps/tool/run\x00", commit: []string{"laptop/steps/tool"},
		},
		{
			name:   "deletions staged and not, a new folder with a link, unicode, and a path unchanged",
			paths:  []string{"laptop/declarations", "laptop/steps/tool", "old", "shared/steps/tool", "studio/steps/tool"},
			status: " M laptop/declarations\x00D  old/run\x00 D studio/steps/tool/run\x00?? shared/steps/tool/café\x00?? shared/steps/tool/link\x00?? shared/steps/tool/run\x00",
			update: []string{"laptop/declarations", "old/run", "studio/steps/tool/run", "shared/steps/tool/café", "shared/steps/tool/link", "shared/steps/tool/run"},
			staged: "laptop/declarations\x00old/run\x00shared/steps/tool/café\x00shared/steps/tool/link\x00shared/steps/tool/run\x00studio/steps/tool/run\x00",
			commit: []string{"laptop/declarations", "old", "shared/steps/tool", "studio/steps/tool"},
		},
		{
			name:   "a rename the user staged, both sides given, named with an arrow, status.renames on",
			paths:  []string{"p -> q", "r s"},
			status: "D  p -> q\x00A  r s\x00", update: []string{"p -> q", "r s"},
			staged: "p -> q\x00r s\x00", commit: []string{"p -> q", "r s"},
		},
		{
			name:   "a rename the user staged, its new side alone given",
			paths:  []string{"b"},
			status: "A  b\x00", update: []string{"b"},
			staged: "b\x00", commit: []string{"b"},
		},
		{
			name:   "a name that's a pattern, beside a file it would match",
			paths:  []string{"a*"},
			status: " M a*\x00", update: []string{"a*"},
			staged: "a*\x00", commit: []string{"a*"},
		},
	} {
		c.run(t)
	}
}

// What git can't keep is left out, never failing the rest: an ignored file
// and an empty folder, which git status never names; a path gone that git
// never kept; a file added, then gone before it was committed (AD), which
// update-index takes out of the index, so it can't block a later commit;
// and a nested repository, which update-index ignores.
func TestCommitLeavesOutWhatGitCantKeep(t *testing.T) {
	for _, c := range []commitCase{
		{
			name:   "an ignored file and an empty folder",
			paths:  []string{"emptyd", "ign", "other"},
			status: "?? other\x00", update: []string{"other"},
			staged: "other\x00", commit: []string{"other"},
		},
		{
			name:   "a path gone that git never kept",
			paths:  []string{"gone", "other"},
			status: "?? other\x00", update: []string{"other"},
			staged: "other\x00", commit: []string{"other"},
		},
		{
			name:  "an ignored file alone",
			paths: []string{"ign"},
		},
		{
			name:   "a file added then gone, beside an edit",
			paths:  []string{"decl", "steps/tool"},
			status: " M decl\x00AD steps/tool/run\x00", update: []string{"decl", "steps/tool/run"},
			staged: "decl\x00", commit: []string{"decl"},
		},
		{
			name:   "a file added then gone, its declaration put back as committed",
			paths:  []string{"decl", "steps/tool"},
			status: "MM decl\x00AD steps/tool/run\x00", update: []string{"decl", "steps/tool/run"},
		},
		{
			name:   "an edit staged, then put back as committed",
			paths:  []string{"decl"},
			status: "MM decl\x00", update: []string{"decl"},
		},
		{
			name:   "a nested repository",
			paths:  []string{"laptop"},
			status: "?? laptop/sub/\x00", update: []string{"laptop/sub/"},
		},
	} {
		c.run(t)
	}
}

func TestCommitWithNothingToCommit(t *testing.T) {
	commitCase{name: "nothing changed", paths: []string{"brew"}}.run(t)
	fake := runnertest.New(t)
	if committed, err := (gitrepo.Repo{Dir: dir, Run: fake}).Commit(context.Background(), "kit test"); committed || err != nil || len(fake.Calls()) != 0 {
		t.Errorf("Commit() of no path = %v, %v, running %q; want nothing run: a commit given no path takes whatever's staged", committed, err, fake.Calls())
	}
}

func TestCommitFails(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("git", git("--literal-pathspecs", "status", "--porcelain", "-z", "--untracked-files=all", "--no-renames", "--", "brew")...).Prints(" M brew\x00")
	fake.On("git", git("update-index", "--add", "--remove", "--", "brew")...)
	fake.On("git", git("--literal-pathspecs", "diff", "--cached", "--name-only", "-z", "--no-renames", "--", "brew")...).Prints("brew\x00")
	fake.On("git", git("--literal-pathspecs", "commit", "--quiet", "-m", "kit add brew jq", "--", "brew")...).Exits(1).PrintsToStderr("error: gpg failed to sign the data")
	if _, err := (gitrepo.Repo{Dir: dir, Run: fake}).Commit(context.Background(), "kit add brew jq", "brew"); err == nil || !strings.Contains(err.Error(), "gpg failed to sign") {
		t.Errorf("Commit() error = %v, want git's", err)
	}
}

// Changes reads status whole, whatever the user's config says: a staged
// rename as its two sides, named with an arrow; unicode unquoted; new
// files hidden by status.showUntrackedFiles=no; a file added then gone is
// no change; one staged as deleted and back untracked is edited. Status
// and the counts are what real git prints.
func TestChanges(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "n ew"), []byte("n\nw\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	in := func(args ...string) []string { return append([]string{"-C", root}, args...) }
	fake := runnertest.New(t)
	fake.On("git", in("--literal-pathspecs", "status", "--porcelain", "-z", "--untracked-files=all", "--no-renames", "--")...).
		Prints(" M café\x00AD gone\x00D  kept\x00D  p -> q\x00A  r s\x00?? kept\x00?? n ew\x00")
	fake.On("git", in("diff", "--numstat", "-z", "--no-renames", "HEAD")...).
		Prints("1\t0\tcafé\x000\t1\tkept\x000\t6\tp -> q\x006\t0\tr s\x00")
	got, err := gitrepo.Repo{Dir: root, Run: fake}.Changes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []gitrepo.Change{
		{Path: "café", State: gitrepo.Edited, Added: 1},
		{Path: "kept", State: gitrepo.Edited, Removed: 1},
		{Path: "p -> q", State: gitrepo.Deleted, Removed: 6},
		{Path: "r s", State: gitrepo.Added, Added: 6},
		{Path: "n ew", State: gitrepo.Added, Added: 2},
	}
	if !slices.Equal(got, want) {
		t.Errorf("Changes() = %+v\nwant %+v", got, want)
	}
}

// Ignored names each path a rule ignores, with the rule, as check-ignore
// prints it: from the repository's .gitignore or the user's global one;
// a rule that keeps a path (!) isn't one ignoring it; none matching exits 1.
func TestIgnored(t *testing.T) {
	paths := []string{"laptop/home/.vscode/settings.json", "laptop/home/keep.jar", "laptop/home/tool.jar", "laptop/home/.zshrc"}
	fake := runnertest.New(t)
	fake.On("git", git("check-ignore", "--verbose", "--stdin", "-z")...).
		Prints(".gitignore\x001\x00.vscode\x00laptop/home/.vscode/settings.json\x00.gitignore\x002\x00!keep.jar\x00laptop/home/keep.jar\x00/Users/someone/.gitignore_global\x001\x00*.jar\x00laptop/home/tool.jar\x00")
	got, err := gitrepo.Repo{Dir: dir, Run: fake}.Ignored(context.Background(), paths...)
	want := map[string]gitrepo.Rule{
		"laptop/home/.vscode/settings.json": {Pattern: ".vscode", Source: ".gitignore", Line: 1},
		"laptop/home/tool.jar":              {Pattern: "*.jar", Source: "/Users/someone/.gitignore_global", Line: 1},
	}
	if err != nil || !maps.Equal(got, want) {
		t.Errorf("Ignored() = %v, %v; want %v", got, err, want)
	}
	if in := fake.Commands()[0].Input; in != strings.Join(paths, "\x00")+"\x00" {
		t.Errorf("check-ignore read %q", in)
	}

	fake = runnertest.New(t)
	fake.On("git", git("check-ignore", "--verbose", "--stdin", "-z")...).Exits(1)
	if got, err := (gitrepo.Repo{Dir: dir, Run: fake}).Ignored(context.Background(), "laptop/home/.zshrc"); err != nil || len(got) != 0 {
		t.Errorf("Ignored() of a path no rule matches = %v, %v", got, err)
	}
	fake = runnertest.New(t)
	fake.On("git", git("check-ignore", "--verbose", "--stdin", "-z")...).Exits(128).PrintsToStderr("fatal: not a git repository")
	if _, err := (gitrepo.Repo{Dir: dir, Run: fake}).Ignored(context.Background(), "laptop/home/.zshrc"); err == nil {
		t.Error("Ignored() with git failing: no error")
	}
}

func TestPush(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("git", git("remote")...).Prints("origin\n")
	fake.On("git", git("pull", "--rebase", "--autostash", "--quiet")...)
	fake.On("git", git("push", "--quiet")...)
	if err := (gitrepo.Repo{Dir: dir, Run: fake}).Push(context.Background()); err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if got := len(fake.Calls()); got != 3 {
		t.Errorf("ran %q, want remote, pull, push", fake.Calls())
	}
}

func TestPushAbortsAConflictingRebase(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("git", git("remote")...).Prints("origin\n")
	fake.On("git", git("pull", "--rebase", "--autostash", "--quiet")...).Exits(1).PrintsToStderr("CONFLICT (content): Merge conflict in brew")
	fake.On("git", git("rebase", "--abort")...)

	err := gitrepo.Repo{Dir: dir, Run: fake}.Push(context.Background())
	if err == nil || !strings.Contains(err.Error(), "conflict with this Mac's, so nothing was pushed; the rebase was undone") {
		t.Errorf("Push() error = %v, want the conflict reported", err)
	}
	if got := fake.Calls(); got[len(got)-1] != "git -C "+dir+" rebase --abort" || slices.Contains(got, "git -C "+dir+" push --quiet") {
		t.Errorf("ran %q, want the rebase aborted and nothing pushed", got)
	}
}

func TestPushFails(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("git", git("remote")...).Prints("origin\n")
	fake.On("git", git("pull", "--rebase", "--autostash", "--quiet")...).Exits(1).PrintsToStderr("fatal: unable to access")
	fake.On("git", git("rebase", "--abort")...).Exits(128).PrintsToStderr("fatal: No rebase in progress?")
	err := gitrepo.Repo{Dir: dir, Run: fake}.Push(context.Background())
	if err == nil || !strings.Contains(err.Error(), "couldn't bring in the remote's changes, so nothing was pushed") {
		t.Errorf("Push() error = %v, want the pull's failure", err)
	}

	fake = runnertest.New(t)
	fake.On("git", git("remote")...).Prints("origin\n")
	fake.On("git", git("pull", "--rebase", "--autostash", "--quiet")...)
	fake.On("git", git("push", "--quiet")...).Exits(1).PrintsToStderr("! [rejected]")
	if err := (gitrepo.Repo{Dir: dir, Run: fake}).Push(context.Background()); err == nil || !strings.Contains(err.Error(), "couldn't push") {
		t.Errorf("Push() error = %v, want the push's failure", err)
	}
}

func TestPushWithoutARemote(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("git", git("remote")...)
	if err := (gitrepo.Repo{Dir: dir, Run: fake}).Push(context.Background()); !errors.Is(err, gitrepo.ErrNoRemote) {
		t.Errorf("Push() error = %v, want ErrNoRemote", err)
	}
}
