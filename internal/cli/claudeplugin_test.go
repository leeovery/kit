package cli_test

import (
	"strings"
	"testing"
)

// pluginWorld is laptopWorld with Claude Code installed: a plugin declared
// and installed, another installed and declared nowhere, and a marketplace
// no plugin comes from.
func pluginWorld(t *testing.T) *world {
	t.Helper()
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "claude plugins", "lsp@makers/official-plugins\n")
	w.write(t, ".claude/plugins/installed_plugins.json", `{"version": 2, "plugins": {"lsp@official": [{"scope": "user"}], "diff@diffs": [{"scope": "user"}]}}`)
	w.write(t, ".claude/plugins/known_marketplaces.json", `{"official": {"source": {"source": "github", "repo": "makers/official-plugins"}}, "diffs": {"source": {"source": "github", "repo": "someone/diffs"}}, "leftover": {"source": {"source": "github", "repo": "gone/leftover"}}}`)
	w.fake.On("claude", "--version")
	return w
}

func TestReconcileAdoptsAPluginAndRemovesAMarketplace(t *testing.T) {
	w := pluginWorld(t)
	out, _, _ := w.run(t, "status", "claude-plugin")
	if !strings.Contains(out, "claude-plugin extra:new diff@someone/diffs\n") || !strings.Contains(out, "claude-plugin unused-dependency:new @gone/leftover\n") {
		t.Errorf("kit status printed\n%s\nwant diff extra and the leftover marketplace unused", out)
	}

	w.expectSync([]string{"laptop"}, "kit reconcile (laptop): adopt claude-plugin:diff@someone/diffs")
	out, errOut, code := w.run(t, "reconcile", "claude-plugin:diff@someone/diffs", "--adopt", "--group", "Review")
	if code != 0 {
		t.Fatalf("kit reconcile --adopt printed\n%s%s exit %d", out, errOut, code)
	}
	if got := w.readSection(t, "laptop", "claude plugins"); got != "lsp@makers/official-plugins\n\n# Review\ndiff@someone/diffs\n" {
		t.Errorf("[claude plugins] = %q", got)
	}

	w.fake.On("claude", "plugin", "marketplace", "remove", "leftover")
	out, errOut, code = w.run(t, "reconcile", "claude-plugin:@gone/leftover", "--remove")
	if code != 0 || !strings.Contains(out, "uninstalled; it wasn't declared") {
		t.Errorf("kit reconcile --remove printed\n%s%s exit %d", out, errOut, code)
	}
}
