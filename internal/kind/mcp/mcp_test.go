package mcp_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/mcp"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// claudeJSON is what Claude Code has installed: docs; design, with its key
// in plain text; the site's mail, with its key in plain text, and pages; an
// extra; and another project's server.
const claudeJSON = `{
  "numStartups": 12,
  "mcpServers": {
    "docs": {"type": "http", "url": "https://docs.example.com/mcp"},
    "design": {"type": "http", "url": "https://design.example.com/mcp", "headers": {"Authorization": "Bearer made-up-key"}},
    "stray": {"type": "stdio", "command": "stray-mcp", "args": [], "env": {}}
  },
  "projects": {
    "HOME/Code/site": {
      "allowedTools": [],
      "mcpServers": {
        "mail": {"type": "stdio", "command": "npx", "args": ["-y", "mail-mcp"], "env": {"MAIL_API_KEY": "made-up-key"}},
        "pages": {"type": "http", "url": "https://pages.example.com/mcp"}
      }
    },
    "HOME/Code/other": {"mcpServers": {"docs": {"type": "http", "url": "https://docs.example.com/mcp"}}},
    "HOME/Code/empty": {"mcpServers": {}}
  }
}`

// declared is what laptop declares: docs; tablet, off; design and the
// site's mail, their keys named; the site's pages; and a server for a folder
// that isn't here.
func declared() config.List {
	e := func(name, value string, line int) config.Entry {
		return config.Entry{Name: name, Value: value, File: "laptop", Line: line}
	}
	return config.List{Kind: "claude-mcp", Entries: []config.Entry{
		e("docs", "--transport http https://docs.example.com/mcp", 2),
		e("tablet", "--off -- zsh -c tablet-mcp", 3),
		e("design", `--transport http https://design.example.com/mcp --header "Authorization: Bearer ${DESIGN_KEY}"`, 4),
		e("~/Code/site:mail", "--env MAIL_API_KEY=${MAIL_API_KEY_SITE} -- npx -y mail-mcp", 7),
		e("~/Code/site:pages", "--transport http https://pages.example.com/mcp", 8),
		e("~/Code/gone:tracker", "--transport http https://tracker.example.com/mcp", 11),
	}}
}

// world is a home with Claude Code's config in it, the site's folder there.
func world(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(strings.ReplaceAll(claudeJSON, "HOME", home)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "Code", "site"), 0o700); err != nil {
		t.Fatal(err)
	}
	return home
}

// newMCP is the kind, with claude installed as far as the fake says, and
// laptop's servers read.
func newMCP(t *testing.T, fake *runnertest.Fake, home string) (*mcp.MCP, config.List) {
	t.Helper()
	fake.On("claude", "--version")
	m := mcp.New(fake, home)
	list, err := m.Values(declared())
	if err != nil {
		t.Fatal(err)
	}
	return m, list
}

func TestValuesMarkWhatsOff(t *testing.T) {
	_, list := newMCP(t, runnertest.New(t), world(t))
	for _, e := range list.Entries {
		if e.Off != (e.Name == "tablet") {
			t.Errorf("%s: Off = %v", e.Name, e.Off)
		}
	}
}

func TestValuesRefuse(t *testing.T) {
	for value, want := range map[string]string{
		`--transport http https://x.example.com --header "Authorization: Bearer abc"`: "laptop:2: x: headers.Authorization holds a key in plain text",
		`--transport http`:      "laptop:2: x: neither a URL nor a command",
		`--frobnicate -- x-mcp`: "kit doesn't know the option --frobnicate",
		`{"type": `:             "its JSON doesn't read",
	} {
		m := mcp.New(runnertest.New(t), t.TempDir())
		_, err := m.Values(config.List{Entries: []config.Entry{{Name: "x", Value: value, File: "laptop", Line: 2}}})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Values(%q) error = %v, want %q", value, err, want)
		}
	}
}

