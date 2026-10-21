package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/leeovery/kit/internal/runner"
)

// heldFile is the file in the git folder naming the commit kit held back,
// as ErrHeadMoved says: nothing's pushed while it's in the branch. Never
// one of ownIndex's, which kit sweeps away.
const heldFile = "kit-held"

// hold marks commit held back, in the repository whose git folder is
// gitDir.
func hold(gitDir, commit string) error {
	return os.WriteFile(filepath.Join(gitDir, heldFile), []byte(commit+"\n"), 0o600)
}

// held errors while the commit kit held back is in the branch, saying how
// to take it out; the mark's cleared once it isn't, as when git reset took
// it out.
func (r Repo) held(ctx context.Context, gitDir string) error {
	path := filepath.Join(gitDir, heldFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	commit := strings.TrimSpace(string(data))
	_, err = r.git(ctx, "merge-base", "--is-ancestor", commit, "HEAD")
	exit, exited := errors.AsType[*runner.ExitError](err)
	switch {
	case err == nil:
		head, err := r.commitAt(ctx, "HEAD")
		if err != nil {
			return err
		}
		return fmt.Errorf("kit's commit %s went onto another commit than it was made from, so it may undo what that one changed, and nothing's pushed or brought in while it's in the branch: %s", commit, takeOut(commit, head))
	case exited && (exit.Code == 1 || exit.Code == 128):
		// Not in the branch, or not in the repository at all.
		return os.Remove(path)
	}
	return err
}

// takeOut is how to take commit out of the branch whose last is head,
// keeping its change, so kit commits it again onto the commit it should
// have.
func takeOut(commit, head string) string {
	if commit == head {
		return "git reset --soft HEAD~ takes it out, keeping its change, then run kit again"
	}
	return "git reset --soft " + commit + "~ takes it and the commits since out, keeping their changes, then run kit again"
}
