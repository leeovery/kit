package brew_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/engine"
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
	if err := liner.Line(context.Background(), kind.CopiedLine{Verb: []string{"install"}, Args: []string{"--cask", "--force", "owner/tap/tool"}, Installs: true, New: map[string][]string{"cask": {"owner/tap/tool"}}}); err != nil {
		t.Fatal(err)
	}
	if err := liner.Line(context.Background(), kind.CopiedLine{Verb: []string{"uninstall"}, Args: []string{"--zap", "firefox"}}); err != nil {
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
	err := brew.New(fake).Formulae().(kind.Liner).Line(context.Background(), kind.CopiedLine{Verb: []string{"install"}, Args: []string{"nosuch"}, Installs: true})
	if err == nil || !strings.Contains(err.Error(), `No available formula with the name "nosuch"`) {
		t.Errorf("Line = %v, want brew's error", err)
	}
}

// A verb of more than one word goes to the program as words of its own.
func TestALinesVerbIsItsWords(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("brew", "extension", "remove", "owner/tool")
	if err := brew.New(fake).Formulae().(kind.Liner).Line(t.Context(), kind.CopiedLine{Verb: []string{"extension", "remove"}, Args: []string{"owner/tool"}}); err != nil {
		t.Fatal(err)
	}
	if got := fake.Commands()[0].Args; !slices.Equal(got, []string{"extension", "remove", "owner/tool"}) {
		t.Errorf("ran brew with %q, want each word apart", got)
	}
}

// A line of casks the catalogue vets gets --adopt, as kit's own cask
// installs do, its apps already there taken over; not one that says
// --adopt or --force itself, nor one with a cask kit can't vet, a tap's,
// which runs as given.
func TestALineOfCasksAdopts(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		new  []string
		want string
	}{
		{"vetted", []string{"--cask", "google-chrome", "jq"}, []string{"google-chrome"}, "brew install --adopt --cask google-chrome jq"},
		{"--force given", []string{"--cask", "--force", "google-chrome"}, []string{"google-chrome"}, "brew install --cask --force google-chrome"},
		{"--adopt given", []string{"--adopt", "google-chrome"}, []string{"google-chrome"}, "brew install --adopt google-chrome"},
		{"a tap's among them", []string{"google-chrome", "owner/tap/tool"}, []string{"google-chrome", "owner/tap/tool"}, "brew install google-chrome owner/tap/tool"},
		{"installed already", []string{"google-chrome"}, nil, "brew install google-chrome"},
		{"saying more", []string{"--verbose", "-q", "--casks", "google-chrome"}, []string{"google-chrome"}, "brew install --adopt --verbose -q --casks google-chrome"},
		{"elsewhere", []string{"--appdir=/Users/someone/Applications", "google-chrome"}, []string{"google-chrome"}, "brew install --appdir=/Users/someone/Applications google-chrome"},
		{"otherwise", []string{"--no-quarantine", "google-chrome"}, []string{"google-chrome"}, "brew install --no-quarantine google-chrome"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake, _, _ := adopting(t)
			fake.On("brew", strings.Fields(tt.want)[1:]...)
			line := kind.CopiedLine{Verb: []string{"install"}, Args: tt.args, Installs: true, New: map[string][]string{"cask": tt.new}}
			if err := brew.Of(&kind.Deps{Run: fake, Home: "/Users/someone", AppDirs: []string{t.TempDir()}}).Formulae().(kind.Liner).Line(t.Context(), line); err != nil {
				t.Fatal(err)
			}
			if got := installs(fake); !slices.Equal(got, []string{tt.want}) {
				t.Errorf("ran %q, want %q", got, tt.want)
			}
		})
	}
}

