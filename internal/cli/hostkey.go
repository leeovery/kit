package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/leeovery/kit/internal/boot"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/restore/oldmac"
	"github.com/leeovery/kit/internal/steps"
)

// hostKeyStep is the step recording this Mac's SSH host key in kit-config,
// its host keys in dir and its Remote Login answering at address, each
// kit's own when "", with the Remote Login feature on (remoteLogin), after
// that feature's step; committed and pushed as the new Mac the boot added
// is (placeCommits), unless kit.toml has edits not committed.
func (r *run) hostKeyStep(dir, address, stateDir string, remoteLogin bool) engine.Step {
	step := steps.HostKey(steps.HostKeying{
		RemoteLogin: remoteLogin, Dir: cmp.Or(dir, oldmac.SSHDir), Address: cmp.Or(address, steps.SSHAddress),
		Recorded: r.cfg.Macs[r.machine].HostKey,
		Record: func(ctx context.Context, key string) (check.Result, error) {
			return r.recordHostKey(ctx, stateDir, key)
		},
	})
	if remoteLogin {
		step.After = append(step.After, steps.FeatureRemoteLogin)
	}
	return step
}

// recordHostKey records key as this Mac's SSH host key in kit.toml, and
// commits and pushes it, saying how that went. kit.toml with edits not
// committed, it waits, as committing it whole would take them too: the
// person's, for kit reconcile; or this Mac's table, as the boot left it,
// for the new-mac step, kit's state, stateDir, saying so. Committed
// nothing, as when git can't sign yet, kit.toml goes back as it was: left
// edited, the key would read as recorded, and no run would commit it.
func (r *run) recordHostKey(ctx context.Context, stateDir, key string) (check.Result, error) {
	changes, err := r.repo.Changes(ctx)
	if err != nil {
		return check.Result{}, err
	}
	if slices.ContainsFunc(changes, func(c gitrepo.Change) bool { return c.Path == config.File }) {
		newMac, err := boot.NewMac(stateDir)
		switch {
		case err != nil:
			return check.Result{}, err
		case newMac == r.machine:
			return check.Result{}, engine.NotYet(config.File + " holds this Mac's table, not committed yet: the new-mac step commits it, then kit apply records the key")
		}
		return check.Result{}, engine.NotYet(config.File + " has edits not committed: kit reconcile settles them, then kit apply records the key")
	}
	path := filepath.Join(r.cfg.Dir, config.File)
	was, err := os.ReadFile(path)
	if err != nil {
		return check.Result{}, fmt.Errorf("read the config: %w", err)
	}
	if err := r.cfg.SetHostKey(r.machine, key); err != nil {
		return check.Result{}, err
	}
	out, err := r.repo.Commit(ctx, commitMessage("record", steps.HostKeyName, []string{r.machine}, r.machine, ""), config.File)
	if !out.Committed && err != nil {
		if undo := os.WriteFile(path, was, 0o644); undo != nil {
			return check.Result{}, errors.Join(err, fmt.Errorf("put %s back as it was: %w", config.File, undo))
		}
		if errors.Is(err, gitrepo.ErrUnsigned) {
			return check.Result{}, engine.NotYet(err.Error() + "; the next kit apply tries again")
		}
		return check.Result{}, fmt.Errorf("couldn't commit %s: %s", config.File, whyNot(err))
	}
	sync := syncing{repo: r.repo, files: []string{config.File}, what: config.File}
	return noted(sync.committed(ctx, out.Committed, err), out.Notes), nil
}
