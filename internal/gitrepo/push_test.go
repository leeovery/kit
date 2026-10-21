package gitrepo_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// The commands of a push.
var (
	wholeStatus = git("--literal-pathspecs", "status", "--porcelain", "-z", "--untracked-files=all", "--no-renames", "--")
	incomingOf  = git("diff", "--name-only", "-z", "--no-renames", "HEAD...refs/remotes/origin/main")
	foldsCase   = git("config", "--get", "--type=bool", "core.ignorecase")
	ignoredHere = git("ls-files", "-z", "--others", "--ignored", "--exclude-standard", "--directory")
	stashRef    = git("rev-parse", "--verify", "--quiet", "refs/stash")
	stashPush   = git("stash", "push", "--quiet", "--message", "kit: set aside while rebasing onto the remote")
	stashPop    = git("stash", "pop", "--index", "--quiet", "stash@{0}")
	rebase      = git("rebase", "--no-fork-point", "--quiet", "refs/remotes/origin/main")
	abort       = git("rebase", "--abort")
	pushIt      = git("push", "--quiet")
	fetchIt     = git("fetch", "--prune", "--quiet", "origin")
	behindOf    = git("rev-list", "--count", "HEAD..refs/remotes/origin/main")
)

// The commands that find the branch's upstream, as fetched: the branch,
// its upstream and the upstream's remote, the upstream's commit, the
// remote's branches, and, with no upstream, where it's pushed.
var (
	branchOf     = git("symbolic-ref", "-q", "HEAD")
	upstreamOf   = git("for-each-ref", "--format=%(upstream)%00%(upstream:remotename)", "refs/heads/main")
	upstreamAt   = git("rev-parse", "--verify", "--quiet", "refs/remotes/origin/main")
	branchesOf   = git("for-each-ref", "--format=%(refname)", "refs/remotes/origin/")
	pushRemoteOf = git("config", "--get", "branch.main.pushRemote")
	pushDefault  = git("config", "--get", "remote.pushDefault")
)

// tracksOrigin is git naming origin's main, upstreamRef, as the branch's
// upstream.
const (
	tracksOrigin = upstreamRef + "\x00origin\n"
	upstreamRef  = "refs/remotes/origin/main"
)

// upstream is the commit the tests' upstream, origin's main, is at.
const upstream = "51cd7f5e2b6cbb1bd2ff3e2d02cd4f1bbd1c7a33"

// The stashes the tests' git names: the user's own, and the one kit makes.
const (
	theirStash = "1111111111111111111111111111111111111111"
	kitsStash  = "bfcfca69ab8231f56369d4652e7ff0a5ffc81478"
)

// offline is what real git prints when the remote, over ssh, can't be
// reached: why, above its last line.
const offline = "ssh: Could not resolve hostname github.com: nodename nor servname provided, or not known\nfatal: Could not read from remote repository.\n\nPlease make sure you have the correct access rights\nand the repository exists.\n"

// pushing scripts a push as far as the rebase, as real git answers: the
// remote; the git folder, and what's changed and not committed (status,
// -z); the fetch, the branch's upstream, and its commits HEAD hasn't
// (behind).
func pushing(fake *runnertest.Fake, gitDir, status, behind string) *sequence {
	return pushingIn(fake, dir, gitDir, status, behind)
}

// pushingIn is pushing in the repository at root.
func pushingIn(fake *runnertest.Fake, root, gitDir, status, behind string) *sequence {
	s := &sequence{fake: fake, root: root}
	s.fetches(gitDir, status)
	s.expect(call{args: upstreamAt}).Prints(upstream + "\n")
	s.expect(call{args: behindOf}).Prints(behind + "\n")
	return s
}

// fetches scripts a push as far as the fetch, the branch's upstream
// origin's main: the remote; the git folder, and what's changed and not
// committed (status, -z); the branch, its upstream and where it's pushed,
// and the fetch.
func (s *sequence) fetches(gitDir, status string) {
	s.expect(call{args: git("remote")}).Prints("origin\n")
	s.expect(call{args: gitDirOf}).Prints(gitDir + "\n")
	s.expect(call{args: wholeStatus}).Prints(status)
	s.tracks(tracksOrigin, "", "")
	s.expect(call{args: fetchIt})
}

// tracks scripts git naming the branch, main, its upstream and the
// upstream's remote (upstream, -z), and where it's pushed, as set:
// branch.main.pushRemote (branchSays), then remote.pushDefault
// (defaultSays), "" for unset, each asked till one is set.
func (s *sequence) tracks(upstream, branchSays, defaultSays string) {
	s.expect(call{args: branchOf}).Prints("refs/heads/main\n")
	s.expect(call{args: upstreamOf}).Prints(upstream)
	names(s.expect(call{args: pushRemoteOf}), branchSays)
	if branchSays == "" {
		names(s.expect(call{args: pushDefault}), defaultSays)
	}
}

// incoming scripts the remote's changes as git names them: the paths its
// commits change (changed, -z), and, when there are any, git folding case,
// as on macOS's own disks, and the files it ignores here (ignored, -z).
func (s *sequence) incoming(changed, ignored string) {
	s.expect(call{args: incomingOf}).Prints(changed)
	if changed == "" {
		return
	}
	s.expect(call{args: foldsCase}).Prints("true\n")
	s.expect(call{args: ignoredHere}).Prints(ignored)
}

// drift scripts the files on disk that differ from the last commit
// (Changes): git's status, and its count of what differs (numstat, -z).
func (s *sequence) drift(status, numstat string) {
	s.expect(call{args: wholeStatus}).Prints(status)
	s.expect(call{args: headOf}).Prints(base + "\n")
	s.expect(call{args: git("read-tree", base), index: readIndex})
	s.expect(call{args: git("diff", "--numstat", "-z", "--no-renames", "--no-ext-diff", "HEAD"), index: readIndex}).Prints(numstat)
}

// stashes scripts git stash push, git naming before as its latest stash
// before it, after after it, "" for none.
func (s *sequence) stashes(before, after string) {
	names(s.expect(call{args: stashRef}), before)
	s.expect(call{args: stashPush})
	names(s.expect(call{args: stashRef}), after)
}

// pops scripts the look at git's latest stash, latest, then, if it's kit's,
// its pop, and is that.
func (s *sequence) pops(latest string) *runnertest.Script {
	names(s.expect(call{args: stashRef}), latest)
	if latest != kitsStash {
		return nil
	}
	return s.expect(call{args: stashPop})
}

// names has rev-parse --verify --quiet name stash, or none when "", as it
// says by exiting 1.
func names(script *runnertest.Script, stash string) {
	if stash == "" {
		script.Exits(1)
		return
	}
	script.Prints(stash + "\n")
}

// rebasing has script start a rebase that stops part way, as git leaves
// one, its folder in gitDir; aborting, script undoes it.
func rebasing(script *runnertest.Script, gitDir string, stops bool) {
	script.Does(func() {
		path := filepath.Join(gitDir, "rebase-merge")
		if stops {
			_ = os.Mkdir(path, 0o700)
			return
		}
		_ = os.Remove(path)
	})
}