// Adopting, a cask whose app there differs from its download is replaced
// as kit cask add replaces it, when it's an older copy of its major
// version; one whose app there isn't its own refuses the line, nothing
// run.
func TestALineOfCasksReplacesOrRefuses(t *testing.T) {
	fake, apps, _ := adopting(t)
	h := withApps(fake, apps, t.TempDir())
	installVersion(t, apps, "Example Editor.app", "com.example.editor", "3.1")
	fake.On("brew", "install", "--adopt", "--cask", "example-editor").Exits(1).PrintsToStderr(notAdopted)
	fake.On("brew", "install", "--cask", "--force", "example-editor")
	fake.On("brew", "list", "--cask", "--full-name", "-1").Prints("example-editor\n")
	line := kind.CopiedLine{Verb: []string{"install"}, Args: []string{"--cask", "example-editor"}, Installs: true, New: map[string][]string{"cask": {"example-editor"}}}
	if err := h.Formulae().(kind.Liner).Line(t.Context(), line); err != nil {
		t.Errorf("Line = %v, want example-editor replaced", err)
	}
	if got, want := installs(fake), []string{"brew install --adopt --cask example-editor", "brew install --cask --force example-editor"}; !slices.Equal(got, want) {
		t.Errorf("ran %q, want %q", got, want)
	}

	fake, apps, _ = adopting(t)
	installApp(t, apps, "Spaceman.app", "dev.fork.Spaceman")
	line = kind.CopiedLine{Verb: []string{"install"}, Args: []string{"spaceman"}, Installs: true, New: map[string][]string{"cask": {"spaceman"}}}
	liner := withApps(fake, apps, t.TempDir()).Formulae().(kind.Liner)
	refused := "Spaceman.app is already in " + apps + " and isn't the cask's (dev.fork.Spaceman)"
	if err := liner.Refuses(t.Context(), line); err == nil || err.Error() != refused {
		t.Errorf("Refuses = %v, want %q", err, refused)
	}
	err := liner.Line(t.Context(), line)
	if err == nil || err.Error() != refused || len(installs(fake)) != 0 {
		t.Errorf("Line = %v, running %q; want it refused, nothing installed", err, installs(fake))
	}
	line.Args = []string{"--appdir=/Users/someone/Applications", "spaceman"}
	if err := liner.Refuses(t.Context(), line); err != nil {
		t.Errorf("Refuses = %v, want a line kit doesn't adopt run as given", err)
	}
}

// A replace never hides another failure of the line's: a formula's stays
// brew's error.
func TestALinesReplaceKeepsAFormulasFailure(t *testing.T) {
	fake, apps, _ := adopting(t)
	h := withApps(fake, apps, t.TempDir())
	installVersion(t, apps, "Example Editor.app", "com.example.editor", "3.1")
	fake.On("brew", "install", "--adopt", "example-editor", "hello").Exits(1).PrintsToStderr("Error: hello: Download failed\n" + notAdopted)
	fake.On("brew", "install", "--cask", "--force", "example-editor")
	fake.On("brew", "list", "--cask", "--full-name", "-1").Prints("example-editor\n")
	line := kind.CopiedLine{Verb: []string{"install"}, Args: []string{"example-editor", "hello"}, Installs: true, New: map[string][]string{"cask": {"example-editor"}, "brew": {"hello"}}}
	err := h.Formulae().(kind.Liner).Line(t.Context(), line)
	if err == nil || engine.Reason(err) != "Error: hello: Download failed" {
		t.Errorf("Line = %v, want hello's failure alone", err)
	}
}

// A dry run runs without brew update, and adopts nothing.
func TestALineDryRun(t *testing.T) {
	fake, _, _ := adopting(t)
	fake.On("brew", "install", "-n", "google-chrome")
	line := kind.CopiedLine{Verb: []string{"install"}, Args: []string{"-n", "google-chrome"}, Installs: true, DryRun: true, New: map[string][]string{"cask": {"google-chrome"}}}
	if err := withApps(fake, t.TempDir()).Formulae().(kind.Liner).Line(t.Context(), line); err != nil {
		t.Fatal(err)
	}
	if got := fake.Calls(); !slices.Equal(got, []string{"brew install -n google-chrome"}) {
		t.Errorf("ran %q, want the line alone", got)
	}
}
