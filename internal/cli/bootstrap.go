package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/askpass"
	"github.com/leeovery/kit/internal/boot"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
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
	var handedOver bool
	cmd := &cobra.Command{
		Use:   "bootstrap",
		Short: "Set up a new Mac, from nothing: the install script runs it",
		Long: `Set up a new Mac, from nothing: the install script runs it, and running it
again carries on where it stopped.

It signs in to GitHub on your phone, asks which of your Macs this is and for
the password once, then installs the rest without you: Homebrew, the terminal
kit.toml names, kit-config, its files linked. With a
terminal named, you give it Full Disk Access, then kit opens it and carries
on there: the whole of kit apply, then a fresh shell.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			err := a.bootstrap(cmd.Context(), mac, repo, handedOver)
			if !handedOver || a.Shell == nil {
				return err
			}
			// Handed over, kit is the terminal's own command: it becomes the
			// login shell, so the window stays, whatever the boot came to, a
			// blank line under its last word.
			if _, isAttention := errors.AsType[attention](err); err != nil && !isAttention && !errors.Is(err, ask.ErrCancelled) {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "kit: %v\n", err)
			}
			_, _ = fmt.Fprintln(a.Stdout)
			return a.Shell()
		},
	}
	cmd.Flags().StringVar(&mac, "mac", "", "this Mac's name: one of kit-config's, or a new one's, rather than asked")
	cmd.Flags().StringVar(&repo, "config", "", "your config repository on GitHub, as owner/name: yours named kit-config, unless given")
	cmd.Flags().BoolVar(&handedOver, "handed-over", false, "started by the boot in Terminal, in the terminal it hands over to")
	_ = cmd.Flags().MarkHidden("handed-over")
	return cmd
}

// bootSudoRefresh is how often the boot renews sudo's hold on the password
// while it installs, well inside sudo's own five minutes.
const bootSudoRefresh = time.Minute

func (a *app) bootstrap(ctx context.Context, mac, repo string, handedOver bool) error {
	dirs, err := a.dirs()
	if err != nil {
		return err
	}
	home, err := a.HomeDir()
	if err != nil {
		return fmt.Errorf("find the home directory: %w", err)
	}
	if handedOver {
		b := &boot.Boot{
			GitHub: boot.NewGitHub(&http.Client{Timeout: time.Minute}), Now: a.Now, Ask: &plainAsker{},
			State: dirs.State, Data: dirs.Data, Config: dirs.Config, Repo: repo, Mac: mac, Getenv: a.Getenv,
			Home: home, Applications: "/Applications", SudoLocal: a.SudoLocal, HandedOver: true,
		}
		// Handed over, kit says so at once: the boot in Terminal is waiting.
		if err := b.Arrive(); err != nil {
			return err
		}
		return a.inTerminal(ctx, b, true)
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
		asker boot.Asker = &plainAsker{}
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
	// What the boot runs asks it for the password through sudo (askpass):
	// given once, in the password's row, it's kept for the run, and asked
	// again only if sudo wants it again.
	self, err := os.Executable()
	if err != nil {
		_ = face.Close()
		return fmt.Errorf("find kit: %w", err)
	}
	asks, err := askpass.Start("", self, func(ctx context.Context, asked int) (string, error) {
		question := "sudo wants the password"
		if asked > 0 {
			question = "that wasn't it: try again"
		}
		step := boot.PasswordStep
		if boots != nil && boots.Running() != "" {
			step = boots.Running()
		}
		return asker.Secret(ctx, step, question)
	})
	if err == nil {
		defer func() { _ = asks.Close() }()
	}
	exec := runner.WithEnv(a.Runner(bootPath, childEnv(a.Getenv, a.Environ(), home, bootPath)), func() []string {
		if asks == nil {
			return nil
		}
		return []string{"SUDO_ASKPASS=" + asks.Helper()}
	})
	observed := runner.Observed(exec, func(ctx context.Context, rep runner.Report) { sink.Emit(event.Command(ctx, rep)) }, a.Now)
	observed = runner.Streamed(observed, event.Changing, func(ctx context.Context, cmd runner.Command, line string) {
		sink.Emit(event.Output{Time: a.Now(), Step: event.StepOf(ctx), Command: redact.Text(cmd.String()), Line: redact.Text(line)})
	})
	b := &boot.Boot{
		Run: observed, Sink: sink, Ask: asker, GitHub: boot.NewGitHub(&http.Client{Timeout: time.Minute}), Now: a.Now,
		State: dirs.State, Data: dirs.Data, Config: dirs.Config, Repo: repo, Mac: mac, Getenv: a.Getenv,
		Home: home, Applications: "/Applications", SudoLocal: a.SudoLocal, Self: self, HandedOver: handedOver,
	}
	if asks != nil {
		b.Keep = asks.Keep
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
	if boots != nil {
		if err := boots.PlaySelfTest(ctx, test); err != nil {
			return err
		}
	}
	for _, t := range test.Tests {
		if t.State == check.Failed {
			_ = face.Close()
			return fmt.Errorf("%s: %s", strings.ToLower(t.Name), strings.Join(t.Says, ": "))
		}
	}

	// Signing in first: it reads kit-config, which the boot order is
	// planned from.
	opts := engine.Options{Command: "bootstrap", Version: a.Version, Jobs: 1, Now: a.Now}
	signIn, err := engine.New(b.SignIn()...)
	if err != nil {
		_ = face.Close()
		return err
	}
	planned, err := signIn.Planned(opts)
	if err != nil {
		_ = face.Close()
		return err
	}
	if boots != nil {
		boots.Start(planned)
	}
	report, err := signIn.Apply(ctx, sink, opts)
	if err == nil && ctx.Err() == nil && !report.Attention() {
		report, err = a.bootOrder(ctx, b, boots, sink, opts, planned)
	}
	booted := err == nil && ctx.Err() == nil && !report.Attention()
	if booted && boots != nil && !b.InTerminal() {
		if res, ok := report.Results[boot.TerminalStep]; ok && res.State == check.OK {
			boots.Finish("this window can be closed")
		}
	}
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
	case b.InTerminal():
		// Run in the terminal kit.toml names, the boot carries on in it, as
		// it would have, handed over to.
		return a.inTerminal(ctx, b, false)
	}
	return nil
}

// inTerminal is the boot in the terminal kit.toml names, carrying on there:
// the arrival, then the boot's steps, checked again, as a light of their
// own, and the whole of kit apply. fresh is whether the window is kit's
// own, opened for it, its screen blank.
func (a *app) inTerminal(ctx context.Context, b *boot.Boot, fresh bool) error {
	mac, err := config.ReadMachine(b.State)
	if err != nil {
		return err
	}
	if a.pretty(a.Stdout) {
		if !fresh {
			// What's on screen goes up into the scrollback, for the arrival
			// to land at the top of the window.
			_, _ = fmt.Fprint(a.Stdout, strings.Repeat("\n", a.Height(a.Stdout))+"\x1b[H")
		}
		arrival := render.NewArrival(lipgloss.HasDarkBackground(os.Stdin, os.Stdout), "bootstrap", mac, a.Now()).KeepHeader()
		if err := arrival.Play(ctx, terminal()); err != nil {
			return err
		}
		if arrival.Stopped() {
			return ask.ErrCancelled
		}
		// Landed, its header stays in the window: the run carries on under it.
		a.underHome = arrival.Landed()
	}
	r, err := a.prepare("bootstrap", "bootstrap")
	if err != nil {
		return err
	}
	b.Run, b.Sink, b.Ask = r.run, r.sink, &plainAsker{}
	if err := r.lead(b.Booted()); err != nil {
		_ = r.close()
		return err
	}
	// The heading shows at once, with the run's lights, as kit apply's does.
	planned, _ := r.pipeline.Planned(r.options(a, nil))
	r.sink.Emit(event.Preparing{Time: r.now, Command: r.command, Machine: r.machine, Doing: "checking what will need an administrator's password", Steps: planned})
	_, stopAdmin := a.holdAdmin(ctx, r, toInstall(ctx, r, nil))
	report, err := r.pipeline.Apply(ctx, r.sink, r.options(a, nil))
	stopAdmin()
	if err == nil {
		err = r.remember(report)
	}
	if closeErr := r.close(); err == nil {
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

// bootOrder runs the boot order, planned from the kit-config signing in
// read, its rows under signing in's (planned), with sudo's hold on the
// password kept fresh once it's given.
func (a *app) bootOrder(ctx context.Context, b *boot.Boot, boots *render.BootFace, sink event.Sink, opts engine.Options, planned []event.Step) (engine.Report, error) {
	order, err := engine.New(b.Order()...)
	if err != nil {
		return engine.Report{}, err
	}
	more, err := order.Planned(opts)
	if err != nil {
		return engine.Report{}, err
	}
	if boots != nil {
		boots.Show(slices.Concat(planned, more))
	}
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	go func() {
		tick := time.NewTicker(bootSudoRefresh)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if b.Held() {
					_, _ = b.Run.Run(ctx, runner.Command{Name: "sudo", Args: []string{"-n", "-v"}})
				}
			}
		}
	}()
	return order.Apply(ctx, sink, opts)
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

func (a bootAsker) Pick(ctx context.Context, step string, keys []boot.Key) (string, error) {
	return a.face.Pick(ctx, step, lookKeys(keys))
}

func (a bootAsker) Secret(ctx context.Context, step, question string) (string, error) {
	return a.face.Secret(ctx, step, question)
}

func (a bootAsker) Wait(ctx context.Context, step, says, todo string, keys []boot.Key) (string, error) {
	return a.face.Wait(ctx, step, says, todo, lookKeys(keys))
}

// lookKeys are the boot's keys, as the face shows them.
func lookKeys(keys []boot.Key) []look.Key {
	ks := make([]look.Key, len(keys))
	for i, k := range keys {
		ks[i] = look.Key{Key: k.Key, Does: k.Does}
	}
	return ks
}

// errNoTerminal is a question asked without a terminal to ask it at.
var errNoTerminal = errors.New("no terminal to ask at")

// plainAsker is the boot without a terminal: what it would ask fails,
// naming the flag that answers it, and no key is ever pressed.
type plainAsker struct{ picked bool }

func (*plainAsker) Choose(_ context.Context, step, _ string, _ []boot.Answer) (int, error) {
	return 0, fmt.Errorf("%w: give this Mac's name with --mac (the %s step)", errNoTerminal, step)
}

func (*plainAsker) Name(_ context.Context, step, _ string) (string, error) {
	return "", fmt.Errorf("%w: give this Mac's name with --mac (the %s step)", errNoTerminal, step)
}

// Secret fails: a password is typed at a terminal.
func (*plainAsker) Secret(_ context.Context, step, _ string) (string, error) {
	return "", fmt.Errorf("%w: the password is asked at a terminal (the %s step): run kit bootstrap in Terminal", errNoTerminal, step)
}

// Wait fails, saying what there's to do.
func (*plainAsker) Wait(_ context.Context, step, _, todo string, _ []boot.Key) (string, error) {
	return "", fmt.Errorf("%w: %s, then run kit bootstrap again (the %s step)", errNoTerminal, strings.TrimSuffix(todo, ", then press enter"), step)
}

// Pick takes the first of keys, the first time a step asks: without a
// terminal, signing in shows its code in plain lines, to enter on any
// device; asked again, it waits, as no key will come.
func (a *plainAsker) Pick(ctx context.Context, _ string, keys []boot.Key) (string, error) {
	if !a.picked && len(keys) > 0 {
		a.picked = true
		return keys[0].Key, nil
	}
	<-ctx.Done()
	return "", ctx.Err()
}
