package devtool_test

import (
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/devtool"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// A line's names are its packages or extensions, as kit-config declares
// them, its flags and their values left out: anything else in place of
// one is refused, as is an npm line that isn't global.
func TestLineNames(t *testing.T) {
	for _, tt := range []struct {
		d    kind.Descriptor
		args []string
		want []string
	}{
		{devtool.NPMDescriptor, []string{"-g", "typescript"}, []string{"typescript"}},
		{devtool.NPMDescriptor, []string{"--global", "typescript@5", "@scope/tool", "@scope/tool@^2.1"}, []string{"typescript@5", "@scope/tool", "@scope/tool@^2.1"}},
		{devtool.NPMDescriptor, []string{"--location=global", "--registry", "https://registry.example.com", "tool"}, []string{"tool"}},
		{devtool.NPMDescriptor, []string{"--location", "global", "tool"}, []string{"tool"}},
		{devtool.NPMDescriptor, []string{"--location", "user", "tool"}, nil},
		{devtool.NPMDescriptor, []string{"typescript"}, nil},
		{devtool.NPMDescriptor, []string{"-g"}, nil},
		{devtool.NPMDescriptor, []string{"-g", "owner/repo"}, nil},
		{devtool.NPMDescriptor, []string{"-g", "github:owner/repo"}, nil},
		{devtool.NPMDescriptor, []string{"-g", "git+ssh://git@example.com/tool.git"}, nil},
		{devtool.NPMDescriptor, []string{"-g", "https://example.com/tool.tgz"}, nil},
		{devtool.NPMDescriptor, []string{"-g", "tool.tgz"}, nil},
		{devtool.NPMDescriptor, []string{"-g", "../tool"}, nil},
		{devtool.NPMDescriptor, []string{"-g", "file:../tool"}, nil},
		{devtool.NPMDescriptor, []string{"-g", "tool@npm:other"}, nil},
		{devtool.ComposerDescriptor, []string{"laravel/valet"}, []string{"laravel/valet"}},
		{devtool.ComposerDescriptor, []string{"laravel/valet:^4.0", "vendor/a=1.*", "vendor/b ^2", "vendor/c", "~3.1", "vendor/d"}, []string{"laravel/valet:^4.0", "vendor/a:1.*", "vendor/b:^2", "vendor/c:~3.1", "vendor/d"}},
		{devtool.ComposerDescriptor, []string{"--prefer-install", "dist", "--dev", "vendor/a"}, []string{"vendor/a"}},
		{devtool.ComposerDescriptor, []string{"vendor/a:^1", "^2"}, nil},
		{devtool.ComposerDescriptor, []string{"valet"}, nil},
		{devtool.ComposerDescriptor, []string{"./vendor/a"}, nil},
		{devtool.ComposerDescriptor, []string{"--dev"}, nil},
		{devtool.GHDescriptor, []string{"owner/gh-tool", "--pin", "v1.0.0", "--force"}, []string{"owner/gh-tool"}},
		{devtool.GHDescriptor, []string{"tool"}, []string{"tool"}},
		{devtool.GHDescriptor, []string{"."}, nil},
		{devtool.GHDescriptor, []string{"https://github.com/owner/gh-tool"}, nil},
		{devtool.GHDescriptor, []string{"example.com/owner/gh-tool"}, nil},
		{devtool.GHDescriptor, []string{"--pin", "v1"}, nil},
	} {
		got, err := tt.d.Help.Line.Names(tt.args)
		if !slices.Equal(got, tt.want) || (err == nil) != (tt.want != nil) {
			t.Errorf("%s's Names(%q) = %q, %v; want %q", tt.d.Name, tt.args, got, err, tt.want)
		}
	}
}

// gh takes an extension by its command's name, with gh- or without, which
// resolves to the repository of the one installed that has it; a
// repository, or a command no extension installed has, is its own. npm's
// and Composer's resolve to themselves, asking nothing.
func TestResolveAnExtensionsCommand(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("gh", "extension", "list").Prints("gh dash\tdlvhdr/gh-dash\tv4.0.0\n")
	got, err := gh(fake).(kind.Resolver).Resolve(t.Context(), []string{"dash", "gh-dash", "owner/gh-tool", "nosuch"})
	want := map[string]string{"dash": "dlvhdr/gh-dash", "gh-dash": "dlvhdr/gh-dash", "owner/gh-tool": "owner/gh-tool", "nosuch": "nosuch"}
	if err != nil || !maps.Equal(got, want) {
		t.Errorf("Resolve = %v, %v; want %v", got, err, want)
	}
	if calls := fake.Calls(); len(calls) != 1 {
		t.Errorf("ran %q, want gh's list once", calls)
	}

	fake = runnertest.New(t)
	got, err = npm(fake).(kind.Resolver).Resolve(t.Context(), []string{"tool", "@scope/tool@2"})
	if want := map[string]string{"tool": "tool", "@scope/tool@2": "@scope/tool@2"}; err != nil || !maps.Equal(got, want) || len(fake.Calls()) != 0 {
		t.Errorf("npm's Resolve = %v, %v, running %q; want each its own, nothing run", got, err, fake.Calls())
	}
}

// gh installs an extension by its repository: a line giving one by its
// command alone is refused. npm's and Composer's refuse nothing.
func TestRefusesAnExtensionByItsCommand(t *testing.T) {
	line := func(args ...string) kind.CopiedLine {
		return kind.CopiedLine{Verb: []string{"extension", "install"}, Args: args, Installs: true}
	}
	fake := runnertest.New(t)
	if err := gh(fake).(kind.Liner).Refuses(t.Context(), line("owner/gh-tool")); err != nil {
		t.Errorf("Refuses(owner/gh-tool) = %v, want nil", err)
	}
	want := "gh-dash isn't a repository: give gh one, as owner/gh-dash"
	if err := gh(fake).(kind.Liner).Refuses(t.Context(), line("gh-dash")); err == nil || err.Error() != want {
		t.Errorf("Refuses(gh-dash) = %v, want %q", err, want)
	}
	if err := npm(fake).(kind.Liner).Refuses(t.Context(), kind.CopiedLine{Verb: []string{"install"}, Args: []string{"-g", "tool"}, Installs: true}); err != nil {
		t.Errorf("npm's Refuses = %v, want nil", err)
	}
}

// A line runs as given, its verb as typed, from where the program's own
// commands run, given the time its own install has, for each name when it
// installs a name a command.
func TestALineRunsAsGiven(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("npm", "i", "-g", "typescript", "docx")
	fake.On("gh", "ext", "install", "owner/gh-tool", "--pin", "v1")
	fake.On("composer", "g", "rm", "vendor/a")
	lines := []struct {
		k kind.Kind
		l kind.CopiedLine
	}{
		{npm(fake), kind.CopiedLine{Verb: []string{"i"}, Args: []string{"-g", "typescript", "docx"}, Installs: true}},
		{gh(fake), kind.CopiedLine{Verb: []string{"ext", "install"}, Args: []string{"owner/gh-tool", "--pin", "v1"}, Installs: true}},
		{composer(fake), kind.CopiedLine{Verb: []string{"g", "rm"}, Args: []string{"vendor/a"}}},
	}
	for _, tt := range lines {
		if err := tt.k.(kind.Liner).Line(t.Context(), tt.l); err != nil {
			t.Errorf("%s's Line = %v", tt.k.Name(), err)
		}
	}
	cmds := fake.Commands()
	if len(cmds) != 3 || cmds[0].Dir != "/home" || cmds[0].Timeout != 10*time.Minute || cmds[1].Timeout != 5*time.Minute || cmds[2].Timeout != 10*time.Minute {
		t.Errorf("ran %+v; want npm from the home folder, each given its own install's or uninstall's time", cmds)
	}
}
