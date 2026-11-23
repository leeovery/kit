// Package cli is kit's command line. Commands stay thin: they parse flags,
// call the packages that do the work, and print.
package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/askpass"
	"github.com/leeovery/kit/internal/boot"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kinds"
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/render"
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
	// After is a wait of d, for what w says, as time.After: how long a step
	// by hand's card waits to go up, and to post its notification, and
	// between its looks.
	After  func(w steps.Wait, d time.Duration) <-chan time.Time
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// Terminal reports whether f, an output or the standard input, is a
	// terminal, as IsTerminal does: the pretty face shows on one, the plain
	// face elsewhere.
	Terminal func(f any) bool
	// Width is how many columns wide the terminal out is, as TerminalWidth
	// says.
	Width func(out io.Writer) int
	// Height is how many lines high the terminal out is, as TerminalHeight
	// finds it.
	Height func(out io.Writer) int
	// Runner returns the runner kit runs programs with, finding them on path
	// and running them in env, as runner.Exec does.
	Runner func(path, env []string) runner.Runner
	// Choose asks a question at the terminal, in place, as ask.Choose does:
	// the answer taken, by its place, or ask.ErrCancelled.
	Choose func(ctx context.Context, q ask.Question) (int, error)
	// Walk asks questions one after another at the terminal, in place, as
	// ask.Walk does: the answers taken, all of them, or those before
	// ask.ErrStopped; or ask.ErrCancelled.
	Walk func(ctx context.Context, header string, qs []ask.Question) ([]int, error)
	// ReadSecret asks, at the terminal, in place, for a value typed without
	// being shown, for the thing about is, as ask.Secret does.
	ReadSecret func(ctx context.Context, about look.Row) (string, error)
	// Offer offers what lines say at the terminal, in place, keys under
	// them, as ask.Offer does: whether enter took it up.
	Offer func(ctx context.Context, lines []string, keys []look.Key) (bool, error)
	// Pick shows a list at the terminal, in place, under head, to pick a line
	// from, as ask.Pick does: the line's value, or ask.ErrCancelled.
	Pick func(ctx context.Context, head []string, lines []ask.Line, keys []look.Key) (string, error)
	// Listen reads keys at the terminal as they're pressed, in its raw mode,
	// for applying's screen, as ask.Listen does: they come till stop puts
	// the terminal back. Without it, applying's screen reads none.
	Listen func() (keys <-chan ask.Key, stop func())
	// Become has kit become the kit at kit run with args, in its own
	// environment with env's variables added, as if it had been run so at
	// the terminal: it replaces this process, and returns only when it
	// can't.
	Become func(kit string, args, env []string) error
	// Shell has kit become the person's login shell, as a terminal starts
	// it: it replaces this process, and returns only when it can't.
	Shell func() error
	// GitHub is GitHub's site and API, as the boot and kit setup sign in and
	// read repositories through them.
	GitHub func() boot.GitHub
	// Self is the path kit runs from, as os.Executable finds it: the kit the
	// hourly launch runs.
	Self func() (string, error)
	// SudoLocal is sudo's file of local settings, which the touch-id-sudo
	// feature checks: /etc/pam.d/sudo_local.
	SudoLocal string
	// Homebrew are Homebrew's folders, which a config starting on this Mac
	// puts on every Mac's PATH: config.HomebrewPaths.
	Homebrew []string
	// SSHDir is where the Mac keeps its SSH host keys, which the host-key
	// step reads, and SSHAddress where its Remote Login answers, which the
	// step wakes to make them: /etc/ssh and 127.0.0.1:22 when "".
	SSHDir, SSHAddress string
	// TCC is macOS's database of privacy grants, readable only with Full
	// Disk Access: one of what prefs probes for it.
	TCC string
	// AppDirs are the folders apps are installed in, but the one in the
	// home, which kit adds: /Applications.
	AppDirs []string
	// WatchDialogs is whether a run at a terminal watches for macOS asking
	// the person at the Mac something, its time limits waiting meanwhile.
	WatchDialogs bool
}

// Exit statuses: a command that ran exits 0, even when what it found needs
// attention; one in which something it tried failed exits failedStatus, and
// one that couldn't do its job at all couldntStatus.
const (
	failedStatus  = 1
	couldntStatus = 2
)

// briefSchema is the version of the short documents commands print with
// --json that have no schema of their own: what went wrong, kit's version,
// this Mac's name, a step by hand marked, and kit prefs's history, its
// start afresh and what it cleared.
const briefSchema = 1

// failure is the error a command returns when something it tried failed,
// the face having said what: kit exits failedStatus for it, printing
// nothing more.
type failure struct{}

func (failure) Error() string {
	return "something kit tried failed"
}

// notFound is the error a command returns when what it was asked for isn't
// there: kit prints the message, and exits 0, the command having done its
// job.
type notFound struct {
	message string
}

func (n notFound) Error() string {
	return n.message
}

