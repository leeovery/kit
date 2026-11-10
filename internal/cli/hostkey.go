package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/steps"
)

// hostKeyStep is the step recording this Mac's SSH host key, in dir, in
// kit-config, committed and pushed as the new Mac the boot added is
// (placeCommits).
func (r *run) hostKeyStep(dir string) engine.Step {
	return steps.HostKey(steps.HostKeying{Dir: dir, Recorded: r.cfg.Macs[r.machine].HostKey, Record: r.recordHostKey})
}

// recordHostKey records key as this Mac's SSH host key in kit.toml, and
// commits and pushes it, saying how that went. Committed nothing, as when
// git can't sign yet, kit.toml goes back as it was: left edited, the key
// would read as recorded, and no run would commit it.
func (r *run) recordHostKey(ctx context.Context, key string) (check.Result, error) {
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
