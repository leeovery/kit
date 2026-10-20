package appstore_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/event"
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
		{ID: "mas:bear@1091189122", Name: "bear@1091189122", State: kind.Missing, Action: kind.Install},
		{ID: "mas:bear", Name: "bear", State: kind.Missing, Detail: "unknown"},
		{ID: "mas:heic-converter@1294126402", Name: "heic-converter@1294126402", State: kind.Extra},
	}
	if got.State != check.Attention || got.Summary != "4 declared, 2 installed" || !slices.Equal(got.Items, want) {
		t.Errorf("Compare() = %+v\nwant items %+v", got, want)
	}
}

// Installing goes through sudo, an app at a time: till one's in, each given
// minutes, as it's the check that someone's signed in; then each given an
// hour or more, as Xcode is gigabytes.
func TestInstallAndRemoveGoThroughSudo(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("sudo", "-n", "mas", "install", "1091189122")
	fake.On("sudo", "-n", "mas", "install", "497799835")
	fake.On("sudo", "-n", "mas", "install", "409203825")
	fake.On("sudo", "-n", "mas", "uninstall", "1294126402")
	s := appstore.New(fake)
	if err := s.Install(t.Context(), []string{"bear@1091189122", "xcode@497799835", "numbers@409203825"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(t.Context(), []string{"heic-converter@1294126402"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"sudo -n mas install 1091189122", "sudo -n mas install 497799835", "sudo -n mas install 409203825", "sudo -n mas uninstall 1294126402"}
	if !slices.Equal(fake.Calls(), want) {
		t.Errorf("ran %q, want %q", fake.Calls(), want)
	}
	cmds := fake.Commands()
	if first := cmds[0].Timeout; first <= 0 || first > 10*time.Minute {
		t.Errorf("the first install is given %v, want minutes", first)
	}
	for _, cmd := range cmds[1:3] {
		if cmd.Timeout < time.Hour {
			t.Errorf("%s is given %v, want an hour or more", cmd, cmd.Timeout)
		}
	}
	if err := s.Install(t.Context(), []string{"bear"}); err == nil || !strings.Contains(err.Error(), "bear isn't an App Store app's name") {
		t.Errorf("Install(bear) = %v, want it refused", err)
	}
}

// mas says, in a line, as it's installed an app, which, by its name and
// maybe its version: the app declared by that name. Nothing else it says
// is one.
func TestDoneRecognisesWhatsInstalled(t *testing.T) {
	counter := appstore.New(nil)
	names := []string{"bear@1091189122", "xcode@497799835", "1password-for-safari@1569813296"}
	for line, want := range map[string]string{
		"==> Installed Xcode":                "xcode@497799835",
		"==> Installed Xcode (16.0)":         "xcode@497799835",
		"==> Installed 1Password for Safari": "1password-for-safari@1569813296",
		"  ==> Installed bear":               "bear@1091189122",
		"==> Downloading Xcode":              "",
		"==> Installed Pages (14.4)":         "",
		"Warning: No installed apps found":   "",
	} {
		got, ok := counter.Done(line, names)
		if got != want || ok != (want != "") {
			t.Errorf("Done(%q) = %q, %v; want %q", line, got, ok, want)
		}
	}
}

// Applying the App Store's apps counts them as mas says each is installed,
// its lines read as they come, so an install that goes quiet is noticed as
// Homebrew's is.
func TestApplyingCountsTheApps(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("mas", "list", "--json").Prints("")
	fake.On("sudo", "-n", "mas", "install", "1091189122").Prints("==> Downloading Bear\n==> Installed Bear\n")
	fake.On("sudo", "-n", "mas", "install", "497799835").Prints("==> Downloading Xcode\n==> Installed Xcode (16.0)\n")
	var said []event.Doing
	var told []string
	ctx := event.WithLogging(event.WithSaying(t.Context(), func(d event.Doing) { said = append(said, d) }), func(l event.Logged) { told = append(told, l.Line) })
	s := kind.Step(appstore.New(fake), declared("bear@1091189122", "xcode@497799835"))
	if err := s.Apply(ctx, s.Check(ctx)); err != nil {
		t.Fatal(err)
	}
	want := []event.Doing{
		{Says: "installing bear@1091189122", Of: 2},
		{Says: "installing xcode@497799835", Done: 1, Of: 2},
		{Says: "installing", Done: 2, Of: 2},
	}
	if !slices.Equal(said, want) || !slices.Equal(told, []string{"Installed bear@1091189122", "Installed xcode@497799835"}) {
		t.Errorf("applying said %+v and told the log %q\nwant %+v, each installed", said, told, want)
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

// signedOut is what mas says when no one's signed in to the App Store.
const signedOut = "[ERROR] 2026/10/16 04:22:49 account is not signed in"

// installed is what mas list says of the apps named, by id.
func installed(ids ...int64) string {
	var b strings.Builder
	for _, id := range ids {
		fmt.Fprintf(&b, "{\"adamID\":%d,\"name\":\"App %d\"}\n", id, id)
	}
	return b.String()
}

// storeRun applies the App Store's apps, declared, after a step by hand
// signing in to it, done again as many times as it's asked, unless it
// can't be (no one to act): the App Store's result, and how many times
// signing in was done again.
func storeRun(t *testing.T, fake *runnertest.Fake, someone bool, declared config.List) (check.Result, int) {
	t.Helper()
	again := 0
	signIn := engine.Step{Name: "appstore", Before: []string{"mas"}, Check: func(context.Context) check.Result {
		return check.Result{State: check.OK, Summary: "done"}
	}}
	if someone {
		signIn.Again = func(context.Context) error {
			again++
			return nil
		}
	}
	p, err := engine.New(signIn, kind.Step(appstore.New(fake), declared))
	if err != nil {
		t.Fatal(err)
	}
	report, err := p.Apply(t.Context(), event.NewFanout(), engine.Options{Jobs: 1})
	if err != nil {
		t.Fatal(err)
	}
	return report.Results["mas"], again
}

// The first app's install is the check that someone's signed in: while mas
// says no one is, signing in, the step by hand before the App Store's, is
// done again, then the first's tried again; only once it's in, the rest,
// an app at a time.
func TestASignInNotDoneIsDoneAgain(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("mas", "list", "--json").Prints("").Then().Prints(installed(1091189122, 409203825, 497799835))
	fake.On("sudo", "-n", "mas", "install", "1091189122").Exits(1).PrintsToStderr(signedOut).
		Then().Exits(1).PrintsToStderr(signedOut).
		Then()
	fake.On("sudo", "-n", "mas", "install", "409203825")
	fake.On("sudo", "-n", "mas", "install", "497799835")
	res, again := storeRun(t, fake, true, declared("bear@1091189122", "numbers@409203825", "xcode@497799835"))
	if res.State != check.OK || again != 2 {
		t.Errorf("the App Store's apps stand %+v, signing in done again %d times; want them installed, it done again twice", res, again)
	}
	want := []string{
		"mas list --json",
		"sudo -n mas install 1091189122", "sudo -n mas install 1091189122", "sudo -n mas install 1091189122",
		"sudo -n mas install 409203825", "sudo -n mas install 497799835",
		"mas list --json",
	}
	if !slices.Equal(fake.Calls(), want) {
		t.Errorf("ran %q\nwant %q", fake.Calls(), want)
	}
}

// mas 7, with no one signed in, waits for good rather than saying so: the
// first app's install that runs past its minutes is taken as no one signed
// in, so signing in is done again, and the app's tried again.
func TestAHangIsTakenAsNoOneSignedIn(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("mas", "list", "--json").Prints("").Then().Prints(installed(1091189122, 497799835))
	fake.On("sudo", "-n", "mas", "install", "1091189122").Fails(fmt.Errorf("sudo -n mas install 1091189122: %w", context.DeadlineExceeded)).
		Then()
	fake.On("sudo", "-n", "mas", "install", "497799835")
	res, again := storeRun(t, fake, true, declared("bear@1091189122", "xcode@497799835"))
	if res.State != check.OK || again != 1 {
		t.Errorf("the App Store's apps stand %+v, signing in done again %d times; want them installed, it done again once", res, again)
	}
}

// Without anyone to sign in, a hang fails every app, in kit's words, naming
// the fix, never the command; the rest aren't tried.
func TestAHangWithNoOneToSignInFailsNamingTheFix(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("mas", "list", "--json").Prints("")
	fake.On("sudo", "-n", "mas", "install", "1091189122").Fails(fmt.Errorf("sudo -n mas install 1091189122: %w", context.DeadlineExceeded))
	res, _ := storeRun(t, fake, false, declared("bear@1091189122", "xcode@497799835"))
	want := "couldn't install bear@1091189122, xcode@497799835: the App Store didn't answer: sign in to the App Store, then try again"
	if res.State != check.Failed || res.Reason != want {
		t.Errorf("the App Store's apps stand %+v; want them failed, %q", res, want)
	}
}

// Another failure of an app's install fails that app alone, in kit's words,
// what mas said or how it ended, never the command: nothing's done again,
// and the next app's the check that someone's signed in, then the rest, an
// app at a time, none of them held up by it.
func TestAnInstallFailingOtherwiseFailsItAlone(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("mas", "list", "--json").Prints("").Then().Prints(installed(497799835))
	fake.On("sudo", "-n", "mas", "install", "1091189122").Exits(1).PrintsToStderr("[ERROR] 2026/10/16 04:22:49 no app has the id 1091189122")
	fake.On("sudo", "-n", "mas", "install", "409203825").Fails(errors.New("run sudo -n mas install 409203825: signal: terminated"))
	fake.On("sudo", "-n", "mas", "install", "497799835")
	res, again := storeRun(t, fake, true, declared("bear@1091189122", "numbers@409203825", "xcode@497799835"))
	want := "couldn't install bear@1091189122, numbers@409203825: [ERROR] 2026/10/16 04:22:49 no app has the id 1091189122; mas was stopped"
	if res.State != check.Failed || res.Reason != want || again != 0 {
		t.Errorf("the App Store's apps stand %+v, signing in done again %d times; want them failed, %q, nothing done again", res, again, want)
	}
	cmds := fake.Commands()
	for _, cmd := range cmds[1:3] {
		if cmd.Timeout > 10*time.Minute {
			t.Errorf("%s is given %v: want minutes, as no app's in yet to say someone's signed in", cmd, cmd.Timeout)
		}
	}
	if len(cmds) < 4 || cmds[3].String() != "sudo -n mas install 497799835" {
		t.Errorf("ran %q, want xcode tried after the others failed", fake.Calls())
	}
}

// With no one to sign in, as without a terminal, or outside a run, no one
// signed in fails the install at once, every app, naming the fix: the rest
// aren't tried.
func TestNoOneToSignInFailsNamingTheFix(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("mas", "list", "--json").Prints("")
	fake.On("sudo", "-n", "mas", "install", "1091189122").Exits(1).PrintsToStderr(signedOut)
	res, _ := storeRun(t, fake, false, declared("bear@1091189122", "numbers@409203825", "xcode@497799835"))
	want := "couldn't install bear@1091189122, numbers@409203825, xcode@497799835: no one's signed in: sign in to the App Store, then try again"
	if res.State != check.Failed || res.Reason != want {
		t.Errorf("the App Store's apps stand %+v; want them failed, %q", res, want)
	}
	err := appstore.New(fake).Install(t.Context(), []string{"bear@1091189122", "xcode@497799835"})
	if err == nil || err.Error() != "no one's signed in: sign in to the App Store, then try again" {
		t.Errorf("outside a run, Install = %v; want it failed, naming the fix", err)
	}
	if slices.ContainsFunc(fake.Calls(), func(c string) bool { return strings.HasSuffix(c, "497799835") }) {
		t.Errorf("ran %q; want the rest not tried", fake.Calls())
	}
}