// What's installed otherwise than declared is changed, and says where,
// never what: the keys in plain text stay unsaid. A server declared off is
// neither missing nor extra; a project's whose folder isn't here waits.
func TestCompare(t *testing.T) {
	m, list := newMCP(t, runnertest.New(t), world(t))
	got := kind.Compare(t.Context(), m, list)
	want := []check.Item{
		{ID: "claude-mcp:~/Code/gone:tracker", Name: "~/Code/gone:tracker", State: kind.Missing, Detail: "the folder isn't here yet"},
		{ID: "claude-mcp:design", Name: "design", State: kind.Changed, Detail: "installed differently: headers", Action: kind.Install},
		{ID: "claude-mcp:~/Code/site:mail", Name: "~/Code/site:mail", State: kind.Changed, Detail: "installed differently: env", Action: kind.Install},
		{ID: "claude-mcp:stray", Name: "stray", State: kind.Extra},
		{ID: "claude-mcp:~/Code/other:docs", Name: "~/Code/other:docs", State: kind.Extra},
	}
	by := func(a, b check.Item) int { return strings.Compare(a.State+a.ID, b.State+b.ID) }
	slices.SortFunc(got.Items, by)
	slices.SortFunc(want, by)
	if got.Summary != "5 declared, 4 installed; 2 changed; 1 off" || !slices.Equal(got.Items, want) {
		t.Errorf("Compare() = %+v\nwant items %+v", got, want)
	}
	out, _ := json.Marshal(got)
	if strings.Contains(string(out), "made-up-key") {
		t.Errorf("the result holds a key: %s", out)
	}
}

func TestWithoutClaude(t *testing.T) {
	m := mcp.New(runnertest.New(t), world(t))
	list, err := m.Values(declared())
	if err != nil {
		t.Fatal(err)
	}
	if got := kind.Compare(t.Context(), m, list); got.State != check.Deferred || got.Reason != "needs claude, which isn't installed" {
		t.Errorf("Compare() = %+v, want it deferred", got)
	}
}

// Installing replaces a server installed otherwise, in its scope: a
// project's in local scope, run in its folder; the definition handed over
// names its keys, never holds them.
func TestInstallAndRemove(t *testing.T) {
	home := world(t)
	fake := runnertest.New(t)
	fake.On("claude", "mcp", "remove", "-s", "user", "design")
	fake.On("claude", "mcp", "add-json", "-s", "user", "design", `{"headers":{"Authorization":"Bearer ${DESIGN_KEY}"},"type":"http","url":"https://design.example.com/mcp"}`)
	fake.On("claude", "mcp", "remove", "-s", "local", "mail")
	fake.On("claude", "mcp", "add-json", "-s", "local", "mail", `{"args":["-y","mail-mcp"],"command":"npx","env":{"MAIL_API_KEY":"${MAIL_API_KEY_SITE}"},"type":"stdio"}`)
	fake.On("claude", "mcp", "remove", "-s", "user", "stray")
	m, _ := newMCP(t, fake, home)
	if err := m.Install(t.Context(), []string{"design", "~/Code/site:mail"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove(t.Context(), []string{"stray"}); err != nil {
		t.Fatal(err)
	}
	for _, c := range fake.Commands() {
		if c.Name == "claude" && slices.Contains(c.Args, "local") && c.Dir != filepath.Join(home, "Code", "site") {
			t.Errorf("ran %s in %q, want the project's folder", c, c.Dir)
		}
	}
	if err := m.Install(t.Context(), []string{"nosuch"}); err == nil {
		t.Error("Install() of a server not declared = nil error")
	}
}

// Declaring a server Claude Code has writes its line as claude mcp add's
// options, its empty fields left out; one holding a key in plain text isn't
// declared, and the key goes unsaid.
func TestValue(t *testing.T) {
	m, _ := newMCP(t, runnertest.New(t), world(t))
	if got, err := m.Value(t.Context(), "stray"); err != nil || got != "-- stray-mcp" {
		t.Errorf("Value(stray) = %q, %v", got, err)
	}
	if got, err := m.Value(t.Context(), "~/Code/site:pages"); err != nil || got != "--transport http https://pages.example.com/mcp" {
		t.Errorf("Value(pages) = %q, %v", got, err)
	}
	_, err := m.Value(t.Context(), "design")
	if err == nil || !strings.Contains(err.Error(), "design holds a key in plain text (headers.Authorization)") || strings.Contains(err.Error(), "made-up-key") {
		t.Errorf("Value(design) = %v, want it refused without the key", err)
	}
	if _, err := m.Value(t.Context(), "nosuch"); err == nil {
		t.Error("Value() of a server not installed = nil error")
	}
}