func push(t *testing.T, fake *runnertest.Fake) error {
	t.Helper()
	_, err := gitrepo.Repo{Dir: dir, Run: fake}.Push(context.Background())
	return err
}

// repoAt is a repository at a folder of its own, the files on disk in it
// those of files, by path, a folder for one ending in a slash.
func repoAt(t *testing.T, files ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, file := range files {
		path := filepath.Join(root, file)
		if strings.HasSuffix(file, "/") {
			if err := os.MkdirAll(path, 0o700); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := errors.Join(os.MkdirAll(filepath.Dir(path), 0o700), os.WriteFile(path, nil, 0o600)); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// pushOrPull is Push, or Pull when pull.
func pushOrPull(t *testing.T, fake *runnertest.Fake, pull bool) error {
	t.Helper()
	if pull {
		_, err := gitrepo.Repo{Dir: dir, Run: fake}.Pull(context.Background())
		return err
	}
	return push(t, fake)
}

// With nothing new on the remote, Push pushes, touching nothing else: real
// git's staged work stays staged.
func TestPush(t *testing.T) {
	fake := runnertest.New(t)
	s := pushing(fake, t.TempDir(), "M  base\x00AM unst\x00", "0")
	s.expect(call{args: pushIt})
	if err := push(t, fake); err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	checkCalls(t, "nothing new", fake, "", s.want)
}

// Pull brings in the remote's changes as Push does, and pushes nothing.
func TestPull(t *testing.T) {
	fake := runnertest.New(t)
	s := pushing(fake, t.TempDir(), "", "1")
	s.incoming("r\x00", "")
	s.expect(call{args: rebase})
	if _, err := (gitrepo.Repo{Dir: dir, Run: fake}).Pull(context.Background()); err != nil {
		t.Fatalf("Pull() error = %v", err)
	}
	checkCalls(t, "pull", fake, "", s.want)
}

// While git is part way through a merge, cherry-pick, revert, rebase or
// am, Push refuses before it fetches, as setting what's not committed
// aside would end it: real git's stash push takes away MERGE_HEAD and
// CHERRY_PICK_HEAD. A conflict waiting to be resolved is refused too. So
// does Pull, the prefs store's first step.
func TestPushRefusesWhileGitIsPartWay(t *testing.T) {
	for _, pull := range []bool{false, true} {
		for _, file := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-merge", "rebase-apply", "sequencer"} {
			gitDir := t.TempDir()
			if err := os.Mkdir(filepath.Join(gitDir, file), 0o700); err != nil {
				t.Fatal(err)
			}
			fake := runnertest.New(t)
			s := &sequence{fake: fake}
			s.expect(call{args: git("remote")}).Prints("origin\n")
			s.expect(call{args: gitDirOf}).Prints(gitDir + "\n")
			if err := pushOrPull(t, fake, pull); err == nil || !strings.Contains(err.Error(), "under way in the repository ("+file+"): kit's change waits till it's finished") {
				t.Errorf("with %s, pull %v = %v, want it refused", file, pull, err)
			}
			checkCalls(t, file, fake, "", s.want)
		}

		fake := runnertest.New(t)
		s := &sequence{fake: fake}
		s.expect(call{args: git("remote")}).Prints("origin\n")
		s.expect(call{args: gitDirOf}).Prints(t.TempDir() + "\n")
		s.expect(call{args: wholeStatus}).Prints("UU f\x00")
		if err := pushOrPull(t, fake, pull); err == nil || err.Error() != "a conflict in f is waiting to be resolved: kit's change waits till it's finished" {
			t.Errorf("with a conflict waiting, pull %v = %v, want it refused", pull, err)
		}
		checkCalls(t, "a conflict waiting", fake, "", s.want)
	}
}

// With the remote ahead, what's committed here is rebased onto it, by the
// upstream's commits since the two parted, never git's fork-point, which
// leaves out a commit the upstream once had, so the rebase changes no more
// than the remote's commits do. Work not committed, none of it changed by
// them, is set aside in a stash of its own first and put back after, by
// that stash, staged or not, as real git keeps it; untracked files alone,
// which git stash leaves, set nothing aside; a stash of the user's own,
// there before, is never touched.
func TestPushRebasesOntoTheRemote(t *testing.T) {
	for _, c := range []struct {
		name, status  string
		before, after string
		stashes       bool
	}{
		{"nothing changed here", "", "", "", false},
		{"work set aside and put back", "M  base\x00 M shared\x00", "", kitsStash, true},
		{"untracked files alone", "?? notes\x00", "", "", false},
		{"the user's own stash, work set aside and put back", "M  base\x00", theirStash, kitsStash, true},
		{"the user's own stash, nothing git stashes", "MM base\x00", theirStash, theirStash, true},
	} {
		fake := runnertest.New(t)
		s := pushing(fake, t.TempDir(), c.status, "1")
		s.incoming("r\x00", "")
		if c.stashes {
			s.stashes(c.before, c.after)
		}
		s.expect(call{args: rebase})
		if c.after == kitsStash {
			s.pops(kitsStash)
		}
		s.expect(call{args: pushIt})
		if err := push(t, fake); err != nil {
			t.Errorf("%s: Push() error = %v", c.name, err)
		}
		checkCalls(t, c.name, fake, "", s.want)
	}
}

// With the remote's commits changing what's not committed here, nothing's
// set aside or rebased, nothing pushed, and Push names the files, and what
// to do with each: a file edited here, new here, or in a repository nested
// here, which would come back with git's markers in it, kit reconcile's to
// commit or undo; a change only staged, its file on disk as last
// committed, which drift doesn't show, the user's to commit or unstage; a
// file git ignores here, which the rebase would replace, as real git does
// without a word; and a folder of the remote's where a file
// is here, or a file where a folder is, which git would refuse to check
// out. What git prints is what real git does.
func TestPushLeavesAClashAlone(t *testing.T) {
	const reconcile = " changed here: commit or undo the change with kit reconcile"
	for _, c := range []struct {
		name, status, incoming, ignored, numstat, want string
	}{
		{name: "a file edited here", status: " M f\x00?? notes\x00", incoming: "f\x00g\x00", numstat: "1\t1\tf\x00", want: "the remote's commits change what's not committed here, so nothing was touched or pushed: f" + reconcile},
		{name: "a file new here", status: "?? r\x00", incoming: "r\x00", want: ": r" + reconcile},
		{name: "a nested repository", status: "?? vendor/tool/\x00", incoming: "vendor/tool/x\x00", want: ": vendor/tool/" + reconcile},
		{name: "a change only staged", status: "MM notes\x00", incoming: "notes\x00", want: ": notes staged here, its file on disk as last committed: git's index is yours, to commit or unstage (git restore --staged notes)"},
		{name: "a file git ignores", incoming: ".gitignore\x00secret.env\x00", ignored: "cache/\x00secret.env\x00", want: ": secret.env ignored here, which the remote's would replace: move it aside"},
		{name: "a file here, the remote's folder", status: "?? x\x00", incoming: "x/y\x00", want: ": x" + reconcile},
		{name: "a folder's file here, the remote's file", status: "?? d/new\x00", incoming: "d\x00", want: ": d/new" + reconcile},
	} {
		fake := runnertest.New(t)
		s := pushing(fake, t.TempDir(), c.status, "1")
		s.incoming(c.incoming, c.ignored)
		if c.status != "" {
			s.drift(c.status, c.numstat)
		}
		if err := push(t, fake); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: Push() error = %v, want %q", c.name, err, c.want)
		}
		checkCalls(t, c.name, fake, "", s.want)
	}
}

// A folder git ignores whole, listed as one, clashes only where the
// remote's would replace what's in it, as real git checks out into it
// without a word: a file there, or a file where the remote's needs a
// folder, or the folder itself made a file; a file new to it, or one in a
// folder of it, is no clash. Each is named once, however git lists it, as
// when a rule ignores the file by its name too, and git lists the folder
// and the file. Folding case, git folds ASCII letters alone, so the Kelvin
// sign is no k. What git lists is what real git does: the folder alone
// under logs/, the folder and its file under *.log.
func TestPushLeavesAnIgnoredFolderAlone(t *testing.T) {
	for _, c := range []struct {
		name, incoming, ignored string
		files                   []string
		want                    string
	}{
		{"a file new to it", "logs/new\x00", "logs/\x00", []string{"logs/a.log"}, ""},
		{"a file in a folder of it", "logs/2026/new\x00", "logs/\x00", []string{"logs/2026/old"}, ""},
		{"a file of it", "logs/a.log\x00", "logs/\x00", []string{"logs/a.log"}, ": logs/a.log ignored here, which the remote's would replace: move it aside"},
		{"a file of it, ignored by its name too", "logs/a.log\x00", "logs/\x00logs/a.log\x00", []string{"logs/a.log"}, ": logs/a.log ignored here, which the remote's would replace: move it aside"},
		{"a folder named by a letter git doesn't fold", "k/x\x00", "\u212a/\x00", []string{"\u212a/a"}, ""},
		{"a file where a folder's needed", "logs/sub/x\x00", "logs/\x00", []string{"logs/sub"}, ": logs/sub ignored here"},
		{"the folder made a file", "logs\x00", "logs/\x00", []string{"logs/a.log"}, ": logs/ ignored here"},
	} {
		root := repoAt(t, c.files...)
		fake := runnertest.New(t)
		s := pushingIn(fake, root, t.TempDir(), "", "1")
		s.incoming(c.incoming, c.ignored)
		if c.want == "" {
			s.expect(call{args: rebase})
			s.expect(call{args: pushIt})
		}
		_, err := gitrepo.Repo{Dir: root, Run: fake}.Push(context.Background())
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: Push() error = %v", c.name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want) || strings.Count(err.Error(), "logs") != 1):
			t.Errorf("%s: Push() error = %v, want %q, named once", c.name, err, c.want)
		}
		checkCalls(t, c.name, fake, "", s.want)
	}
}

