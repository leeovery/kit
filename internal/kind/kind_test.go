package kind_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/runner"
)

// fakeKind is a kind whose installed things and other names a test gives,
// noting what it's asked to install and remove.
type fakeKind struct {
	installed  []kind.Installed
	err        error
	aliases    map[string]string
	resolveErr error
	resolved   [][]string
	installs   [][]string
	// envs are the variables set for each install, as its context sets
	// them.
	envs       [][]string
	removes    [][]string
	installErr error
}

func (f *fakeKind) Install(ctx context.Context, names []string) error {
	f.installs = append(f.installs, names)
	f.envs = append(f.envs, runner.SecretEnv(ctx))
	return f.installErr
}

func (f *fakeKind) Remove(_ context.Context, names []string) error {
	f.removes = append(f.removes, names)
	return nil
}

// blockingKind is a fakeKind that says some names can't be installed now.
type blockingKind struct {
	*fakeKind
	reasons map[string]string
	asked   map[string]string
}

func (b *blockingKind) Blocked(_ context.Context, missing map[string]string, _ []kind.Installed) (map[string]string, error) {
	b.asked = missing
	return b.reasons, nil
}

func (*fakeKind) Name() string    { return "brew" }
func (*fakeKind) Program() string { return "brew" }

func (f *fakeKind) Installed(context.Context) ([]kind.Installed, error) {
	return f.installed, f.err
}

func (f *fakeKind) Resolve(_ context.Context, names []string) (map[string]string, error) {
	f.resolved = append(f.resolved, names)
	if f.resolveErr != nil {
		return nil, f.resolveErr
	}
	out := map[string]string{}
	for _, n := range names {
		if full, ok := f.aliases[n]; ok {
			out[n] = full
		}
	}
	return out, nil
}

// commandedKind is a fakeKind whose check and install run no command.
type commandedKind struct{ *fakeKind }

func (commandedKind) Command() string { return "" }

// A kind's command is its program, unless it says otherwise.
func TestCommand(t *testing.T) {
	if got := kind.Command(&fakeKind{}); got != "brew" {
		t.Errorf("Command() = %q, want its program, brew", got)
	}
	if got := kind.Command(commandedKind{&fakeKind{}}); got != "" {
		t.Errorf("Command() = %q, want none, as it says", got)
	}
}

func declared(names ...string) config.List {
	l := config.List{Kind: "brew"}
	for _, n := range names {
		l.Entries = append(l.Entries, config.Entry{Name: n, File: "brew"})
	}
	return l
}

func TestCompare(t *testing.T) {
	k := &fakeKind{
		installed: []kind.Installed{
			{Name: "jq", Explicit: true},
			{Name: "oniguruma", Needed: true},
			{Name: "python@3.14", Explicit: true},
			{Name: "owner/tap/tool", Explicit: true},
			{Name: "ffmpeg", Explicit: true},
			{Name: "zstd", Explicit: true, Needed: true},
			{Name: "node@20"},
			{Name: "lame", Explicit: false, Needed: true},
		},
		aliases: map[string]string{"python": "python@3.14", "ripgrep": "ripgrep", "homebrew/core/jq": "jq"},
	}

	got := kind.Compare(context.Background(), k, declared("jq", "python", "owner/tap/tool", "ripgrep", "typo-name"))
	want := check.Result{
		State:   check.Attention,
		Summary: "5 declared, 3 installed",
		Counts:  map[string]int{"declared": 5, "installed": 3, "missing": 2, "extra": 1, "unused_dependencies": 1},
		Items: []check.Item{
			{ID: "brew:ripgrep", Name: "ripgrep", State: kind.Missing, Action: kind.Install},
			{ID: "brew:typo-name", Name: "typo-name", State: kind.Missing, Detail: "unknown"},
			{ID: "brew:ffmpeg", Name: "ffmpeg", State: kind.Extra},
			{ID: "brew:node@20", Name: "node@20", State: kind.UnusedDependency},
		},
	}
	if got.State != want.State || got.Summary != want.Summary || !maps.Equal(got.Counts, want.Counts) || !slices.Equal(got.Items, want.Items) {
		t.Errorf("Compare() = %+v\nwant %+v", got, want)
	}
	if want := [][]string{{"python", "ripgrep", "typo-name"}}; !slices.EqualFunc(k.resolved, want, slices.Equal) {
		t.Errorf("resolved %q, want only the names that matched nothing, at once", k.resolved)
	}
}

