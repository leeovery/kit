package ghext_test

import (
	"slices"
	"testing"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/ghext"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

const listing = "gh stack\tgithub/gh-stack\tv0.1.1\ngh dash\tdlvhdr/gh-dash\tv4.7.0\ngh local\t/Users/someone/gh-local\t\n"

func TestCompare(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("gh", "extension", "list").Prints(listing)
	got := kind.Compare(t.Context(), ghext.New(fake), config.List{Kind: "gh", Entries: []config.Entry{{Name: "GitHub/gh-stack"}, {Name: "owner/gh-copilot"}}})
	want := []check.Item{
		{ID: "gh:owner/gh-copilot", Name: "owner/gh-copilot", State: kind.Missing, Action: kind.Install},
		{ID: "gh:dlvhdr/gh-dash", Name: "dlvhdr/gh-dash", State: kind.Extra},
	}
	if got.Summary != "2 declared, 1 installed" || !slices.Equal(got.Items, want) {
		t.Errorf("Compare() = %+v\nwant items %+v", got, want)
	}
}

func TestInstallAndRemove(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("gh", "extension", "install", "github/gh-stack")
	fake.On("gh", "extension", "install", "dlvhdr/gh-dash")
	fake.On("gh", "extension", "remove", "stack")
	g := ghext.New(fake)
	if err := g.Install(t.Context(), []string{"github/gh-stack", "dlvhdr/gh-dash"}); err != nil {
		t.Fatal(err)
	}
	if err := g.Remove(t.Context(), []string{"github/gh-stack"}); err != nil {
		t.Fatal(err)
	}
}