// A file git ignores, named by a letter git doesn't fold, as the Kelvin
// sign, is no clash with the remote's k, as git folds ASCII letters alone;
// on macOS's own disks, which fold it, git then refuses to rebase over it,
// naming k, and nothing's pushed. What git prints is what real git does.
func TestPushFoldsASCIILettersAlone(t *testing.T) {
	root := repoAt(t, "K")
	fake := runnertest.New(t)
	s := pushingIn(fake, root, t.TempDir(), "", "1")
	s.incoming("k\x00", "K\x00")
	s.expect(call{args: rebase}).Exits(1).PrintsToStderr("error: The following untracked working tree files would be overwritten by checkout:\n\tk\nPlease move or remove them before you switch branches.\nAborting\nerror: could not detach HEAD\n")
	_, err := gitrepo.Repo{Dir: root, Run: fake}.Push(context.Background())
	if err == nil || !strings.HasPrefix(err.Error(), "couldn't rebase onto the remote's changes") {
		t.Errorf("Push() error = %v, want git's refusal to rebase", err)
	}
	checkCalls(t, "a letter git doesn't fold", fake, "", s.want)
}

// A file is named once, however git lists it: one both changed here and
// ignored, as one staged as deleted and still on disk, git ignoring it now,
// as changed, as committing or undoing that change lets the push go on;
// one git's status lists twice, staged as deleted and untracked on disk, as
// git rm --cached leaves it, once. What git lists is what real git does.
func TestPushNamesAClashOnce(t *testing.T) {
	root := repoAt(t, "secret")
	fake := runnertest.New(t)
	s := pushingIn(fake, root, t.TempDir(), "D  secret\x00", "1")
	s.incoming("secret\x00", "secret\x00")
	s.drift("D  secret\x00", "")
	_, err := gitrepo.Repo{Dir: root, Run: fake}.Push(context.Background())
	if err == nil || !strings.Contains(err.Error(), ": secret changed here") || strings.Count(err.Error(), "secret") != 1 {
		t.Errorf("Push() error = %v, want secret named once, as changed", err)
	}
	checkCalls(t, "changed and ignored", fake, "", s.want)

	root = repoAt(t, "kept")
	fake = runnertest.New(t)
	s = pushingIn(fake, root, t.TempDir(), "D  kept\x00?? kept\x00", "1")
	s.incoming("kept\x00", "")
	s.drift("D  kept\x00?? kept\x00", "")
	_, err = gitrepo.Repo{Dir: root, Run: fake}.Push(context.Background())
	if err == nil || !strings.Contains(err.Error(), ": kept staged here") || strings.Contains(err.Error(), "kept, kept") {
		t.Errorf("Push() error = %v, want kept named once", err)
	}
	checkCalls(t, "listed twice", fake, "", s.want)
}

// Paths that differ only by case are one where git folds case, as on
// macOS's own disks, and checking out the remote's would meet the file
// here; two where it doesn't, and the rebase goes on.
func TestPushFoldsCaseAsTheDiskDoes(t *testing.T) {
	fake := runnertest.New(t)
	s := pushing(fake, t.TempDir(), "?? notes\x00", "1")
	s.incoming("Notes\x00", "")
	s.drift("?? notes\x00", "")
	if err := push(t, fake); err == nil || !strings.Contains(err.Error(), "notes changed here") {
		t.Errorf("Push() folding case = %v, want notes named", err)
	}
	checkCalls(t, "folding case", fake, "", s.want)

	// Unset, git exits 1; set false, it says so.
	for _, folds := range []struct {
		name, prints string
		exit         int
	}{{"unset", "", 1}, {"set false", "false\n", 0}} {
		fake = runnertest.New(t)
		s = pushing(fake, t.TempDir(), "?? notes\x00", "1")
		s.expect(call{args: incomingOf}).Prints("Notes\x00")
		s.expect(call{args: foldsCase}).Prints(folds.prints).Exits(folds.exit)
		s.expect(call{args: ignoredHere})
		s.expect(call{args: rebase})
		s.expect(call{args: pushIt})
		if err := push(t, fake); err != nil {
			t.Errorf("Push(), core.ignorecase %s = %v", folds.name, err)
		}
		checkCalls(t, "core.ignorecase "+folds.name, fake, "", s.want)
	}
}