func TestCompareAllWell(t *testing.T) {
	k := &fakeKind{installed: []kind.Installed{{Name: "jq", Explicit: true}, {Name: "oniguruma", Needed: true}}}
	got := kind.Compare(context.Background(), k, declared("jq"))
	if got.State != check.OK || got.Summary != "1 declared, all installed" || len(got.Items) != 0 || k.resolved != nil {
		t.Errorf("Compare() = %+v, resolving %q; want ok, nothing resolved", got, k.resolved)
	}
	got = kind.Compare(context.Background(), &fakeKind{}, declared())
	if got.State != check.OK || got.Summary != "none declared" {
		t.Errorf("Compare() of nothing = %+v, want ok, none declared", got)
	}
}

func TestCompareFails(t *testing.T) {
	got := kind.Compare(context.Background(), &fakeKind{err: errors.New("brew leaves exited 1")}, declared("jq"))
	if got.State != check.Failed || got.Reason != "brew leaves exited 1" {
		t.Errorf("Compare() = %+v, want it failed with the error", got)
	}
	got = kind.Compare(context.Background(), &fakeKind{resolveErr: errors.New("brew info: context canceled")}, declared("jq"))
	if got.State != check.Failed || got.Reason != "brew info: context canceled" {
		t.Errorf("Compare() = %+v, want it failed with resolve's error", got)
	}
	script := &runner.ExitError{Command: "osascript -l JavaScript -e 'function run() {\n  return 1;\n}'", Code: 1, Stderr: "execution error: Error: AppleEvent timed out. (-1712)"}
	got = kind.Compare(context.Background(), &fakeKind{err: script}, declared("jq"))
	if got.State != check.Failed || got.Reason != "execution error: Error: AppleEvent timed out. (-1712)" {
		t.Errorf("Compare() = %+v, want it failed saying what the command said, not the command", got)
	}
}

// A kind macOS hasn't let look at what's installed needs the person at the
// Mac: the step says so, its one item what isn't allowed and where to allow
// it, and nothing applying would do.
func TestCompareNotAllowed(t *testing.T) {
	no := &kind.NotAllowed{What: "macOS hasn't let the terminal control System Events", Where: "System Settings › Privacy & Security › Automation"}
	got := kind.Compare(context.Background(), &fakeKind{err: fmt.Errorf("read: %w", no)}, declared("jq"))
	want := check.Item{ID: "brew:allowed", Name: no.What, State: check.NotAllowed, Detail: no.Where}
	if got.State != check.Attention || got.Summary != "needs your permission" || len(got.Items) != 1 || got.Items[0] != want || got.Actions() {
		t.Errorf("Compare() = %+v, want it needing you, its item %+v", got, want)
	}
	if it, ok := got.Refused(); !ok || it != want {
		t.Errorf("Refused() = %+v, %v; want its item", it, ok)
	}
}

func TestStep(t *testing.T) {
	s := kind.Step(&fakeKind{installed: []kind.Installed{{Name: "jq", Explicit: true}}}, declared("jq"), "homebrew")
	if s.Name != "brew" || s.Title != "Homebrew formulae" || !slices.Equal(s.Needs, []string{"homebrew"}) {
		t.Errorf("Step() = %+v, want the kind's name and label, needing homebrew", s)
	}
	if got := s.Check(context.Background()); got.State != check.OK {
		t.Errorf("its check = %+v, want ok", got)
	}
}

// linkingKind is a fakeKind whose install fails having installed some of
// what it was given: the rest stay missing.
type linkingKind struct {
	*fakeKind
	takes []string
}

// knowing is a fakeKind that knows names, each by itself.
func knowing(names ...string) *fakeKind {
	f := &fakeKind{aliases: map[string]string{}}
	for _, n := range names {
		f.aliases[n] = n
	}
	return f
}

func (l *linkingKind) Install(ctx context.Context, names []string) error {
	for _, name := range names {
		if slices.Contains(l.takes, name) {
			l.installed = append(l.installed, kind.Installed{Name: name, Explicit: true})
		}
	}
	return errors.Join(l.fakeKind.Install(ctx, names), errors.New("brew install exited 1"))
}

func TestStepFailingInstallThatInstalledAll(t *testing.T) {
	// As Homebrew does for a formula it installs but can't link.
	k := &linkingKind{fakeKind: knowing("php"), takes: []string{"php"}}
	s := kind.Step(k, declared("php"))
	if err := s.Apply(context.Background(), s.Check(context.Background())); err != nil || len(k.installs) != 1 {
		t.Errorf("Apply() = %v, installs %q; want php installed, and nil", err, k.installs)
	}
}

// lookingKind is a linkingKind noting, each time it's asked what's
// installed, whether its commands' output would be shown.
type lookingKind struct {
	*linkingKind
	shown []bool
}

func (l *lookingKind) Installed(ctx context.Context) ([]kind.Installed, error) {
	l.shown = append(l.shown, event.Changing(ctx))
	return l.linkingKind.Installed(ctx)
}

