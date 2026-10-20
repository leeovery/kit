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

// head is what git rev-parse --absolute-git-dir --verify --quiet HEAD
// prints for a repository whose git folder is gitDir, with a commit.
func head(gitDir string) string {
	return gitDir + "\n0e0bf17a399109574ffb00e33300280a33f81ab7\n"
}

// commitCase is a commit of paths, git's answers to it as real git gives
// them, and what kit has git do: status of the paths; then, in an index of
// its own, the files it stages as they are on disk (stage) and those it
// commits as deleted (deleted), what's staged there (staged), and a
// commit when anything is; then the files whose entries in the user's
// index take the last commit's (reset). Neither stage nor deleted: nothing
// runs after status.
type commitCase struct {
	name                  string
	paths                 []string
	status                string
	unborn                bool
	stage, deleted, reset []string
	staged                string
}

// call is a command kit ran: its arguments, what it read, and whether it
// ran in kit's own index.
type call struct {
	args  []string
	input string
	own   bool
}

// run scripts git's answers to c, runs Commit, and checks the commands it
// ran, in order, with what each read and in which index; that the index of
// its own is one file in the git folder, gone once it's done; and that it
// committed when, and only when, it ran commit.
func (c commitCase) run(t *testing.T) {
	t.Helper()
	gitDir := t.TempDir()
	fake := runnertest.New(t)
	want := []call{{args: git(slices.Concat([]string{"--literal-pathspecs", "status", "--porcelain", "-z", "--untracked-files=all", "--no-renames", "--"}, c.paths)...)}}
	fake.On("git", want[0].args...).Prints(c.status)
	commits := c.staged != ""
	if c.stage != nil || c.deleted != nil {
		revParse := git("rev-parse", "--absolute-git-dir", "--verify", "--quiet", "HEAD")
		want = append(want, call{args: revParse})
		if c.unborn {
			fake.On("git", revParse...).Prints(gitDir + "\n").Exits(1)
		} else {
			fake.On("git", revParse...).Prints(head(gitDir))
			want = append(want, call{args: git("read-tree", "HEAD"), own: true})
		}
		if c.deleted != nil {
			want = append(want, call{args: git("update-index", "--force-remove", "-z", "--stdin"), input: nul(c.deleted), own: true})
		}
		if c.stage != nil {
			want = append(want, call{args: git("update-index", "--add", "--remove", "--replace", "-z", "--stdin"), input: nul(c.stage), own: true})
		}
		want = append(want, call{args: git("diff", "--cached", "--name-only", "-z", "--no-renames"), own: true})
		fake.On("git", want[len(want)-1].args...).Prints(c.staged)
		if commits {
			want = append(want, call{args: git("commit", "--quiet", "-m", "kit test"), own: true})
		}
		want = append(want, call{args: git("--literal-pathspecs", "reset", "--quiet", "--pathspec-from-file=-", "--pathspec-file-nul"), input: nul(c.reset)})
		for _, w := range want[1:] {
			if w.args[2] != "rev-parse" && w.args[2] != "diff" {
				fake.On("git", w.args...)
			}
		}
	}
	committed, err := gitrepo.Repo{Dir: dir, Run: fake}.Commit(context.Background(), "kit test", c.paths...)
	if err != nil || committed != commits {
		t.Errorf("%s: Commit() = %v, %v; want committed %v", c.name, committed, err, commits)
	}
	checkCalls(t, c.name, fake, gitDir, want)
}

// checkCalls checks fake ran want, in order, each reading what it says and
// in kit's own index or the user's: its own one file in gitDir, gone now.
func checkCalls(t *testing.T, name string, fake *runnertest.Fake, gitDir string, want []call) {
	t.Helper()
	var ran []call
	var index string
	for _, cmd := range fake.Commands() {
		own := false
		for _, e := range cmd.Env {
			if path, ok := strings.CutPrefix(e, "GIT_INDEX_FILE="); ok {
				own = true
				if index == "" {
					index = path
				}
				if path != index || filepath.Dir(path) != gitDir || !strings.HasPrefix(filepath.Base(path), "kit-index-") {
					t.Errorf("%s: %s ran in the index %s, want one of kit's own in %s", name, cmd, path, gitDir)
				}
			}
		}
		ran = append(ran, call{args: cmd.Args, input: cmd.Input, own: own})
	}
	if !slices.EqualFunc(ran, want, func(a, b call) bool { return slices.Equal(a.args, b.args) && a.input == b.input && a.own == b.own }) {
		t.Errorf("%s: ran\n%s\nwant\n%v", name, strings.Join(fake.Calls(), "\n"), want)
	}
	if left, _ := os.ReadDir(gitDir); len(left) != 0 {
		t.Errorf("%s: left %v in the git folder", name, left)
	}
}