// A stash that won't go back is kept, never dropped, and nothing's pushed:
// one real git leaves whole, the tree clean, as when it can't put back
// what was staged, said to be wholly in the stash; one it put back with
// conflicts, its markers in a file, said so, naming the file and what to
// run; one no longer git's latest left be, never popped.
func TestPushKeepsAStashThatWontGoBack(t *testing.T) {
	for _, c := range []struct {
		name, latest, after string
		exit                int
		stderr, want        string
	}{
		{"its staged part won't go back", kitsStash, "", 128, "error: conflicts in index. Try without --index.\n", `what wasn't committed is wholly in git's stash, ` + kitsStash + ` ("kit: set aside while rebasing onto the remote"), as it couldn't be put back, so nothing was pushed: git stash apply ` + kitsStash + ` puts it back`},
		{"back with conflicts", kitsStash, "UU f\x00 M g\x00", 1, "Auto-merging f\nCONFLICT (content): Merge conflict in f\nOn branch main\nUnmerged paths:\n\tboth modified:   f\nIndex was not unstashed.\nThe stash entry is kept in case you need it again.\n", "what wasn't committed came back with conflicts in f: each holds the remote's version and this Mac's, between git's <<<<<<< Updated upstream and >>>>>>> Stashed changes lines, so nothing was pushed: make each what it should be, git add it, then git stash drop, as git's stash, " + kitsStash + ", still holds it all"},
		{"no longer git's latest", theirStash, "", 0, "", "what wasn't committed is wholly in git's stash, " + kitsStash},
	} {
		fake := runnertest.New(t)
		s := pushing(fake, t.TempDir(), " M f\x00 M g\x00", "1")
		s.incoming("r\x00", "")
		s.stashes("", kitsStash)
		s.expect(call{args: rebase})
		if pop := s.pops(c.latest); pop != nil {
			pop.Exits(c.exit).PrintsToStderr(c.stderr)
			// What's left once it won't go back.
			s.expect(call{args: wholeStatus}).Prints(c.after)
		}
		if err := push(t, fake); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: Push() error = %v, want %q", c.name, err, c.want)
		}
		checkCalls(t, c.name, fake, "", s.want)
	}
}

// A rebase that stops on a conflict is aborted, by git's own account of it
// under way, the work set aside put back, and nothing pushed. One git
// couldn't abort, still under way, keeps the stash, named, never putting it
// back onto it; one that never started, as when a pre-rebase hook refuses,
// or a file's in the way the clashes missed, leaves nothing to abort, and
// the work goes back. What git prints is what real git does.
func TestPushWhenTheRebaseStops(t *testing.T) {
	const conflict = "Auto-merging f\nCONFLICT (content): Merge conflict in f\nerror: could not apply 7ff9573... kit\n"
	for _, c := range []struct {
		name, stderr    string
		starts, aborted bool
		want            string
	}{
		{"a conflict", conflict, true, true, "the remote's changes conflict with this Mac's, so nothing was pushed; the rebase was undone"},
		{"a conflict git couldn't abort", conflict, true, false, "git rebase --abort undoes it"},
		{"refused by a hook", "pre-rebase says no\nerror: The pre-rebase hook refused to rebase.\n", false, false, "couldn't rebase onto the remote's changes, so nothing was pushed: git -C " + dir + " rebase --no-fork-point --quiet refs/remotes/origin/main exited 1: error: The pre-rebase hook refused to rebase."},
		{"a file in the way", "error: The following untracked working tree files would be overwritten by checkout:\n\tnew\nPlease move or remove them before you switch branches.\nAborting\nerror: could not detach HEAD\n", false, false, "couldn't rebase onto the remote's changes, so nothing was pushed: git -C " + dir + " rebase --no-fork-point --quiet refs/remotes/origin/main exited 1: error: could not detach HEAD"},
	} {
		gitDir := t.TempDir()
		fake := runnertest.New(t)
		s := pushing(fake, gitDir, " M f\x00", "1")
		s.incoming("r\x00", "")
		s.stashes("", kitsStash)
		rebasing(s.expect(call{args: rebase}).Exits(1).PrintsToStderr(c.stderr), gitDir, c.starts)
		if c.starts {
			ab := s.expect(call{args: abort})
			if c.aborted {
				rebasing(ab, gitDir, false)
			} else {
				ab.Exits(128).PrintsToStderr("error: Unable to create '" + gitDir + "/index.lock': File exists.\n\nAnother git process seems to be running in this repository, or the lock file may be stale\nfatal: could not move back to 22f46b0\n")
			}
		}
		if c.aborted || !c.starts {
			s.pops(kitsStash)
		}
		err := push(t, fake)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: Push() error = %v, want %q", c.name, err, c.want)
		}
		if kept := strings.Contains(err.Error(), kitsStash); kept == (c.aborted || !c.starts) {
			t.Errorf("%s: Push() error = %v; the stash named %v", c.name, err, kept)
		}
		checkCalls(t, c.name, fake, "", s.want)
	}
}

func TestPushFails(t *testing.T) {
	fetchFails := func(stderr string) func(*runnertest.Fake) *sequence {
		return func(fake *runnertest.Fake) *sequence {
			s := &sequence{fake: fake}
			s.expect(call{args: git("remote")}).Prints("origin\n")
			s.expect(call{args: gitDirOf}).Prints(t.TempDir() + "\n")
			s.expect(call{args: wholeStatus})
			s.tracks(tracksOrigin, "", "")
			s.expect(call{args: fetchIt}).Exits(128).PrintsToStderr(stderr)
			return s
		}
	}
	for _, c := range []struct {
		name, want string
		script     func(fake *runnertest.Fake) *sequence
	}{
		{"offline, over https", "couldn't reach the remote, so nothing was pushed", fetchFails("fatal: unable to access 'https://github.com/someone/kit-config.git/': Could not resolve host: github.com\n")},
		{"offline, over ssh", "couldn't reach the remote, so nothing was pushed", fetchFails(offline)},
		{"the remote gone", "couldn't bring in the remote's changes, so nothing was pushed", fetchFails("fatal: 'someone/kit-config.git' does not appear to be a git repository\nfatal: Could not read from remote repository.\n\nPlease make sure you have the correct access rights\nand the repository exists.\n")},
		{"offline, the port closed", "couldn't reach the remote, so nothing was pushed", fetchFails("ssh: connect to host 127.0.0.1 port 1: Connection refused\nfatal: Could not read from remote repository.\n\nPlease make sure you have the correct access rights\nand the repository exists.\n")},
		{"offline, the host silent", "couldn't reach the remote, so nothing was pushed", fetchFails("Connection timed out during banner exchange\nConnection to 127.0.0.1 port 52223 timed out\nfatal: Could not read from remote repository.\n\nPlease make sure you have the correct access rights\nand the repository exists.\n")},
		{"a transport's own line", "couldn't bring in the remote's changes, so nothing was pushed", fetchFails("mirror: Connection refused by the first mirror, its repository gone\nfatal: Could not read from remote repository.\n\nPlease make sure you have the correct access rights\nand the repository exists.\n")},
		{"HEAD on no branch", "HEAD isn't on a branch, so nothing was pushed", func(fake *runnertest.Fake) *sequence {
			s := &sequence{fake: fake}
			s.expect(call{args: git("remote")}).Prints("origin\n")
			s.expect(call{args: gitDirOf}).Prints(t.TempDir() + "\n")
			s.expect(call{args: wholeStatus})
			s.expect(call{args: branchOf}).Exits(1)
			return s
		}},
		{"nothing set aside", "couldn't set aside what's not committed, so nothing was pushed", func(fake *runnertest.Fake) *sequence {
			s := pushing(fake, t.TempDir(), " M f\x00", "1")
			s.incoming("r\x00", "")
			s.expect(call{args: stashRef}).Exits(1)
			s.expect(call{args: stashPush}).Exits(1).PrintsToStderr("fatal: Unable to create '/Users/someone/.config/kit/.git/index.lock': File exists.\n\nAnother git process seems to be running in this repository, or the lock file may be stale\n")
			return s
		}},
		{"the push refused", "couldn't push", func(fake *runnertest.Fake) *sequence {
			s := pushing(fake, t.TempDir(), "", "0")
			s.expect(call{args: pushIt}).Exits(1).PrintsToStderr("To github.com:someone/kit-config.git\n ! [rejected]        main -> main (fetch first)\nerror: failed to push some refs to 'github.com:someone/kit-config.git'\n")
			return s
		}},
	} {
		fake := runnertest.New(t)
		s := c.script(fake)
		err := push(t, fake)
		if err == nil || !strings.Contains(err.Error(), c.want) || strings.HasPrefix(c.name, "offline") && strings.Contains(err.Error(), "fatal") {
			t.Errorf("%s: Push() error = %v, want %q", c.name, err, c.want)
		}
		checkCalls(t, c.name, fake, "", s.want)
	}
}

