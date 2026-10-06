package cli_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// mcpLines are laptop's MCP servers: design, its key named, and docs.
const mcpLines = `design --transport http https://design.example.com/mcp --header "Authorization: Bearer ${DESIGN_KEY}"
docs --transport http https://docs.example.com/mcp
`

// mcpWorld is laptopWorld with Claude Code installed: it has docs, as
// declared; design, declared with its key named but installed with it in
// plain text; a stray server; and another project's server.
func mcpWorld(t *testing.T) *world {
	t.Helper()
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "claude mcp", mcpLines)
	w.write(t, ".claude.json", `{"mcpServers": {
  "docs": {"type": "http", "url": "https://docs.example.com/mcp"},
  "design": {"type": "http", "url": "https://design.example.com/mcp", "headers": {"Authorization": "Bearer made-up-key"}},
  "stray": {"type": "stdio", "command": "stray-mcp", "args": [], "env": {}}
}, "projects": {"`+filepath.Join(w.home, "Code", "other")+`": {"mcpServers": {"tracker": {"type": "http", "url": "https://tracker.example.com/mcp"}}}}}`)
	w.fake.On("claude", "--version")
	return w
}

func TestStatusShowsMCPServers(t *testing.T) {
	out, _, _ := mcpWorld(t).run(t, "status", "claude-mcp")
	// Changed is drift like any, quiet for its first day; applying acts on it all the same.
	for _, want := range []string{"claude-mcp ok 2 declared, all installed; 1 changed\n", "claude-mcp changed:new design (installed differently: headers) (to install)\n", "claude-mcp extra:new stray, ~/Code/other:tracker\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("kit status claude-mcp printed\n%s\nwant it to hold %q", out, want)
		}
	}
	if strings.Contains(out, "made-up-key") {
		t.Error("kit status printed a key")
	}
}

// A line holding a key in plain text fails the step, saying where.
func TestStatusRefusesAKeyInPlainText(t *testing.T) {
	w := mcpWorld(t)
	w.writeSection(t, "laptop", "claude mcp", "docs --transport http https://docs.example.com/mcp --header \"Authorization: Bearer abc\"\n")
	out, _, code := w.run(t, "status", "claude-mcp")
	if !strings.Contains(out, "claude-mcp failed laptop/declarations:5: docs: headers.Authorization holds a key in plain text") || code != 1 {
		t.Errorf("kit status printed\n%s exit %d", out, code)
	}
}

// Applying replaces a server installed otherwise than declared: what Claude
// Code is handed names the key, never holds it.
func TestApplyReplacesAChangedServer(t *testing.T) {
	w := mcpWorld(t)
	w.fake.On("claude", "mcp", "remove", "-s", "user", "design")
	w.fake.On("claude", "mcp", "add-json", "-s", "user", "design", `{"headers":{"Authorization":"Bearer ${DESIGN_KEY}"},"type":"http","url":"https://design.example.com/mcp"}`)
	if _, errOut, _ := w.run(t, "apply", "claude-mcp"); errOut != "" {
		t.Errorf("kit apply printed %q", errOut)
	}
	calls := w.fake.Calls()
	if i, j := slices.Index(calls, "claude mcp remove -s user design"), slices.IndexFunc(calls, func(c string) bool { return strings.HasPrefix(c, "claude mcp add-json -s user design") }); i < 0 || j < i {
		t.Errorf("ran %q, want design removed, then added as declared", calls)
	}
}

func TestReconcileAdoptsAndRemovesMCPServers(t *testing.T) {
	w := mcpWorld(t)
	w.expectSync([]string{"laptop/declarations"}, "kit reconcile (laptop): adopt claude-mcp:stray: a helper")
	out, errOut, code := w.run(t, "reconcile", "claude-mcp:stray", "--adopt", "--note", "a helper")
	if code != 0 || !strings.Contains(out, "claude-mcp:stray ok already installed; declared in laptop\n") {
		t.Fatalf("kit reconcile --adopt printed\n%s%s exit %d", out, errOut, code)
	}
	if got := w.readSection(t, "laptop", "claude mcp"); got != mcpLines+"stray -- stray-mcp   # a helper\n" {
		t.Errorf("[claude mcp] =\n%s", got)
	}

	w.fake.On("claude", "mcp", "remove", "-s", "local", "tracker")
	out, errOut, code = w.run(t, "reconcile", "claude-mcp:~/Code/other:tracker", "--remove")
	if code != 0 || !strings.Contains(out, "uninstalled; it wasn't declared") {
		t.Fatalf("kit reconcile --remove printed\n%s%s exit %d", out, errOut, code)
	}
	for _, c := range w.fake.Commands() {
		if strings.Join(c.Args, " ") == "mcp remove -s local tracker" && c.Dir != filepath.Join(w.home, "Code", "other") {
			t.Errorf("removed tracker in %q, want its project's folder", c.Dir)
		}
	}
}