// Looking at what's left after an install fails is a check: what it runs
// isn't shown as the install's output.
func TestStepFailingInstallLooksAgainUnseen(t *testing.T) {
	k := &lookingKind{linkingKind: &linkingKind{fakeKind: knowing("jq")}}
	s := kind.Step(k, declared("jq"))
	if err := s.Apply(event.WithChanging(t.Context()), s.Check(t.Context())); err == nil {
		t.Fatal("Apply() = nil, want the install's failure")
	}
	if want := []bool{false, false}; !slices.Equal(k.shown, want) {
		t.Errorf("asked what's installed with output shown %v, want %v: the check, then the look after the failure, neither shown", k.shown, want)
	}
}

func TestStepFailingInstallSaysWhatsLeft(t *testing.T) {
	k := &linkingKind{fakeKind: knowing("jq", "xpdf"), takes: []string{"jq"}}
	s := kind.Step(k, declared("jq", "xpdf"))
	err := s.Apply(context.Background(), s.Check(context.Background()))
	if err == nil || !strings.HasPrefix(err.Error(), "couldn't install xpdf: ") {
		t.Errorf("Apply() = %v, want it to name xpdf alone", err)
	}
	k = &linkingKind{fakeKind: knowing("a", "b", "c", "d")}
	s = kind.Step(k, declared("a", "b", "c", "d"))
	if err := s.Apply(context.Background(), s.Check(context.Background())); err == nil || !strings.HasPrefix(err.Error(), "couldn't install 4 of 4: ") {
		t.Errorf("Apply() = %v, want them counted", err)
	}
}

func TestLabels(t *testing.T) {
	for name, label := range kind.Labels {
		// A kind declared in a section of kit-config's is named as it is.
		if header := config.Header(name); header != name && !strings.EqualFold(label, header) {
			t.Errorf("%s's row is %q, want its section's words, %q", name, label, header)
		}
	}
	if got := kind.Label("brew"); got != "Homebrew formulae" {
		t.Errorf("Label(brew) = %q, want Homebrew formulae", got)
	}
	if got := kind.Label("unknown"); got != "unknown" {
		t.Errorf("Label(unknown) = %q, want its name", got)
	}
}

func TestCompareAsksABlockerWhatCantBeInstalled(t *testing.T) {
	k := &blockingKind{
		fakeKind: &fakeKind{aliases: map[string]string{"php": "php", "zoom": "zoom"}},
		reasons:  map[string]string{"php": "another tap's owner/tap/php is installed under that name"},
	}
	got := kind.Compare(context.Background(), k, declared("php", "zoom"))
	want := []check.Item{
		{ID: "brew:php", Name: "php", State: kind.Missing, Detail: "another tap's owner/tap/php is installed under that name"},
		{ID: "brew:zoom", Name: "zoom", State: kind.Missing, Action: kind.Install},
	}
	if !slices.Equal(got.Items, want) {
		t.Errorf("items = %+v\nwant %+v", got.Items, want)
	}
	if fmt.Sprint(k.asked) != fmt.Sprint(map[string]string{"php": "php", "zoom": "zoom"}) {
		t.Errorf("the blocker was asked of %v, want the missing names, by full name", k.asked)
	}
}

func TestStepAppliesByInstallingWhatsMissing(t *testing.T) {
	k := &blockingKind{
		fakeKind: &fakeKind{
			installed: []kind.Installed{{Name: "jq", Explicit: true}, {Name: "ffmpeg", Explicit: true}},
			aliases:   map[string]string{"ripgrep": "ripgrep", "php": "php", "go": "go"},
		},
		reasons: map[string]string{"php": "held"},
	}
	s := kind.Step(k, declared("jq", "ripgrep", "php", "go", "typo"))
	if err := s.Apply(context.Background(), s.Check(context.Background())); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if want := [][]string{{"go", "ripgrep"}}; !slices.EqualFunc(k.installs, want, slices.Equal) {
		t.Errorf("installed %q, want what's missing and can be, leaving the blocked and the unknown", k.installs)
	}
	if k.removes != nil {
		t.Errorf("removed %q, want nothing: applying never removes", k.removes)
	}
}

// countingKind is a fakeKind whose install prints lines, each "done" line
// saying a thing is installed.
type countingKind struct {
	*fakeKind
	prints []string
}

func (c *countingKind) Install(ctx context.Context, names []string) error {
	if lines := kind.Lines(ctx); lines != nil {
		for _, l := range c.prints {
			lines(l)
		}
	}
	return c.fakeKind.Install(ctx, names)
}

func (*countingKind) Done(line string, _ []string) (string, bool) {
	return strings.CutPrefix(line, "done ")
}

// saying is a context that keeps what a step says it's doing.
func saying(said *[]event.Doing) context.Context {
	return event.WithSaying(context.Background(), func(d event.Doing) { *said = append(*said, d) })
}

