package cli_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// editsWorld is laptopWorld with kit-config edited outside kit: a linked
// file changed, and a new file.
func editsWorld(t *testing.T) *world {
	t.Helper()
	w := laptopWorld(t)
	dir := filepath.Join(w.home, ".config", "kit")
	w.write(t, ".config/kit/notes", "a\nb\n")
	w.write(t, ".config/kit/shared/home/.zshrc", "alias a=c\n")
	w.keeps("shared/home/.zshrc")
	if err := os.Symlink(filepath.Join(dir, "shared", "home", ".zshrc"), filepath.Join(w.home, ".zshrc")); err != nil {
		t.Fatal(err)
	}
	w.edits(t, " M shared/home/.zshrc\x00?? notes\x00", "3\t1\tshared/home/.zshrc\x00")
	return w
}

func TestStatusShowsKitConfigsEdits(t *testing.T) {
	out, _, _ := editsWorld(t).run(t, "status")
	if !strings.Contains(out, "config ok 2 files not committed\nconfig edited:new shared/home/.zshrc (+3 −1 lines)\nconfig added:new notes (+2 −0 lines)\n") {
		t.Errorf("kit status printed\n%s", out)
	}
}

func TestReconcileCommitsAnEdit(t *testing.T) {
	w := editsWorld(t)
	w.expectCommit(t, edited("kit reconcile (laptop): adopt config:shared/home/.zshrc: a new alias", "shared/home/.zshrc"))
	out, _, code := w.run(t, "reconcile", "config:shared/home/.zshrc", "--adopt", "--note", "a new alias")
	if code != 0 || !strings.Contains(out, "kit-config ok committed and pushed shared/home/.zshrc") {
		t.Errorf("kit reconcile printed\n%s exit %d", out, code)
	}
}

// An edit staged, then put back as committed, is no change to commit:
// adopting it stages it as it is, and the sync says nothing changed. It
// pushes only commits waiting for the upstream, as one whose push failed:
// none without an upstream. What git prints is what real git does.
func TestReconcileAdoptingNothingToCommit(t *testing.T) {
	for _, c := range []struct {
		name, unpushed string
		exit           int
		want           string
		pushes         bool
	}{
		{"none waiting", "0\n", 0, "kit-config ok nothing changed\n", false},
		{"one waiting", "1\n", 0, "kit-config ok nothing changed; pushed 1 commit waiting\n", true},
		{"no upstream", "", 128, "kit-config ok nothing changed\n", false},
	} {
		w := laptopWorld(t)
		dir := filepath.Join(w.home, ".config", "kit")
		w.write(t, ".config/kit/shared/home/.zshrc", "alias a=b\n")
		w.keeps("shared/home/.zshrc")
		if err := os.Symlink(filepath.Join(dir, "shared", "home", ".zshrc"), filepath.Join(w.home, ".zshrc")); err != nil {
			t.Fatal(err)
		}
		git := func(args ...string) []string { return append([]string{"-C", dir}, args...) }
		w.edits(t, "MM shared/home/.zshrc\x00", "")
		w.fake.On("git", git("--literal-pathspecs", "status", "--porcelain", "-z", "--untracked-files=all", "--no-renames", "--", "shared/home/.zshrc")...).Prints("MM shared/home/.zshrc\x00")
		w.fake.On("git", git("update-index", "--add", "--remove", "--replace", "-z", "--stdin")...)
		w.fake.On("git", git("diff", "--cached", "--name-only", "-z", "--no-renames")...)
		w.fake.On("git", git("--literal-pathspecs", "reset", "--quiet", "--pathspec-from-file=-", "--pathspec-file-nul")...)
		w.fake.On("git", git("rev-list", "--count", "@{u}..HEAD")...).Prints(c.unpushed).Exits(c.exit)
		w.fake.On("git", git("remote")...).Prints("origin\n")
		w.fake.On("git", git("pull", "--rebase", "--autostash", "--quiet")...)
		w.fake.On("git", git("push", "--quiet")...)
		out, _, code := w.run(t, "reconcile", "config:shared/home/.zshrc", "--adopt")
		if code != 0 || !strings.Contains(out, "config:shared/home/.zshrc ok to commit\n"+c.want) {
			t.Errorf("%s: kit reconcile printed\n%s exit %d", c.name, out, code)
		}
		pushed := false
		for _, cmd := range w.fake.Commands() {
			pushed = pushed || slices.Equal(cmd.Args, git("push", "--quiet"))
			if slices.Contains(cmd.Args, "commit") {
				t.Errorf("%s: ran %s, want nothing committed", c.name, cmd)
			}
		}
		if pushed != c.pushes {
			t.Errorf("%s: pushed %v, want %v", c.name, pushed, c.pushes)
		}
	}
}

