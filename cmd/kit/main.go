// Command kit sets a Mac up from a config repository, and keeps it that way.
package main

import (
	"os"

	"github.com/leeovery/kit/internal/cli"
)

// version is set at build time with -ldflags "-X main.version=…".
var version = "dev"

func main() {
	root := cli.NewRootCommand(cli.Real(version))
	root.SetArgs(os.Args[1:])
	os.Exit(cli.Execute(root))
}
