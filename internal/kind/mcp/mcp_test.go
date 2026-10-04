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

// shared declares two user-level servers, one off; laptop's file declares
// one more, and a project's two.
const (
	shared = `{
  "docs": {
    "_note": "the docs server",
    "type": "http",
    "url": "https://docs.example.com/mcp"
  },
  "tablet": {
    "_enabled": false,
    "args": ["-c", "tablet-mcp"],
    "command": "zsh",
    "type": "stdio"
  }
}
`
	laptop = `{
  "design": {
    "headers": {
      "Authorization": "Bearer ${DESIGN_KEY}"
    },
    "type": "http",
    "url": "https://design.example.com/mcp"
  },
  "~/Code/site": {
    "mail": {
      "args": ["-y", "mail-mcp"],
      "command": "npx",
      "env": {
        "MAIL_API_KEY": "${MAIL_API_KEY_SITE}"
      },
      "type": "stdio"
    },
    "pages": {
      "type": "http",
      "url": "https://pages.example.com/mcp"
    }
  },
  "~/Code/gone": {
    "tracker": {
      "type": "http",
      "url": "https://tracker.example.com/mcp"
    }
  }
}
`
	// claudeJSON is what Claude Code has installed: docs; design, with its
	// key in plain text; the site's mail, with its key in plain text, and
	// pages; an extra; and another project's server.
	claudeJSON = `{
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
)

// world is a home with Claude Code's config in it, and a config repository
// declaring servers for laptop.
func world(t *testing.T) (home, dir string) {
	t.Helper()
	home, dir = t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".claude.json"), strings.ReplaceAll(claudeJSON, "HOME", home))
	write(t, filepath.Join(dir, "mcp.json"), shared)
	write(t, filepath.Join(dir, "mcp.laptop.json"), laptop)
	if err := os.MkdirAll(filepath.Join(home, "Code", "site"), 0o700); err != nil {
		t.Fatal(err)
	}
	return home, dir
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// newMCP is the kind, with claude installed as far as the fake says.
func newMCP(t *testing.T, fake *runnertest.Fake, home, dir string) *mcp.MCP {
	t.Helper()
	fake.On("claude", "--version")
	return mcp.New(fake, home, dir, "laptop")
}

func TestDeclared(t *testing.T) {
	home, dir := world(t)
	got, err := newMCP(t, runnertest.New(t), home, dir).Declared()
	want := []config.Entry{
		{Name: "docs", File: "mcp.json", Line: 2, Note: "the docs server"},
		{Name: "tablet", File: "mcp.json", Line: 7, Off: true},
		{Name: "design", File: "mcp.laptop.json", Line: 2},
		{Name: "~/Code/gone:tracker", File: "mcp.laptop.json", Line: 24},
		{Name: "~/Code/site:mail", File: "mcp.laptop.json", Line: 10},
		{Name: "~/Code/site:pages", File: "mcp.laptop.json", Line: 18},
	}
	if err != nil || got.Kind != "mcp" || !slices.Equal(got.Entries, want) {
		t.Errorf("Declared() = %+v, %v\nwant %+v", got.Entries, err, want)
	}
}

func TestDeclaredRefuses(t *testing.T) {
	for name, tt := range map[string]struct{ file, want string }{
		"a key in plain text": {`{"design": {"type": "http", "url": "https://x.example.com", "headers": {"Authorization": "Bearer abc"}}}`, "mcp.laptop.json: design: headers.Authorization holds a key in plain text"},
		"no command or url":   {`{"odd": {"type": "stdio"}}`, "mcp.laptop.json: odd: a definition needs a command or a url"},
		"not JSON":            {`{"docs": `, "mcp.laptop.json: not JSON kit reads"},
		"a server in both":    {`{"docs": {"type": "http", "url": "https://docs.example.com/mcp"}}`, "mcp.laptop.json: docs is in mcp.json too"},
		"_enabled not a bool": {`{"x": {"_enabled": "no", "command": "x"}}`, "x: _enabled isn't true or false"},
	} {
		t.Run(name, func(t *testing.T) {
			home, dir := world(t)
			write(t, filepath.Join(dir, "mcp.laptop.json"), tt.file)
			if _, err := newMCP(t, runnertest.New(t), home, dir).Declared(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Declared() error = %v, want %q", err, tt.want)
			}
		})
	}
}

// What's installed otherwise than declared is changed, and says where,
// never what: the keys in plain text stay unsaid. A server declared off is
// neither missing nor extra; a project's whose folder isn't here waits.
func TestCompare(t *testing.T) {
	home, dir := world(t)
	m := newMCP(t, runnertest.New(t), home, dir)
	list, err := m.Declared()
	if err != nil {
		t.Fatal(err)
	}
	got := kind.Compare(t.Context(), m, list)
	want := []check.Item{
		{ID: "mcp:~/Code/gone:tracker", Name: "~/Code/gone:tracker", State: kind.Missing, Detail: "the folder isn't here yet"},
		{ID: "mcp:design", Name: "design", State: kind.Changed, Detail: "installed with a different headers", Action: kind.Install},
		{ID: "mcp:~/Code/site:mail", Name: "~/Code/site:mail", State: kind.Changed, Detail: "installed with a different env", Action: kind.Install},
		{ID: "mcp:stray", Name: "stray", State: kind.Extra},
		{ID: "mcp:~/Code/other:docs", Name: "~/Code/other:docs", State: kind.Extra},
	}
	slices.SortFunc(got.Items, func(a, b check.Item) int { return strings.Compare(a.State+a.ID, b.State+b.ID) })
	slices.SortFunc(want, func(a, b check.Item) int { return strings.Compare(a.State+a.ID, b.State+b.ID) })
	if got.Summary != "5 declared, 4 installed; 2 changed; 1 off" || !slices.Equal(got.Items, want) {
		t.Errorf("Compare() = %+v\nwant items %+v", got, want)
	}
	out, _ := json.Marshal(got)
	if strings.Contains(string(out), "made-up-key") {
		t.Errorf("the result holds a key: %s", out)
	}
}

func TestWithoutClaude(t *testing.T) {
	home, dir := world(t)
	fake := runnertest.New(t)
	m := mcp.New(fake, home, dir, "laptop")
	list, _ := m.Declared()
	if got := kind.Compare(t.Context(), m, list); got.State != check.Deferred || got.Reason != "needs claude, which isn't installed" {
		t.Errorf("Compare() = %+v, want it deferred", got)
	}
}

// Installing replaces a server installed otherwise, in its scope: a
// project's in local scope, run in its folder; the definition handed over
// names its keys, never holds them.
func TestInstallAndRemove(t *testing.T) {
	home, dir := world(t)
	site := filepath.Join(home, "Code", "site")
	fake := runnertest.New(t)
	fake.On("claude", "mcp", "remove", "-s", "user", "design")
	fake.On("claude", "mcp", "add-json", "-s", "user", "design", `{"headers":{"Authorization":"Bearer ${DESIGN_KEY}"},"type":"http","url":"https://design.example.com/mcp"}`)
	fake.On("claude", "mcp", "remove", "-s", "local", "mail")
	fake.On("claude", "mcp", "add-json", "-s", "local", "mail", `{"args":["-y","mail-mcp"],"command":"npx","env":{"MAIL_API_KEY":"${MAIL_API_KEY_SITE}"},"type":"stdio"}`)
	fake.On("claude", "mcp", "remove", "-s", "user", "stray")
	m := newMCP(t, fake, home, dir)
	if _, err := m.Declared(); err != nil {
		t.Fatal(err)
	}
	if err := m.Install(t.Context(), []string{"design", "~/Code/site:mail"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove(t.Context(), []string{"stray"}); err != nil {
		t.Fatal(err)
	}
	for _, c := range fake.Commands() {
		if wantDir := map[string]string{"local": site, "user": ""}[c.Args[min(3, len(c.Args)-1)]]; c.Name == "claude" && len(c.Args) > 3 && c.Dir != wantDir {
			t.Errorf("ran %s in %q, want %q", c, c.Dir, wantDir)
		}
	}
	if err := m.Install(t.Context(), []string{"nosuch"}); err == nil {
		t.Error("Install() of a server not declared = nil error")
	}
}
