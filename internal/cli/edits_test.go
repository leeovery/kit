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

// Drift is kit-config's files on disk against the last commit: a
// difference only in git's index, as the user staged it, the file on disk
// as last committed, is the user's own business in git, never an edit, as
// an edit staged then put back. What git prints is what real git does.
func TestEditsOnlyInGitsIndexArentDrift(t *testing.T) {
	w := laptopWorld(t)
	w.edits(t, "MM shared/home/.zshrc\x00", "")
	if out, _, _ := w.run(t, "status"); !strings.Contains(out, "config ok all committed\n") {
		t.Errorf("kit status printed\n%s", out)
	}
	if _, errOut, code := w.run(t, "reconcile", "config:shared/home/.zshrc", "--adopt"); code != 2 || !strings.Contains(errOut, "no item config:shared/home/.zshrc") {
		t.Errorf("kit reconcile printed %q, exit %d; want no such item", errOut, code)
	}
}

// With nothing to commit, the sync says nothing changed, and pushes only
// commits waiting for the upstream, as one whose push failed: none without
// a remote, and none while git's part way through a merge, which pushing
// would set aside.
func TestNothingChangedPushesWhatsWaiting(t *testing.T) {
	for _, c := range []struct {
		name, remotes, waiting string
		merging                bool
		want                   string
		pushes                 bool
	}{
		{"none waiting", "origin\n", "", false, "kit-config ok nothing changed\n", false},
		{"one waiting", "origin\n", "1767312245\n", false, "kit-config ok nothing changed; pushed 1 commit waiting\n", true},
		{"no remote", "", "", false, "kit-config ok nothing changed\n", false},
		{"one waiting, a merge under way", "origin\n", "1767312245\n", true, "kit-config attention nothing changed; 1 commit waiting, not pushed: a merge is under way in the repository (MERGE_HEAD): kit's change waits till it's finished\n", false},
	} {
		w := laptopWorld(t)
		dir := filepath.Join(w.home, ".config", "kit")
		git := func(args ...string) []string { return append([]string{"-C", dir}, args...) }
		gitDir := t.TempDir()
		if c.merging {
			if err := os.WriteFile(filepath.Join(gitDir, "MERGE_HEAD"), []byte(head+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		w.expectPush(t, gitDir)
		w.waiting(c.waiting)
		w.fake.On("git", git("remote")...).Prints(c.remotes)
		out, _, _ := w.run(t, "brew", "add", "jq")
		if !strings.Contains(out, "jq ok already installed; declared already, in shared\n"+c.want) {
			t.Errorf("%s: kit brew add jq printed\n%s", c.name, out)
		}
		pushed := false
		for _, cmd := range w.fake.Commands() {
			pushed = pushed || slices.Equal(cmd.Args, git("push", "--quiet", "origin", "HEAD:refs/heads/main"))
			if slices.Contains(cmd.Args, "commit") || slices.Contains(cmd.Args, "stash") {
				t.Errorf("%s: ran %s, want nothing committed or set aside", c.name, cmd)
			}
		}
		if pushed != c.pushes {
			t.Errorf("%s: pushed %v, want %v", c.name, pushed, c.pushes)
		}
	}
}

// A commit git refuses leaves nothing committed: kit says what git said,
// the edit left as drift, and nothing's pushed or brought in.
func TestReconcileWhenTheCommitFails(t *testing.T) {
	w := editsWorld(t)
	dir := filepath.Join(w.home, ".config", "kit")
	c := edited("kit reconcile (laptop): adopt config:shared/home/.zshrc", "shared/home/.zshrc")
	c.fails = "fatal: unable to write new_index file\n"
	w.expectCommit(t, c)
	out, _, _ := w.run(t, "reconcile", "config:shared/home/.zshrc", "--adopt")
	if !strings.Contains(out, "kit-config failed couldn't commit shared/home/.zshrc: ") || !strings.Contains(out, "unable to write new_index file") {
		t.Errorf("kit reconcile printed\n%s", out)
	}
	calls := w.fake.Calls()
	committing := slices.IndexFunc(calls, func(call string) bool { return strings.Contains(call, " commit ") })
	if committing < 0 || slices.ContainsFunc(calls[committing:], func(call string) bool {
		return strings.HasPrefix(call, "git -C "+dir+" push") || strings.HasPrefix(call, "git -C "+dir+" fetch")
	}) {
		t.Errorf("ran %q, want nothing pushed or brought in after the commit", calls)
	}
}

// A repository nested in kit-config is one git can't keep, and a whole
// repository isn't kit's to put in the Bin: kit says so, offering to snooze
// it alone. Status names it as real git does, a folder ending in a slash.
func TestReconcileANestedRepository(t *testing.T) {
	w := laptopWorld(t)
	w.edits(t, "?? vendor/tool/\x00", "")
	if out, _, _ := w.run(t, "status"); !strings.Contains(out, "config repository:new vendor/tool/ (kit-config can't keep it)\n") {
		t.Errorf("kit status printed\n%s", out)
	}
	for flag, past := range map[string]string{"--adopt": "adopted", "--revert": "reverted"} {
		if _, errOut, code := w.run(t, "reconcile", "config:vendor/tool/", flag); code != 2 || !strings.Contains(errOut, "config:vendor/tool/ can't be "+past+": --snooze") {
			t.Errorf("kit reconcile %s printed %q, exit %d", flag, errOut, code)
		}
	}
}

// A file the user took out of git's index, kept on disk as git ignores it,
// is deleted, as git reads it. Status names it as real git does.
func TestStatusShowsAFileTakenOutOfGitsIndexDeleted(t *testing.T) {
	const secret = "shared/home/.secret"
	w := laptopWorld(t)
	w.write(t, ".config/kit/"+secret, "s\n")
	// Out of git's index, and ignored: no file of kit's to link.
	w.keeps()
	w.edits(t, "D  "+secret+"\x00", "0\t1\t"+secret+"\x00")
	if out, _, _ := w.run(t, "status"); !strings.Contains(out, "config deleted:new "+secret+" (+0 −1 lines)\n") {
		t.Errorf("kit status printed\n%s", out)
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
	if want := "Commit|Undo|Snooze|Skip"; strings.Join(offered, "|") != want {
		t.Errorf("offered %q", offered)
	}
}
