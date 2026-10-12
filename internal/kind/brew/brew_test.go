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
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/event"
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
	if !slices.Equal(got, want) || k.Name() != "brew" {
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
	if err != nil || !slices.Equal(got, want) || k.Name() != "cask" {
		t.Errorf("Installed() = %+v, %v\nwant %+v", got, err, want)
	}
}

func TestResolveAtOnce(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("brew", "tap").Prints("oven-sh/bun\n")
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
	fake.On("brew", "tap").Prints("owner/tap\n")
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

// On a new Mac no tap is added: a formula or cask in one is its own full
// name, missing, as installing it adds its tap; only the rest are looked up,
// in one go. A name brew info doesn't know is left out, and the rest asked
// again together, not one at a time.
func TestResolveBeforeTapsAreAdded(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("brew", "tap")
	fake.On("brew", "info", "--json=v2", "--formula", "jq", "mistyped").Exits(1).PrintsToStderr(`Error: No available formula with the name "mistyped".`)
	fake.On("brew", "info", "--json=v2", "--formula", "jq").Prints(`{"formulae": [{"name": "jq", "full_name": "jq"}], "casks": []}`)
	got, err := brew.New(fake).Formulae().Resolve(context.Background(), []string{"leeovery/tools/portal", "jq", "shivammathur/php/php@8.4", "mistyped"})
	want := map[string]string{"leeovery/tools/portal": "leeovery/tools/portal", "shivammathur/php/php@8.4": "shivammathur/php/php@8.4", "jq": "jq"}
	if err != nil || !mapsEqual(got, want) {
		t.Errorf("Resolve() = %v, %v; want %v", got, err, want)
	}
	if n := len(fake.Commands()); n != 3 {
		t.Errorf("ran %d commands: want the taps, then brew info twice, not once a name", n)
	}

	casks := runnertest.New(t)
	casks.On("brew", "tap").Prints("homebrew/cask\n")
	casks.On("brew", "info", "--json=v2", "--cask", "1password").Prints(`{"formulae": [], "casks": [{"token": "1password", "full_token": "1password"}]}`)
	got, err = brew.New(casks).Casks().Resolve(context.Background(), []string{"1password", "1password/tap/1password-cli"})
	if want := map[string]string{"1password": "1password", "1password/tap/1password-cli": "1password/tap/1password-cli"}; err != nil || !mapsEqual(got, want) {
		t.Errorf("Resolve() of a tap's cask before its tap's added = %v, %v; want %v", got, err, want)
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
	// More to install has longer: a new Mac's every cask in one go.
	if three, one := fake.Commands()[1].Timeout, fake.Commands()[2].Timeout; three <= one {
		t.Errorf("three formulae may take %v, one cask %v: want longer for more", three, one)
	}
}

func TestInstallFailsWhenUpdateDoes(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("brew", "update", "--quiet").Exits(1).PrintsToStderr("Error: no network")
	if err := brew.New(fake).Formulae().Install(context.Background(), []string{"jq"}); err == nil || !strings.Contains(err.Error(), "brew update --quiet exited 1") {
		t.Errorf("Install() error = %v, want update's", err)
	}
}

// Homebrew's formulae and casks tell, in a line brew install prints, which
// of the things asked for is installed: a formula by its keg's summary, the
// badge or not, a cask by its own; a tap's by its name in its tap. A
// dependency pulled in isn't one of them, and a formula's bottle poured is
// its install starting, not done.
func TestDoneRecognisesWhatsInstalled(t *testing.T) {
	formulae, casks := brew.New(nil).Formulae().(kind.Counter), brew.New(nil).Casks().(kind.Counter)
	for _, tt := range []struct {
		name  string
		kind  kind.Counter
		line  string
		names []string
		want  string
	}{
		{"keg", formulae, "🍺  /opt/homebrew/Cellar/hello/2.12.3: 12 files, 193.3KB", []string{"jq", "hello"}, "hello"},
		{"keg, no badge", formulae, "/opt/homebrew/Cellar/hello/2.12.3: 12 files, 193.3KB", []string{"hello"}, "hello"},
		{"keg of a file", formulae, "🍺  /opt/homebrew/Cellar/tiny/1.0_1: 512B", []string{"tiny"}, "tiny"},
		{"keg built", formulae, "🍺  /opt/homebrew/Cellar/hello/2.12.3: 12 files, 193.3KB, built in 1 minute 3 seconds", []string{"hello"}, "hello"},
		{"a tap's keg", formulae, "🍺  /opt/homebrew/Cellar/php@8.5/8.5.0: 1,520 files, 80.1MB", []string{"jq", "owner/tap/php@8.5"}, "owner/tap/php@8.5"},
		{"a dependency's keg", formulae, "🍺  /opt/homebrew/Cellar/aom/3.13.1: 24 files, 9.8MB", []string{"ffmpeg"}, ""},
		{"bottle poured", formulae, "==> Pouring hello--2.12.3.arm64_tahoe.bottle.1.tar.gz", []string{"hello"}, ""},
		{"bottle fetched", formulae, "✔︎ Bottle hello (2.12.3)", []string{"hello"}, ""},
		{"a cask's line", formulae, "🍺  hello was successfully installed!", []string{"hello"}, ""},
		{"cask", casks, "🍺  font-sf-mono was successfully installed!", []string{"font-sf-mono"}, "font-sf-mono"},
		{"cask, no badge", casks, "ghostty was successfully installed!", []string{"zoom", "ghostty"}, "ghostty"},
		{"a tap's cask", casks, "🍺  1password-cli was successfully installed!", []string{"1password/tap/1password-cli"}, "1password/tap/1password-cli"},
		{"a dependency's cask", casks, "🍺  ghostty was successfully installed!", []string{"zoom"}, ""},
		{"cask installing", casks, "==> Installing Cask ghostty", []string{"ghostty"}, ""},
		{"a formula's line", casks, "🍺  /opt/homebrew/Cellar/ghostty/1.0: 12 files, 1MB", []string{"ghostty"}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := tt.kind.Done(tt.line, tt.names)
			if got != tt.want || ok != (tt.want != "") {
				t.Errorf("Done(%q, %q) = %q, %v; want %q", tt.line, tt.names, got, ok, tt.want)
			}
		})
	}
}

// installing is what brew install --formula ffmpeg hello prints: ffmpeg
// with a dependency, then hello.
const installing = `==> Fetching downloads for: ffmpeg and hello
✔︎ Bottle aom (3.13.1)
✔︎ Bottle ffmpeg (8.0)
✔︎ Bottle hello (2.12.3)
==> Installing dependencies for ffmpeg: aom
==> Installing ffmpeg dependency: aom
==> Pouring aom--3.13.1.arm64_tahoe.bottle.tar.gz
🍺  /opt/homebrew/Cellar/aom/3.13.1: 24 files, 9.8MB
==> Installing ffmpeg
==> Pouring ffmpeg--8.0.arm64_tahoe.bottle.tar.gz
🍺  /opt/homebrew/Cellar/ffmpeg/8.0: 285 files, 55.3MB
==> Pouring hello--2.12.3.arm64_tahoe.bottle.1.tar.gz
🍺  /opt/homebrew/Cellar/hello/2.12.3: 12 files, 193.3KB
`

// Applying the formulae counts them by what brew install prints, in one
// install of them all.
func TestApplyingCountsTheFormulae(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("brew", "list", "--formula", "--full-name", "-1").Prints("jq\n")
	fake.On("brew", "leaves").Prints("jq\n")
	fake.On("brew", "leaves", "--installed-on-request").Prints("jq\n")
	fake.On("brew", "info", "--json=v2", "--formula", "ffmpeg", "hello").Prints(`{"formulae": [{"name": "ffmpeg", "full_name": "ffmpeg"}, {"name": "hello", "full_name": "hello"}], "casks": []}`)
	fake.On("brew", "update", "--quiet")
	fake.On("brew", "install", "--formula", "ffmpeg", "hello").Prints(installing)
	var said []event.Doing
	ctx := event.WithSaying(t.Context(), func(d event.Doing) { said = append(said, d) })
	s := kind.Step(brew.New(fake).Formulae(), config.List{Kind: "brew", Entries: []config.Entry{{Name: "jq"}, {Name: "ffmpeg"}, {Name: "hello"}}})
	if err := s.Apply(ctx, s.Check(ctx)); err != nil {
		t.Fatal(err)
	}
	want := []event.Doing{{Says: "installing ffmpeg", Of: 2}, {Says: "installing hello", Done: 1, Of: 2}, {Says: "installing", Done: 2, Of: 2}}
	if !slices.Equal(said, want) {
		t.Errorf("applying said %+v, want %+v", said, want)
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

// With an administrator's password at hand for the run, brew is told its
// check of sudo is done, which would have sudo forget it; without, brew
// checks as it would.
func TestInstallsKeepSudosPassword(t *testing.T) {
	for _, held := range []bool{true, false} {
		fake := runnertest.New(t)
		fake.On("brew", "update", "--quiet")
		fake.On("brew", "install", "--cask", "zoom")
		fake.On("brew", "uninstall", "--cask", "zoom")
		h := brew.New(fake)
		h.Admin = func(context.Context) bool { return held }
		if err := h.Casks().Install(t.Context(), []string{"zoom"}); err != nil {
			t.Fatal(err)
		}
		if err := h.Casks().Remove(t.Context(), []string{"zoom"}); err != nil {
			t.Fatal(err)
		}
		for _, cmd := range fake.Commands() {
			if cmd.Args[0] == "update" {
				continue
			}
			if told := slices.Contains(cmd.Env, "HOMEBREW_SUDO_CHECKED=1"); told != held {
				t.Errorf("held %v: %s ran with %q", held, cmd, cmd.Env)
			}
		}
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

// Homebrew missing is to install, through its own install script, run
// unattended: with an administrator's password at hand, and refused
// without one.
func TestTheHomebrewStepInstalls(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("brew", "--prefix").Fails(runner.ErrNotFound)
	h := brew.New(fake)
	s := h.Step()
	found := s.Check(context.Background())
	if !s.Admin || len(found.Items) != 1 || found.Items[0].Action != kind.Install {
		t.Fatalf("Step() admin %v, found %+v; want an admin step with Homebrew to install", s.Admin, found)
	}
	if err := s.Apply(context.Background(), found); err == nil || !strings.Contains(err.Error(), "administrator's password") {
		t.Errorf("Apply() without a password = %v; want it waiting for one", err)
	}
	h.Admin = func(context.Context) bool { return true }
	script := brew.InstallerFile()
	fake.On("curl", "-fsSL", "--retry", "2", "-o", script, "https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh")
	fake.On("/bin/bash", script)
	if err := s.Apply(context.Background(), found); err != nil {
		t.Fatalf("Apply() = %v", err)
	}
	run := fake.Commands()[len(fake.Commands())-1]
	if !slices.Contains(run.Env, "NONINTERACTIVE=1") || run.Timeout < 30*time.Minute {
		t.Errorf("the installer ran with %q, timeout %s; want it unattended, with time for the Xcode tools", run.Env, run.Timeout)
	}
}

func TestInstallSaysWhatFailed(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("curl", "-fsSL", "--retry", "2", "-o", brew.InstallerFile(), "https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh").Exits(6)
	if err := brew.Install(context.Background(), fake); err == nil || !strings.HasPrefix(err.Error(), "download Homebrew's installer") {
		t.Errorf("Install() = %v", err)
	}
}