// A kind that tells, in its install's lines, which thing is installed has
// its step count them: from the start, and as each is done, it says it's
// installing the next not yet done, and how many of those it's installing
// are. A line saying nothing, a thing it wasn't asked to install, as a
// dependency, and a thing said twice, count nothing.
func TestStepCountsWhatsInstalled(t *testing.T) {
	k := &countingKind{
		fakeKind: knowing("a", "b", "c"),
		prints:   []string{"==> Fetching downloads", "done b", "done libdep", "done a", "done a", "done c"},
	}
	var said []event.Doing
	s := kind.Step(k, declared("a", "b", "c"))
	if err := s.Apply(saying(&said), s.Check(context.Background())); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	want := []event.Doing{
		{Says: "installing a", Done: 0, Of: 3},
		{Says: "installing a", Done: 1, Of: 3},
		{Says: "installing c", Done: 2, Of: 3},
		{Says: "installing", Done: 3, Of: 3},
	}
	if !slices.Equal(said, want) {
		t.Errorf("the step said %+v, want %+v", said, want)
	}
}

// A kind that can't tell has its step say nothing as it installs, and its
// install has no lines to give.
func TestStepWithoutCountingSaysNothing(t *testing.T) {
	var lines func(string)
	k := &linesKind{fakeKind: knowing("a"), lines: &lines}
	var said []event.Doing
	s := kind.Step(k, declared("a"))
	if err := s.Apply(saying(&said), s.Check(context.Background())); err != nil || said != nil || lines != nil {
		t.Errorf("Apply() = %v, said %+v, lines given: %v; want nothing said, no lines", err, said, lines != nil)
	}
}

// linesKind is a fakeKind that keeps where its install's lines would go.
type linesKind struct {
	*fakeKind
	lines *func(string)
}

func (l *linesKind) Install(ctx context.Context, names []string) error {
	*l.lines = kind.Lines(ctx)
	return l.fakeKind.Install(ctx, names)
}

func TestStepAppliesNothingWhenNothingsMissing(t *testing.T) {
	k := &fakeKind{installed: []kind.Installed{{Name: "jq", Explicit: true}, {Name: "ffmpeg", Explicit: true}}}
	s := kind.Step(k, declared("jq"))
	if err := s.Apply(context.Background(), s.Check(context.Background())); err != nil || k.installs != nil {
		t.Errorf("Apply() = %v, installing %q; want nothing done", err, k.installs)
	}
}

// While a kind's program isn't installed, there's nothing to find: fine with
// nothing declared, deferred with something.
func TestCompareWithoutTheProgram(t *testing.T) {
	k := &fakeKind{err: fmt.Errorf("brew: %w on kit's PATH", runner.ErrNotFound)}
	got := kind.Compare(t.Context(), k, declared())
	if got.State != check.OK || got.Summary != "none declared; brew isn't installed" {
		t.Errorf("Compare() with nothing declared = %+v, want ok", got)
	}
	got = kind.Compare(t.Context(), k, declared("jq"))
	if got.State != check.Deferred || got.Reason != "needs brew, which isn't installed" || len(got.Items) != 0 {
		t.Errorf("Compare() with jq declared = %+v, want it deferred", got)
	}
	k.err = errors.New("brew list exited 1")
	if got := kind.Compare(t.Context(), k, declared()); got.State != check.Failed {
		t.Errorf("Compare() when listing fails = %+v, want it failed", got)
	}
}

// differingKind is a fakeKind whose things can be installed otherwise than
// declared.
type differingKind struct {
	*fakeKind
	differs map[string]string
	asked   []string
}

func (d *differingKind) Differs(_ context.Context, names []string) (map[string]string, error) {
	d.asked = names
	out := map[string]string{}
	for _, n := range names {
		if why, ok := d.differs[n]; ok {
			out[n] = why
		}
	}
	return out, nil
}

// A thing installed otherwise than declared is changed, to install again; a
// thing declared off is neither missing nor extra.
func TestCompareChangedAndOff(t *testing.T) {
	k := &differingKind{
		fakeKind: &fakeKind{installed: []kind.Installed{{Name: "refero", Explicit: true}, {Name: "sentry", Explicit: true}, {Name: "paper", Explicit: true}}},
		differs:  map[string]string{"refero": "its headers differ"},
	}
	list := declared("refero", "sentry", "paper", "remarkable")
	list.Entries[2].Off = true
	list.Entries[3].Off = true
	got := kind.Compare(t.Context(), k, list)
	want := []check.Item{{ID: "brew:refero", Name: "refero", State: kind.Changed, Detail: "its headers differ", Action: kind.Install}}
	if got.State != check.Attention || got.Summary != "2 declared, all installed; 1 changed; 2 off" || !slices.Equal(got.Items, want) {
		t.Errorf("Compare() = %+v\nwant items %+v", got, want)
	}
	if !slices.Equal(k.asked, []string{"refero", "sentry"}) {
		t.Errorf("asked how %q differ, want the two declared on and installed", k.asked)
	}
	if got.Counts["changed"] != 1 || got.Counts["off"] != 2 {
		t.Errorf("counts = %v", got.Counts)
	}
}

