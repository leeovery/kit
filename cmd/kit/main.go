// Command kit sets a Mac up from a config repository, and keeps it that way.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/leeovery/kit/internal/askpass"
	"github.com/leeovery/kit/internal/cli"
)

// version is set at build time with -ldflags "-X main.version=…".
var version = "dev"

func main() {
	// Run through the link a run makes for sudo, kit is the helper that asks
	// the run for an administrator's password.
	if filepath.Base(os.Args[0]) == askpass.Name {
		prompt := ""
		if len(os.Args) > 1 {
			prompt = os.Args[1]
		}
		if err := askpass.Ask(os.Args[0], prompt, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "kit:", err)
			os.Exit(1)
		}
		return
	}
	// An interrupt ends the commands kit is running, each with everything it
	// started, before kit exits: each runs in a process group of its own,
	// which the terminal's interrupt doesn't reach.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	root := cli.NewRootCommand(cli.Real(version))
	status := cli.Execute(ctx, root, os.Args[1:])
	stop()
	os.Exit(status)
}
