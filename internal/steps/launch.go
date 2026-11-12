package steps

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/macapp"
	"github.com/leeovery/kit/internal/nightly"
	"github.com/leeovery/kit/internal/plist"
	"github.com/leeovery/kit/internal/runner"
)

// LaunchName names the step installing the hourly launch.
const LaunchName = "hourly-launch"

// LaunchLabel is the hourly launch's label in launchd, after kit.app's.
const LaunchLabel = macapp.BundleID + ".nightly"

// App is kit.app as the hourly launch opens it: where it is; whether it's
// built from the files this kit embeds; and ready, built first when it
// isn't, false when it can't be.
type App interface {
	Path() string
	Current() bool
	Ready(ctx context.Context) bool
}

// Launching is what the hourly launch is made of.
type Launching struct {
	Run runner.Runner
	// Dir is the user's LaunchAgents folder, and Domain launchd's for the
	// user's agents, as in gui/501.
	Dir, Domain string
	// Kit is the kit the launch runs: the one running, at the path it was
	// run from, so one moved, as Homebrew's after a release, is written
	// again.
	Kit string
	App App
	// StateDir is kit's state, where the launch's loading is recorded.
	StateDir string
	Now      func() time.Time
}

// LaunchAgent is the hourly launch's property list: on the hour, launchd
// opens kit.app, at app, and waits till it quits; kit.app runs the kit at
// kit nightly, as its child, so that Full Disk Access is kit.app's. A new
// instance each time, as one posting a notification may be open.
func LaunchAgent(app, kit string) ([]byte, error) {
	return plist.Encode(map[string]any{
		"Label":                 LaunchLabel,
		"ProgramArguments":      []any{"/usr/bin/open", "-W", "-g", "-n", app, "--args", "nightly", kit},
		"StartCalendarInterval": map[string]any{"Minute": 0},
	})
}

// HourlyLaunch checks kit's launch agent is in place, saying what
// LaunchAgent says for this kit and kit.app, kit.app built from this kit's
// files, and loaded; applying builds kit.app, writes the agent and loads
// it, loading it again when it's changed, and records when, so that the
// check on the runs gives the first run its hour.
func HourlyLaunch(l Launching) engine.Step {
	file := filepath.Join(l.Dir, LaunchLabel+".plist")
	service := l.Domain + "/" + LaunchLabel
	missing := func(state, detail string) check.Result {
		return check.Result{State: check.Attention, Summary: detail, Items: []check.Item{{
			ID: LaunchName + ":" + LaunchName, Name: "kit's hourly run", State: state, Detail: detail, Action: ActionInstall,
		}}}
	}
	return engine.Step{
		Name: LaunchName, Title: "Hourly launch", Area: nightly.Area,
		Check: func(ctx context.Context) check.Result {
			want, err := LaunchAgent(l.App.Path(), l.Kit)
			if err != nil {
				return failed(err)
			}
			have, err := os.ReadFile(file)
			switch {
			case errors.Is(err, fs.ErrNotExist):
				return missing("missing", "not installed")
			case err != nil:
				return failed(err)
			case !bytes.Equal(have, want):
				return missing("changed", "not this kit's")
			case !l.App.Current():
				return missing("changed", "kit.app not built from this kit")
			}
			if _, err := output(ctx, l.Run, "launchctl", "print", service); err != nil {
				return missing("unloaded", "not loaded")
			}
			return check.Result{State: check.OK, Summary: "hourly, through kit.app"}
		},
		Apply: func(ctx context.Context, _ check.Result) error {
			if !l.App.Ready(ctx) {
				return engine.NotYet("kit.app isn't built: it needs Swift's compiler, from the command line tools; the next kit apply builds it")
			}
			want, err := LaunchAgent(l.App.Path(), l.Kit)
			if err != nil {
				return err
			}
			have, _ := os.ReadFile(file)
			changed := !bytes.Equal(have, want)
			if changed {
				if err := writeWhole(file, want); err != nil {
					return err
				}
			}
			_, err = output(ctx, l.Run, "launchctl", "print", service)
			loaded := err == nil
			if loaded && !changed {
				return nil
			}
			if loaded {
				if _, err := l.Run.Run(ctx, runner.Command{Name: "launchctl", Args: []string{"bootout", service}}); err != nil {
					return fmt.Errorf("unload the hourly launch: %w", err)
				}
			}
			if _, err := l.Run.Run(ctx, runner.Command{Name: "launchctl", Args: []string{"bootstrap", l.Domain, file}}); err != nil {
				return fmt.Errorf("load the hourly launch: %w", err)
			}
			return nightly.Update(l.StateDir, func(r *nightly.Record) { r.Scheduled = l.Now() })
		},
	}
}

// writeWhole writes data to file, making its folder, as a file renamed
// into place, so that it's never read half written.
func writeWhole(file string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	tmp := file + ".kit-new"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, file); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