// heldWorld is a kind knowing jq, fetcher and helper, none installed, the
// last two declared in a section holding them back for TOKEN, which ready
// says is in place or not; and that section, its variable's value as kit's
// secrets hold it.
func heldWorld(ready bool) (*fakeKind, config.List, []kind.Held) {
	k := knowing("jq", "owner/private/fetcher", "owner/private/helper")
	list := declared("jq")
	for _, name := range []string{"owner/private/fetcher", "owner/private/helper"} {
		list.Entries = append(list.Entries, config.Entry{Name: name, File: "brew", Hold: "--secret TOKEN"})
	}
	token := engine.Wait{Step: "secret", Thing: "TOKEN", Ready: func(context.Context) bool { return ready }}
	held := []kind.Held{{Hold: "--secret TOKEN", Waits: []engine.Wait{token}, Env: func() ([]string, error) { return []string{"TOKEN=plain-words-value"}, nil }}}
	return k, list, held
}

// A section of lines held back is installed apart, after the rest, as one
// install with its variable set, which the rest's isn't.
func TestHeldLinesInstallApart(t *testing.T) {
	k, list, held := heldWorld(true)
	s := kind.Placed(k, list, func() kind.Placing { return kind.Placing{Held: held} })
	if err := s.Apply(t.Context(), s.Check(t.Context())); err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{"jq"}, {"owner/private/fetcher", "owner/private/helper"}}; !slices.EqualFunc(k.installs, want, slices.Equal) {
		t.Errorf("installed %q, want jq, then the section held back, in one go", k.installs)
	}
	if len(k.envs) != 2 || k.envs[0] != nil || !slices.Equal(k.envs[1], []string{"TOKEN=plain-words-value"}) {
		t.Error("the variable wasn't set for the held section's install alone")
	}
}

// Lines held back for what never comes aren't installed: they stay
// missing, saying what isn't there, with nothing to do about them, once
// applying's tried; the rest install.
func TestHeldLinesWaitingForWhatNeverComes(t *testing.T) {
	k, list, held := heldWorld(false)
	s := kind.Placed(k, list, func() kind.Placing { return kind.Placing{Held: held} })
	found := s.Check(t.Context())
	if got := found.Items; len(got) != 3 || got[0].Action != kind.Install || got[2].Action != kind.Install {
		t.Fatalf("before applying, items = %+v: want each to install", got)
	}
	if err := s.Apply(t.Context(), found); err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{"jq"}}; !slices.EqualFunc(k.installs, want, slices.Equal) {
		t.Errorf("installed %q, want jq alone", k.installs)
	}
	k.installed = []kind.Installed{{Name: "jq", Explicit: true}}
	got := s.Check(t.Context())
	want := []check.Item{
		{ID: "brew:owner/private/fetcher", Name: "owner/private/fetcher", State: kind.Missing, Detail: "needs TOKEN, which isn't there yet"},
		{ID: "brew:owner/private/helper", Name: "owner/private/helper", State: kind.Missing, Detail: "needs TOKEN, which isn't there yet"},
	}
	if !slices.Equal(got.Items, want) {
		t.Errorf("after applying, items = %+v\nwant %+v", got.Items, want)
	}
}

// echoingKind is a fakeKind whose install with a secret set fails, saying
// the secret's value, as a program might.
type echoingKind struct{ *fakeKind }

func (e echoingKind) Install(ctx context.Context, names []string) error {
	_ = e.fakeKind.Install(ctx, names)
	if values := runner.SecretValues(ctx); len(values) > 0 {
		return errors.New("fetch failed with " + values[0])
	}
	return nil
}

// An install with a secret set that fails says so without its value.
func TestAHeldInstallFailingHidesItsSecret(t *testing.T) {
	k, list, held := heldWorld(true)
	s := kind.Placed(echoingKind{k}, list, func() kind.Placing { return kind.Placing{Held: held} })
	err := s.Apply(t.Context(), s.Check(t.Context()))
	if err == nil || err.Error() != "couldn't install owner/private/fetcher, owner/private/helper: fetch failed with [redacted]" {
		t.Errorf("Apply() = %v, want the held install's failure, its secret hidden", err)
	}
}

// fetchingKind is a countingKind whose install downloads first, a
// "fetched" line for each download, and says "begin" as it begins
// installing.
// fetchingKind is a countingKind whose install downloads first: its
// command starts once it's done what it does first, saying so, if anything.
type fetchingKind struct {
	*countingKind
	first string
}

