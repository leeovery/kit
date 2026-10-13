package cli_test

import (
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/runner"
)

// On a new Mac, each kind waits for its own command, which steps of the
// config bring, not for the formulae: the secrets sync once the sign-in,
// raised once 1Password's casks are in, is done; npm's packages install
// once mise's step has put npm there; Claude Code's MCP servers once its
// step has installed claude; all while the rest of the formulae install.
func TestEachKindWaitsForItsCommand(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "homebrew formulae", "go\nmise\nripgrep\n")
	w.writeSection(t, "laptop", "homebrew casks", "1password\nowner/tap/1password-cli\n")
	w.writeSection(t, "laptop", itemSection, "TOKEN   Section/token\n")
	w.writeSection(t, "laptop", "npm packages", "intelephense\n")
	w.writeSection(t, "laptop", "claude mcp", "docs --transport http https://docs.example.com/mcp\n")
	w.writeSection(t, "laptop", "manual", `1password "Sign in to 1Password" --after cask:1password --after cask:owner/tap/1password-cli --before secret --test test -f /signed-in`+"\n")
	w.writeSection(t, "laptop", "steps", strings.Join([]string{
		`claude-code "Claude Code, from its installer"`,
		`mise "The tool versions mise names" --after brew:mise --before npm`,
	}, "\n")+"\n")
	f := w.fake
	formulae := "go\njq\noniguruma\nowner/tap/tool\nnode@20\nffmpeg\n"
	f.On("brew", "info", "--json=v2", "--formula", "mise", "ripgrep").Prints(`{"formulae": [{"name": "mise", "full_name": "mise"}, {"name": "ripgrep", "full_name": "ripgrep"}], "casks": []}`)
	f.On("brew", "info", "--json=v2", "--cask", "1password", "owner/tap/1password-cli").Prints(`{"formulae": [], "casks": [{"token": "1password", "full_token": "1password", "artifacts": [{"app": ["1Password.app"]}]}, {"token": "1password-cli", "full_token": "owner/tap/1password-cli", "artifacts": [{"binary": ["op"]}]}]}`)
	f.On("brew", "tap").Prints("owner/tap\n")
	f.On("brew", "update", "--quiet")
	f.On("sudo", "-n", "true").Exits(1)
	f.On("brew", "install", "--formula", "mise").Does(func() {
		formulae += "mise\n"
		f.On("brew", "list", "--formula", "--full-name", "-1").Prints(formulae)
	})
	synced, npm, mcp := "op read op://vault/Item/Section/token", "npm install --global intelephense", `claude mcp add-json -s user docs '{"type":"http","url":"https://docs.example.com/mcp"}'`
	// The rest of the formulae take a while, as a new Mac's do.
	var meanwhile atomic.Bool
	f.On("brew", "install", "--formula", "ripgrep").Does(func() {
		for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline) && !meanwhile.Load(); time.Sleep(time.Millisecond) {
			calls := f.Calls()
			meanwhile.Store(slices.Contains(calls, synced) && slices.Contains(calls, npm) && slices.Contains(calls, mcp))
		}
		formulae += "ripgrep\n"
		f.On("brew", "list", "--formula", "--full-name", "-1").Prints(formulae)
	})
	f.On("brew", "install", "--cask", "1password", "owner/tap/1password-cli").Does(func() {
		f.On("brew", "list", "--cask", "--full-name", "-1").Prints("ghostty\nfirefox\n1password\nowner/tap/1password-cli\n")
		f.On("op", "whoami")
		f.On("op", "read", "op://vault/Item/Section/token").Prints("plain-token-value\n")
	})
	f.On("test", "-f", "/signed-in")
	// A program is on kit's PATH once a command of it is scripted.
	mise := w.stepScript(t, "laptop", "mise")
	f.On(mise, "check").Exits(1)
	f.On(mise, "apply").Does(func() {
		f.On(mise, "check")
		f.On("npm", "ls", "--global", "--depth=0", "--json").Prints(`{"dependencies": {}}`)
		f.On("npm", "install", "--global", "intelephense")
	})
	claude := w.stepScript(t, "laptop", "claude-code")
	f.On(claude, "check").Exits(1)
	f.On(claude, "apply").Does(func() {
		f.On(claude, "check")
		f.On("claude", "mcp", "add-json", "-s", "user", "docs", `{"type":"http","url":"https://docs.example.com/mcp"}`)
	})

	out, errOut, _ := w.run(t, "apply")
	calls := f.Calls()
	if !before(calls, "brew install --cask 1password owner/tap/1password-cli", "test -f /signed-in") || !before(calls, "test -f /signed-in", synced) {
		t.Errorf("ran %q: want the secrets synced once 1Password's casks were in and the sign-in done", calls)
	}
	if !before(calls, mise+" apply", npm) {
		t.Errorf("ran %q: want npm's packages installed once mise's step had put npm there", calls)
	}
	if !before(calls, claude+" apply", mcp) {
		t.Errorf("ran %q: want the MCP server added once Claude Code's step had installed claude", calls)
	}
	if !meanwhile.Load() {
		t.Errorf("ran %q: want the secrets, npm's packages and the MCP server in while the rest of the formulae installed", calls)
	}
	for _, unwanted := range []string{"which isn't there yet", "which isn't installed"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("kit apply printed\n%s%s\nwant nothing waiting on a command that came", out, errOut)
		}
	}
}

// A command that never comes leaves its kind deferred, once nothing at
// work in the run could bring it, saying so; the nightly run and kit status
// wait for no command, saying it isn't installed.
func TestAKindWhoseCommandNeverComes(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "npm packages", "intelephense\n")
	w.fake.On("npm", "ls", "--global", "--depth=0", "--json").Fails(runner.ErrNotFound)
	for _, tt := range []struct {
		command, want string
	}{
		{"apply", "npm deferred needs npm, which isn't there yet\n"},
		{"nightly", "npm deferred needs npm, which isn't installed\n"},
		{"status", "npm deferred needs npm, which isn't installed\n"},
	} {
		if out, errOut, _ := w.run(t, tt.command); !strings.Contains(out, tt.want) {
			t.Errorf("kit %s printed\n%s%s\nwant it to hold %q", tt.command, out, errOut, tt.want)
		}
	}
}

// A kind whose config is wrong fails, saying so, at once: it waits for no
// command, which, never coming, would hide what's wrong.
func TestAWrongConfigWaitsForNoCommand(t *testing.T) {
	for _, tt := range []struct {
		name, kind, header, lines, want string
	}{
		{"a line that doesn't read", "claude-mcp", "claude mcp", "docs --transport http https://docs.example.com/mcp --header \"Authorization: Bearer abc\"\n", "claude-mcp failed laptop/declarations:5: docs: headers.Authorization holds a key in plain text"},
		{"a section needing a secret not declared", "npm", "npm packages --secret TOKEN", "intelephense\n", "npm failed laptop/declarations: [npm packages --secret TOKEN]: it needs TOKEN, which no [secrets] section declares"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := laptopWorld(t)
			w.writeSection(t, "laptop", tt.header, tt.lines)
			if out, errOut, _ := w.run(t, "apply", tt.kind); !strings.Contains(out, tt.want) {
				t.Errorf("kit apply %s printed\n%s%s\nwant it to hold %q", tt.kind, out, errOut, tt.want)
			}
		})
	}
}