// A remote with nothing of the branch yet takes it whole: with the
// branch's upstream set to it but not there, as for a clone of an empty
// remote, there's nothing to bring in, and the branch is pushed to it, as
// Pull pushes nothing; with none set, as for a remote added, it's pushed to
// the same branch of the remote it's pushed to, which becomes its upstream:
// branch.<name>.pushRemote, then remote.pushDefault, then origin, or the
// only remote. What git says is what real git does.
func TestPushToANewRemote(t *testing.T) {
	for _, pull := range []bool{false, true} {
		fake := runnertest.New(t)
		s := &sequence{fake: fake}
		s.fetches(t.TempDir(), "")
		s.expect(call{args: upstreamAt}).Exits(1)
		s.expect(call{args: branchesOf})
		if !pull {
			s.expect(call{args: pushIt})
		}
		if err := pushOrPull(t, fake, pull); err != nil {
			t.Errorf("pull %v, the upstream not there = %v", pull, err)
		}
		checkCalls(t, "the upstream not there", fake, "", s.want)
	}

	for _, c := range []struct{ name, remotes, pushRemote, pushDefault, to string }{
		{"origin", "backup\norigin\n", "", "", "origin"},
		{"the only remote", "backup\n", "", "", "backup"},
		{"the branch's push remote", "backup\norigin\n", "backup", "", "backup"},
		{"the remotes' push default", "backup\norigin\n", "", "backup", "backup"},
		{"a remote named with a slash", "origin\nteam/fork\n", "team/fork", "", "team/fork"},
	} {
		fake := runnertest.New(t)
		s := &sequence{fake: fake}
		s.expect(call{args: git("remote")}).Prints(c.remotes)
		s.expect(call{args: gitDirOf}).Prints(t.TempDir() + "\n")
		s.expect(call{args: wholeStatus})
		s.tracks("\x00\n", c.pushRemote, c.pushDefault)
		s.expect(call{args: git("fetch", "--prune", "--quiet", c.to)})
		s.expect(call{args: git("rev-parse", "--verify", "--quiet", "refs/remotes/"+c.to+"/main")}).Exits(1)
		s.expect(call{args: git("push", "--quiet", "-u", c.to, "HEAD")})
		if err := push(t, fake); err != nil {
			t.Errorf("Push(), no upstream, %s = %v", c.name, err)
		}
		checkCalls(t, "no upstream, "+c.name, fake, "", s.want)
	}
}

// A branch set to be pushed to another remote than its upstream's, as in a
// triangular workflow, brings in from its upstream's remote, and is pushed
// with git push, which sends it to the other. That remote's fetched too,
// and its branch, with commits this Mac hasn't, refused, naming them,
// before anything's rebased, as git push would refuse it, or, forced, lose
// them. Pull, pushing nowhere, brings in from the upstream's remote alone;
// Holding refuses as Push would, as far as the last fetch tells.
func TestPushToAnotherRemoteThanItsUpstreams(t *testing.T) {
	const backupAt = "refs/remotes/backup/main"
	for _, c := range []struct {
		name string
		// at is where backup's main is, "" for not there, and ahead its
		// commits HEAD hasn't.
		at, ahead, want string
	}{
		{name: "the branch there not moved on", at: upstream, ahead: "0"},
		{name: "the branch not there"},
		{name: "the branch moved on", at: upstream, ahead: "2", want: "backup/main, where main is pushed, has 2 commits this Mac hasn't, so nothing was pushed: kit doesn't rebase onto one remote and push to another; bring them in, or push over them, by hand"},
	} {
		for _, holding := range []bool{false, true} {
			fake := runnertest.New(t)
			s := &sequence{fake: fake}
			if holding {
				s.expect(call{args: gitDirOf}).Prints(t.TempDir() + "\n")
				s.expect(call{args: wholeStatus})
			}
			s.expect(call{args: git("remote")}).Prints("backup\norigin\n")
			if !holding {
				s.expect(call{args: gitDirOf}).Prints(t.TempDir() + "\n")
				s.expect(call{args: wholeStatus})
			}
			s.tracks(tracksOrigin, "", "backup")
			if !holding {
				s.expect(call{args: fetchIt})
			}
			s.expect(call{args: upstreamAt}).Prints(upstream + "\n")
			if !holding {
				s.expect(call{args: git("fetch", "--prune", "--quiet", "backup")})
			}
			names(s.expect(call{args: git("rev-parse", "--verify", "--quiet", backupAt)}), c.at)
			if c.at != "" {
				s.expect(call{args: git("rev-list", "--count", "HEAD.."+backupAt)}).Prints(c.ahead + "\n")
			}
			if c.want == "" {
				s.expect(call{args: behindOf}).Prints("0\n")
				if !holding {
					s.expect(call{args: pushIt})
				}
			}
			repo := gitrepo.Repo{Dir: dir, Run: fake}
			var err error
			if holding {
				err = repo.Holding(context.Background())
			} else {
				_, err = repo.Push(context.Background())
			}
			if (err == nil) != (c.want == "") || err != nil && err.Error() != c.want {
				t.Errorf("%s, holding %v: %v, want %q", c.name, holding, err, c.want)
			}
			checkCalls(t, c.name, fake, "", s.want)
		}
	}

	fake := runnertest.New(t)
	s := &sequence{fake: fake}
	s.expect(call{args: git("remote")}).Prints("backup\norigin\n")
	s.expect(call{args: gitDirOf}).Prints(t.TempDir() + "\n")
	s.expect(call{args: wholeStatus})
	s.tracks(tracksOrigin, "", "backup")
	s.expect(call{args: fetchIt})
	s.expect(call{args: upstreamAt}).Prints(upstream + "\n")
	s.expect(call{args: behindOf}).Prints("0\n")
	if err := pushOrPull(t, fake, true); err != nil {
		t.Errorf("Pull(), pushed to another remote = %v", err)
	}
	checkCalls(t, "pulled, pushed to another remote", fake, "", s.want)
}