func (f *fetchingKind) Install(ctx context.Context, names []string) error {
	if f.first != "" {
		kind.Saying(ctx, f.first)()
	}
	kind.Starting(ctx)
	return f.countingKind.Install(ctx, names)
}

func (*fetchingKind) Fetched(line string, _ []string) (string, bool) {
	return strings.CutPrefix(line, "fetched ")
}

func (*fetchingKind) Begun(line string) bool { return line == "begin" }

// A kind whose install downloads first has its step say it's downloading,
// from when its command starts, counting those it's installing as each is
// downloaded, and, once the install proper's begun, installing, counting
// them done. A dependency's download, and one said twice, count nothing.
func TestStepSaysItsDownloading(t *testing.T) {
	k := &fetchingKind{countingKind: &countingKind{
		fakeKind: knowing("a", "b"),
		prints:   []string{"fetched b", "fetched libdep", "fetched b", "fetched a", "begin", "fetched a", "done a", "done b"},
	}}
	var said []event.Doing
	s := kind.Step(k, declared("a", "b"))
	if err := s.Apply(saying(&said), s.Check(context.Background())); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	want := []event.Doing{
		{Says: "downloading", Of: 2},
		{Says: "downloading", Done: 1, Of: 2},
		{Says: "downloading", Done: 2, Of: 2},
		{Says: "installing a", Of: 2},
		{Says: "installing b", Done: 1, Of: 2},
		{Says: "installing", Done: 2, Of: 2},
	}
	if !slices.Equal(said, want) {
		t.Errorf("the step said %+v\nwant %+v", said, want)
	}
}

// A kind whose install downloads first says nothing of downloading before
// its command starts: what it does first, or waits for, as Homebrew's
// update, then downloading once its command starts.
func TestStepSaysDownloadingOnceItsCommandStarts(t *testing.T) {
	k := &fetchingKind{countingKind: &countingKind{fakeKind: knowing("a"), prints: []string{"fetched a", "done a"}}, first: "waiting for Homebrew's update"}
	var said []event.Doing
	s := kind.Step(k, declared("a"))
	if err := s.Apply(saying(&said), s.Check(context.Background())); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	want := []event.Doing{
		{Says: "waiting for Homebrew's update"},
		{Says: "downloading", Of: 1},
		{Says: "downloading", Done: 1, Of: 1},
		{Says: "installing", Done: 1, Of: 1},
	}
	if !slices.Equal(said, want) {
		t.Errorf("the step said %+v\nwant %+v", said, want)
	}
	said = nil
	kind.Saying(saying(&said), "updating Homebrew")()
	kind.Starting(saying(&said))
	if said != nil {
		t.Errorf("outside an install, Saying and Starting said %+v, want nothing", said)
	}
}

// A thing said installed ends the downloading, though nothing said the
// install had begun.
func TestStepInstallingWithoutABeginning(t *testing.T) {
	k := &fetchingKind{countingKind: &countingKind{fakeKind: knowing("a", "b"), prints: []string{"done a"}}}
	var said []event.Doing
	s := kind.Step(k, declared("a", "b"))
	if err := s.Apply(saying(&said), s.Check(context.Background())); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if want := []event.Doing{{Says: "downloading", Of: 2}, {Says: "installing b", Done: 1, Of: 2}}; !slices.Equal(said, want) {
		t.Errorf("the step said %+v, want %+v", said, want)
	}
}

// waitingKind is a countingKind whose install waits its turn behind
// another's step before it runs.
type waitingKind struct{ *countingKind }

func (w *waitingKind) Install(ctx context.Context, names []string) error {
	resume := kind.Waiting(ctx, "Homebrew formulae")
	resume()
	return w.countingKind.Install(ctx, names)
}

// An install waiting its turn says so, by the title of the step it waits
// for, then how it's going once it's its turn; one nothing counts says
// nothing.
func TestAnInstallWaitingItsTurn(t *testing.T) {
	k := &waitingKind{&countingKind{fakeKind: knowing("a"), prints: []string{"done a"}}}
	var said []event.Doing
	s := kind.Step(k, declared("a"))
	if err := s.Apply(saying(&said), s.Check(context.Background())); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	want := []event.Doing{{Says: "installing a", Of: 1}, {Says: "waiting for Homebrew formulae"}, {Says: "installing a", Of: 1}, {Says: "installing", Done: 1, Of: 1}}
	if !slices.Equal(said, want) {
		t.Errorf("the step said %+v\nwant %+v", said, want)
	}
	said = nil
	kind.Waiting(saying(&said), "Homebrew formulae")()
	if said != nil {
		t.Errorf("waiting outside an install said %+v, want nothing", said)
	}
}

