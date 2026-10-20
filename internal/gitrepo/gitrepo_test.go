package gitrepo_test

import (
	"context"
	"errors"
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

// repoWith is a repository's folder holding files, each with something in
// it, for Commit to find on disk; and git, scripting git's arguments in it.
func repoWith(t *testing.T, files ...string) (string, func(args ...string) []string) {
	t.Helper()
	root := t.TempDir()
	for _, f := range files {
		path := filepath.Join(root, f)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root, func(args ...string) []string { return append([]string{"-C", root}, args...) }
}

func TestCommit(t *testing.T) {
	root, git := repoWith(t, "brew.laptop")
	fake := runnertest.New(t)
	fake.On("git", git("status", "--porcelain", "--", "brew.laptop")...).Prints(" M brew.laptop\n")
	fake.On("git", git("add", "--", "brew.laptop")...)
	fake.On("git", git("commit", "--quiet", "-m", "kit add brew jq (laptop)", "--", "brew.laptop")...)

	committed, err := gitrepo.Repo{Dir: root, Run: fake}.Commit(context.Background(), "kit add brew jq (laptop)", "brew.laptop")
	if err != nil || !committed {
		t.Fatalf("Commit() = %v, %v; want it committed", committed, err)
	}
	want := []string{
		"git -C " + root + " status --porcelain -- brew.laptop",
		"git -C " + root + " add -- brew.laptop",
		"git -C " + root + " commit --quiet -m 'kit add brew jq (laptop)' -- brew.laptop",
	}
	if got := fake.Calls(); !slices.Equal(got, want) {
		t.Errorf("ran\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestCommitWithNothingToCommit(t *testing.T) {
	root, git := repoWith(t, "brew")
	fake := runnertest.New(t)
	fake.On("git", git("status", "--porcelain", "--", "brew")...)
	committed, err := gitrepo.Repo{Dir: root, Run: fake}.Commit(context.Background(), "kit add brew jq", "brew")
	if err != nil || committed || len(fake.Calls()) != 1 {
		t.Errorf("Commit() = %v, %v, running %q; want nothing committed, and no error", committed, err, fake.Calls())
	}
}

func TestCommitFails(t *testing.T) {
	root, git := repoWith(t, "brew")
	fake := runnertest.New(t)
	fake.On("git", git("status", "--porcelain", "--", "brew")...).Prints(" M brew\n")
	fake.On("git", git("add", "--", "brew")...)
	fake.On("git", git("commit", "--quiet", "-m", "kit add brew jq", "--", "brew")...).Exits(1).PrintsToStderr("error: gpg failed to sign the data")
	if _, err := (gitrepo.Repo{Dir: root, Run: fake}).Commit(context.Background(), "kit add brew jq", "brew"); err == nil || !strings.Contains(err.Error(), "gpg failed to sign") {
		t.Errorf("Commit() error = %v, want git's", err)
	}
}

// A path gone from disk is committed as git status names it, never added,
// as git won't add what isn't there: a rename staged already, both sides; a
// deletion staged already, or not. One gone that status doesn't name, git
// never kept, is left out. Each status is what git prints for it.
func TestCommitTakesWhatsGoneAsStatusNamesIt(t *testing.T) {
	for _, c := range []struct {
		name, status string
		onDisk       []string
		files        []string
		add, commit  []string
	}{
		{"a rename staged", "R  laptop/steps/tool/run -> shared/steps/tool/run\n", []string{"shared/steps/tool/run"},
			[]string{"laptop/steps/tool", "shared/steps/tool"}, []string{"shared/steps/tool"}, []string{"laptop/steps/tool", "shared/steps/tool"}},
		{"a deletion staged", "D  laptop/steps/tool/run\n M laptop/declarations\n", []string{"laptop/declarations"},
			[]string{"laptop/declarations", "laptop/steps/tool"}, []string{"laptop/declarations"}, []string{"laptop/declarations", "laptop/steps/tool"}},
		{"a deletion not staged, a new folder", " M laptop/declarations\n?? shared/steps/tool/\n D studio/steps/tool/run\n", []string{"laptop/declarations", "shared/steps/tool/run"},
			[]string{"laptop/declarations", "laptop/steps/tool", "shared/steps/tool", "studio/steps/tool"}, []string{"laptop/declarations", "shared/steps/tool"}, []string{"laptop/declarations", "shared/steps/tool", "studio/steps/tool"}},
		{"a deletion alone", "D  laptop/steps/tool/run\n", nil,
			[]string{"laptop/steps/tool"}, nil, []string{"laptop/steps/tool"}},
	} {
		root, git := repoWith(t, c.onDisk...)
		fake := runnertest.New(t)
		fake.On("git", git(append([]string{"status", "--porcelain", "--"}, c.files...)...)...).Prints(c.status)
		fake.On("git", git(append([]string{"add", "--"}, c.add...)...)...)
		fake.On("git", git(append([]string{"commit", "--quiet", "-m", "kit step add tool (laptop)", "--"}, c.commit...)...)...)
		committed, err := gitrepo.Repo{Dir: root, Run: fake}.Commit(context.Background(), "kit step add tool (laptop)", c.files...)
		if err != nil || !committed {
			t.Errorf("%s: Commit() = %v, %v, running %q", c.name, committed, err, fake.Calls())
		}
		if c.add == nil && slices.ContainsFunc(fake.Calls(), func(call string) bool { return strings.HasPrefix(call, "git -C "+root+" add ") }) {
			t.Errorf("%s: ran %q, want nothing added", c.name, fake.Calls())
		}
	}
}

// A path gone that git never kept, with nothing else of the commit's
// changed, commits nothing: never a commit given no path, which would take
// whatever else is staged.
func TestCommitOfAPathGitNeverKept(t *testing.T) {
	root, git := repoWith(t, "other")
	fake := runnertest.New(t)
	fake.On("git", git("status", "--porcelain", "--", "laptop/steps/tool")...)
	committed, err := gitrepo.Repo{Dir: root, Run: fake}.Commit(context.Background(), "kit step add tool (laptop)", "laptop/steps/tool")
	if err != nil || committed || len(fake.Calls()) != 1 {
		t.Errorf("Commit() = %v, %v, running %q; want nothing committed", committed, err, fake.Calls())
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
