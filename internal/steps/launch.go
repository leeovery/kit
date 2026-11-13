package steps

import (
	"bytes"
	"cmp"
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
	// Kit is the kit the launch runs, as the person runs it (LaunchKit):
	// "" when the kit running isn't one to name, Why saying why, and the
	// launch is left as it stands.
	Kit, Why string
	App      App
	// StateDir is kit's state, where the launch's loading is recorded.
	StateDir string
	Now      func() time.Time
	// Sleep waits before launchd's asked again to load the launch:
	// time.Sleep when nil.
	Sleep func(time.Duration)
}

// LaunchKit is the kit the hourly launch is to run, self being the kit
// running: the kit on path, kit's PATH, the one the person runs, when it's
// self, named as it's found there, so Homebrew's link outlasts its
// upgrades. Another kit on path, or none and self a build that won't last
// (in temp, Go's build cache or a worktree), is none to name: why says so.
func LaunchKit(self string, path []string, temp string) (kit, why string) {
	onPath, err := runner.Exec{Path: path}.Find("kit")
	switch {
	case err == nil && sameFile(self, onPath):
		return onPath, ""
	case err == nil:
		return "", fmt.Sprintf("this kit, %s, isn't the one on kit's PATH, %s: the hourly launch is left for that kit's apply", self, onPath)
	case passing(self, temp):
		return "", fmt.Sprintf("this kit, %s, is a build that won't last: the hourly launch is left for the kit on kit's PATH", self)
	}
	return self, ""
}

// sameFile reports whether a and b are one file, links followed.
func sameFile(a, b string) bool {
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	return err == nil && os.SameFile(ai, bi)
}

// passing reports whether the kit at self is a build that won't last: in
// temp, or Go's build cache, as go run's is, or a worktree's, its links
// followed or not.
func passing(self, temp string) bool {
	paths := []string{self}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		paths = append(paths, resolved)
	}
	temps := []string{temp}
	if resolved, err := filepath.EvalSymlinks(temp); err == nil {
		temps = append(temps, resolved)
	}
	for _, p := range paths {
		if strings.Contains(p, "/go-build") || strings.Contains(p, "/.claude/worktrees/") {
			return true
		}
		for _, t := range temps {
			if rel, err := filepath.Rel(t, p); err == nil && !strings.HasPrefix(rel, "..") {
				return true
			}
		}
	}
	return false
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

// agentKit is the kit the launch agent data runs: "" when it names none.
func agentKit(data []byte) string {
	v, err := plist.Decode(data)
	if err != nil {
		return ""
	}
	dict, _ := v.(map[string]any)
	args, _ := dict["ProgramArguments"].([]any)
	if len(args) == 0 {
		return ""
	}
	kit, _ := args[len(args)-1].(string)
	return kit
}

// reloadWait is how long launchd's given before it's asked again to load
// the launch it refused, as it can while the job it unloaded is torn down.
const reloadWait = time.Second

// HourlyLaunch checks kit's launch agent is in place, saying what
// LaunchAgent says for the kit to run and kit.app, kit.app built from this
// kit's files, and loaded; applying builds kit.app, writes the agent and
// loads it, loading it again when it's changed, and records when, so that
// the check on the runs gives the first run its hour. With no kit to name
// (Kit ""), the check looks at the agent as it stands, the kit it names,
// and applying leaves it, saying why.
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
			have, err := os.ReadFile(file)
			switch {
			case errors.Is(err, fs.ErrNotExist):
				return missing("missing", "not installed")
			case err != nil:
				return failed(err)
			}
			named := agentKit(have)
			kit := cmp.Or(l.Kit, named)
			want, err := LaunchAgent(l.App.Path(), kit)
			if err != nil {
				return failed(err)
			}
			switch {
			case named != kit:
				return missing("changed", "runs another kit ("+named+")")
			case !bytes.Equal(have, want):
				return missing("changed", "not as kit writes it")
			case !runner.Has(runner.Exec{}, kit):
				return missing("changed", "runs a kit that's gone ("+kit+")")
			case !l.App.Current():
				return missing("changed", "kit.app not built from this kit")
			}
			if _, err := output(ctx, l.Run, "launchctl", "print", service); err != nil {
				return missing("unloaded", "not loaded")
			}
			return check.Result{State: check.OK, Summary: "hourly, through kit.app"}
		},
		Apply: func(ctx context.Context, _ check.Result) error {
			if l.Kit == "" {
				return engine.NotYet(l.Why)
			}
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
				// The folder as macOS makes it.
				if err := writeWhole(file, want, 0o755); err != nil {
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
			if err := l.load(ctx, file); err != nil {
				return err
			}
			return nightly.Update(l.StateDir, func(r *nightly.Record) { r.Scheduled = l.Now() })
		},
	}
}

// load has launchd load the launch agent at file, asking again once, a
// moment later, should it refuse, as it can while the job unloaded just
// before is torn down.
func (l Launching) load(ctx context.Context, file string) error {
	bootstrap := runner.Command{Name: "launchctl", Args: []string{"bootstrap", l.Domain, file}}
	_, err := l.Run.Run(ctx, bootstrap)
	if err == nil {
		return nil
	}
	sleep := l.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	sleep(reloadWait)
	if _, err = l.Run.Run(ctx, bootstrap); err != nil {
		return fmt.Errorf("launchd refused to load the hourly launch, so the next kit apply tries again: %w", err)
	}
	return nil
}

// writeWhole writes data to path, whole or not at all, as a file renamed
// into its place, making its folder with dirMode.
func writeWhole(path string, data []byte, dirMode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Chmod(0o644)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