// app is what every command shares: its dependencies, and the flags every
// command takes.
type app struct {
	Deps
	json    bool
	plain   bool
	verbose bool
	version bool
	// parsed is whether kit got as far as running the command: its command
	// line read, so what went wrong is no mistake in it.
	parsed bool
	// document is standard output with --json, which knows whether the
	// command has written its document.
	document *document
	// helpRefused is help asked for with --json, which it doesn't take:
	// cobra shows help without an error to return.
	helpRefused error
	// underHome is whether kit runs from its home's menu, under the home's
	// wordmark: its own heading leaves the wordmark out.
	underHome bool
	// restoring are kit apply's flags naming the data restore's source;
	// newMacRun has the run a new Mac's started from this one, as kit agent
	// order places steps, the data restore in it wherever [restore] has
	// lines, and the reinstall after it; named are the steps kit apply
	// names, to apply those alone.
	restoring restoreFlags
	newMacRun bool
	named     []string
	// scheduled is whether the run is the hourly launch's, kit nightly
	// --scheduled, kit.app's child: what it finds of Full Disk Access is
	// kit.app's, not a terminal's.
	scheduled bool
	// pull is whether the run brings in kit-config's remote commits before
	// it reads the config (pullConfig), as kit apply, status, reconcile and
	// nightly do; pullQuietly, whether it says nothing meanwhile, as a run
	// of steps named shows no heading.
	pull, pullQuietly bool
	// unwinds put back what the faces kit shows have taken of the
	// terminal, should kit panic.
	unwinds []func()
	// args are kit's command line, as given, for a command that reads it
	// whole: a line copied for a kind's program.
	args []string
}