// A branch set to be pushed to a URL, or to a remote that isn't one, is
// refused, as git push would push there unchecked: a URL said plainly,
// never shown, as it may carry a token.
func TestPushRefusesAPushRemoteThatIsntOne(t *testing.T) {
	for _, c := range []struct{ name, branchSays, defaultSays, want string }{
		{"a URL", "git@github.com:someone/fork.git", "", "branch.main.pushRemote is a URL, not a remote's name, so nothing was pushed: kit pushes only to one of the repository's remotes, by its name"},
		// Split at its @, which the personal-data scan would take for an
		// email address's.
		{"a URL with a token", "", "https://x-access-token:secret@" + "github.com/someone/fork", "remote.pushDefault is a URL, not a remote's name, so nothing was pushed: kit pushes only to one of the repository's remotes, by its name"},
		{"a local path", "", "../fork", "remote.pushDefault is a URL, not a remote's name, so nothing was pushed: kit pushes only to one of the repository's remotes, by its name"},
		{"no remote", "fork", "", "branch.main.pushRemote is fork, not one of the repository's remotes, so nothing was pushed: kit pushes only to a remote by its name"},
	} {
		fake := runnertest.New(t)
		s := &sequence{fake: fake}
		s.expect(call{args: git("remote")}).Prints("origin\n")
		s.expect(call{args: gitDirOf}).Prints(t.TempDir() + "\n")
		s.expect(call{args: wholeStatus})
		s.tracks(tracksOrigin, c.branchSays, c.defaultSays)
		if err := push(t, fake); err == nil || err.Error() != c.want {
			t.Errorf("Push(), pushed to %s = %v, want %q", c.name, err, c.want)
		}
		checkCalls(t, c.name, fake, "", s.want)
	}
}

// PushURLs are where Push pushes, as git push has them: the remote the
// branch is pushed to, its upstream's or the one set, and its push URLs, a
// line each; ErrNowhere when Push can't tell where to push, ErrNoRemote
// without a remote, and git's error when it can't name the URLs.
func TestPushURLs(t *testing.T) {
	const private, public = "git@github.com:someone/prefs.git", "git@github.com:someone/public.git"
	for _, c := range []struct {
		name, remotes, upstream, pushDefault, to, urls string
		want                                           []string
		err                                            string
	}{
		{"the upstream's remote", "origin\n", tracksOrigin, "", "origin", private + "\n", []string{private}, ""},
		{"the remote set, two push URLs", "backup\norigin\n", tracksOrigin, "backup", "backup", private + "\n" + public + "\n", []string{private, public}, ""},
		{"a path with a space", "backup\norigin\n", tracksOrigin, "backup", "backup", "/Volumes/My Backups/kit.git\n", []string{"/Volumes/My Backups/kit.git"}, ""},
		{"no upstream, nor a remote set", "backup\nupstream\n", "\x00\n", "", "", "", nil, "main has no upstream, and which remote it's pushed to isn't set"},
	} {
		fake := runnertest.New(t)
		s := &sequence{fake: fake}
		s.expect(call{args: git("remote")}).Prints(c.remotes)
		s.tracks(c.upstream, "", c.pushDefault)
		if c.to != "" {
			s.expect(call{args: git("remote", "get-url", "--push", "--all", c.to)}).Prints(c.urls)
		}
		remote, urls, err := gitrepo.Repo{Dir: dir, Run: fake}.PushURLs(context.Background())
		if remote != c.to || !slices.Equal(urls, c.want) || (err == nil) != (c.err == "") || err != nil && (!strings.HasPrefix(err.Error(), c.err) || !errors.Is(err, gitrepo.ErrNowhere)) {
			t.Errorf("%s: PushURLs() = %q, %q, %v; want %q, %q, %q", c.name, remote, urls, err, c.to, c.want, c.err)
		}
		checkCalls(t, c.name, fake, "", s.want)
	}

	fake := runnertest.New(t)
	fake.On("git", git("remote")...)
	if _, _, err := (gitrepo.Repo{Dir: dir, Run: fake}).PushURLs(context.Background()); !errors.Is(err, gitrepo.ErrNoRemote) {
		t.Errorf("PushURLs(), no remote = %v, want ErrNoRemote", err)
	}

	fake = runnertest.New(t)
	s := &sequence{fake: fake}
	s.expect(call{args: git("remote")}).Prints("origin\n")
	s.tracks(tracksOrigin, "", "")
	s.expect(call{args: git("remote", "get-url", "--push", "--all", "origin")}).Exits(128).PrintsToStderr("fatal: unable to read config file '.git/config': Permission denied\n")
	if remote, urls, err := (gitrepo.Repo{Dir: dir, Run: fake}).PushURLs(context.Background()); remote != "" || urls != nil || err == nil || errors.Is(err, gitrepo.ErrNowhere) {
		t.Errorf("PushURLs(), the URLs unread = %q, %q, %v; want git's error", remote, urls, err)
	}
	checkCalls(t, "the URLs unread", fake, "", s.want)
}

// URL is a remote's fetch URL, as git takes it, none without that remote.
func TestURL(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("git", git("remote", "get-url", "origin")...).Prints("git@github.com:someone/kit.git\n")
	fake.On("git", git("remote", "get-url", "backup")...).Exits(2).PrintsToStderr("error: No such remote 'backup'\n")
	fake.On("git", git("remote", "get-url", "broken")...).Exits(128).PrintsToStderr("fatal: not a git repository (or any of the parent directories): .git\n")
	repo := gitrepo.Repo{Dir: dir, Run: fake}
	for _, c := range []struct{ remote, want, err string }{
		{"origin", "git@github.com:someone/kit.git", ""},
		{"backup", "", ""},
		{"broken", "", "fatal: not a git repository"},
	} {
		url, err := repo.URL(context.Background(), c.remote)
		if url != c.want || (err == nil) != (c.err == "") || err != nil && !strings.Contains(err.Error(), c.err) {
			t.Errorf("URL(%s) = %q, %v; want %q, %q", c.remote, url, err, c.want, c.err)
		}
	}
}

// Each of Push's refusals for want of where to push is ErrNowhere: HEAD on
// no branch, a push setting that's a URL or names no remote.
func TestPushNowhere(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("git", git("remote")...).Prints("origin\n")
	fake.On("git", branchOf...).Exits(1)
	if _, _, err := (gitrepo.Repo{Dir: dir, Run: fake}).PushURLs(context.Background()); err == nil || err.Error() != "HEAD isn't on a branch, so nothing was pushed" || !errors.Is(err, gitrepo.ErrNowhere) {
		t.Errorf("PushURLs(), HEAD on no branch = %v, want ErrNowhere", err)
	}
	for _, setting := range []string{"git@github.com:someone/fork.git", "fork"} {
		fake := runnertest.New(t)
		s := &sequence{fake: fake}
		s.expect(call{args: git("remote")}).Prints("origin\n")
		s.tracks(tracksOrigin, setting, "")
		if _, _, err := (gitrepo.Repo{Dir: dir, Run: fake}).PushURLs(context.Background()); !errors.Is(err, gitrepo.ErrNowhere) {
			t.Errorf("PushURLs(), pushRemote %s = %v, want ErrNowhere", setting, err)
		}
	}
}

