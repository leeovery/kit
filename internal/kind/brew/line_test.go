package brew_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/brew"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// A line's names are its arguments but its flags, and the value after a
// flag that takes one there; a file or an address in place of a name, or
// no name at all, is refused.
func TestALinesNames(t *testing.T) {
	names := brew.FormulaeDescriptor.Help.Line.Names
	tests := []struct {
		args    []string
		want    []string
		refused string
	}{
		{args: []string{"jq"}, want: []string{"jq"}},
		{args: []string{"--cask", "--force", "owner/tap/tool", "-v", "firefox"}, want: []string{"owner/tap/tool", "firefox"}},
		{args: []string{"--cask", "--appdir", "/Users/someone/Applications", "firefox"}, want: []string{"firefox"}},
		{args: []string{"--appdir=/Users/someone/Applications", "--cc", "clang", "--language", "en", "--bottle-arch", "arm64", "jq"}, want: []string{"jq"}},
		{args: []string{"./tool.rb"}, refused: "./tool.rb is a file or an address"},
		{args: []string{"jq", "/tmp/tool.rb"}, refused: "/tmp/tool.rb is a file or an address"},
		{args: []string{"../tool"}, refused: "../tool is a file or an address"},
		{args: []string{"~/tool"}, refused: "~/tool is a file or an address"},
		{args: []string{"tool.RB"}, refused: "tool.RB is a file or an address"},
		{args: []string{"https://example.com/tool.rb"}, refused: "https://example.com/tool.rb is a file or an address"},
		{args: []string{"--cask", "--force"}, refused: "the line names nothing for brew"},
		{args: nil, refused: "the line names nothing for brew"},
	}
	for _, tt := range tests {
		got, err := names(tt.args)
		if tt.refused != "" {
			if err == nil || !strings.HasPrefix(err.Error(), tt.refused) {
				t.Errorf("Names(%q) = %q, %v; want it refused: %s", tt.args, got, err, tt.refused)
			}
			continue
		}
		if err != nil || !slices.Equal(got, tt.want) {
			t.Errorf("Names(%q) = %q, %v; want %q", tt.args, got, err, tt.want)
		}
	}
}

// A line's things are casks with --cask, formulae with --formula, and
// either without, formulae first, as brew takes a name that's both.
func TestALinesKinds(t *testing.T) {
	kinds := brew.FormulaeDescriptor.Help.Line.Kinds
	for _, tt := range []struct {
		args, want []string
	}{
		{args: []string{"--cask", "firefox"}, want: []string{"cask"}},
		{args: []string{"firefox", "--casks"}, want: []string{"cask"}},
		{args: []string{"--formula", "jq"}, want: []string{"brew"}},
		{args: []string{"--formulae", "jq"}, want: []string{"brew"}},
		{args: []string{"--force", "jq"}, want: []string{"brew", "cask"}},
	} {
		if got := kinds(tt.args); !slices.Equal(got, tt.want) {
			t.Errorf("Kinds(%q) = %q, want %q", tt.args, got, tt.want)
		}
	}
}

// An install line runs as given after brew update, as kit runs brew
// without updating itself; an uninstall line without it. Each runs as
// kit's own installs do: Homebrew's git never asking, its sudo asking the
// run, watched for going quiet.
func TestALineRunsAsGiven(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("brew", "update", "--quiet")
	fake.On("brew", "install", "--cask", "--force", "owner/tap/tool")
	fake.On("brew", "uninstall", "--zap", "firefox")
	liner := brew.New(fake).Formulae().(kind.Liner)
	if err := liner.Line(context.Background(), "install", []string{"--cask", "--force", "owner/tap/tool"}); err != nil {
		t.Fatal(err)
	}
	if err := liner.Line(context.Background(), "uninstall", []string{"--zap", "firefox"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"brew update --quiet", "brew install --cask --force owner/tap/tool", "brew uninstall --zap firefox"}
	if got := fake.Calls(); !slices.Equal(got, want) {
		t.Errorf("ran %q, want %q", got, want)
	}
	for _, cmd := range fake.Commands()[1:] {
		sshConfig := slices.ContainsFunc(cmd.Env, func(kv string) bool { return strings.HasPrefix(kv, "HOMEBREW_SSH_CONFIG_PATH=") })
		if !sshConfig || !cmd.SudoAsks || cmd.Lines == nil {
			t.Errorf("%s ran with %q, its sudo asking %v, watched %v; want it run as kit's installs are", cmd, cmd.Env, cmd.SudoAsks, cmd.Lines != nil)
		}
	}
}

// A line that fails fails as brew said.
func TestALineThatFails(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("brew", "update", "--quiet")
	fake.On("brew", "install", "nosuch").Exits(1).PrintsToStderr(`Error: No available formula with the name "nosuch".`)
	err := brew.New(fake).Formulae().(kind.Liner).Line(context.Background(), "install", []string{"nosuch"})
	if err == nil || !strings.Contains(err.Error(), `No available formula with the name "nosuch"`) {
		t.Errorf("Line = %v, want brew's error", err)
	}
}
