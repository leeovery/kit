// Command kit sets a Mac up from a config repository, and keeps it that way.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/leeovery/kit/internal/cli"
)

// version is set at build time with -ldflags "-X main.version=…".
var version = "dev"

func main() {
	// An interrupt ends the commands kit is running, each with everything it
	// started, before kit exits: each runs in a process group of its own,
	// which the terminal's interrupt doesn't reach.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	root := cli.NewRootCommand(cli.Real(version))
	status := cli.Execute(ctx, root, os.Args[1:])
	stop()
	os.Exit(status)
}
