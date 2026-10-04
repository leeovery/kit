package mcp_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/kind/mcp"
)

// The declarations file as kit writes it: keys sorted, two spaces' indent,
// a server's own keys first, a project's servers in its folder's object.
func TestDeclareWritesTheFileAsKitDoes(t *testing.T) {
	dir := t.TempDir()
	declare := func(s mcp.Server) {
		t.Helper()
		if err := mcp.Declare(dir, "mcp.laptop.json", s); err != nil {
			t.Fatal(err)
		}
	}
	declare(mcp.Server{Name: "pages", Definition: map[string]any{"type": "http", "url": "https://pages.example.com/mcp?a=1&b=2"}, Note: "the pages server"})
	declare(mcp.Server{Folder: "~/Code/site", Name: "mail", Definition: map[string]any{"command": "npx", "args": []any{"-y", "mail-mcp"}, "env": map[string]any{"MAIL_API_KEY": "${MAIL_KEY}"}}})
	declare(mcp.Server{Name: "docs", Definition: map[string]any{"type": "http", "url": "https://docs.example.com/mcp"}})
	want := `{
  "docs": {
    "type": "http",
    "url": "https://docs.example.com/mcp"
  },
  "pages": {
    "_note": "the pages server",
    "type": "http",
    "url": "https://pages.example.com/mcp?a=1&b=2"
  },
  "~/Code/site": {
    "mail": {
      "args": [
        "-y",
        "mail-mcp"
      ],
      "command": "npx",
      "env": {
        "MAIL_API_KEY": "${MAIL_KEY}"
      }
    }
  }
}
`
	if got := read(t, filepath.Join(dir, "mcp.laptop.json")); got != want {
		t.Errorf("mcp.laptop.json =\n%s\nwant\n%s", got, want)
	}

	if err := mcp.SetOff(dir, "mcp.laptop.json", "docs", true); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dir, "mcp.laptop.json")); !strings.Contains(got, "  \"docs\": {\n    \"_enabled\": false,\n") {
		t.Errorf("docs not off:\n%s", got)
	}
	if err := mcp.SetOff(dir, "mcp.laptop.json", "docs", false); err != nil {
		t.Fatal(err)
	}
	if err := mcp.Undeclare(dir, "mcp.laptop.json", "~/Code/site:mail"); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dir, "mcp.laptop.json")); strings.Contains(got, "~/Code/site") || strings.Contains(got, "_enabled") {
		t.Errorf("after turning docs on and undeclaring mail:\n%s", got)
	}
	if err := mcp.Undeclare(dir, "mcp.laptop.json", "nosuch"); err == nil || err.Error() != "mcp.laptop.json doesn't declare nosuch" {
		t.Errorf("Undeclare(nosuch) = %v", err)
	}
}

// A declaration holding a key in plain text is never written.
func TestDeclareRefusesAKeyInPlainText(t *testing.T) {
	dir := t.TempDir()
	err := mcp.Declare(dir, "mcp.json", mcp.Server{Name: "mail", Definition: map[string]any{"command": "npx", "env": map[string]any{"MAIL_API_KEY": "made-up"}}})
	if err == nil || !strings.Contains(err.Error(), "env.MAIL_API_KEY holds a key in plain text") {
		t.Errorf("Declare() = %v, want it refused", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "mcp.json")); !os.IsNotExist(statErr) {
		t.Errorf("mcp.json written: %v", statErr)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
