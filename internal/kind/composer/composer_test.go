package composer_test

import (
	"slices"
	"testing"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/composer"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

const listing = `{
    "installed": [
        {"name": "beyondcode/expose", "direct-dependency": true, "version": "2.6.2"},
        {"name": "laravel/installer", "direct-dependency": true, "version": "v5.31.1"},
        {"name": "laravel/valet", "direct-dependency": true, "version": "v4.12.0"}
    ]
}`

func declared(names ...string) config.List {
	l := config.List{Kind: "composer"}
	for _, n := range names {
		l.Entries = append(l.Entries, config.Entry{Name: n, File: "composer"})
	}
	return l
}

func TestInstalled(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("composer", "global", "show", "--direct", "--format=json").Prints(listing)
	got, err := composer.New(fake).Installed(t.Context())
	want := []kind.Installed{{Name: "beyondcode/expose", Explicit: true}, {Name: "laravel/installer", Explicit: true}, {Name: "laravel/valet", Explicit: true}}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("Installed() = %+v, %v; want %+v", got, err, want)
	}
	fake.On("composer", "global", "show", "--direct", "--format=json").Prints(`{"installed": []}`)
	if got, err := composer.New(fake).Installed(t.Context()); err != nil || len(got) != 0 {
		t.Errorf("Installed() of none = %+v, %v", got, err)
	}
}

// A package declared with a constraint matches on its name.
func TestCompare(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("composer", "global", "show", "--direct", "--format=json").Prints(listing)
	got := kind.Compare(t.Context(), composer.New(fake), declared("laravel/valet:^4.0", "Laravel/Installer", "tightenco/takeout"))
	want := []check.Item{
		{ID: "composer:tightenco/takeout", Name: "tightenco/takeout", State: kind.Missing, Action: kind.Install},
		{ID: "composer:beyondcode/expose", Name: "beyondcode/expose", State: kind.Extra},
	}
	if got.Summary != "3 declared, 2 installed" || !slices.Equal(got.Items, want) {
		t.Errorf("Compare() = %+v\nwant items %+v", got, want)
	}
}

func TestInstallAndRemove(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("composer", "global", "require", "--no-interaction", "laravel/valet:^4.0", "tightenco/takeout")
	fake.On("composer", "global", "remove", "--no-interaction", "laravel/valet")
	c := composer.New(fake)
	if err := c.Install(t.Context(), []string{"laravel/valet:^4.0", "tightenco/takeout"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Remove(t.Context(), []string{"laravel/valet:^4.0"}); err != nil {
		t.Fatal(err)
	}
}
