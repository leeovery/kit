package cli_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var mcpLaptop = filepath.Join(".config", "kit", "mcp.laptop.json")

// mcpWorld is laptopWorld with Claude Code installed: it has docs, as
// declared; design, declared with its key named but installed with it in
// plain text; a stray server; and another project's server.
func mcpWorld(t *testing.T) *world {
	t.Helper()
	w := laptopWorld(t)
	w.write(t, mcpLaptop, `{
  "design": {
    "headers": {
      "Authorization": "Bearer ${DESIGN_KEY}"
    },
    "type": "http",
    "url": "https://design.example.com/mcp"
  },
  "docs": {
    "type": "http",
    "url": "https://docs.example.com/mcp"
  }
}
`)
	w.write(t, ".claude.json", `{"mcpServers": {
  "docs": {"type": "http", "url": "https://docs.example.com/mcp"},
  "design": {"type": "http", "url": "https://design.example.com/mcp", "headers": {"Authorization": "Bearer made-up-key"}},
  "stray": {"type": "stdio", "command": "stray-mcp", "args": [], "env": {}}
}, "projects": {"`+filepath.Join(w.home, "Code", "other")+`": {"mcpServers": {"tracker": {"type": "http", "url": "https://tracker.example.com/mcp"}}}}}`)
	w.fake.On("claude", "--version")
	return w
}

func TestStatusShowsMCPServers(t *testing.T) {
	out, _, _ := mcpWorld(t).run(t, "status", "mcp")
	// Changed is drift like any, quiet for its first day; applying acts on it all the same.
	for _, want := range []string{"mcp ok 2 declared, all installed; 1 changed\n", "mcp changed:new design (installed differently: headers) (to install)\n", "mcp extra:new stray, ~/Code/other:tracker\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("kit status mcp printed\n%s\nwant it to hold %q", out, want)
		}
	}
	if strings.Contains(out, "made-up-key") {
		t.Error("kit status printed a key")
	}
}

// Applying replaces a server installed otherwise than declared: what Claude
// Code is handed names the key, never holds it.
func TestApplyReplacesAChangedServer(t *testing.T) {
	w := mcpWorld(t)
	w.fake.On("claude", "mcp", "remove", "-s", "user", "design")
	w.fake.On("claude", "mcp", "add-json", "-s", "user", "design", `{"headers":{"Authorization":"Bearer ${DESIGN_KEY}"},"type":"http","url":"https://design.example.com/mcp"}`)
	if _, errOut, _ := w.run(t, "apply", "mcp"); errOut != "" {
		t.Errorf("kit apply printed %q", errOut)
	}
	calls := w.fake.Calls()
	if i, j := slices.Index(calls, "claude mcp remove -s user design"), slices.IndexFunc(calls, func(c string) bool { return strings.HasPrefix(c, "claude mcp add-json -s user design") }); i < 0 || j < i {
		t.Errorf("ran %q, want design removed, then added as declared", calls)
	}
}

func TestReconcileAdoptsAndRemovesMCPServers(t *testing.T) {
	w := mcpWorld(t)
	w.expectSync([]string{"mcp.laptop.json"}, "kit reconcile (laptop): adopt mcp:stray: a helper")
	out, errOut, code := w.run(t, "reconcile", "mcp:stray", "--adopt", "--note", "a helper")
	if code != 0 || !strings.Contains(out, "mcp:stray ok already installed; declared in mcp.laptop.json") {
		t.Fatalf("kit reconcile --adopt printed\n%s%s exit %d", out, errOut, code)
	}
	if got := w.read(t, mcpLaptop); !strings.Contains(got, "\"stray\": {\n    \"_note\": \"a helper\",") {
		t.Errorf("mcp.laptop.json =\n%s", got)
	}

	w.fake.On("claude", "mcp", "remove", "-s", "local", "tracker")
	out, errOut, code = w.run(t, "reconcile", "mcp:~/Code/other:tracker", "--remove")
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
	w.write(t, mcpLaptop, "{}\n")
	out, _, code := w.run(t, "add", "mcp", "design")
	if !strings.Contains(out, "design holds a key in plain text (headers.Authorization)") || code != 1 || strings.Contains(out, "made-up-key") {
		t.Errorf("kit add mcp design printed\n%s exit %d; want it refused, the key unsaid", out, code)
	}
	if got := w.read(t, mcpLaptop); got != "{}\n" {
		t.Errorf("mcp.laptop.json = %q, want it unchanged", got)
	}
}

// kit mcp off declares a server off and removes it; on declares it on and
// installs it; the file ends as it began.
func TestMCPOffAndOn(t *testing.T) {
	w := mcpWorld(t)
	before := w.read(t, mcpLaptop)
	w.fake.On("claude", "mcp", "remove", "-s", "user", "docs")
	w.expectSync([]string{"mcp.laptop.json"}, "kit mcp off docs (laptop)")
	out, errOut, code := w.run(t, "mcp", "off", "docs")
	if code != 0 || !strings.Contains(out, "docs ok declared off in mcp.laptop.json; removed from Claude Code\n") {
		t.Fatalf("kit mcp off printed\n%s%s exit %d", out, errOut, code)
	}
	if got := w.read(t, mcpLaptop); !strings.Contains(got, "\"docs\": {\n    \"_enabled\": false,") {
		t.Errorf("mcp.laptop.json =\n%s", got)
	}

	// Claude Code no longer has docs.
	w.write(t, ".claude.json", `{"mcpServers": {"design": {"type": "http", "url": "https://design.example.com/mcp", "headers": {"Authorization": "Bearer ${DESIGN_KEY}"}}}}`)
	w.fake.On("claude", "mcp", "add-json", "-s", "user", "docs", `{"type":"http","url":"https://docs.example.com/mcp"}`)
	w.expectSync([]string{"mcp.laptop.json"}, "kit mcp on docs (laptop)")
	out, errOut, code = w.run(t, "mcp", "on", "docs")
	if code != 0 || !strings.Contains(out, "docs ok declared on in mcp.laptop.json; installed\n") {
		t.Fatalf("kit mcp on printed\n%s%s exit %d", out, errOut, code)
	}
	if got := w.read(t, mcpLaptop); got != before {
		t.Errorf("mcp.laptop.json =\n%s\nwant it as it began\n%s", got, before)
	}

	_, _, code = w.run(t, "mcp", "on", "nosuch")
	if code != 1 {
		t.Errorf("kit mcp on nosuch exit %d, want 1", code)
	}
}