// nul is paths, each ended by a NUL, as git reads them with -z.
func nul(paths []string) string {
	var b strings.Builder
	for _, p := range paths {
		b.WriteString(p + "\x00")
	}
	return b.String()
}

func TestCommit(t *testing.T) {
	commitCase{
		name: "an edit", paths: []string{"laptop/declarations"},
		status: " M laptop/declarations\x00", stage: []string{"laptop/declarations"},
		staged: "laptop/declarations\x00", reset: []string{"laptop/declarations"},
	}.run(t)
}

// Every change under the paths goes in whole, file by file, whatever the
// user's config says: a new folder's every file, a link, a deletion staged
// or not, both sides of a rename, a file become a folder and a folder a
// file, names with spaces, " -> ", unicode or a pattern's characters, and
// paths given as dir/ or ./x. The commit's made in an index of kit's own,
// so anything else staged stays as it is, never named by status of the
// paths. Each status is what real git prints.
func TestCommitTakesEachChangeWhole(t *testing.T) {
	for _, c := range []commitCase{
		{
			name:   "a new folder hidden by status.showUntrackedFiles=no",
			paths:  []string{"laptop/steps/tool"},
			status: "?? laptop/steps/tool/run\x00", stage: []string{"laptop/steps/tool/run"},
			staged: "laptop/steps/tool/run\x00", reset: []string{"laptop/steps/tool/run"},
		},
		{
			name:    "deletions staged and not, a new folder with a link, unicode, and a path unchanged",
			paths:   []string{"laptop/declarations", "laptop/steps/tool", "old", "shared/steps/tool", "studio/steps/tool"},
			status:  " M laptop/declarations\x00D  old/run\x00 D studio/steps/tool/run\x00?? shared/steps/tool/café\x00?? shared/steps/tool/link\x00?? shared/steps/tool/run\x00",
			stage:   []string{"laptop/declarations", "studio/steps/tool/run", "shared/steps/tool/café", "shared/steps/tool/link", "shared/steps/tool/run"},
			deleted: []string{"old/run"},
			staged:  "laptop/declarations\x00old/run\x00shared/steps/tool/café\x00shared/steps/tool/link\x00shared/steps/tool/run\x00studio/steps/tool/run\x00",
			reset:   []string{"laptop/declarations", "old/run", "studio/steps/tool/run", "shared/steps/tool/café", "shared/steps/tool/link", "shared/steps/tool/run"},
		},
		{
			name:   "a rename the user staged, both sides given, named with an arrow, status.renames on",
			paths:  []string{"p -> q", "r s"},
			status: "D  p -> q\x00A  r s\x00", stage: []string{"r s"}, deleted: []string{"p -> q"},
			staged: "p -> q\x00r s\x00", reset: []string{"p -> q", "r s"},
		},
		{
			name:   "a rename the user staged, its new side alone given",
			paths:  []string{"b"},
			status: "A  b\x00", stage: []string{"b"},
			staged: "b\x00", reset: []string{"b"},
		},
		{
			name:   "a name that's a pattern, beside a file it would match",
			paths:  []string{"a*"},
			status: " M a*\x00", stage: []string{"a*"},
			staged: "a*\x00", reset: []string{"a*"},
		},
		{
			name:   "a file become a folder",
			paths:  []string{"laptop"},
			status: " D laptop/conf\x00?? laptop/conf/inner\x00", stage: []string{"laptop/conf", "laptop/conf/inner"},
			staged: "laptop/conf\x00laptop/conf/inner\x00", reset: []string{"laptop/conf", "laptop/conf/inner"},
		},
		{
			name:   "a folder become a file",
			paths:  []string{"laptop"},
			status: " D laptop/conf/inner\x00?? laptop/conf\x00", stage: []string{"laptop/conf/inner", "laptop/conf"},
			staged: "laptop/conf\x00laptop/conf/inner\x00", reset: []string{"laptop/conf/inner", "laptop/conf"},
		},
		{
			name:   "paths given as dir/ and ./x",
			paths:  []string{"d/", "./x"},
			status: " M d/a\x00 M x\x00", stage: []string{"d/a", "x"},
			staged: "d/a\x00x\x00", reset: []string{"d/a", "x"},
		},
		{
			name:   "a file the user untracked, ignored, stays deleted",
			paths:  []string{"secret"},
			status: "D  secret\x00", deleted: []string{"secret"},
			staged: "secret\x00", reset: []string{"secret"},
		},
		{
			name:   "a file the user untracked, not ignored, is kept as it is on disk",
			paths:  []string{"kept"},
			status: "D  kept\x00?? kept\x00", stage: []string{"kept"}, reset: []string{"kept"},
		},
	} {
		c.run(t)
	}
}

