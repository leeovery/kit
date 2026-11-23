package spotlight_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/spotlight"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// query asks Spotlight for every file in a folder, whatever its kind: a
// folder holding files of no one kind reads as indexed all the same.
const query = `kMDItemDisplayName == "*"`

// homeWith is a home holding folders.
func homeWith(t *testing.T, folders ...string) string {
	t.Helper()
	home := t.TempDir()
	for _, f := range folders {
		mkdir(t, filepath.Join(home, f))
	}
	return home
}

func mkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

// adds scripts sudo adding folder to Spotlight's list, the list there
// already.
func adds(fake *runnertest.Fake, folder string) {
	fake.On("sudo", "-n", "/usr/libexec/PlistBuddy", "-c", "Add :Exclusions array", spotlight.VolumeConfiguration).Exits(1)
	fake.On("sudo", "-n", "/usr/libexec/PlistBuddy", "-c", "Add :Exclusions: string "+folder, spotlight.VolumeConfiguration)
}

// items are what Compare finds of list, described.
func items(t *testing.T, s *spotlight.Spotlight, list config.List) []string {
	t.Helper()
	return described(kind.Compare(t.Context(), s, list))
}

// described is each of res's items as its name, state, action and detail.
func described(res check.Result) []string {
	var out []string
	for _, it := range res.Items {
		out = append(out, strings.TrimSpace(it.Name+" "+it.State+" "+it.Action+" "+it.Detail))
	}
	return out
}

// A folder still indexed is to be excluded; once added, it waits for a
// restart, till it's kept out, or undeclared: should it be indexed again
// after that, as when taken off the list in System Settings, it's to be
// excluded again, never waiting for a restart for good.
func TestSpotlight(t *testing.T) {
	fake := runnertest.New(t)
	home := homeWith(t, "Code", "Notes")
	code := filepath.Join(home, "Code")
	s := spotlight.New(fake, home, t.TempDir())
	list, _ := s.Expand(config.List{Entries: []config.Entry{{Name: "~/Code"}, {Name: "~/Notes"}}})
	fake.On("mdfind", "-onlyin", code, "-count", query).Prints("1234\n")
	fake.On("mdfind", "-onlyin", filepath.Join(home, "Notes"), "-count", query).Prints("0\n")
	s.SetAdmin(func(context.Context) bool { return true })
	if got := items(t, s, list); !slices.Equal(got, []string{"~/Code missing install"}) {
		t.Fatalf("items = %q", got)
	}
	adds(fake, code)
	if err := s.Install(t.Context(), []string{"~/Code"}); err != nil {
		t.Fatal(err)
	}
	if got := items(t, s, list); len(got) != 1 || !strings.HasPrefix(got[0], "~/Code missing  added to Spotlight's list: it takes effect after a restart") {
		t.Errorf("after adding it, items = %q; want it waiting for a restart", got)
	}
	fake.On("mdfind", "-onlyin", code, "-count", query).Prints("0\n")
	if got := items(t, s, list); len(got) != 0 {
		t.Errorf("after a restart, items = %q; want none", got)
	}
	fake.On("mdfind", "-onlyin", code, "-count", query).Prints("1234\n")
	if got := items(t, s, list); !slices.Equal(got, []string{"~/Code missing install"}) {
		t.Errorf("indexed again once it was kept out, items = %q; want it to be excluded again", got)
	}

	if err := s.Install(t.Context(), []string{"~/Code"}); err != nil {
		t.Fatal(err)
	}
	notes, _ := s.Expand(config.List{Entries: []config.Entry{{Name: "~/Notes"}}})
	kind.Compare(t.Context(), s, notes)
	if _, err := s.Expand(list); err != nil {
		t.Fatal(err)
	}
	if got := items(t, s, list); !slices.Equal(got, []string{"~/Code missing install"}) {
		t.Errorf("declared again once undeclared, items = %q; want it to be excluded, not waiting for a restart", got)
	}
}

