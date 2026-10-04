package kind_test

import (
	"context"
	"errors"
	"maps"
	"slices"
	"testing"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind"
)

// fakeKind is a kind whose installed things and other names a test gives.
type fakeKind struct {
	installed  []kind.Installed
	err        error
	aliases    map[string]string
	resolveErr error
	resolved   [][]string
}

func (*fakeKind) Name() string  { return "brew" }
func (*fakeKind) Title() string { return "Formulae" }

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
			{ID: "brew:ripgrep", Name: "ripgrep", State: kind.Missing},
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
	if s.Name != "brew" || s.Title != "Formulae" || !slices.Equal(s.Needs, []string{"homebrew"}) {
		t.Errorf("Step() = %+v, want the kind's name and title, needing homebrew", s)
	}
	if got := s.Check(context.Background()); got.State != check.OK {
		t.Errorf("its check = %+v, want ok", got)
	}
}
