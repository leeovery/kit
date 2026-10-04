// Package cli is kit's command line. Commands stay thin: they parse flags,
// call the packages that do the work, and print.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/config"
)

// Deps is what the commands take from the process around them. main passes the
// real ones; tests pass their own.
type Deps struct {
	Version string
	Getenv  func(key string) string
	HomeDir func() (string, error)
	Stdout  io.Writer
	Stderr  io.Writer
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

// app is what every command shares.
type app struct {
	Deps
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
	}
	root.SetOut(deps.Stdout)
	root.SetErr(deps.Stderr)
	root.AddCommand(newMachineCommand(a), newVersionCommand())
	return root
}

// Execute runs root in ctx, prints what went wrong, if anything, and returns
// the status to exit with: 0, attentionStatus, or failedStatus.
func Execute(ctx context.Context, root *cobra.Command) int {
	err := root.ExecuteContext(ctx)
	if err == nil {
		return 0
	}
	_, _ = fmt.Fprintf(root.ErrOrStderr(), "kit: %v\n", err)
	if _, ok := errors.AsType[attention](err); ok {
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

// Real returns the Deps of the running process.
func Real(version string) Deps {
	return Deps{
		Version: version,
		Getenv:  os.Getenv,
		HomeDir: os.UserHomeDir,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
	}
}
