package spotlight_test

import (
	"context"
	"fmt"
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

// A folder still indexed is to be excluded; once added, it waits for a
// restart, till it's kept out, or undeclared: should it be indexed again
// after that, as when taken off the list in System Settings, it's to be
// excluded again, never waiting for a restart for good.
func TestSpotlight(t *testing.T) {
	fake := runnertest.New(t)
	s := spotlight.New(fake, "/home/someone", t.TempDir())
	list, _ := s.Expand(config.List{Entries: []config.Entry{{Name: "~/Code"}, {Name: "~/Notes"}}})
	fake.On("mdfind", "-onlyin", "/home/someone/Code", "-count", query).Prints("1234\n")
	fake.On("mdfind", "-onlyin", "/home/someone/Notes", "-count", query).Prints("0\n")
	items := func() []string {
		var out []string
		for _, it := range kind.Compare(t.Context(), s, list).Items {
			out = append(out, strings.TrimSpace(it.Name+" "+it.State+" "+it.Action+" "+it.Detail))
		}
		return out
	}
	s.SetAdmin(func(context.Context) bool { return true })
	if got := items(); !slices.Equal(got, []string{"~/Code missing install"}) {
		t.Fatalf("items = %q", got)
	}
	fake.On("sudo", "-n", "/usr/libexec/PlistBuddy", "-c", "Add :Exclusions array", spotlight.VolumeConfiguration).Exits(1)
	fake.On("sudo", "-n", "/usr/libexec/PlistBuddy", "-c", "Add :Exclusions: string /home/someone/Code", spotlight.VolumeConfiguration)
	if err := s.Install(t.Context(), []string{"~/Code"}); err != nil {
		t.Fatal(err)
	}
	if got := items(); len(got) != 1 || !strings.HasPrefix(got[0], "~/Code missing  added to Spotlight's list: it takes effect after a restart") {
		t.Errorf("after adding it, items = %q; want it waiting for a restart", got)
	}
	fake.On("mdfind", "-onlyin", "/home/someone/Code", "-count", query).Prints("0\n")
	if got := items(); len(got) != 0 {
		t.Errorf("after a restart, items = %q; want none", got)
	}
	fake.On("mdfind", "-onlyin", "/home/someone/Code", "-count", query).Prints("1234\n")
	if got := items(); !slices.Equal(got, []string{"~/Code missing install"}) {
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
	if got := items(); !slices.Equal(got, []string{"~/Code missing install"}) {
		t.Errorf("declared again once undeclared, items = %q; want it to be excluded, not waiting for a restart", got)
	}
}

// A count that runs out of time is of a folder indexed, as a large one not
// yet kept out is: to be excluded, or, added, still waiting for a restart.
// One that isn't a number fails the check, kit's record of what it added
// kept, rather than reading as none.
func TestSpotlightsCountThatDoesntAnswer(t *testing.T) {
	fake := runnertest.New(t)
	s := spotlight.New(fake, "/home/someone", t.TempDir())
	s.SetAdmin(func(context.Context) bool { return true })
	list, _ := s.Expand(config.List{Entries: []config.Entry{{Name: "~/Code"}}})
	count := fake.On("mdfind", "-onlyin", "/home/someone/Code", "-count", query).Fails(fmt.Errorf("mdfind: %w", context.DeadlineExceeded))
	res := kind.Compare(t.Context(), s, list)
	if len(res.Items) != 1 || res.Items[0].Name != "~/Code" || res.Items[0].Action != "install" {
		t.Errorf("a count out of time: %+v; want ~/Code to be excluded", res)
	}
	fake.On("sudo", "-n", "/usr/libexec/PlistBuddy", "-c", "Add :Exclusions array", spotlight.VolumeConfiguration)
	fake.On("sudo", "-n", "/usr/libexec/PlistBuddy", "-c", "Add :Exclusions: string /home/someone/Code", spotlight.VolumeConfiguration)
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