// What git can't keep is left out, never failing the rest: an ignored file
// and an empty folder, which git status never names; a path gone that git
// never kept; and a repository nested in this one. A file added, then gone
// before it was committed (AD), is staged as gone, and the user's index
// takes the last commit's entry, none, so it's out of it.
func TestCommitLeavesOutWhatGitCantKeep(t *testing.T) {
	for _, c := range []commitCase{
		{
			name:   "an ignored file and an empty folder",
			paths:  []string{"emptyd", "ign", "other"},
			status: "?? other\x00", stage: []string{"other"},
			staged: "other\x00", reset: []string{"other"},
		},
		{
			name:   "a path gone that git never kept",
			paths:  []string{"gone", "other"},
			status: "?? other\x00", stage: []string{"other"},
			staged: "other\x00", reset: []string{"other"},
		},
		{
			name:  "an ignored file alone",
			paths: []string{"ign"},
		},
		{
			name:   "a file added then gone, beside an edit",
			paths:  []string{"decl", "steps/tool"},
			status: " M decl\x00AD steps/tool/run\x00", stage: []string{"decl", "steps/tool/run"},
			staged: "decl\x00", reset: []string{"decl", "steps/tool/run"},
		},
		{
			name:   "a file added then gone, its declaration put back as committed",
			paths:  []string{"decl", "steps/tool"},
			status: "MM decl\x00AD steps/tool/run\x00", stage: []string{"decl", "steps/tool/run"}, reset: []string{"decl", "steps/tool/run"},
		},
		{
			name:   "an edit staged, then put back as committed",
			paths:  []string{"decl"},
			status: "MM decl\x00", stage: []string{"decl"}, reset: []string{"decl"},
		},
		{
			name:   "a nested repository",
			paths:  []string{"laptop"},
			status: "?? laptop/sub/\x00",
		},
		{
			name:   "a nested repository beside an edit",
			paths:  []string{"laptop"},
			status: " M laptop/decl\x00?? laptop/sub/\x00", stage: []string{"laptop/decl"},
			staged: "laptop/decl\x00", reset: []string{"laptop/decl"},
		},
	} {
		c.run(t)
	}
}

// Before the repository's first commit, kit's own index starts empty.
func TestCommitBeforeTheFirst(t *testing.T) {
	commitCase{
		name: "a Mac's first capture", paths: []string{"laptop"}, unborn: true,
		status: "?? laptop/.gitignore\x00?? laptop/domains/a.plist\x00", stage: []string{"laptop/.gitignore", "laptop/domains/a.plist"},
		staged: "laptop/.gitignore\x00laptop/domains/a.plist\x00", reset: []string{"laptop/.gitignore", "laptop/domains/a.plist"},
	}.run(t)
}

func TestCommitWithNothingToCommit(t *testing.T) {
	commitCase{name: "nothing changed", paths: []string{"brew"}}.run(t)
	fake := runnertest.New(t)
	if committed, err := (gitrepo.Repo{Dir: dir, Run: fake}).Commit(context.Background(), "kit test"); committed || err != nil || len(fake.Calls()) != 0 {
		t.Errorf("Commit() of no path = %v, %v, running %q; want nothing run", committed, err, fake.Calls())
	}
}

