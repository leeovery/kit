package brew_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/brew"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestTheHomebrewStep(t *testing.T) {
	tests := []struct {
		name   string
		script func(*runnertest.Fake)
		want   check.Result
	}{
		{
			name:   "installed",
			script: func(f *runnertest.Fake) { f.On("brew", "--prefix").Prints("/opt/homebrew\n") },
			want:   check.Result{State: check.OK, Summary: "/opt/homebrew"},
		},
		{
			name:   "not installed",
			script: func(f *runnertest.Fake) { f.On("brew", "--prefix").Fails(runner.ErrNotFound) },
			want:   check.Result{State: check.Attention, Summary: "not installed: brew isn't on kit's PATH"},
		},
		{
			name:   "broken",
			script: func(f *runnertest.Fake) { f.On("brew", "--prefix").Exits(1).PrintsToStderr("Error: broken") },
			want:   check.Result{State: check.Failed, Reason: "brew --prefix exited 1: Error: broken"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := runnertest.New(t)
			tt.script(fake)
			s := brew.Homebrew{Run: fake}.Step()
			if s.Name != "homebrew" || s.Title != "Homebrew" {
				t.Errorf("Step() = %s, %s; want homebrew, Homebrew", s.Name, s.Title)
			}
			if got := s.Check(context.Background()); got.State != tt.want.State || got.Summary != tt.want.Summary || got.Reason != tt.want.Reason {
				t.Errorf("check = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestFormulaeInstalled(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("brew", "list", "--formula", "--full-name", "-1").Prints("jq\noniguruma\noven-sh/bun/bun\nnode@20\n")
	fake.On("brew", "leaves").Prints("jq\noven-sh/bun/bun\nnode@20\n")
	fake.On("brew", "leaves", "--installed-on-request").Prints("jq\noven-sh/bun/bun\n")
	k := brew.Homebrew{Run: fake}.Formulae()

	got, err := k.Installed(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []kind.Installed{
		{Name: "jq", Explicit: true},
		{Name: "oniguruma", Needed: true},
		{Name: "oven-sh/bun/bun", Explicit: true},
		{Name: "node@20"},
	}
	if !slices.Equal(got, want) || k.Name() != "brew" || k.Title() != "Formulae" {
		t.Errorf("Installed() = %+v\nwant %+v", got, want)
	}
}

func TestFormulaeInstalledFailsWithAListing(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("brew", "list", "--formula", "--full-name", "-1").Prints("jq\n")
	fake.On("brew", "leaves").Exits(1).PrintsToStderr("Error: no")
	fake.On("brew", "leaves", "--installed-on-request").Prints("jq\n")
	if _, err := (brew.Homebrew{Run: fake}).Formulae().Installed(context.Background()); err == nil || !strings.Contains(err.Error(), "brew leaves exited 1") {
		t.Errorf("Installed() error = %v, want brew leaves' failure", err)
	}
}

func TestCasksInstalled(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("brew", "list", "--cask", "--full-name", "-1").Prints("ghostty\n1password/tap/1password-cli\n")
	k := brew.Homebrew{Run: fake}.Casks()
	got, err := k.Installed(context.Background())
	want := []kind.Installed{{Name: "ghostty", Explicit: true}, {Name: "1password/tap/1password-cli", Explicit: true}}
	if err != nil || !slices.Equal(got, want) || k.Name() != "cask" || k.Title() != "Casks" {
		t.Errorf("Installed() = %+v, %v\nwant %+v", got, err, want)
	}
}

func TestResolveAtOnce(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("brew", "info", "--json=v2", "--formula", "python", "homebrew/core/jq", "oven-sh/bun/bun").Prints(fixture(t, "info-formulae.json"))
	got, err := brew.Homebrew{Run: fake}.Formulae().Resolve(context.Background(), []string{"python", "homebrew/core/jq", "oven-sh/bun/bun"})
	want := map[string]string{"python": "python@3.14", "homebrew/core/jq": "jq", "oven-sh/bun/bun": "oven-sh/bun/bun"}
	if err != nil || !mapsEqual(got, want) {
		t.Errorf("Resolve() = %v, %v; want %v", got, err, want)
	}
}

func TestResolveOneAtATimeWhenANameIsUnknown(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("brew", "info", "--json=v2", "--cask", "tailscale", "no-such-cask").Exits(1).PrintsToStderr(`Error: Cask 'no-such-cask' is unavailable`)
	fake.On("brew", "info", "--json=v2", "--cask", "tailscale").Prints(fixture(t, "info-casks.json"))
	fake.On("brew", "info", "--json=v2", "--cask", "no-such-cask").Exits(1).PrintsToStderr(`Error: Cask 'no-such-cask' is unavailable`)

	got, err := brew.Homebrew{Run: fake}.Casks().Resolve(context.Background(), []string{"tailscale", "no-such-cask"})
	if want := map[string]string{"tailscale": "tailscale-app"}; err != nil || !mapsEqual(got, want) {
		t.Errorf("Resolve() = %v, %v; want %v, the unknown name left out", got, err, want)
	}
}

func TestResolveFailsWhenBrewCant(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("brew", "info", "--json=v2", "--formula", "jq").Fails(context.Canceled)
	if _, err := (brew.Homebrew{Run: fake}).Formulae().Resolve(context.Background(), []string{"jq"}); !errors.Is(err, context.Canceled) {
		t.Errorf("Resolve() error = %v, want it passed on", err)
	}
	fake.On("brew", "info", "--json=v2", "--formula", "jq").Prints("not json")
	if _, err := (brew.Homebrew{Run: fake}).Formulae().Resolve(context.Background(), []string{"jq"}); err == nil || !strings.Contains(err.Error(), "read brew info's answer") {
		t.Errorf("Resolve() of a garbled answer: error = %v", err)
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
