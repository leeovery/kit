// Command kitapp lays out kit.app's bundle for a release, as press builds it
// (.press.toml): all but its executable, which press's build compiles from
// internal/macapp/kit.swift, then signs with the team's Developer ID. It runs
// no program.
//
// Usage:
//
//	kitapp <bundle> <version> <team>
package main

import (
	"fmt"
	"os"

	"github.com/leeovery/kit/internal/macapp"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: kitapp <bundle> <version> <team>")
		os.Exit(2)
	}
	if err := macapp.Lay(os.Args[1], os.Args[2], os.Args[3]); err != nil {
		fmt.Fprintln(os.Stderr, "kitapp:", err)
		os.Exit(1)
	}
}
