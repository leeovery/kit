package claudeplugin_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/claudeplugin"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

const (
	// installed: lsp and diff at user scope, art at user scope too, and a
	// project's tool, which isn't kit's.
	installedJSON = `{"version": 2, "plugins": {
  "lsp@official": [{"scope": "user", "version": "1.0.0"}],
  "diff@diffs": [{"scope": "user", "version": "0.8.0"}],
  "art@official": [{"scope": "user", "version": "2.0.0"}],
  "tool@tools": [{"scope": "project", "projectPath": "/x"}]
}}`
	marketsJSON = `{
  "official": {"source": {"source": "github", "repo": "makers/official-plugins"}},
  "diffs": {"source": {"source": "github", "repo": "someone/diffs"}},
  "tools": {"source": {"source": "git", "url": "https://git.example.com/tools.git"}},
  "leftover": {"source": {"source": "github", "repo": "gone/leftover"}}
}`
	settingsJSON = `{"enabledPlugins": {"lsp@official": true, "diff@diffs": false, "art@official": true}}`
)

// home is a home whose Claude Code has those plugins and marketplaces.
func home(t *testing.T) string {
	t.Helper()
	h := t.TempDir()
	for path, content := range map[string]string{
		".claude/plugins/installed_plugins.json":  installedJSON,
		".claude/plugins/known_marketplaces.json": marketsJSON,
		".claude/settings.json":                   settingsJSON,
	} {
		write(t, filepath.Join(h, path), content)
	}
	return h
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func newPlugins(t *testing.T, fake *runnertest.Fake, h string) *claudeplugin.Plugins {
	t.Helper()
	fake.On("claude", "--version")
	return claudeplugin.New(fake, h)
}

func declared(names ...string) config.List {
	l := config.List{Kind: "claude-plugin"}
	for _, n := range names {
		l.Entries = append(l.Entries, config.Entry{Name: n})
	}
	return l
}

// Plugins are named by their marketplaces' sources; marketplaces are
// dependencies, needed while a plugin comes from one.
func TestInstalled(t *testing.T) {
	got, err := newPlugins(t, runnertest.New(t), home(t)).Installed(t.Context())
	want := []kind.Installed{
		{Name: "art@makers/official-plugins", Explicit: true},
		{Name: "diff@someone/diffs", Explicit: true},
		{Name: "lsp@makers/official-plugins", Explicit: true},
		{Name: "@someone/diffs", Needed: true},
		{Name: "@gone/leftover"},
		{Name: "@makers/official-plugins", Needed: true},
		{Name: "@https://git.example.com/tools.git"},
	}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("Installed() = %+v, %v\nwant %+v", got, err, want)
	}
}

func TestCompare(t *testing.T) {
	p := newPlugins(t, runnertest.New(t), home(t))
	got := kind.Compare(t.Context(), p, declared("lsp@makers/official-plugins", "diff@Someone/Diffs", "new@someone/new-market", "@https://git.example.com/tools.git"))
	want := []check.Item{
		{ID: "claude-plugin:new@someone/new-market", Name: "new@someone/new-market", State: kind.Missing, Action: kind.Install},
		{ID: "claude-plugin:diff@Someone/Diffs", Name: "diff@Someone/Diffs", State: kind.Changed, Detail: "installed differently: disabled", Action: kind.Install},
		{ID: "claude-plugin:art@makers/official-plugins", Name: "art@makers/official-plugins", State: kind.Extra},
		{ID: "claude-plugin:@gone/leftover", Name: "@gone/leftover", State: kind.UnusedDependency},
	}
	if got.Summary != "4 declared, 3 installed; 1 changed" || !slices.Equal(got.Items, want) {
		t.Errorf("Compare() = %+v\nwant items %+v", got, want)
	}
}

// Installing adds a plugin's marketplace first when Claude Code hasn't it,
// and turns on one installed but off.
func TestInstall(t *testing.T) {
	h := home(t)
	fake := runnertest.New(t)
	fake.On("claude", "plugin", "marketplace", "add", "someone/new-market").Does(func() {
		write(t, filepath.Join(h, ".claude/plugins/known_marketplaces.json"),
			strings.Replace(marketsJSON, "{\n", "{\n  \"newmk\": {\"source\": {\"source\": \"github\", \"repo\": \"someone/new-market\"}},\n", 1))
	})
	fake.On("claude", "plugin", "install", "new@newmk", "--scope", "user")
	fake.On("claude", "plugin", "enable", "diff@diffs", "--scope", "user")
	if err := newPlugins(t, fake, h).Install(t.Context(), []string{"new@someone/new-market", "diff@someone/diffs"}); err != nil {
		t.Fatal(err)
	}
	calls := fake.Calls()
	if i, j := slices.Index(calls, "claude plugin marketplace add someone/new-market"), slices.Index(calls, "claude plugin install new@newmk --scope user"); i < 0 || j < i {
		t.Errorf("ran %q, want the marketplace added, then the plugin installed", calls)
	}
}

func TestInstallAMarketplaceAlone(t *testing.T) {
	h := home(t)
	fake := runnertest.New(t)
	fake.On("claude", "plugin", "marketplace", "add", "someone/browse").Does(func() {
		write(t, filepath.Join(h, ".claude/plugins/known_marketplaces.json"), `{"browse": {"source": {"source": "github", "repo": "someone/browse"}}}`)
	})
	if err := newPlugins(t, fake, h).Install(t.Context(), []string{"@someone/browse"}); err != nil {
		t.Fatal(err)
	}
	fake.On("claude", "plugin", "marketplace", "add", "someone/silent")
	if err := newPlugins(t, fake, h).Install(t.Context(), []string{"x@someone/silent"}); err == nil || !strings.Contains(err.Error(), "claude added someone/silent, yet doesn't list it") {
		t.Errorf("Install() = %v", err)
	}
}

func TestRemove(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("claude", "plugin", "uninstall", "art@official", "--scope", "user")
	fake.On("claude", "plugin", "marketplace", "remove", "leftover")
	p := newPlugins(t, fake, home(t))
	if err := p.Remove(t.Context(), []string{"art@makers/official-plugins", "@gone/leftover"}); err != nil {
		t.Fatal(err)
	}
	if err := p.Remove(t.Context(), []string{"x@nobody/nothing"}); err == nil {
		t.Error("Remove() from a marketplace Claude hasn't = nil error")
	}
}

// Without Claude Code's records, nothing's installed; without claude,
// nothing can be.
func TestWithoutClaude(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("claude", "--version")
	if got, err := claudeplugin.New(fake, t.TempDir()).Installed(t.Context()); err != nil || len(got) != 0 {
		t.Errorf("Installed() with no records = %+v, %v", got, err)
	}
	got := kind.Compare(t.Context(), claudeplugin.New(runnertest.New(t), t.TempDir()), declared("lsp@makers/official-plugins"))
	if got.State != check.Deferred {
		t.Errorf("Compare() without claude = %+v, want it deferred", got)
	}
}
