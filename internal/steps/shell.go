package steps

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/runner"
)

// What applying the shell's steps does about an item.
const (
	ActionWrite   = "write"
	ActionInstall = "install"
	ActionFix     = "fix"
)

// PathFileName names the file in kit's state directory that holds the
// shell's PATH, which the shell reads as it starts, running nothing.
const PathFileName = "path"

// PathFile checks the file at file holds dirs, the PATH the shell puts
// ahead of the one it inherits, colon-separated; applying writes it. While
// it can't be written, new shells keep the last good list.
func PathFile(dirs []string, file string) engine.Step {
	want := strings.Join(dirs, ":") + "\n"
	return engine.Step{
		Name: "path", Title: "Shell PATH", Area: AreaDrift,
		Check: func(context.Context) check.Result {
			data, err := os.ReadFile(file)
			it := check.Item{ID: "path:shell", Name: "the shell's PATH", Action: ActionWrite}
			switch {
			case errors.Is(err, fs.ErrNotExist):
				it.State, it.Detail = "missing", "kit hasn't written it"
			case err != nil:
				return failed(err)
			case string(data) == want:
				return check.Result{State: check.OK, Summary: fmt.Sprintf("%d %s", len(dirs), plural(len(dirs), "directory", "directories"))}
			default:
				it.State, it.Detail = "changed", "[paths] has changed since kit wrote it"
			}
			return check.Result{State: check.Attention, Summary: "not as [paths] says", Items: []check.Item{it}}
		},
		Apply: func(context.Context, check.Result) error {
			if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
				return err
			}
			tmp := file + ".kit-new"
			if err := os.WriteFile(tmp, []byte(want), 0o644); err != nil {
				return err
			}
			if err := os.Rename(tmp, file); err != nil {
				_ = os.Remove(tmp)
				return err
			}
			return nil
		},
	}
}

// ohMyZshInstaller is where Oh My Zsh's installer is published.
const ohMyZshInstaller = "https://raw.githubusercontent.com/ohmyzsh/ohmyzsh/HEAD/tools/install.sh"

// OhMyZsh checks Oh My Zsh is installed in home; applying runs its
// installer, keeping .zshrc and the login shell as they are. Its own
// updater keeps it current.
func OhMyZsh(run runner.Runner, home string) engine.Step {
	dir := filepath.Join(home, ".oh-my-zsh")
	return engine.Step{
		Name: FeatureOhMyZsh, Title: "Oh My Zsh", Area: AreaDrift,
		Check: func(context.Context) check.Result {
			if _, err := os.Stat(filepath.Join(dir, "oh-my-zsh.sh")); err == nil {
				return check.Result{State: check.OK, Summary: "installed"}
			}
			return check.Result{State: check.Attention, Summary: "not installed", Items: []check.Item{{ID: FeatureOhMyZsh + ":" + FeatureOhMyZsh, Name: "Oh My Zsh", State: "missing", Action: ActionInstall}}}
		},
		Apply: func(ctx context.Context, _ check.Result) error {
			res, err := run.Run(ctx, runner.Command{Name: "curl", Args: []string{"-fsSL", ohMyZshInstaller}})
			if err != nil {
				return fmt.Errorf("couldn't download Oh My Zsh's installer: %w", err)
			}
			// --unattended: no questions, the login shell and .zshrc kept.
			_, err = run.Run(ctx, runner.Command{Name: "env", Args: []string{"KEEP_ZSHRC=yes", "ZSH=" + dir, "sh", "-s", "--", "--unattended"}, Input: string(res.Stdout), Timeout: 5 * time.Minute})
			return err
		},
	}
}