// A commit that fails, as a hook or signing does, leaves the user's index
// as it was, and kit's own gone.
func TestCommitFails(t *testing.T) {
	gitDir := t.TempDir()
	fake := runnertest.New(t)
	status := git("--literal-pathspecs", "status", "--porcelain", "-z", "--untracked-files=all", "--no-renames", "--", "brew")
	revParse := git("rev-parse", "--absolute-git-dir", "--verify", "--quiet", "HEAD")
	stage := git("update-index", "--add", "--remove", "--replace", "-z", "--stdin")
	diff := git("diff", "--cached", "--name-only", "-z", "--no-renames")
	commit := git("commit", "--quiet", "-m", "kit add brew jq")
	fake.On("git", status...).Prints(" M brew\x00")
	fake.On("git", revParse...).Prints(head(gitDir))
	fake.On("git", git("read-tree", "HEAD")...)
	fake.On("git", stage...)
	fake.On("git", diff...).Prints("brew\x00")
	fake.On("git", commit...).Exits(1).PrintsToStderr("error: gpg failed to sign the data")
	if _, err := (gitrepo.Repo{Dir: dir, Run: fake}).Commit(context.Background(), "kit add brew jq", "brew"); err == nil || !strings.Contains(err.Error(), "gpg failed to sign") {
		t.Errorf("Commit() error = %v, want git's", err)
	}
	checkCalls(t, "a commit refused", fake, gitDir, []call{
		{args: status}, {args: revParse}, {args: git("read-tree", "HEAD"), own: true},
		{args: stage, input: "brew\x00", own: true}, {args: diff, own: true}, {args: commit, own: true},
	})

	// Committed, then the user's index not brought up to it: committed all
	// the same, the error the index's.
	fake = runnertest.New(t)
	fake.On("git", status...).Prints(" M brew\x00")
	fake.On("git", revParse...).Prints(head(gitDir))
	fake.On("git", git("read-tree", "HEAD")...)
	fake.On("git", stage...)
	fake.On("git", diff...).Prints("brew\x00")
	fake.On("git", commit...)
	fake.On("git", git("--literal-pathspecs", "reset", "--quiet", "--pathspec-from-file=-", "--pathspec-file-nul")...).Exits(128).PrintsToStderr("fatal: Unable to create '.git/index.lock': File exists.")
	if committed, err := (gitrepo.Repo{Dir: dir, Run: fake}).Commit(context.Background(), "kit add brew jq", "brew"); !committed || err == nil || !strings.Contains(err.Error(), "git's index still shows them as they were") {
		t.Errorf("Commit(), the index locked = %v, %v; want committed, the index's error", committed, err)
	}

	fake = runnertest.New(t)
	fake.On("git", status...).Prints(" M brew\x00")
	fake.On("git", revParse...).Exits(128).PrintsToStderr("fatal: not a git repository")
	if _, err := (gitrepo.Repo{Dir: dir, Run: fake}).Commit(context.Background(), "kit add brew jq", "brew"); err == nil {
		t.Error("Commit() without a git folder: no error")
	}
}

// Changes reads status whole, whatever the user's config says: a staged
// rename as its two sides, named with an arrow; unicode unquoted; new
// files hidden by status.showUntrackedFiles=no; a file added then gone is
// no change; one staged as deleted and back untracked is edited; a nested
// repository is one. Lines are counted against the last commit, in kit's
// own index of it, whatever's staged: a file new to it, by its own lines.
// What git prints is what real git does.
func TestChanges(t *testing.T) {
	root, gitDir := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "n ew"), []byte("n\nw\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "r s"), []byte("1\n2\n3\n4\n5\n6\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	in := func(args ...string) []string { return append([]string{"-C", root}, args...) }
	fake := runnertest.New(t)
	status := in("--literal-pathspecs", "status", "--porcelain", "-z", "--untracked-files=all", "--no-renames", "--")
	revParse := in("rev-parse", "--absolute-git-dir", "--verify", "--quiet", "HEAD")
	numstat := in("diff", "--numstat", "-z", "--no-renames", "--no-ext-diff", "HEAD")
	fake.On("git", status...).Prints(" M café\x00AD gone\x00D  kept\x00D  p -> q\x00A  r s\x00?? kept\x00?? n ew\x00?? nested/\x00")
	fake.On("git", revParse...).Prints(head(gitDir))
	fake.On("git", in("read-tree", "HEAD")...)
	fake.On("git", numstat...).Prints("1\t0\tcafé\x000\t6\tp -> q\x00")
	got, err := gitrepo.Repo{Dir: root, Run: fake}.Changes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []gitrepo.Change{
		{Path: "café", State: gitrepo.Edited, Added: 1},
		{Path: "kept", State: gitrepo.Edited},
		{Path: "p -> q", State: gitrepo.Deleted, Removed: 6},
		{Path: "r s", State: gitrepo.Added, Added: 6},
		{Path: "n ew", State: gitrepo.Added, Added: 2},
		{Path: "nested", State: gitrepo.Repository},
	}
	if !slices.Equal(got, want) {
		t.Errorf("Changes() = %+v\nwant %+v", got, want)
	}
	checkCalls(t, "changes", fake, gitDir, []call{{args: status}, {args: revParse}, {args: in("read-tree", "HEAD"), own: true}, {args: numstat, own: true}})
}

// Unpushed counts the commits waiting for the upstream: none without one,
// as git says when there isn't.
func TestUnpushed(t *testing.T) {
	for _, c := range []struct {
		prints string
		exit   int
		want   int
	}{{"1\n", 0, 1}, {"0\n", 0, 0}, {"", 128, 0}} {
		fake := runnertest.New(t)
		fake.On("git", git("rev-list", "--count", "@{u}..HEAD")...).Prints(c.prints).Exits(c.exit).PrintsToStderr(map[bool]string{true: "fatal: no upstream configured for branch 'main'"}[c.exit != 0])
		if got := (gitrepo.Repo{Dir: dir, Run: fake}).Unpushed(context.Background()); got != c.want {
			t.Errorf("Unpushed() with git printing %q, exit %d = %d, want %d", c.prints, c.exit, got, c.want)
		}
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
