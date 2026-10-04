package testguard

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// The variables no test may see: kit's own, which lead to its real config;
// those of the programs it drives, which can hold real tokens (Homebrew's,
// 1Password's, GitHub's, Claude Code's and the Anthropic API's) or lead to
// real state (git's, the SSH agent's, tmux's); and proxies', as a proxy on
// loopback would carry a request past the dial guard to the network.
// scripts/test-isolated unsets the same before any test binary starts.
var (
	clearedPrefixes = []string{"KIT_", "HOMEBREW_", "OP_", "GH_", "GITHUB_", "GIT_", "SSH_", "CLAUDE_", "ANTHROPIC_", "TMUX"}
	// clearedSuffix counts in either case, as proxies' lower-case names are
	// honoured too.
	clearedSuffix = "_PROXY"
)

// isolateEnv clears the variables no test may see, points HOME and XDG's base
// directories at home, and PATH at the stubs' directory alone.
func isolateEnv(home, stubs string) error {
	for _, variable := range os.Environ() {
		name, _, _ := strings.Cut(variable, "=")
		if !cleared(name) {
			continue
		}
		if err := os.Unsetenv(name); err != nil {
			return fmt.Errorf("clear %s: %w", name, err)
		}
	}
	for name, value := range map[string]string{
		"HOME":            home,
		"XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		"XDG_STATE_HOME":  filepath.Join(home, ".local", "state"),
		"XDG_CACHE_HOME":  filepath.Join(home, ".cache"),
		"XDG_DATA_HOME":   filepath.Join(home, ".local", "share"),
		"PATH":            stubs,
	} {
		if err := os.Setenv(name, value); err != nil {
			return fmt.Errorf("set %s: %w", name, err)
		}
	}
	return nil
}

func cleared(name string) bool {
	hasPrefix := func(prefix string) bool { return strings.HasPrefix(name, prefix) }
	return slices.ContainsFunc(clearedPrefixes, hasPrefix) || strings.HasSuffix(strings.ToUpper(name), clearedSuffix)
}