func TestAddingAnMCPServerWithAKeyInPlainTextIsRefused(t *testing.T) {
	w := mcpWorld(t)
	w.writeSection(t, "laptop", "claude mcp", "docs --transport http https://docs.example.com/mcp\n")
	out, _, code := w.run(t, "claude-mcp", "add", "design")
	if !strings.Contains(out, "design holds a key in plain text (headers.Authorization)") || code != 1 || strings.Contains(out, "made-up-key") {
		t.Errorf("kit claude-mcp add design printed\n%s exit %d; want it refused, the key unsaid", out, code)
	}
	if got := w.readSection(t, "laptop", "claude mcp"); got != "docs --transport http https://docs.example.com/mcp\n" {
		t.Errorf("[claude mcp] = %q, want it unchanged", got)
	}
}

// kit claude-mcp off declares a server off and removes it; on declares it
// on and installs it; the file ends as it began.
func TestMCPOffAndOn(t *testing.T) {
	w := mcpWorld(t)
	before := w.read(t, filepath.Join(".config", "kit", "laptop", "declarations"))
	w.fake.On("claude", "mcp", "remove", "-s", "user", "docs")
	w.expectSync([]string{"laptop/declarations"}, "kit claude-mcp off docs (laptop)")
	out, errOut, code := w.run(t, "claude-mcp", "off", "docs")
	if code != 0 || !strings.Contains(out, "docs ok declared off in laptop; removed from Claude Code\n") {
		t.Fatalf("kit claude-mcp off printed\n%s%s exit %d", out, errOut, code)
	}
	if got := w.readSection(t, "laptop", "claude mcp"); !strings.Contains(got, "docs --off --transport http https://docs.example.com/mcp\n") {
		t.Errorf("[claude mcp] =\n%s", got)
	}

	// Claude Code no longer has docs.
	w.write(t, ".claude.json", `{"mcpServers": {"design": {"type": "http", "url": "https://design.example.com/mcp", "headers": {"Authorization": "Bearer ${DESIGN_KEY}"}}}}`)
	w.fake.On("claude", "mcp", "add-json", "-s", "user", "docs", `{"type":"http","url":"https://docs.example.com/mcp"}`)
	w.expectSync([]string{"laptop/declarations"}, "kit claude-mcp on docs (laptop)")
	out, errOut, code = w.run(t, "claude-mcp", "on", "docs")
	if code != 0 || !strings.Contains(out, "docs ok declared on in laptop; installed\n") {
		t.Fatalf("kit claude-mcp on printed\n%s%s exit %d", out, errOut, code)
	}
	if got := w.read(t, filepath.Join(".config", "kit", "laptop", "declarations")); got != before {
		t.Errorf("laptop =\n%s\nwant it as it began\n%s", got, before)
	}

	_, _, code = w.run(t, "claude-mcp", "on", "nosuch")
	if code != 1 {
		t.Errorf("kit claude-mcp on nosuch exit %d, want 1", code)
	}
}

// A server declared off lists as off, not as missing or installed.
func TestListShowsAServerDeclaredOff(t *testing.T) {
	w := mcpWorld(t)
	w.writeSection(t, "laptop", "claude mcp", mcpLines+"tablet --off -- zsh -c tablet-mcp\n")
	out, _, _ := w.run(t, "list", "claude-mcp")
	if !strings.Contains(out, "claude-mcp  tablet  laptop  (off)\n") {
		t.Errorf("kit list printed\n%s\nwant tablet off", out)
	}
	out, _, _ = w.run(t, "list", "claude-mcp", "--json")
	if !strings.Contains(out, `"name": "tablet",`) || !strings.Contains(out, `"installed": false,`) || !strings.Contains(out, `"off": true`) {
		t.Errorf("kit list --json printed\n%s\nwant tablet off and not installed", out)
	}
}
