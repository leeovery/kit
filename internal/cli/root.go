// Package cli is kit's command line. Commands stay thin: they parse flags,
// call the packages that do the work, and print.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/steps"
)

// Deps is what the commands take from the process around them. main passes the
// real ones; tests pass their own.
type Deps struct {
	Version string
	Getenv  func(key string) string
	// Environ lists the whole environment, as os.Environ does: where the
	// pretty face reads which colours the terminal shows.
	Environ func() []string
	HomeDir func() (string, error)
	Now     func() time.Time
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	// Terminal reports whether out is a terminal, as IsTerminal does: the
	// pretty face shows on one, the plain face elsewhere.
	Terminal func(out io.Writer) bool
	// Width is how many columns wide the terminal out is, as TerminalWidth
	// says.
	Width func(out io.Writer) int
	// Runner returns the runner kit runs programs with, finding them on path
	// and running them in env, as runner.Exec does.
	Runner func(path, env []string) runner.Runner
	// Choose asks, at the terminal, which of options to take, as ask.Choose
	// does: the index taken, or ask.ErrCancelled.
	Choose func(ctx context.Context, question string, options []string) (int, error)
	// ReadSecret asks, at the terminal, for a value typed without being
	// shown, after prompt.
	ReadSecret func(prompt string) (string, error)
	// Scratch is the Scratch volume, which the scratch feature checks and
	// clears: /Volumes/Scratch.
	Scratch string
	// SudoLocal is sudo's file of local settings, which the touch-id-sudo
	// feature checks: /etc/pam.d/sudo_local.
	SudoLocal string
	// UID is the user's id, which names Claude Code's folder in Scratch.
	UID int
}

// Exit statuses: a command that needs attention exits attentionStatus, one
// that couldn't do its job failedStatus.
const (
	attentionStatus = 1
	failedStatus    = 2
)

// attention is an error a command returns when it ran but found something
// that needs attention: kit exits 1 for it, printing the message.
type attention struct {
	message string
}

func (a attention) Error() string {
	return a.message
}

// attention without a message needs nothing printed: the face has said what
// needs attention.

// app is what every command shares: its dependencies, and the flags every
// command takes.
type app struct {
	Deps
	json    bool
	plain   bool
	verbose bool
}

// NewRootCommand builds the kit command tree.
func NewRootCommand(deps Deps) *cobra.Command {
	a := &app{Deps: deps}
	root := &cobra.Command{
		Use:     "kit",
		Short:   "Set up a Mac from a config repository, and keep it that way",
		Version: deps.Version,
		// Flags and arguments have parsed by the time this runs, so usage still
		// follows a mistyped command line but not a failure after it.
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			cmd.SilenceUsage = true
		},
		SilenceErrors:     true,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
		Long: `kit sets a Mac up from a config repository, and keeps it that way.

Run alone, it shows what needs attention at a glance, a line an area (backups,
the Mac, drift from the config, the config repository, your own checks), and
what to run about it. kit status is the full report.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return a.glance(cmd) },
	}
	root.SetOut(deps.Stdout)
	root.SetErr(deps.Stderr)
	flags := root.PersistentFlags()
	flags.BoolVar(&a.json, "json", false, "print one JSON document, for scripts and agents")
	flags.BoolVar(&a.plain, "plain", false, "print plain lines, no colour or animation, as without a terminal")
	flags.BoolVar(&a.verbose, "verbose", false, "keep commands' output whole in the run's log")
	// Commands show in the order they're added: the whole Mac's by use,
	// then what kit manages, by group.
	cobra.EnableCommandSorting = false
	root.AddGroup(groups...)
	for _, cmd := range []*cobra.Command{newStatusCommand(a), newApplyCommand(a), newReconcileCommand(a), newNightlyCommand(a), newListCommand(a), newWhyCommand(a), newLogCommand(a), newMachineCommand(a)} {
		cmd.GroupID = groupMac
		root.AddCommand(cmd)
	}
	root.AddCommand(nounCommands(a)...)
	root.AddCommand(newVersionCommand())
	return root
}

// Execute runs root in ctx, prints what went wrong, if anything, and returns
// the status to exit with: 0, attentionStatus, or failedStatus.
func Execute(ctx context.Context, root *cobra.Command) int {
	err := root.ExecuteContext(ctx)
	if err == nil {
		return 0
	}
	a, isAttention := errors.AsType[attention](err)
	if !isAttention || a.message != "" {
		_, _ = fmt.Fprintf(root.ErrOrStderr(), "kit: %v\n", err)
	}
	if isAttention {
		return attentionStatus
	}
	return failedStatus
}

// dirs locates kit's directories.
func (a *app) dirs() (config.Dirs, error) {
	home, err := a.HomeDir()
	if err != nil {
		return config.Dirs{}, fmt.Errorf("find the home directory: %w", err)
	}
	return config.Locate(home, a.Getenv)
}

// loadConfig loads the config repository, and checks this kit reads it.
func (a *app) loadConfig(dirs config.Dirs) (*config.Config, error) {
	cfg, err := config.Load(dirs.Config)
	if errors.Is(err, config.ErrNoConfig) {
		return nil, fmt.Errorf("%w: clone your config repository to %s, or point KIT_CONFIG at it", err, dirs.Config)
	}
	if err != nil {
		return nil, err
	}
	if err := cfg.Supports(a.Version); err != nil {
		return nil, err
	}
	return cfg, nil
}

// pretty reports whether out shows the pretty face: a terminal, and
// neither --json nor --plain.
func (a *app) pretty(out io.Writer) bool {
	return !a.json && !a.plain && a.Terminal(out)
}

// colors is a writer to out that brings colour down to what out shows: none,
// unless it's the pretty face's terminal.
func (a *app) colors(out io.Writer) io.Writer {
	if !a.pretty(out) {
		return &colorprofile.Writer{Forward: out, Profile: colorprofile.NoTTY}
	}
	return colorprofile.NewWriter(out, a.Environ())
}

// Real returns the Deps of the running process.
func Real(version string) Deps {
	return Deps{
		Version:  version,
		Getenv:   os.Getenv,
		Environ:  os.Environ,
		HomeDir:  os.UserHomeDir,
		Now:      time.Now,
		Stdin:    os.Stdin,
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
		Terminal: IsTerminal,
		Width:    TerminalWidth,
		Runner: func(path, env []string) runner.Runner {
			return runner.Exec{Path: path, Env: env, Now: time.Now, Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}
		},
		Choose: func(ctx context.Context, question string, options []string) (int, error) {
			return ask.Choose(ctx, os.Stdin, os.Stdout, question, options)
		},
		ReadSecret: func(prompt string) (string, error) {
			_, _ = fmt.Fprint(os.Stderr, prompt)
			b, err := term.ReadPassword(os.Stdin.Fd())
			_, _ = fmt.Fprintln(os.Stderr)
			return string(b), err
		},
		Scratch:   "/Volumes/Scratch",
		SudoLocal: steps.SudoLocal,
		UID:       os.Getuid(),
	}
}

// IsTerminal reports whether out is a terminal.
func IsTerminal(out io.Writer) bool {
	f, ok := out.(*os.File)
	return ok && term.IsTerminal(f.Fd())
}

// TerminalWidth is how many columns wide the terminal out is: 80 when it
// can't tell.
func TerminalWidth(out io.Writer) int {
	if f, ok := out.(*os.File); ok {
		if width, _, err := term.GetSize(f.Fd()); err == nil && width > 0 {
			return width
		}
	}
	return 80
}