// A branch with no upstream, its remote's branch of the same name there,
// as when the upstream was never set or was unset, follows that: made its
// upstream, caught up with, then pushed, never pushed over it.
func TestPushFollowsTheRemotesBranch(t *testing.T) {
	fake := runnertest.New(t)
	s := &sequence{fake: fake}
	s.expect(call{args: git("remote")}).Prints("origin\n")
	s.expect(call{args: gitDirOf}).Prints(t.TempDir() + "\n")
	s.expect(call{args: wholeStatus})
	s.tracks("\x00\n", "", "")
	s.expect(call{args: fetchIt})
	s.expect(call{args: upstreamAt}).Prints(upstream + "\n")
	s.expect(call{args: git("branch", "--quiet", "--set-upstream-to=refs/remotes/origin/main")})
	s.expect(call{args: behindOf}).Prints("1\n")
	s.incoming("r\x00", "")
	s.expect(call{args: rebase})
	s.expect(call{args: pushIt})
	if err := push(t, fake); err != nil {
		t.Errorf("Push() = %v", err)
	}
	checkCalls(t, "the remote's branch there", fake, "", s.want)
}

// Push refuses, pushing nothing: an upstream gone from a remote that has
// other branches, as when it's renamed or deleted there, which pushing
// would make anew; and, before anything's brought in, a branch with no
// upstream, several remotes and none set to push to. Holding says so too.
// What git says is what real git does.
func TestPushRefusesWithNowhereToGo(t *testing.T) {
	for _, c := range []struct {
		name, remotes, upstream string
		gone                    bool
		want                    string
	}{
		{"the upstream gone", "origin\n", tracksOrigin, true, "main's upstream, origin/main, is gone from origin, as when it's renamed or deleted there, so nothing was pushed: git branch --set-upstream-to=<remote>/<branch> follows the one it's become"},
		{"no remote set", "backup\nupstream\n", "\x00\n", false, "main has no upstream, and which remote it's pushed to isn't set, so nothing was pushed: git config remote.pushDefault <remote> sets it"},
	} {
		for _, holding := range []bool{false, true} {
			fake := runnertest.New(t)
			s := &sequence{fake: fake}
			if !holding {
				s.expect(call{args: git("remote")}).Prints(c.remotes)
			}
			s.expect(call{args: gitDirOf}).Prints(t.TempDir() + "\n")
			s.expect(call{args: wholeStatus})
			if holding {
				s.expect(call{args: git("remote")}).Prints(c.remotes)
			}
			s.tracks(c.upstream, "", "")
			if c.gone {
				if !holding {
					s.expect(call{args: fetchIt})
				}
				s.expect(call{args: upstreamAt}).Exits(1)
				s.expect(call{args: branchesOf}).Prints("refs/remotes/origin/HEAD\nrefs/remotes/origin/trunk\n")
			}
			repo := gitrepo.Repo{Dir: dir, Run: fake}
			check := func(ctx context.Context) error {
				_, err := repo.Push(ctx)
				return err
			}
			if holding {
				check = repo.Holding
			}
			err := check(context.Background())
			if err == nil || err.Error() != c.want {
				t.Errorf("%s, holding %v: %v, want %q", c.name, holding, err, c.want)
			}
			checkCalls(t, c.name, fake, "", s.want)
		}
	}
}

func TestPushWithoutARemote(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("git", git("remote")...)
	if err := push(t, fake); !errors.Is(err, gitrepo.ErrNoRemote) {
		t.Errorf("Push() error = %v, want ErrNoRemote", err)
	}
}

// Holding is why Push would hold back now, as far as the last fetch tells,
// fetching nothing: a clash, or git part way through; nothing when the
// remote's last known commits are all here.
func TestHolding(t *testing.T) {
	holding := func(fake *runnertest.Fake) error {
		return gitrepo.Repo{Dir: dir, Run: fake}.Holding(context.Background())
	}
	settled := func(fake *runnertest.Fake, status, behind string) *sequence {
		s := &sequence{fake: fake}
		s.expect(call{args: gitDirOf}).Prints(t.TempDir() + "\n")
		s.expect(call{args: wholeStatus}).Prints(status)
		s.expect(call{args: git("remote")}).Prints("origin\n")
		s.tracks(tracksOrigin, "", "")
		s.expect(call{args: upstreamAt}).Prints(upstream + "\n")
		s.expect(call{args: behindOf}).Prints(behind + "\n")
		return s
	}

	fake := runnertest.New(t)
	s := settled(fake, "MM notes\x00", "0")
	if err := holding(fake); err != nil {
		t.Errorf("Holding(), nothing new = %v", err)
	}
	checkCalls(t, "nothing new", fake, "", s.want)

	fake = runnertest.New(t)
	s = settled(fake, "MM notes\x00", "1")
	s.incoming("notes\x00", "")
	s.drift("MM notes\x00", "")
	if err := holding(fake); err == nil || !strings.Contains(err.Error(), "notes staged here") {
		t.Errorf("Holding(), a staged change the remote changed = %v", err)
	}
	checkCalls(t, "a clash", fake, "", s.want)

	gitDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(gitDir, "MERGE_HEAD"), 0o700); err != nil {
		t.Fatal(err)
	}
	fake = runnertest.New(t)
	fake.On("git", gitDirOf...).Prints(gitDir + "\n")
	if err := holding(fake); err == nil || !strings.Contains(err.Error(), "a merge is under way") {
		t.Errorf("Holding(), mid-merge = %v", err)
	}
}

