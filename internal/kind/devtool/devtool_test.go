package devtool_test

import (
	"slices"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/devtool"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// npm, composer and gh are each kind, driven through run; npm run from
// /home.
func npm(run runner.Runner) kind.Kind {
	return devtool.NPMDescriptor.New(&kind.Deps{Run: run, Home: "/home"})
}

func composer(run runner.Runner) kind.Kind {
	return devtool.ComposerDescriptor.New(&kind.Deps{Run: run})
}

func gh(run runner.Runner) kind.Kind { return devtool.GHDescriptor.New(&kind.Deps{Run: run}) }

func declared(kindName string, names ...string) config.List {
	l := config.List{Kind: kindName}
	for _, n := range names {
		l.Entries = append(l.Entries, config.Entry{Name: n})
	}
	return l
}

const npmListing = `{
  "name": "lib",
  "dependencies": {
    "intelephense": {"version": "1.18.4", "overridden": false},
    "@scope/tool": {"version": "1.2.3", "overridden": false},
    "docx": {"version": "9.6.0", "overridden": false}
  }
}`

func TestNPMInstalled(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("npm", "ls", "--global", "--depth=0", "--json").Prints(npmListing)
	got, err := npm(fake).Installed(t.Context())
	want := []kind.Installed{{Name: "@scope/tool", Explicit: true}, {Name: "docx", Explicit: true}, {Name: "intelephense", Explicit: true}}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("Installed() = %+v, %v; want %+v", got, err, want)
	}
	if dir := fake.Commands()[0].Dir; dir != "/home" {
		t.Errorf("ran npm in %q, want the home: the default Node, never a project's", dir)
	}
}

// npm ls exits 1 over a problem it finds, yet lists what's there.
func TestNPMInstalledDespiteAProblem(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("npm", "ls", "--global", "--depth=0", "--json").Prints(npmListing).Exits(1).PrintsToStderr("npm error missing: peer@1")
	if got, err := npm(fake).Installed(t.Context()); err != nil || len(got) != 3 {
		t.Errorf("Installed() = %+v, %v; want the three packages", got, err)
	}
	fake.On("npm", "ls", "--global", "--depth=0", "--json").Exits(1).PrintsToStderr("npm error broken")
	if _, err := npm(fake).Installed(t.Context()); err == nil {
		t.Error("Installed() with nothing listed = nil error, want npm's")
	}
}

// A package declared with a version matches on its name.
func TestNPMCompare(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("npm", "ls", "--global", "--depth=0", "--json").Prints(npmListing)
	got := kind.Compare(t.Context(), npm(fake), declared("npm", "intelephense@1", "@scope/tool@1.2.3", "typescript@5"))
	want := []check.Item{
		{ID: "npm:typescript@5", Name: "typescript@5", State: kind.Missing, Action: kind.Install},
		{ID: "npm:docx", Name: "docx", State: kind.Extra},
	}
	if got.Summary != "3 declared, 2 installed" || !slices.Equal(got.Items, want) {
		t.Errorf("Compare() = %+v\nwant items %+v", got, want)
	}
}

func TestNPMInstallAndRemove(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("npm", "install", "--global", "typescript@5", "@scope/tool@1.2.3")
	fake.On("npm", "uninstall", "--global", "typescript", "@scope/tool")
	n := npm(fake)
	if err := n.Install(t.Context(), []string{"typescript@5", "@scope/tool@1.2.3"}); err != nil {
		t.Fatal(err)
	}
	if err := n.Remove(t.Context(), []string{"typescript@5", "@scope/tool@1.2.3"}); err != nil {
		t.Fatal(err)
	}
}

const composerListing = `{
    "installed": [
        {"name": "beyondcode/expose", "direct-dependency": true, "version": "2.6.2"},
        {"name": "laravel/installer", "direct-dependency": true, "version": "v5.31.1"},
        {"name": "laravel/valet", "direct-dependency": true, "version": "v4.12.0"}
    ]
}`

func TestComposerInstalled(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("composer", "global", "show", "--direct", "--format=json").Prints(composerListing)
	got, err := composer(fake).Installed(t.Context())
	want := []kind.Installed{{Name: "beyondcode/expose", Explicit: true}, {Name: "laravel/installer", Explicit: true}, {Name: "laravel/valet", Explicit: true}}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("Installed() = %+v, %v; want %+v", got, err, want)
	}
	fake.On("composer", "global", "show", "--direct", "--format=json").Prints(`{"installed": []}`)
	if got, err := composer(fake).Installed(t.Context()); err != nil || len(got) != 0 {
		t.Errorf("Installed() of none = %+v, %v", got, err)
	}
	// A new Mac has no global composer.json yet: nothing installed.
	fresh := runnertest.New(t)
	fresh.On("composer", "global", "show", "--direct", "--format=json").Exits(1).PrintsToStderr("To initialize a project, please create a composer.json file. See https://getcomposer.org/basic-usage\n")
	if got, err := composer(fresh).Installed(t.Context()); err != nil || len(got) != 0 {
		t.Errorf("Installed() before a global composer.json = %+v, %v; want nothing installed", got, err)
	}
}

