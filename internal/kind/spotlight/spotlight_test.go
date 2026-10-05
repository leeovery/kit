package spotlight_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/spotlight"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

const query = `kMDItemFSName == "*.md" || kMDItemFSName == "*.json"`

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
}