// NewRootCommand builds the kit command tree. A command is known by the
// start of its name, when no other starts so: kit rec is kit reconcile.
func NewRootCommand(deps Deps) *cobra.Command {
	cobra.EnablePrefixMatching = true
	a := &app{Deps: deps}
	root := &cobra.Command{
		Use:   "kit",
		Short: "Set up a Mac from a config repository, and keep it that way",
		// Flags and arguments have parsed by the time this runs, so usage still
		// follows a mistyped command line but not a failure after it.
		PersistentPreRun:  func(cmd *cobra.Command, _ []string) { a.ready(cmd) },
		SilenceErrors:     true,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
		Long: `kit sets a Mac up from a config repository, and keeps it that way.

Run alone at a terminal, it's kit's menu: what to run next, at once. kit
status is the report, every check.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if a.version {
				return a.showVersion()
			}
			return a.home(cmd)
		},
	}
	root.SuggestionsMinimumDistance = 2
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageError{err} })
	root.SetOut(deps.Stdout)
	root.SetErr(&errOut{Writer: deps.Stderr, a: a})
	root.SetHelpFunc(a.help(root.HelpFunc()))
	flags := root.PersistentFlags()
	flags.BoolVar(&a.json, "json", false, "print one JSON document, for scripts and agents")
	flags.BoolVar(&a.plain, "plain", false, "print plain lines, no colour or animation, as without a terminal")
	flags.BoolVar(&a.verbose, "verbose", false, "keep commands' output whole in the run's log")
	flags.BoolVar(&a.underHome, underHome, false, "run from kit's home, under its wordmark")
	_ = flags.MarkHidden(underHome)
	root.Flags().BoolVarP(&a.version, "version", "v", false, "version for kit")
	// Commands show in the order they're added: the whole Mac's by use,
	// then what kit manages, by group.
	cobra.EnableCommandSorting = false
	root.AddGroup(groups...)
	for _, cmd := range []*cobra.Command{newStatusCommand(a), newApplyCommand(a), newReconcileCommand(a), newNightlyCommand(a), newListCommand(a), newWhyCommand(a), newLogCommand(a), newMachineCommand(a), newTerminalCommand(a), newBootstrapCommand(a), newSetupCommand(a), newAgentCommand(a)} {
		cmd.GroupID = groupMac
		root.AddCommand(cmd)
	}
	root.AddCommand(nounCommands(a)...)
	root.AddCommand(newSplashCommand(a))
	return root
}

// ready readies kit to run cmd, its command line read: with --json,
// standard output is the document.
func (a *app) ready(cmd *cobra.Command) {
	a.parsed = true
	if a.json {
		a.document = &document{Writer: a.Stdout}
		a.Stdout = a.document
		cmd.Root().SetOut(a.Stdout)
	}
}

// Execute runs root with args in ctx, shows what went wrong, if anything,
// and returns the status to exit with: 0, failedStatus, or couldntStatus.
// Should kit panic, the terminal is put back before the panic shows.
func Execute(ctx context.Context, root *cobra.Command, args []string) int {
	root.SetArgs(args)
	e, ok := root.ErrOrStderr().(*errOut)
	if ok {
		e.a.args = args
		defer e.a.unwind()
	}
	// kit shows a usage itself, where one helps (errOut.show): cobra's goes
	// to standard output.
	root.SilenceUsage = true
	cmd, err := root.ExecuteContextC(ctx)
	if err == nil && ok {
		err = e.a.helpRefused
	}
	if err == nil {
		return 0
	}
	if _, isFailure := errors.AsType[failure](err); isFailure {
		return failedStatus
	}
	status := couldntStatus
	switch _, isNotFound := errors.AsType[notFound](err); {
	case isNotFound:
		status = 0
	// A question cancelled exits as a failure: nothing was done, so what
	// needed attention still does.
	case errors.Is(err, ask.ErrCancelled):
		status = failedStatus
	}
	if ok {
		e.show(cmd, args, err, status)
	} else {
		_, _ = fmt.Fprintf(root.ErrOrStderr(), "kit: %v\n", err)
	}
	return status
}

// becomeSelf has kit become itself, run with args (Become).
func (a *app) becomeSelf(args []string) error {
	self, err := a.Self()
	if err != nil {
		return fmt.Errorf("find kit: %w", err)
	}
	return a.Become(self, args, nil)
}

// showVersion prints kit's version: as a document with --json.
func (a *app) showVersion() error {
	if a.json {
		return render.WriteJSON(a.Stdout, struct {
			Schema  int    `json:"schema"`
			Version string `json:"version"`
		}{briefSchema, a.Version})
	}
	_, err := fmt.Fprintf(a.Stdout, "kit version %s\n", a.Version)
	return err
}

// unwind, deferred at kit's top, has a panic put the terminal back as the
// faces kit showed found it (applying's screen takes the terminal's
// alternate screen, its raw mode and the cursor), then panic on, for the
// panic to show as it would have.
func (a *app) unwind() {
	p := recover()
	if p == nil {
		return
	}
	for _, unwind := range a.unwinds {
		unwind()
	}
	panic(p)
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
	cfg, err := config.Load(dirs.Config, kinds.Sections())
	if errors.Is(err, config.ErrNoConfig) {
		return nil, fmt.Errorf("%w: set this Mac up with kit setup, clone your config repository to %s, or point KIT_CONFIG at it", err, dirs.Config)
	}
	if err != nil {
		return nil, err
	}
	if err := cfg.Supports(a.Version); err != nil {
		return nil, err
	}
	return cfg, nil
}

// document is standard output with --json, keeping to one document: it
// knows whether one's been written.
type document struct {
	io.Writer
	written bool
}

func (d *document) Write(p []byte) (int, error) {
	d.written = d.written || len(p) > 0
	return d.Writer.Write(p)
}

// documented reports whether the command has written its document to
// standard output, with --json.
func (a *app) documented() bool {
	return a.document != nil && a.document.written
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
		After:    func(_ steps.Wait, d time.Duration) <-chan time.Time { return time.After(d) },
		Stdin:    os.Stdin,
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
		Terminal: IsTerminal,
		Width:    TerminalWidth,
		Height:   TerminalHeight,
		Runner: func(path, env []string) runner.Runner {
			return runner.Exec{Path: path, Env: env, Now: time.Now}
		},
		Choose: func(ctx context.Context, q ask.Question) (int, error) {
			return ask.Choose(ctx, terminal(), q)
		},
		Walk: func(ctx context.Context, header string, qs []ask.Question) ([]int, error) {
			return ask.Walk(ctx, terminal(), header, qs)
		},
		ReadSecret: func(ctx context.Context, about look.Row) (string, error) {
			return ask.Secret(ctx, terminal(), about)
		},
		Offer: func(ctx context.Context, lines []string, keys []look.Key) (bool, error) {
			return ask.Offer(ctx, terminal(), lines, keys)
		},
		Pick: func(ctx context.Context, head []string, lines []ask.Line, keys []look.Key) (string, error) {
			return ask.Pick(ctx, terminal(), head, lines, keys)
		},
		Listen: func() (<-chan ask.Key, func()) {
			return ask.Listen(terminal())
		},
		Become: func(kit string, args, env []string) error {
			return runner.Become(kit, args, append(os.Environ(), env...))
		},
		Shell: func() error {
			return runner.BecomeLoginShell(cmp.Or(os.Getenv("SHELL"), "/bin/zsh"), askpass.Without(os.Environ()))
		},
		GitHub: func() boot.GitHub {
			return boot.NewGitHub(&http.Client{Timeout: time.Minute})
		},
		Self:         os.Executable,
		WatchDialogs: true,
		SudoLocal:    steps.SudoLocal,
		Homebrew:     config.HomebrewPaths,
		TCC:          "/Library/Application Support/com.apple.TCC/TCC.db",
		AppDirs:      []string{"/Applications"},
	}
}

// IsTerminal reports whether f is a terminal.
func IsTerminal(f any) bool {
	file, ok := f.(*os.File)
	return ok && term.IsTerminal(file.Fd())
}

// terminal is the process's terminal, where kit asks: its standard input,
// and its standard output, in the colours it shows.
func terminal() ask.Terminal {
	return ask.Terminal{
		In:  os.Stdin,
		Out: colorprofile.NewWriter(os.Stdout, os.Environ()),
		Size: func() (int, int) {
			return TerminalWidth(os.Stdout), TerminalHeight(os.Stdout)
		},
	}
}

// TerminalHeight is how many lines high the terminal out is: 24 when it
// isn't a terminal, or doesn't say.
func TerminalHeight(out io.Writer) int {
	if f, ok := out.(*os.File); ok {
		if _, height, err := term.GetSize(f.Fd()); err == nil && height > 0 {
			return height
		}
	}
	return 24
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
