package brew_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

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
			s := brew.New(fake).Step()
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
	k := brew.New(fake).Formulae()

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
	if _, err := brew.New(fake).Formulae().Installed(context.Background()); err == nil || !strings.Contains(err.Error(), "brew leaves exited 1") {
		t.Errorf("Installed() error = %v, want brew leaves' failure", err)
	}
}

func TestCasksInstalled(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("brew", "list", "--cask", "--full-name", "-1").Prints("ghostty\n1password/tap/1password-cli\n")
	k := brew.New(fake).Casks()
	got, err := k.Installed(context.Background())
	want := []kind.Installed{{Name: "ghostty", Explicit: true}, {Name: "1password/tap/1password-cli", Explicit: true}}
	if err != nil || !slices.Equal(got, want) || k.Name() != "cask" || k.Title() != "Casks" {
		t.Errorf("Installed() = %+v, %v\nwant %+v", got, err, want)
	}
}

func TestResolveAtOnce(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("brew", "info", "--json=v2", "--formula", "python", "homebrew/core/jq", "oven-sh/bun/bun").Prints(fixture(t, "info-formulae.json"))
	got, err := brew.New(fake).Formulae().Resolve(context.Background(), []string{"python", "homebrew/core/jq", "oven-sh/bun/bun"})
	want := map[string]string{"python": "python@3.14", "homebrew/core/jq": "jq", "oven-sh/bun/bun": "oven-sh/bun/bun"}
	if err != nil || !mapsEqual(got, want) {
		t.Errorf("Resolve() = %v, %v; want %v", got, err, want)
	}
}

func TestResolveATapsOtherNames(t *testing.T) {
	fake := runnertest.New(t)
	names := []string{"owner/tap/tool@2", "owner/tap/tool", "tool@2", "owner/tap/old-tool"}
	fake.On("brew", append([]string{"info", "--json=v2", "--formula"}, names...)...).Prints(`{"formulae": [
		{"name": "tool", "full_name": "owner/tap/tool", "aliases": ["tool@2"], "oldnames": ["old-tool"]}
	], "casks": []}`)
	got, err := brew.New(fake).Formulae().Resolve(context.Background(), names)
	want := map[string]string{"owner/tap/tool@2": "owner/tap/tool", "owner/tap/tool": "owner/tap/tool", "tool@2": "owner/tap/tool", "owner/tap/old-tool": "owner/tap/tool"}
	if err != nil || !mapsEqual(got, want) {
		t.Errorf("Resolve() = %v, %v; want %v", got, err, want)
	}

	fake.On("brew", "info", "--json=v2", "--cask", "owner/tap/old-app").Prints(`{"formulae": [], "casks": [
		{"token": "app", "full_token": "owner/tap/app", "old_tokens": ["old-app"]}
	]}`)
	got, err = brew.New(fake).Casks().Resolve(context.Background(), []string{"owner/tap/old-app"})
	if want := map[string]string{"owner/tap/old-app": "owner/tap/app"}; err != nil || !mapsEqual(got, want) {
		t.Errorf("Resolve() of a tap cask's old token = %v, %v; want %v", got, err, want)
	}
}

func TestResolveOneAtATimeWhenANameIsUnknown(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("brew", "info", "--json=v2", "--cask", "tailscale", "no-such-cask").Exits(1).PrintsToStderr(`Error: Cask 'no-such-cask' is unavailable`)
	fake.On("brew", "info", "--json=v2", "--cask", "tailscale").Prints(fixture(t, "info-casks.json"))
	fake.On("brew", "info", "--json=v2", "--cask", "no-such-cask").Exits(1).PrintsToStderr(`Error: Cask 'no-such-cask' is unavailable`)

	got, err := brew.New(fake).Casks().Resolve(context.Background(), []string{"tailscale", "no-such-cask"})
	if want := map[string]string{"tailscale": "tailscale-app"}; err != nil || !mapsEqual(got, want) {
		t.Errorf("Resolve() = %v, %v; want %v, the unknown name left out", got, err, want)
	}
}