// The lines a step waits for install first, on their own, as one command,
// then the rest; one installed already is left be, and one held back keeps
// to its section.
func TestWhatsWaitedForInstallsFirst(t *testing.T) {
	k, list, held := heldWorld(true)
	list.Entries = append(list.Entries, config.Entry{Name: "mise", File: "brew"}, config.Entry{Name: "zoxide", File: "brew"})
	k.aliases["mise"], k.aliases["zoxide"], k.aliases["fzf"] = "mise", "zoxide", "fzf"
	k.installed = []kind.Installed{{Name: "fzf", Explicit: true}}
	list.Entries = append(list.Entries, config.Entry{Name: "fzf", File: "brew"})
	placing := kind.Placing{First: []string{"zoxide", "mise", "fzf", "owner/private/helper"}, Held: held}
	s := kind.Placed(k, list, func() kind.Placing { return placing })
	if err := s.Apply(t.Context(), s.Check(t.Context())); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"mise", "zoxide"}, {"jq"}, {"owner/private/fetcher", "owner/private/helper"}}
	if !slices.EqualFunc(k.installs, want, slices.Equal) {
		t.Errorf("installed %q, want %q: what's waited for first, as the check lists it, then the rest, then the section held back", k.installs, want)
	}
}

// installLog is what kinds taking turns installed, in the order they did,
// each as kind:names.
type installLog struct {
	mu       sync.Mutex
	installs []string
}

func (l *installLog) add(name string, names []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.installs = append(l.installs, name+":"+strings.Join(names, ","))
}

func (l *installLog) has(install string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Contains(l.installs, install)
}

// await waits till install is in the log, or a while.
func (l *installLog) await(install string) {
	for deadline := time.Now().Add(3 * time.Second); !l.has(install) && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
}

func (l *installLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.installs)
}

// turnKind is a fakeKind named name whose installs go in log, each once
// what its after says it waits for is in log, or a while.
type turnKind struct {
	*fakeKind
	name  string
	log   *installLog
	after map[string]string
}

func (k *turnKind) Name() string { return k.name }

func (k *turnKind) Install(ctx context.Context, names []string) error {
	if after, ok := k.after[strings.Join(names, ",")]; ok {
		k.log.await(after)
	}
	k.log.add(k.name, names)
	return k.fakeKind.Install(ctx, names)
}

// watcher is a sink that closes heard once step says says.
type watcher struct {
	step, says string
	heard      chan struct{}
	once       sync.Once
}

func (w *watcher) Emit(e event.Event) {
	if d, ok := e.(event.Doing); ok && d.Step == w.step && d.Says == w.says {
		w.once.Do(func() { close(w.heard) })
	}
}

// sharingWorld is two kinds that take turns: brew, missing jq, behind the
// thing cask installs first, app, which ready says is in, and cask,
// missing app and zoom, whose first check is as checking says, given
// whether brew is waiting, or its own, once checking says nothing. cask
// installs zoom once brew's installed jq, or a while.
func sharingWorld(log *installLog, ready func(context.Context) bool, checking func(waiting <-chan struct{}) check.Result) (brew, cask engine.Step, w *watcher) {
	w = &watcher{step: "brew", says: "waiting for Homebrew casks", heard: make(chan struct{})}
	brewKind := &turnKind{fakeKind: knowing("jq"), name: "brew", log: log}
	brewing := kind.Placing{Behind: []engine.Wait{{Step: "cask", Thing: "app", Ready: ready}}}
	brew = kind.Placed(brewKind, declared("jq"), func() kind.Placing { return brewing })
	caskKind := &turnKind{fakeKind: knowing("app", "zoom"), name: "cask", log: log, after: map[string]string{"zoom": "brew:jq"}}
	casking := kind.Placing{First: []string{"app"}}
	cask = kind.Placed(caskKind, declared("app", "zoom"), func() kind.Placing { return casking })
	compare := cask.Check
	var first sync.Once
	cask.Check = func(ctx context.Context) check.Result {
		var res check.Result
		first.Do(func() { res = checking(w.heard) })
		if res.State != "" {
			return res
		}
		return compare(ctx)
	}
	return brew, cask, w
}

// applied applies steps in ctx, failing the test should the run not
// finish in a few seconds, as when a wait is never told what it waits for
// came: the engine looks again by itself only every 30 seconds.
func applied(ctx context.Context, t *testing.T, sink event.Sink, opts engine.Options, steps ...engine.Step) engine.Report {
	t.Helper()
	p, err := engine.New(steps...)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan engine.Report)
	go func() {
		report, _ := p.Apply(ctx, sink, opts)
		done <- report
	}()
	select {
	case report := <-done:
		return report
	case <-time.After(5 * time.Second):
		t.Fatal("the run never finished")
		return engine.Report{}
	}
}