// A repository nested in kit-config is one git can't keep, and a whole
// repository isn't kit's to put in the Bin: kit says so, offering to snooze
// it alone. Status names it as real git does, a folder ending in a slash.
func TestReconcileANestedRepository(t *testing.T) {
	w := laptopWorld(t)
	w.edits(t, "?? vendor/tool/\x00", "")
	if out, _, _ := w.run(t, "status"); !strings.Contains(out, "config repository:new vendor/tool (a repository of its own, which kit-config can't keep)\n") {
		t.Errorf("kit status printed\n%s", out)
	}
	for flag, past := range map[string]string{"--adopt": "adopted", "--revert": "reverted"} {
		if _, errOut, code := w.run(t, "reconcile", "config:vendor/tool", flag); code != 2 || !strings.Contains(errOut, "config:vendor/tool can't be "+past+": --snooze") {
			t.Errorf("kit reconcile %s printed %q, exit %d", flag, errOut, code)
		}
	}
}

func TestReconcileUndoesAnEdit(t *testing.T) {
	w := editsWorld(t)
	w.fake.On("git", "-C", filepath.Join(w.home, ".config", "kit"), "--literal-pathspecs", "restore", "--source=HEAD", "--staged", "--worktree", "--", "shared/home/.zshrc")
	out, _, code := w.run(t, "reconcile", "config:shared/home/.zshrc", "--revert")
	if code != 0 || !strings.Contains(out, "undone: back to the last commit") {
		t.Errorf("kit reconcile printed\n%s exit %d", out, code)
	}
}

func TestReconcileUndoesANewFile(t *testing.T) {
	w := editsWorld(t)
	out, _, code := w.run(t, "reconcile", "config:notes", "--revert")
	if code != 0 || !strings.Contains(out, "undone: the file is in the Bin") || w.read(t, ".Trash/notes") != "a\nb\n" {
		t.Errorf("kit reconcile printed\n%s exit %d; want notes in the Bin", out, code)
	}
}

func TestReconcileAtATerminalShowsAnEditsDiff(t *testing.T) {
	w := editsWorld(t)
	w.terminal = true
	w.fake.On("git", "-C", filepath.Join(w.home, ".config", "kit"), "--literal-pathspecs", "diff", "--no-color", "--no-ext-diff", "HEAD", "--", "shared/home/.zshrc").Prints("@@ -1 +1 @@\n-alias a=b\n+alias a=c\n")
	var asked string
	var offered []string
	w.choose = func(question string, options []string) (int, error) {
		if strings.HasPrefix(question, "shared/home/.zshrc  config") {
			asked, offered = question, options
		}
		return len(options) - 1, nil
	}
	w.run(t, "reconcile", "--all", "config")
	if want := "shared/home/.zshrc  config · edited, not committed · +3 −1 lines\n\n@@ -1 +1 @@\n-alias a=b\n+alias a=c"; asked != want {
		t.Errorf("asked %q, want %q", asked, want)
	}
	if want := "Adopt|Revert|Snooze|Skip"; strings.Join(offered, "|") != want {
		t.Errorf("offered %q", offered)
	}
}
