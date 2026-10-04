package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/brew"
	"github.com/leeovery/kit/internal/logs"
	"github.com/leeovery/kit/internal/render"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/steps"
)

func newStatusCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "status [step...]",
		Short: "Show how this Mac stands against the config: what needs attention",
		Long: `Show how this Mac stands against its config: each step's check, and what needs
attention, such as packages declared but missing, or installed but not declared.

Name steps (homebrew, brew, cask, config-private) to check only those, with
what they need. Exits 0 when nothing needs attention, 1 when something does,
and 2 when kit couldn't check. Each run is logged: see kit log.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := a.prepare("status")
			if err != nil {
				return err
			}
			report, err := r.pipeline.Check(cmd.Context(), r.sink, r.options(a, args))
			if closeErr := r.close(); err == nil {
				err = closeErr
			}
			if err != nil {
				return err
			}
			if report.Attention() {
				return attention{}
			}
			return nil
		},
	}
}

// run is a run of the engine, ready to go: its pipeline, and where its
// events go.
type run struct {
	command  string
	machine  string
	pipeline *engine.Pipeline
	sink     event.Sink
	face     render.Face
	log      *logs.File
}

func (r *run) options(a *app, only []string) engine.Options {
	return engine.Options{Command: r.command, Machine: r.machine, Version: a.Version, Only: only, Now: a.Now}
}

// close finishes the run's face and log, returning the first error.
func (r *run) close() error {
	faceErr := r.face.Close()
	if err := r.log.Close(); faceErr == nil {
		return err
	}
	return faceErr
}

// prepare readies a run of command: the config, this Mac's name, settled
// before anything reads a Mac's own files, kit's own PATH, the runner, the
// faces and the log, and the pipeline.
func (a *app) prepare(command string) (*run, error) {
	dirs, err := a.dirs()
	if err != nil {
		return nil, err
	}
	cfg, err := a.loadConfig(dirs)
	if err != nil {
		return nil, err
	}
	machine, err := config.ReadMachine(dirs.State)
	if err != nil {
		return nil, err
	}
	switch {
	case machine == "":
		return nil, fmt.Errorf("this Mac has no name yet: run kit machine <name>, one of %s", strings.Join(cfg.MacNames(), ", "))
	case !cfg.Knows(machine):
		return nil, fmt.Errorf("this Mac is named %s, which %s doesn't know: run kit machine <name>, one of %s", machine, config.File, strings.Join(cfg.MacNames(), ", "))
	}
	home, err := a.HomeDir()
	if err != nil {
		return nil, fmt.Errorf("find the home directory: %w", err)
	}
	path, err := cfg.SearchPath(home)
	if err != nil {
		return nil, err
	}
	formulae, err := cfg.List("brew", machine)
	if err != nil {
		return nil, err
	}
	casks, err := cfg.List("cask", machine)
	if err != nil {
		return nil, err
	}

	log, err := logs.Open(dirs.Logs, a.Now(), command, a.verbose)
	if err != nil {
		return nil, err
	}
	face := a.face()
	sink := event.NewFanout(face, log)
	exec := a.Runner(path, childEnv(a.Getenv, home, path))
	observed := runner.Observed(exec, func(ctx context.Context, rep runner.Report) { sink.Emit(event.Command(ctx, rep)) }, a.Now)
	hb := brew.Homebrew{Run: observed}
	pipeline, err := engine.New(
		hb.Step(),
		kind.Step(hb.Formulae(), formulae, brew.StepName),
		kind.Step(hb.Casks(), casks, brew.StepName),
		steps.ConfigPrivate(observed, dirs.Config),
	)
	if err != nil {
		_ = face.Close()
		_ = log.Close()
		return nil, err
	}
	return &run{command: command, machine: machine, pipeline: pipeline, sink: sink, face: face, log: log}, nil
}

// face is the face a run shows on: JSON with --json, plain with --plain or
// without a terminal, pretty at one.
func (a *app) face() render.Face {
	out := a.Stdout
	switch {
	case a.json:
		return render.NewJSON(out)
	case !a.pretty(out):
		return render.NewPlain(out)
	}
	return render.NewPretty(a.colors(out), a.Width(out), true)
}

// childEnv is the environment kit runs programs in, never the one it
// inherited whole: who and where the user is, kit's own PATH, and Homebrew
// kept from updating itself, nagging or colouring its output on its own.
func childEnv(getenv func(string) string, home string, path []string) []string {
	env := []string{"HOME=" + home, "PATH=" + strings.Join(path, ":")}
	for _, name := range []string{"USER", "LOGNAME", "SHELL", "TMPDIR", "LANG", "LC_ALL"} {
		if v := getenv(name); v != "" {
			env = append(env, name+"="+v)
		}
	}
	return append(env, "HOMEBREW_NO_AUTO_UPDATE=1", "HOMEBREW_NO_ANALYTICS=1", "HOMEBREW_NO_ENV_HINTS=1", "HOMEBREW_NO_COLOR=1")
}