// A kind taking turns with another installs the rest of its lines once
// the other's first are in, though it's ready first: the thing another
// step waits for isn't held up behind them. It says what it waits for,
// and looks again as soon as the other's install ends, not once the
// other's step has.
func TestTheRestWaitsBehindAnotherKindsFirst(t *testing.T) {
	log := &installLog{}
	brew, cask, w := sharingWorld(log, func(context.Context) bool { return log.has("cask:app") }, func(waiting <-chan struct{}) check.Result {
		select {
		case <-waiting:
		case <-time.After(3 * time.Second):
		}
		return check.Result{}
	})
	applied(t.Context(), t, w, engine.Options{Jobs: 4}, brew, cask)
	if want := []string{"cask:app", "brew:jq", "cask:zoom"}; !slices.Equal(log.all(), want) {
		t.Errorf("installed %q, want %q: cask's app, which a step waits for, then brew's jq, as soon as app's install ended", log.all(), want)
	}
	select {
	case <-w.heard:
	default:
		t.Error("brew never said it was waiting for Homebrew casks")
	}
}

// A thing of another kind's in place already holds nothing up, nor does
// one whose step fails, or isn't in the run.
func TestTheRestNeverWaitsForGood(t *testing.T) {
	for _, tt := range []struct {
		name  string
		ready bool
		check check.Result
		only  []string
	}{
		{name: "in place", ready: true},
		{name: "its step failing", check: check.Result{State: check.Failed, Reason: "brew list exited 1"}},
		{name: "its step not run", only: []string{"brew"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			log := &installLog{}
			brew, cask, w := sharingWorld(log, func(context.Context) bool { return tt.ready }, func(<-chan struct{}) check.Result {
				// While brew holds nothing up, its install comes first.
				if tt.check.State == "" {
					log.await("brew:jq")
				}
				return tt.check
			})
			report := applied(t.Context(), t, w, engine.Options{Jobs: 4, Only: tt.only}, brew, cask)
			if !log.has("brew:jq") {
				t.Errorf("installed %q, want brew's jq", log.all())
			}
			if tt.ready {
				select {
				case <-w.heard:
					t.Error("brew said it was waiting, for what was in place")
				default:
				}
			}
			if got := report.Results["brew"]; got.State == check.Failed || got.State == check.Deferred {
				t.Errorf("brew = %+v, want it applied", got)
			}
		})
	}
}

// A run stopped while the rest wait behind another kind's first installs
// none of them.
func TestTheRestStoppedWhileWaiting(t *testing.T) {
	log := &installLog{}
	ctx, stop := context.WithCancel(t.Context())
	defer stop()
	brew, cask, w := sharingWorld(log, func(context.Context) bool { return false }, func(waiting <-chan struct{}) check.Result {
		<-waiting
		stop()
		return check.Result{State: check.Failed, Reason: "stopped"}
	})
	applied(ctx, t, w, engine.Options{Jobs: 4}, brew, cask)
	if log.has("brew:jq") {
		t.Errorf("installed %q, though the run was stopped while jq waited", log.all())
	}
}

// signingKind is a fakeKind named cask whose install of app prints that
// it's installed, then goes on till signed is closed, or a while.
type signingKind struct {
	*fakeKind
	in     atomic.Bool
	signed chan struct{}
}

func (*signingKind) Name() string { return "cask" }

func (s *signingKind) Install(ctx context.Context, names []string) error {
	s.in.Store(true)
	kind.Lines(ctx)("done app")
	select {
	case <-s.signed:
	case <-time.After(3 * time.Second):
	}
	return s.fakeKind.Install(ctx, names)
}

func (*signingKind) Done(line string, _ []string) (string, bool) {
	return strings.CutPrefix(line, "done ")
}

// A step waiting for a thing a kind installs looks again as soon as the
// install says it's in, while the rest of that install goes on.
func TestAWaitSeesAThingTheMomentItsCounted(t *testing.T) {
	k := &signingKind{fakeKind: knowing("app", "zoom"), signed: make(chan struct{})}
	cask := kind.Step(k, declared("app", "zoom"))
	signIn := engine.Step{
		Name:  "sign-in",
		Waits: []engine.Wait{{Step: "cask", Thing: "app", Ready: func(context.Context) bool { return k.in.Load() }}},
		Check: func(context.Context) check.Result {
			close(k.signed)
			return check.Result{State: check.OK}
		},
	}
	start := time.Now()
	report := applied(t.Context(), t, &watcher{heard: make(chan struct{})}, engine.Options{Jobs: 4}, cask, signIn)
	if got := report.Results["sign-in"]; got.State != check.OK || time.Since(start) >= 3*time.Second {
		t.Errorf("sign-in = %+v after %v: want it checked while app's install went on", got, time.Since(start))
	}
}
