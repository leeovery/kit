package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/boot"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/logs"
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/redact"
	"github.com/leeovery/kit/internal/render"
	"github.com/leeovery/kit/internal/runner"
)

// bootPath is the PATH a new Mac's boot runs with, before kit-config gives
// it one: Homebrew's, once it's there, then the system's.
var bootPath = []string{"/opt/homebrew/bin", "/opt/homebrew/sbin", "/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin"}

func newBootstrapCommand(a *app) *cobra.Command {
	var mac, repo string
	cmd := &cobra.Command{
		Use:   "bootstrap",
		Short: "Set up a new Mac, from nothing: the install script runs it",
		Long: `Set up a new Mac, from nothing: the install script runs it, and running it
again carries on where it stopped.

It signs in to GitHub on your phone, asks which of your Macs this is and for
the password once, then installs the rest without you: Homebrew, the terminal
and password manager kit.toml names, kit-config.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.bootstrap(cmd.Context(), mac, repo)
		},
	}
	cmd.Flags().StringVar(&mac, "mac", "", "this Mac's name: one of kit-config's, or a new one's, rather than asked")
	cmd.Flags().StringVar(&repo, "config", "", "your config repository on GitHub, as owner/name: yours named kit-config, unless given")
	return cmd
}

func (a *app) bootstrap(ctx context.Context, mac, repo string) error {
	dirs, err := a.dirs()
	if err != nil {
		return err
	}
	home, err := a.HomeDir()
	if err != nil {
		return fmt.Errorf("find the home directory: %w", err)
	}
	now := a.Now()
	log, err := logs.Open(dirs.Logs, now, "bootstrap", a.verbose)
	if err != nil {
		return err
	}
	defer func() { _ = log.Close() }()

	ctx, stop := context.WithCancel(ctx)
	defer stop()
	pretty := a.pretty(a.Stdout)
	var (
		face  render.Face
		boots *render.BootFace
		asker boot.Asker = plainAsker{}
		dark  bool
	)
	if pretty {
		dark = lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
		boots = render.NewBootFace(terminal(), dark, a.Now, stop)
		face, asker = boots, bootAsker{boots}
	} else {
		face = a.face()
	}
	sink := event.NewFanout(face, log)
	exec := a.Runner(bootPath, childEnv(a.Getenv, a.Environ(), home, bootPath))
	observed := runner.Observed(exec, func(ctx context.Context, rep runner.Report) { sink.Emit(event.Command(ctx, rep)) }, a.Now)
	observed = runner.Streamed(observed, event.Changing, func(ctx context.Context, cmd runner.Command, line string) {
		sink.Emit(event.Output{Time: a.Now(), Step: event.StepOf(ctx), Command: redact.Text(cmd.String()), Line: redact.Text(line)})
	})
	b := &boot.Boot{
		Run: observed, Sink: sink, Ask: asker, GitHub: boot.NewGitHub(&http.Client{Timeout: time.Minute}), Now: a.Now,
		State: dirs.State, Data: dirs.Data, Config: dirs.Config, Repo: repo, Mac: mac, Getenv: a.Getenv,
	}

	// The splash plays while the self-test runs; enter at its end starts the
	// boot, and the boot log takes its place.
	tested := make(chan event.SelfTest, 1)
	go func() { tested <- b.SelfTest(ctx, boot.Space) }()
	if pretty {
		splash := render.NewSplash(dark)
		if err := splash.Play(ctx, terminal()); err != nil {
			_ = face.Close()
			return err
		}
		if splash.Stopped() {
			_ = face.Close()
			return ask.ErrCancelled
		}
	}
	test := <-tested
	sink.Emit(test)
	for _, t := range test.Tests {
		if t.State == check.Failed {
			_ = face.Close()
			return fmt.Errorf("%s: %s", strings.ToLower(t.Name), strings.Join(t.Says, ": "))
		}
	}

	pipeline, err := engine.New(b.Steps()...)
	if err != nil {
		_ = face.Close()
		return err
	}
	opts := engine.Options{Command: "bootstrap", Version: a.Version, Jobs: 1, Now: a.Now}
	planned, err := pipeline.Planned(opts)
	if err != nil {
		_ = face.Close()
		return err
	}
	if boots != nil {
		boots.Start(planned)
	}
	report, err := pipeline.Apply(ctx, sink, opts)
	if closeErr := face.Close(); err == nil {
		err = closeErr
	}
	switch {
	case err != nil:
		return err
	case ctx.Err() != nil:
		return ask.ErrCancelled
	case report.Attention():
		return attention{}
	}
	return nil
}

// bootAsker asks the boot's questions in the boot's face, in their rows.
type bootAsker struct{ face *render.BootFace }

func (a bootAsker) Choose(ctx context.Context, step, question string, answers []boot.Answer) (int, error) {
	choices := make([]look.Choice, len(answers))
	for i, an := range answers {
		choices[i] = look.Choice{Label: an.Label, Does: an.Does}
	}
	return a.face.Choose(ctx, step, question, choices)
}

func (a bootAsker) Name(ctx context.Context, step, question string) (string, error) {
	return a.face.Name(ctx, step, question)
}

func (a bootAsker) Press(ctx context.Context, step, key, does string) error {
	return a.face.Press(ctx, step, key, does)
}

// errNoTerminal is a question asked without a terminal to ask it at.
var errNoTerminal = errors.New("no terminal to ask at")

// plainAsker is the boot without a terminal: what it would ask fails,
// naming the flag that answers it, and no key is ever pressed.
type plainAsker struct{}

func (plainAsker) Choose(_ context.Context, step, _ string, _ []boot.Answer) (int, error) {
	return 0, fmt.Errorf("%w: give this Mac's name with --mac (the %s step)", errNoTerminal, step)
}

func (plainAsker) Name(_ context.Context, step, _ string) (string, error) {
	return "", fmt.Errorf("%w: give this Mac's name with --mac (the %s step)", errNoTerminal, step)
}

func (plainAsker) Press(ctx context.Context, _, _, _ string) error {
	<-ctx.Done()
	return ctx.Err()
}