// A declared folder is kept out with nothing in Spotlight's index: empty,
// or not there yet, as on a new Mac before a restore makes it, when
// Spotlight isn't asked about it, as mdfind counts nothing at all for it.
// One with anything indexed is to be excluded; an empty count from a
// folder that's there fails the check, rather than reading as none.
func TestSpotlightsFolders(t *testing.T) {
	for _, tt := range []struct {
		name string
		// there is whether ~/Code is there; counted, what mdfind prints
		// for it.
		there   bool
		counted string
		state   check.State
		items   []string
		asked   bool
	}{
		{name: "not there yet", counted: "", state: check.OK},
		{name: "empty", there: true, counted: "0\n", state: check.OK, asked: true},
		{name: "indexed", there: true, counted: "1234\n", state: check.Attention, items: []string{"~/Code missing install"}, asked: true},
		{name: "there, counted nothing", there: true, counted: "", state: check.Failed, asked: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake := runnertest.New(t)
			home := homeWith(t)
			code := filepath.Join(home, "Code")
			if tt.there {
				mkdir(t, code)
			}
			s := spotlight.New(fake, home, t.TempDir())
			s.SetAdmin(func(context.Context) bool { return true })
			list, _ := s.Expand(config.List{Entries: []config.Entry{{Name: "~/Code"}}})
			fake.On("mdfind", "-onlyin", code, "-count", query).Prints(tt.counted)
			res := kind.Compare(t.Context(), s, list)
			if got := described(res); res.State != tt.state || !slices.Equal(got, tt.items) {
				t.Errorf("Compare = %s %q (%s); want %s %q", res.State, got, res.Reason, tt.state, tt.items)
			}
			if asked := len(fake.Calls()) > 0; asked != tt.asked {
				t.Errorf("Spotlight asked: %v, calls %q; want %v", asked, fake.Calls(), tt.asked)
			}
			if _, err := os.Stat(code); tt.there == os.IsNotExist(err) {
				t.Errorf("~/Code there: %v after the check; want %v", err == nil, tt.there)
			}
		})
	}
}

// A folder not there yet, once made and indexed, is to be excluded at the
// next check; one kit added, gone before the restart, is no longer waiting.
func TestSpotlightsFolderMadeLater(t *testing.T) {
	fake := runnertest.New(t)
	home := homeWith(t)
	code := filepath.Join(home, "Code")
	s := spotlight.New(fake, home, t.TempDir())
	s.SetAdmin(func(context.Context) bool { return true })
	list, _ := s.Expand(config.List{Entries: []config.Entry{{Name: "~/Code"}}})
	fake.On("mdfind", "-onlyin", code, "-count", query).Prints("1234\n")
	if got := items(t, s, list); len(got) != 0 {
		t.Errorf("not there yet, items = %q; want none", got)
	}
	mkdir(t, code)
	if got := items(t, s, list); !slices.Equal(got, []string{"~/Code missing install"}) {
		t.Errorf("made and indexed, items = %q; want it to be excluded", got)
	}
	adds(fake, code)
	if err := s.Install(t.Context(), []string{"~/Code"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(code); err != nil {
		t.Fatal(err)
	}
	if got := items(t, s, list); len(got) != 0 {
		t.Errorf("added, then gone, items = %q; want none", got)
	}
	mkdir(t, code)
	if got := items(t, s, list); !slices.Equal(got, []string{"~/Code missing install"}) {
		t.Errorf("made again and indexed, items = %q; want it to be excluded, not waiting for a restart", got)
	}
}

// A count that runs out of time is of a folder indexed, as a large one not
// yet kept out is: to be excluded, or, added, still waiting for a restart.
// One that isn't a number fails the check, kit's record of what it added
// kept, rather than reading as none.
func TestSpotlightsCountThatDoesntAnswer(t *testing.T) {
	fake := runnertest.New(t)
	home := homeWith(t, "Code")
	code := filepath.Join(home, "Code")
	s := spotlight.New(fake, home, t.TempDir())
	s.SetAdmin(func(context.Context) bool { return true })
	list, _ := s.Expand(config.List{Entries: []config.Entry{{Name: "~/Code"}}})
	count := fake.On("mdfind", "-onlyin", code, "-count", query).Fails(fmt.Errorf("mdfind: %w", context.DeadlineExceeded))
	res := kind.Compare(t.Context(), s, list)
	if len(res.Items) != 1 || res.Items[0].Name != "~/Code" || res.Items[0].Action != "install" {
		t.Errorf("a count out of time: %+v; want ~/Code to be excluded", res)
	}
	adds(fake, code)
	if err := s.Install(t.Context(), []string{"~/Code"}); err != nil {
		t.Fatal(err)
	}
	count.Then().Prints("Spotlight server is disabled\n").Then().Fails(fmt.Errorf("mdfind: %w", context.DeadlineExceeded))
	if res := kind.Compare(t.Context(), s, list); res.State != check.Failed {
		t.Errorf("a count that isn't a number: %+v; want the check failed", res)
	}
	if res := kind.Compare(t.Context(), s, list); len(res.Items) != 1 || !strings.Contains(res.Items[0].Detail, "takes effect after a restart") {
		t.Errorf("added, then a count out of time: %+v; want it still waiting for a restart", res)
	}
}