func TestResolveFailsWhenBrewCant(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("brew", "info", "--json=v2", "--formula", "jq").Fails(context.Canceled)
	if _, err := brew.New(fake).Formulae().Resolve(context.Background(), []string{"jq"}); !errors.Is(err, context.Canceled) {
		t.Errorf("Resolve() error = %v, want it passed on", err)
	}
	fake.On("brew", "info", "--json=v2", "--formula", "jq").Prints("not json")
	if _, err := brew.New(fake).Formulae().Resolve(context.Background(), []string{"jq"}); err == nil || !strings.Contains(err.Error(), "read brew info's answer") {
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

func TestInstallUpdatesOnceThenInstallsATapsFirst(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("brew", "update", "--quiet")
	fake.On("brew", "install", "--formula", "owner/tap/php@8.5", "jq", "composer")
	fake.On("brew", "install", "--cask", "ghostty")
	h := brew.New(fake)

	if err := h.Formulae().Install(context.Background(), []string{"jq", "owner/tap/php@8.5", "composer"}); err != nil {
		t.Fatal(err)
	}
	if err := h.Casks().Install(context.Background(), []string{"ghostty"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"brew update --quiet", "brew install --formula owner/tap/php@8.5 jq composer", "brew install --cask ghostty"}
	if got := fake.Calls(); !slices.Equal(got, want) {
		t.Errorf("ran %q, want %q", got, want)
	}
	for _, cmd := range fake.Commands()[1:] {
		if cmd.Timeout < 10*time.Minute {
			t.Errorf("%s has a timeout of %v, want one long enough for an install", cmd, cmd.Timeout)
		}
	}
}

func TestInstallFailsWhenUpdateDoes(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("brew", "update", "--quiet").Exits(1).PrintsToStderr("Error: no network")
	if err := brew.New(fake).Formulae().Install(context.Background(), []string{"jq"}); err == nil || !strings.Contains(err.Error(), "brew update --quiet exited 1") {
		t.Errorf("Install() error = %v, want update's", err)
	}
}

func TestRemove(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("brew", "uninstall", "--formula", "hello")
	fake.On("brew", "uninstall", "--cask", "zoom").Exits(1).PrintsToStderr("Error: zoom is required by something")
	h := brew.New(fake)
	if err := h.Formulae().Remove(context.Background(), []string{"hello"}); err != nil {
		t.Errorf("Remove(hello) error = %v", err)
	}
	if err := h.Casks().Remove(context.Background(), []string{"zoom"}); err == nil || !strings.Contains(err.Error(), "required by something") {
		t.Errorf("Remove(zoom) error = %v, want Homebrew's refusal", err)
	}
}

func TestFormulaeBlockedByAnotherTapsName(t *testing.T) {
	b := brew.New(runnertest.New(t)).Formulae().(kind.Blocker)
	got, err := b.Blocked(context.Background(),
		map[string]string{"php": "php", "jq": "jq"},
		[]kind.Installed{{Name: "owner/tap/php", Explicit: true}, {Name: "oniguruma", Needed: true}})
	want := map[string]string{"php": "another tap's owner/tap/php is installed under that name"}
	if err != nil || !mapsEqual(got, want) {
		t.Errorf("Blocked() = %v, %v; want %v", got, err, want)
	}
}

func TestCasksBlockedWithoutAnAdministrator(t *testing.T) {
	infoJSON := `{"formulae": [], "casks": [
		{"token": "zoom", "full_token": "zoom", "artifacts": [{"pkg": ["zoomusInstallerFull.pkg"]}, {"uninstall": []}]},
		{"token": "ghostty", "full_token": "ghostty", "artifacts": [{"app": ["Ghostty.app"]}]}
	]}`
	missing := map[string]string{"zoom": "zoom", "ghostty": "ghostty"}
	tests := []struct {
		name  string
		admin func(context.Context) bool
		want  map[string]string
	}{
		{name: "not installing", admin: nil, want: map[string]string{}},
		{name: "no password", admin: func(context.Context) bool { return false }, want: map[string]string{"zoom": "needs an administrator's password: run kit apply at a terminal"}},
		{name: "a password at hand", admin: func(context.Context) bool { return true }, want: map[string]string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := runnertest.New(t)
			fake.On("brew", "info", "--json=v2", "--cask", "ghostty", "zoom").Prints(infoJSON)
			h := brew.New(fake)
			h.Admin = tt.admin
			got, err := h.Casks().(kind.Blocker).Blocked(context.Background(), missing, nil)
			if err != nil || !mapsEqual(got, tt.want) {
				t.Errorf("Blocked() = %v, %v; want %v", got, err, tt.want)
			}
		})
	}
}

func TestNeedsAdmin(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("brew", "info", "--json=v2", "--cask", "zoom", "ghostty", "owner/tap/tool").Prints(`{"formulae": [], "casks": [
		{"token": "zoom", "full_token": "zoom", "artifacts": [{"pkg": ["x.pkg"]}]},
		{"token": "ghostty", "full_token": "ghostty", "artifacts": [{"app": ["Ghostty.app"]}]},
		{"token": "tool", "full_token": "owner/tap/tool", "artifacts": [{"installer": [{"script": "install.sh"}]}]}
	]}`)
	got, err := brew.New(fake).NeedsAdmin(context.Background(), []string{"zoom", "ghostty", "owner/tap/tool"})
	if want := []string{"zoom", "owner/tap/tool"}; err != nil || !slices.Equal(got, want) {
		t.Errorf("NeedsAdmin() = %q, %v; want %q", got, err, want)
	}
	if got, err := brew.New(runnertest.New(t)).NeedsAdmin(context.Background(), nil); err != nil || got != nil {
		t.Errorf("NeedsAdmin() of none = %q, %v; want none, asking nothing", got, err)
	}
}

// Formulae's and casks' installs take turns, though their steps run side by
// side: two of Homebrew installing at once can clash.
func TestInstallsTakeTurns(t *testing.T) {
	fake := runnertest.New(t)
	var mu sync.Mutex
	running, most := 0, 0
	busy := func() {
		mu.Lock()
		running++
		most = max(most, running)
		mu.Unlock()
		time.Sleep(50 * time.Millisecond)
		mu.Lock()
		running--
		mu.Unlock()
	}
	fake.On("brew", "update", "--quiet")
	fake.On("brew", "install", "--formula", "jq").Does(busy)
	fake.On("brew", "install", "--cask", "ghostty").Does(busy)
	h := brew.New(fake)
	var wg sync.WaitGroup
	wg.Go(func() {
		if err := h.Formulae().Install(t.Context(), []string{"jq"}); err != nil {
			t.Error(err)
		}
	})
	wg.Go(func() {
		if err := h.Casks().Install(t.Context(), []string{"ghostty"}); err != nil {
			t.Error(err)
		}
	})
	wg.Wait()
	if most != 1 {
		t.Errorf("%d installs ran at once, want one at a time", most)
	}
}
