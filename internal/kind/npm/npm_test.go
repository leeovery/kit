package npm_test

import (
	"slices"
	"testing"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/npm"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

const listing = `{
  "name": "lib",
  "dependencies": {
    "intelephense": {"version": "1.18.4", "overridden": false},
    "@scope/tool": {"version": "1.2.3", "overridden": false},
    "docx": {"version": "9.6.0", "overridden": false}
  }
}`

func declared(names ...string) config.List {
	l := config.List{Kind: "npm"}
	for _, n := range names {
		l.Entries = append(l.Entries, config.Entry{Name: n, File: "npm"})
	}
	return l
}

func TestInstalled(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("npm", "ls", "--global", "--depth=0", "--json").Prints(listing)
	got, err := npm.New(fake, "/home").Installed(t.Context())
	want := []kind.Installed{{Name: "@scope/tool", Explicit: true}, {Name: "docx", Explicit: true}, {Name: "intelephense", Explicit: true}}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("Installed() = %+v, %v; want %+v", got, err, want)
	}
	if dir := fake.Commands()[0].Dir; dir != "/home" {
		t.Errorf("ran npm in %q, want the home: the default Node, never a project's", dir)
	}
}

// npm ls exits 1 over a problem it finds, yet lists what's there.
func TestInstalledDespiteAProblem(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("npm", "ls", "--global", "--depth=0", "--json").Prints(listing).Exits(1).PrintsToStderr("npm error missing: peer@1")
	if got, err := npm.New(fake, "/home").Installed(t.Context()); err != nil || len(got) != 3 {
		t.Errorf("Installed() = %+v, %v; want the three packages", got, err)
	}
	fake.On("npm", "ls", "--global", "--depth=0", "--json").Exits(1).PrintsToStderr("npm error broken")
	if _, err := npm.New(fake, "/home").Installed(t.Context()); err == nil {
		t.Error("Installed() with nothing listed = nil error, want npm's")
	}
}

// A package declared with a version matches on its name.
func TestCompare(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("npm", "ls", "--global", "--depth=0", "--json").Prints(listing)
	got := kind.Compare(t.Context(), npm.New(fake, "/home"), declared("intelephense@1", "@scope/tool@1.2.3", "typescript@5"))
	want := []check.Item{
		{ID: "npm:typescript@5", Name: "typescript@5", State: kind.Missing, Action: kind.Install},
		{ID: "npm:docx", Name: "docx", State: kind.Extra},
	}
	if got.Summary != "3 declared, 2 installed" || !slices.Equal(got.Items, want) {
		t.Errorf("Compare() = %+v\nwant items %+v", got, want)
	}
}

func TestInstallAndRemove(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("npm", "install", "--global", "typescript@5", "@scope/tool@1.2.3")
	fake.On("npm", "uninstall", "--global", "typescript", "@scope/tool")
	n := npm.New(fake, "/home")
	if err := n.Install(t.Context(), []string{"typescript@5", "@scope/tool@1.2.3"}); err != nil {
		t.Fatal(err)
	}
	if err := n.Remove(t.Context(), []string{"typescript@5", "@scope/tool@1.2.3"}); err != nil {
		t.Fatal(err)
	}
}