// A package declared with a constraint matches on its name.
func TestComposerCompare(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("composer", "global", "show", "--direct", "--format=json").Prints(composerListing)
	got := kind.Compare(t.Context(), composer(fake), declared("composer", "laravel/valet:^4.0", "Laravel/Installer", "tightenco/takeout"))
	want := []check.Item{
		{ID: "composer:tightenco/takeout", Name: "tightenco/takeout", State: kind.Missing, Action: kind.Install},
		{ID: "composer:beyondcode/expose", Name: "beyondcode/expose", State: kind.Extra},
	}
	if got.Summary != "3 declared, 2 installed" || !slices.Equal(got.Items, want) {
		t.Errorf("Compare() = %+v\nwant items %+v", got, want)
	}
}

func TestComposerInstallAndRemove(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("composer", "global", "require", "--no-interaction", "--no-progress", "laravel/valet:^4.0", "tightenco/takeout")
	fake.On("composer", "global", "remove", "--no-interaction", "--no-progress", "laravel/valet")
	c := composer(fake)
	if err := c.Install(t.Context(), []string{"laravel/valet:^4.0", "tightenco/takeout"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Remove(t.Context(), []string{"laravel/valet:^4.0"}); err != nil {
		t.Fatal(err)
	}
}

const ghListing = "gh stack\tgithub/gh-stack\tv0.1.1\ngh dash\tdlvhdr/gh-dash\tv4.7.0\ngh local\t/Users/someone/gh-local\t\n"

func TestGHCompare(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("gh", "extension", "list").Prints(ghListing)
	got := kind.Compare(t.Context(), gh(fake), config.List{Kind: "gh", Entries: []config.Entry{{Name: "GitHub/gh-stack"}, {Name: "owner/gh-copilot"}}})
	want := []check.Item{
		{ID: "gh:owner/gh-copilot", Name: "owner/gh-copilot", State: kind.Missing, Action: kind.Install},
		{ID: "gh:dlvhdr/gh-dash", Name: "dlvhdr/gh-dash", State: kind.Extra},
	}
	if got.Summary != "2 declared, 1 installed" || !slices.Equal(got.Items, want) {
		t.Errorf("Compare() = %+v\nwant items %+v", got, want)
	}
}

func TestGHInstallAndRemove(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("gh", "extension", "install", "github/gh-stack")
	fake.On("gh", "extension", "install", "dlvhdr/gh-dash")
	fake.On("gh", "extension", "remove", "stack")
	g := gh(fake)
	if err := g.Install(t.Context(), []string{"github/gh-stack", "dlvhdr/gh-dash"}); err != nil {
		t.Fatal(err)
	}
	if err := g.Remove(t.Context(), []string{"github/gh-stack"}); err != nil {
		t.Fatal(err)
	}
}

// Installing may take ten minutes, five for an extension, and removing a
// Composer package ten; npm runs from the home folder, the rest wherever kit
// does.
func TestTimeLimitsAndFolder(t *testing.T) {
	for _, c := range []struct {
		kind            func(runner.Runner) kind.Kind
		install, remove time.Duration
		dir             string
	}{
		{npm, 10 * time.Minute, 0, "/home"},
		{composer, 10 * time.Minute, 10 * time.Minute, ""},
		{gh, 5 * time.Minute, 0, ""},
	} {
		fake := runnertest.New(t)
		k := c.kind(fake)
		fake.OnMatch(k.Program(), func([]string) bool { return true })
		if err := k.Install(t.Context(), []string{"owner/name"}); err != nil {
			t.Fatal(err)
		}
		if err := k.Remove(t.Context(), []string{"owner/name"}); err != nil {
			t.Fatal(err)
		}
		ran := fake.Commands()
		if len(ran) != 2 || ran[0].Timeout != c.install || ran[1].Timeout != c.remove || ran[0].Dir != c.dir || ran[1].Dir != c.dir {
			t.Errorf("%s ran %+v\nwant installing in %v, removing in %v, from %q", k.Name(), ran, c.install, c.remove, c.dir)
		}
	}
}
