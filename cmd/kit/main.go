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
	ctx, stop := ending()
	root := cli.NewRootCommand(cli.Real(version))
	status := cli.Execute(ctx, root, os.Args[1:])
	stop()
	os.Exit(status)
}

// ending is the context kit runs in, ended by an interrupt, a SIGTERM or the
// terminal closing (SIGHUP), which ends the commands kit is running, each
// with everything it started, before kit exits: each runs in a process
// group of its own, which the terminal's signals don't reach. kit itself
// isn't killed, so a job begun on git runs on past it and finishes. Its
// writes to a terminal that's gone fail, and end nothing: only SIGPIPE
// would, which a closed pipe sends, never a closed terminal (proven on
// macOS 26).
func ending() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
}
