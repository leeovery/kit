package kind_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
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
	removes    [][]string
	installErr error
}

func (f *fakeKind) Install(_ context.Context, names []string) error {
	f.installs = append(f.installs, names)
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
