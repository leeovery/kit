package mcp_test

import (
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/kind/mcp"
)

// A line's options read back to what claude mcp add-json takes; written
// back, they're the same line, whichever way they were first written.
func TestSetOffRoundTrips(t *testing.T) {
	for line, offLine := range map[string]string{
		"--transport http https://docs.example.com/mcp":                                                       "--off --transport http https://docs.example.com/mcp",
		`--transport sse https://x.example.com/sse --header "Authorization: Bearer ${KEY}" --header "X-A: 1"`: `--off --transport sse https://x.example.com/sse --header "Authorization: Bearer ${KEY}" --header "X-A: 1"`,
		"--env B=2 --env A=${A} -- npx -y mail-mcp":                                                           "--off --env A=${A} --env B=2 -- npx -y mail-mcp",
		`-- zsh -c "cd ~/x && run"`:                                                                           `--off -- zsh -c "cd ~/x && run"`,
		`{"type":"http","url":"https://o.example.com/mcp","oauth":{"clientId":"abc"}}`:                        `--off {"oauth":{"clientId":"abc"},"type":"http","url":"https://o.example.com/mcp"}`,
	} {
		got, err := mcp.SetOff(line, true)
		if err != nil || got != offLine {
			t.Errorf("SetOff(%q, true) = %q, %v\nwant %q", line, got, err, offLine)
			continue
		}
		back, err := mcp.SetOff(got, false)
		if err != nil || back != strings.TrimPrefix(offLine, "--off ") {
			t.Errorf("SetOff(%q, false) = %q, %v", got, back, err)
		}
	}
}

func TestShortOptionsRead(t *testing.T) {
	got, err := mcp.SetOff(`-t http https://x.example.com -H "X-A: 1"`, false)
	if err != nil || got != `--transport http https://x.example.com --header "X-A: 1"` {
		t.Errorf("SetOff() = %q, %v", got, err)
	}
	got, err = mcp.SetOff(`-e K=v -- run`, false)
	if err != nil || got != "--env K=v -- run" {
		t.Errorf("SetOff() = %q, %v", got, err)
	}
}

func TestLinesRefused(t *testing.T) {
	for line, want := range map[string]string{
		"https://a.example.com https://b.example.com": "one URL, or a command after --",
		"--env K=v https://x.example.com":             "variables go to a command's server",
		`--header "X: 1" -- run`:                      "headers go to a URL's server",
		"--transport http -- run":                     "a command runs over stdio, not http",
		"--":                                          "-- needs the command after it",
		"--header":                                    "--header needs a value after it",
		`--header "no colon"`:                         `the header "no colon" isn't a name, a colon and a value`,
	} {
		if _, err := mcp.SetOff(line, false); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("SetOff(%q) error = %v, want %q", line, err, want)
		}
	}
}