// Waiting is when each commit waiting to be pushed was made, and Unpushed
// how many: those the branch Push pushes to hasn't, the upstream, or, when
// another remote is set to be pushed to, the same branch of that; with it
// not there, or no upstream set, those on none of that remote's branches;
// on none of any remote's, when which isn't set; none without a remote.
// What git prints is what real git does.
func TestWaiting(t *testing.T) {
	const backupAt = "refs/remotes/backup/main"
	at := time.Unix(1792552092, 0)
	for _, c := range []struct {
		name, remotes, upstream, pushDefault string
		// pushedTo is the remote-tracking ref of the branch it's pushed
		// to, and there whether it is.
		pushedTo string
		there    bool
		log      []string
	}{
		{"the upstream there", "origin\n", tracksOrigin, "", upstreamRef, true, []string{upstreamRef + "..HEAD"}},
		{"the upstream not there", "origin\n", tracksOrigin, "", upstreamRef, false, []string{"HEAD", "--not", "--remotes=origin"}},
		{"pushed to another remote, its branch there", "backup\norigin\n", tracksOrigin, "backup", backupAt, true, []string{backupAt + "..HEAD"}},
		{"pushed to another remote, its branch not there", "backup\norigin\n", tracksOrigin, "backup", backupAt, false, []string{"HEAD", "--not", "--remotes=backup"}},
		{"no upstream", "origin\n", "\x00\n", "", upstreamRef, false, []string{"HEAD", "--not", "--remotes=origin"}},
		{"no upstream, nor a remote set", "backup\nupstream\n", "\x00\n", "", "", false, []string{"HEAD", "--not", "--remotes"}},
	} {
		fake := runnertest.New(t)
		s := &sequence{fake: fake}
		s.expect(call{args: git("remote")}).Prints(c.remotes)
		s.tracks(c.upstream, "", c.pushDefault)
		if c.pushedTo != "" {
			names(s.expect(call{args: git("rev-parse", "--verify", "--quiet", c.pushedTo)}), map[bool]string{true: upstream}[c.there])
		}
		s.expect(call{args: git(append([]string{"log", "--format=%ct"}, c.log...)...)}).Prints("1792552092\n1792552000\n")
		repo := gitrepo.Repo{Dir: dir, Run: fake}
		if made, err := repo.Waiting(context.Background()); err != nil || !slices.Equal(made, []time.Time{at, time.Unix(1792552000, 0)}) {
			t.Errorf("%s: Waiting() = %v, %v", c.name, made, err)
		}
		checkCalls(t, c.name, fake, "", s.want)
	}

	fake := runnertest.New(t)
	fake.On("git", git("remote")...)
	repo := gitrepo.Repo{Dir: dir, Run: fake}
	if _, err := repo.Waiting(context.Background()); !errors.Is(err, gitrepo.ErrNoRemote) {
		t.Errorf("Waiting(), no remote = %v, want ErrNoRemote", err)
	}
	if n := repo.Unpushed(context.Background()); n != 0 {
		t.Errorf("Unpushed(), no remote = %d", n)
	}

	fake = runnertest.New(t)
	fake.On("git", git("remote")...).Prints("origin\n")
	fake.On("git", branchOf...).Prints("refs/heads/main\n")
	fake.On("git", upstreamOf...).Prints(tracksOrigin)
	fake.On("git", pushRemoteOf...).Exits(1)
	fake.On("git", pushDefault...).Exits(1)
	fake.On("git", upstreamAt...).Prints(upstream + "\n")
	fake.On("git", git("log", "--format=%ct", "refs/remotes/origin/main..HEAD")...).Prints("1792552092\n")
	if n := (gitrepo.Repo{Dir: dir, Run: fake}).Unpushed(context.Background()); n != 1 {
		t.Errorf("Unpushed() = %d, want 1", n)
	}
}

// Push and Pull deal with what a run of kit's that stopped left, as Commit
// does, and say what they did: a lock HEAD's moved onto the commit of put in
// the index's place; the index repaired once the lock was removed, the mark
// then cleared. With no mark, they never take kit's lock on
// committing, so never wait for a kit at work, a commit waiting on its
// signing, say; while git's part way through one of its operations, they
// leave what's left be, the mark kept.
func TestPushDealsWithWhatAStoppedRunLeft(t *testing.T) {
	for _, pull := range []bool{false, true} {
		gitDir := gitFolder(t, false)
		leave(t, gitDir, base)
		fake := runnertest.New(t)
		s := &sequence{fake: fake}
		s.expect(call{args: git("remote")}).Prints("origin\n")
		s.expect(call{args: gitDirOf}).Prints(gitDir + "\n")
		s.head(stopped)
		s.expect(call{args: wholeStatus})
		s.tracks(tracksOrigin, "", "")
		s.expect(call{args: fetchIt})
		s.expect(call{args: upstreamAt}).Prints(upstream + "\n")
		s.expect(call{args: behindOf}).Prints("0\n")
		if !pull {
			s.expect(call{args: pushIt})
		}
		repo := gitrepo.Repo{Dir: dir, Run: fake}
		do := repo.Push
		if pull {
			do = repo.Pull
		}
		notes, err := do(context.Background())
		if err != nil || !slices.Equal(notes, []string{"finished the last run's index"}) {
			t.Errorf("pull %v, a stopped run's lock left = %q, %v; want it finished, and said", pull, notes, err)
		}
		checkCalls(t, "a stopped run's lock left", fake, gitDir, s.want)
		checkGitFolder(t, "a stopped run's lock left", gitDir, left)
	}

	gitDir := gitFolder(t, false)
	writeFile(t, gitDir, marked, markText(base, map[string]string{"brew": "100644 " + brewBefore + " 0"}))
	fake := runnertest.New(t)
	s := &sequence{fake: fake}
	s.expect(call{args: git("remote")}).Prints("origin\n")
	s.expect(call{args: gitDirOf}).Prints(gitDir + "\n")
	s.head(stopped)
	s.expect(call{args: indexed}).Prints(entry(brewBefore, "brew"))
	s.expect(call{args: resetTo(stopped), input: "brew\x00", index: nextIndex}).Does(func() { writeFile(t, gitDir, nextIndex, "repaired") })
	s.expect(call{args: wholeStatus})
	s.tracks(tracksOrigin, "", "")
	s.expect(call{args: fetchIt})
	s.expect(call{args: upstreamAt}).Prints(upstream + "\n")
	s.expect(call{args: behindOf}).Prints("0\n")
	s.expect(call{args: pushIt})
	if notes, err := (gitrepo.Repo{Dir: dir, Run: fake}).Push(context.Background()); err != nil || !slices.Equal(notes, []string{"repaired the index a stopped run left"}) {
		t.Errorf("Push(), the index a stopped run left = %q, %v; want it repaired, and said", notes, err)
	}
	checkCalls(t, "the index a stopped run left", fake, gitDir, s.want)
	checkGitFolder(t, "the index a stopped run left", gitDir, "repaired")

	gitDir = t.TempDir()
	other := lockCommits(t, gitDir)
	defer func() { _ = other.Close() }()
	fake = runnertest.New(t)
	s = pushing(fake, gitDir, "", "0")
	s.expect(call{args: pushIt})
	if err := push(t, fake); err != nil {
		t.Errorf("Push(), another kit committing, no mark = %v; want pushed", err)
	}
	checkCalls(t, "another kit committing, no mark", fake, "", s.want)

	gitDir = gitFolder(t, false)
	leave(t, gitDir, base)
	if err := os.Mkdir(filepath.Join(gitDir, "rebase-merge"), 0o700); err != nil {
		t.Fatal(err)
	}
	fake = runnertest.New(t)
	s = &sequence{fake: fake}
	s.expect(call{args: git("remote")}).Prints("origin\n")
	s.expect(call{args: gitDirOf}).Prints(gitDir + "\n")
	if notes, err := (gitrepo.Repo{Dir: dir, Run: fake}).Push(context.Background()); notes != nil || err == nil || !strings.Contains(err.Error(), "a rebase is under way") {
		t.Errorf("Push(), mid-rebase, a stopped run's lock left = %q, %v; want refused, nothing done", notes, err)
	}
	checkCalls(t, "mid-rebase", fake, gitDir, s.want)
	checkGitFolder(t, "mid-rebase", gitDir, usersEntries, "index.lock", marked, "rebase-merge")
}
