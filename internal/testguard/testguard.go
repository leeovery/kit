// Package testguard keeps tests off the real system: the network beyond this
// machine, the environment and home directory they start with, and the
// programs they must never run. Every package with tests runs them through
// it:
//
//	func TestMain(m *testing.M) { os.Exit(testguard.Main(m)) }
//
// It imports nothing of kit's, so any package's tests can use it, and it's
// the one place allowed to change the process's environment.
package testguard

import (
	"cmp"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Main runs m's tests in isolation, and returns the status to exit with.
//
// Before the tests, it points HOME and XDG's base directories into a
// throwaway root; clears the variables that configure kit and the programs
// it drives, which can hold real tokens or lead to real state, and proxies';
// sets PATH to a directory of stubs alone; and lets http.DefaultTransport,
// and every transport cloned from it after, dial loopback, and unix sockets
// in the temporary directory, alone.
//
// After them, it fails the run, even when every test passed, if a stub ran,
// a dial was blocked, kit's real config changed, its state or logs directory
// appeared, or its Mac's name changed, in the real home or wherever
// KIT_CONFIG, XDG_CONFIG_HOME and XDG_STATE_HOME put them as the tests began,
// saying which on stderr. Then it removes the root.
func Main(m *testing.M) int {
	g, err := install()
	if err != nil {
		fmt.Fprintf(os.Stderr, "testguard: %v\n", err)
		return 1
	}
	return g.finish(m.Run())
}

// guard is the isolation Main installs, and what it checks once the tests
// have run.
type guard struct {
	// root is the throwaway directory the home and the stubs are in.
	root  string
	stubs stubs
	dials *dialGuard
	// real is what the real system held of kit's as the tests began.
	real watchers
}

// install isolates the process as Main says, having first noted what the real
// system holds of kit's, where the environment says as much as in the real
// home.
func install() (*guard, error) {
	home, _ := os.UserHomeDir()
	root, err := os.MkdirTemp("", "testguard-")
	if err != nil {
		return nil, fmt.Errorf("create a throwaway root: %w", err)
	}
	g := &guard{
		root:  root,
		dials: newDialGuard(os.TempDir()),
		real:  watchReal(home, os.Getenv),
	}
	if err := g.isolate(); err != nil {
		_ = os.RemoveAll(root)
		return nil, err
	}
	return g, nil
}

func (g *guard) isolate() error {
	stubs, err := writeStubs(filepath.Join(g.root, "bin"), filepath.Join(g.root, "stub-runs"))
	if err != nil {
		return err
	}
	g.stubs = stubs
	home := filepath.Join(g.root, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		return fmt.Errorf("create the throwaway home: %w", err)
	}
	if err := isolateEnv(home, stubs.dir); err != nil {
		return err
	}
	return g.dials.install(http.DefaultTransport)
}

// finish removes the root and returns the status to exit with: status, or 1
// in place of 0 when the tests reached past their isolation, which it reports.
func (g *guard) finish(status int) int {
	escapes := slices.Concat(g.stubs.runs(), g.dials.escapes(), g.real.changes())
	if err := os.RemoveAll(g.root); err != nil {
		fmt.Fprintf(os.Stderr, "testguard: %v\n", err)
	}
	if len(escapes) == 0 {
		return status
	}
	fmt.Fprint(os.Stderr, report(escapes))
	return cmp.Or(status, 1)
}

// report says how the tests reached past their isolation, a line each.
func report(escapes []string) string {
	var b strings.Builder
	b.WriteString("testguard: the tests reached past their isolation, so the run fails even if every test passed:\n")
	for _, escape := range escapes {
		b.WriteString("  " + escape + "\n")
	}
	b.WriteString("Inject what a test needs instead: see Test isolation in CLAUDE.md.\n")
	return b.String()
}
