package appstore_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/appstore"
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

func declared(names ...string) config.List {
	l := config.List{Kind: "app"}
	for _, n := range names {
		l.Entries = append(l.Entries, config.Entry{Name: n, File: "app"})
	}
	return l
}

func TestName(t *testing.T) {
	for name, want := range map[string]string{
		"Xcode":                          "xcode@1",
		"1Password for Safari":           "1password-for-safari@1",
		"NEVER SLEEP - Even Lid Closed!": "never-sleep-even-lid-closed@1",
		"Café  Menu":                     "caf-menu@1",
		"写真":                             "app@1",
	} {
		if got := appstore.Name(name, 1); got != want {
			t.Errorf("Name(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestInstalled(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("mas", "list", "--json").Prints(fixture(t, "list.jsonl"))
	got, err := appstore.New(fake).Installed(t.Context())
	want := []kind.Installed{
		{Name: "xcode@497799835", Explicit: true},
		{Name: "numbers@409203825", Explicit: true},
		{Name: "heic-converter@1294126402", Explicit: true},
	}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("Installed() = %+v, %v; want %+v", got, err, want)
	}
}

// Apps match by id: one the store renamed still matches its declaration.
func TestCompare(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("mas", "list", "--json").Prints(fixture(t, "list.jsonl"))
	got := kind.Compare(t.Context(), appstore.New(fake), declared("xcode@497799835", "apple-numbers@409203825", "bear@1091189122", "bear"))
	want := []check.Item{
		{ID: "app:bear@1091189122", Name: "bear@1091189122", State: kind.Missing, Action: kind.Install},
		{ID: "app:bear", Name: "bear", State: kind.Missing, Detail: "unknown"},
		{ID: "app:heic-converter@1294126402", Name: "heic-converter@1294126402", State: kind.Extra},
	}
	if got.State != check.Attention || got.Summary != "4 declared, 2 installed" || !slices.Equal(got.Items, want) {
		t.Errorf("Compare() = %+v\nwant items %+v", got, want)
	}
}

func TestInstallAndRemoveGoThroughSudo(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("sudo", "-n", "mas", "install", "1091189122", "497799835")
	fake.On("sudo", "-n", "mas", "uninstall", "1294126402")
	s := appstore.New(fake)
	if err := s.Install(t.Context(), []string{"bear@1091189122", "xcode@497799835"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(t.Context(), []string{"heic-converter@1294126402"}); err != nil {
		t.Fatal(err)
	}
	if cmds := fake.Commands(); len(cmds) != 2 || cmds[0].Timeout < time.Hour {
		t.Errorf("ran %+v, want the install given an hour or more", cmds)
	}
	if err := s.Install(t.Context(), []string{"bear"}); err == nil || !strings.Contains(err.Error(), "bear isn't an App Store app's name") {
		t.Errorf("Install(bear) = %v, want it refused", err)
	}
}

// Every install needs an administrator's password: without one, kit
// installing holds every missing app up, saying why; a check alone holds
// nothing up.
func TestBlockedWithoutThePassword(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("mas", "list", "--json").Prints(fixture(t, "list.jsonl"))
	s := appstore.New(fake)
	if got := kind.Compare(t.Context(), s, declared("bear@1091189122")); got.Items[0].Action != kind.Install {
		t.Errorf("checked alone: %+v, want bear to install", got.Items[0])
	}
	if needing, err := s.NeedsAdmin(t.Context(), []string{"bear@1091189122"}); err != nil || len(needing) != 1 {
		t.Errorf("NeedsAdmin() = %q, %v; want bear", needing, err)
	}
	held := false
	s.SetAdmin(func(context.Context) bool { return held })
	got := kind.Compare(t.Context(), s, declared("bear@1091189122"))
	if it := got.Items[0]; it.Action != "" || it.Detail != "needs an administrator's password: run kit apply at a terminal" {
		t.Errorf("without the password: %+v, want bear held up", it)
	}
	held = true
	if got := kind.Compare(t.Context(), s, declared("bear@1091189122")); got.Items[0].Action != kind.Install {
		t.Errorf("with the password: %+v, want bear to install", got.Items[0])
	}
}

func TestFind(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("mas", "lookup", "--json", "937984704").Prints(strings.SplitAfter(fixture(t, "search.jsonl"), "\n")[0])
	fake.On("mas", "lookup", "--json", "1").Exits(1).PrintsToStderr("Error: No apps found")
	fake.On("mas", "search", "--json", "amphetamine").Prints(fixture(t, "search.jsonl"))
	fake.On("mas", "search", "--json", "sleep").Prints(fixture(t, "search.jsonl"))
	fake.On("mas", "search", "--json", "nothing like it").Exits(1).PrintsToStderr("Error: No apps found")
	s := appstore.New(fake)
	amphetamine := kind.Found{Name: "amphetamine@937984704", Label: "Amphetamine (937984704)"}
	for typed, want := range map[string][]kind.Found{
		"937984704":       {amphetamine},
		"bear@1091189122": {{Name: "bear@1091189122"}},
		"amphetamine":     {amphetamine},
		"sleep": {
			amphetamine,
			{Name: "sleep-control-center@946798523", Label: "Sleep Control Center (946798523)"},
			{Name: "never-sleep-even-lid-closed@1574505861", Label: "NEVER SLEEP - Even Lid Closed! (1574505861)"},
		},
	} {
		got, err := s.Find(t.Context(), typed)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("Find(%q) = %+v, %v; want %+v", typed, got, err, want)
		}
	}
	for typed, want := range map[string]string{"1": "no App Store app has the id 1", "nothing like it": "no App Store app matches nothing like it"} {
		if _, err := s.Find(t.Context(), typed); err == nil || err.Error() != want {
			t.Errorf("Find(%q) error = %v, want %q", typed, err, want)
		}
	}
}
