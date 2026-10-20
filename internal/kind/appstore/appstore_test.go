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
	"github.com/leeovery/kit/internal/runner"
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

// An app the App Store installed that mas doesn't list yet, as Spotlight
// hasn't indexed it, is found, declared and missing, in the Applications
// folders by the store's receipt it carries, its id looked up by its bundle
// id, once: what the store says is kept, that it has none for a day. With
// nothing declared missing, nothing's looked for; an app mas lists isn't
// looked up, nor one without a receipt, as a download's.
func TestAnAppMasDoesntListYetIsFound(t *testing.T) {
	dir := t.TempDir()
	bundle := func(name, id string, receipt bool) {
		t.Helper()
		app := filepath.Join(dir, name+".app", "Contents")
		files := map[string]string{"Info.plist": `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>` + id + `</string></dict></plist>`}
		if receipt {
			files[filepath.Join("_MASReceipt", "receipt")] = "a receipt"
		}
		for name, content := range files {
			if err := os.MkdirAll(filepath.Dir(filepath.Join(app, name)), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(app, name), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	bundle("Xcode", "com.apple.dt.Xcode", true)
	bundle("Amphetamine", "com.example.amphetamine", true)
	bundle("Downloaded", "com.example.downloaded", false)
	bundle("Delisted", "com.example.delisted", true)
	fake := runnertest.New(t)
	fake.On("mas", "list", "--json").Prints(fixture(t, "list.jsonl"))
	fake.On("mas", "lookup", "--json", "--bundle", "com.example.amphetamine").Prints(`{"adamID":937984704,"bundleID":"com.example.amphetamine","name":"Amphetamine"}` + "\n")
	fake.On("mas", "lookup", "--json", "--bundle", "com.example.delisted").Exits(1).PrintsToStderr("Error: No apps found in the App Store for bundle ID com.example.delisted")
	store := appstore.New(fake)
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	store.AppDirs, store.StateDir, store.Now = []string{dir}, t.TempDir(), func() time.Time { return at }
	lookups := func() []string {
		var out []string
		for _, c := range fake.Calls() {
			if strings.HasPrefix(c, "mas lookup") {
				out = append(out, c)
			}
		}
		return out
	}

	kind.Compare(t.Context(), store, declared("xcode@497799835"))
	if got := lookups(); len(got) != 0 {
		t.Errorf("with nothing declared missing, it looked up %q", got)
	}
	for range 2 {
		res := kind.Compare(t.Context(), store, declared("xcode@497799835", "amphetamine@937984704"))
		if slices.ContainsFunc(res.Items, func(it check.Item) bool { return it.Name == "amphetamine@937984704" }) {
			t.Errorf("Compare() = %+v; want the app mas doesn't list yet found", res.Items)
		}
	}
	want := []string{"mas lookup --json --bundle com.example.amphetamine", "mas lookup --json --bundle com.example.delisted"}
	if got := lookups(); !slices.Equal(got, want) {
		t.Errorf("looked up %q; want %q, once each", got, want)
	}
	at = at.Add(25 * time.Hour)
	kind.Compare(t.Context(), store, declared("xcode@497799835", "amphetamine@937984704", "other@1"))
	if got := lookups(); !slices.Equal(got, append(want, "mas lookup --json --bundle com.example.delisted")) {
		t.Errorf("a day on, looked up %q; want the one the store hadn't asked again, alone", got)
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
	for _, cmd := range fake.Commands()[:3] {
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

// stray is how pgrep finds what's left of kit's install of the app id, in
// its process group, 4242.
func stray(id string) []string {
	return []string{"pgrep", "-g", "4242", "-f", "^(/[^ ]*/)?mas install " + id + "$"}
}

// mas 7, with no one signed in, waits for good, saying nothing, rather than
// saying so: the first app's install that says nothing at all for its
// minutes is taken as no one signed in, so what's left of it, in its process
// group, is stopped, signing in is done again, and the app's tried again.
func TestAHangIsTakenAsNoOneSignedIn(t *testing.T) {
	defer appstore.SetFirstWord(50 * time.Millisecond)()
	fake := runnertest.New(t)
	fake.On("mas", "list", "--json").Prints("").Then().Prints(installed(1091189122, 497799835))
	fake.On("sudo", "-n", "mas", "install", "1091189122").Hangs().InGroup(4242).
		Then()
	fake.On(stray("1091189122")[0], stray("1091189122")[1:]...).Prints("5151\n")
	fake.On("sudo", "-n", "kill", "-KILL", "5151")
	fake.On("sudo", "-n", "mas", "install", "497799835")
	res, again := storeRun(t, fake, true, declared("bear@1091189122", "xcode@497799835"))
	if res.State != check.OK || again != 1 {
		t.Errorf("the App Store's apps stand %+v, signing in done again %d times; want them installed, it done again once", res, again)
	}
	want := []string{
		"mas list --json",
		"sudo -n mas install 1091189122", "pgrep -g 4242 -f '^(/[^ ]*/)?mas install 1091189122$'", "sudo -n kill -KILL 5151",
		"sudo -n mas install 1091189122", "sudo -n mas install 497799835",
		"mas list --json",
	}
	if !slices.Equal(fake.Calls(), want) {
		t.Errorf("ran %q\nwant %q", fake.Calls(), want)
	}
}

// slowRunner is the fake, but mas installing an app says it's downloading
// it, then nothing, as through a pipe it draws no progress, for a while,
// then that it's installed it.
type slowRunner struct {
	*runnertest.Fake
	for_ time.Duration
}

func (r slowRunner) Run(ctx context.Context, cmd runner.Command) (runner.Result, error) {
	if !slices.Contains(cmd.Args, "install") {
		return r.Fake.Run(ctx, cmd)
	}
	cmd.Lines("==> Downloading Xcode")
	select {
	case <-ctx.Done():
		return runner.Result{ExitCode: -1}, fmt.Errorf("%s: %w", cmd, ctx.Err())
	case <-time.After(r.for_):
	}
	cmd.Lines("==> Installed Xcode")
	return runner.Result{}, nil
}

// A first app mas has said it's downloading takes as long as it takes,
// though mas says nothing more: only saying nothing at all for the minutes
// is a hang.
func TestASlowFirstDownloadIsntAHang(t *testing.T) {
	defer appstore.SetFirstWord(50 * time.Millisecond)()
	slow := slowRunner{Fake: runnertest.New(t), for_: 300 * time.Millisecond}
	if err := appstore.New(slow).Install(t.Context(), []string{"xcode@497799835"}); err != nil {
		t.Errorf("Install = %v, want Xcode installed, however long it took", err)
	}
}

// Without anyone to sign in, a hang fails every app, in kit's words, naming
// the fix, never the command; the rest aren't tried.
func TestAHangWithNoOneToSignInFailsNamingTheFix(t *testing.T) {
	defer appstore.SetFirstWord(50 * time.Millisecond)()
	fake := runnertest.New(t)
	fake.On("mas", "list", "--json").Prints("")
	fake.On("sudo", "-n", "mas", "install", "1091189122").Hangs().InGroup(4242)
	fake.On(stray("1091189122")[0], stray("1091189122")[1:]...).Exits(1)
	res, _ := storeRun(t, fake, false, declared("bear@1091189122", "xcode@497799835"))
	want := "couldn't install bear@1091189122, xcode@497799835: the App Store didn't answer: sign in to the App Store, then try again"
	if res.State != check.Failed || res.Reason != want {
		t.Errorf("the App Store's apps stand %+v; want them failed, %q", res, want)
	}
}

// Another failure of an app's install fails that app alone, in kit's words,
// what mas said or how it ended, never the command, what's left of one kit
// ended stopped: nothing's done again, and the next app's the check that
// someone's signed in, then the rest, an app at a time, none of them held
// up by it. A sign-in not done then fails what's left, with what failed.
func TestAnInstallFailingOtherwiseFailsItAlone(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("mas", "list", "--json").Prints("").Then().Prints(installed(497799835))
	fake.On("sudo", "-n", "mas", "install", "1091189122").Exits(1).PrintsToStderr("[ERROR] 2026/10/16 04:22:49 no app has the id 1091189122")
	fake.On("sudo", "-n", "mas", "install", "409203825").Fails(errors.New("run sudo -n mas install 409203825: signal: terminated"))
	fake.On("sudo", "-n", "mas", "install", "497799835")
	fake.On("sudo", "-n", "mas", "install", "1295203466").Fails(fmt.Errorf("sudo -n mas install 1295203466: %w", context.DeadlineExceeded)).InGroup(4242)
	fake.On(stray("1295203466")[0], stray("1295203466")[1:]...).Prints("5151\n")
	fake.On("sudo", "-n", "kill", "-KILL", "5151")
	res, again := storeRun(t, fake, true, declared("bear@1091189122", "numbers@409203825", "xcode@497799835", "windows-app@1295203466"))
	want := "couldn't install bear@1091189122, numbers@409203825, windows-app@1295203466: [ERROR] 2026/10/16 04:22:49 no app has the id 1091189122; mas was stopped; mas didn't finish in 90 minutes"
	if res.State != check.Failed || res.Reason != want || again != 0 {
		t.Errorf("the App Store's apps stand %+v, signing in done again %d times; want them failed, %q, nothing done again", res, again, want)
	}
	if calls := fake.Calls(); slices.Index(calls, "sudo -n mas install 497799835") < slices.Index(calls, "sudo -n kill -KILL 5151") {
		t.Errorf("ran %q, want xcode tried after the others failed, and what's left of the one kit ended stopped", calls)
	}

	fake = runnertest.New(t)
	fake.On("mas", "list", "--json").Prints("")
	fake.On("sudo", "-n", "mas", "install", "1091189122").Exits(1).PrintsToStderr("[ERROR] 2026/10/16 04:22:49 no app has the id 1091189122")
	fake.On("sudo", "-n", "mas", "install", "497799835").Exits(1).PrintsToStderr(signedOut)
	res, _ = storeRun(t, fake, false, declared("bear@1091189122", "xcode@497799835"))
	want = "couldn't install bear@1091189122, xcode@497799835: [ERROR] 2026/10/16 04:22:49 no app has the id 1091189122; no one's signed in: sign in to the App Store, then try again"
	if res.Reason != want {
		t.Errorf("the App Store's apps stand %+v; want them failed, %q", res, want)
	}
}

// Each app waits for the password, should kit be asking for it again, as
// once sudo's forgotten it: refused, the rest aren't tried.
func TestEachAppWaitsForThePassword(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("mas", "list", "--json").Prints("")
	fake.On("sudo", "-n", "mas", "install", "1091189122")
	pw := &answers{true, true, false}
	s := kind.Placed(appstore.New(fake), declared("bear@1091189122", "xcode@497799835"), func(context.Context) kind.Placing { return kind.Placing{Admin: pw} })
	if err := s.Apply(t.Context(), s.Check(t.Context())); err != nil {
		t.Fatal(err)
	}
	if want := []string{"mas list --json", "sudo -n mas install 1091189122"}; !slices.Equal(fake.Calls(), want) {
		t.Errorf("ran %q, want Bear installed, Xcode not tried once the password was refused", fake.Calls())
	}
}

// answers is a kind.Password not held, answered each time it's waited for
// as the next of answers.
type answers []bool

func (*answers) Held() bool { return false }

func (a *answers) Wait(context.Context) bool {
	next := (*a)[0]
	*a = (*a)[1:]
	return next
}

func (a *answers) Forgotten(ctx context.Context) bool { return a.Wait(ctx) }

// lapsing is a kind.Password held till sudo forgets it, then given again,
// or not, as again says.
type lapsing struct {
	held, again bool
	forgot      int
}

func (l *lapsing) Held() bool                { return l.held }
func (l *lapsing) Wait(context.Context) bool { return l.held }

func (l *lapsing) Forgotten(context.Context) bool {
	l.forgot++
	l.held = l.again
	return l.again
}

// sudoForgot is what sudo -n says once it's forgotten the password.
const sudoForgot = "sudo: a password is required"

// An app whose sudo says it's forgotten the password, as when the Mac slept,
// has kit find out at once and ask again, then is tried once more; not
// given, it waits for the password, in kit's words, and the rest aren't
// tried.
func TestAForgottenPasswordIsAskedForAgain(t *testing.T) {
	for _, again := range []bool{true, false} {
		fake := runnertest.New(t)
		fake.On("mas", "list", "--json").Prints("")
		fake.On("sudo", "-n", "mas", "install", "1091189122").Exits(1).PrintsToStderr(sudoForgot).Then()
		fake.On("sudo", "-n", "mas", "install", "497799835")
		pw := &lapsing{held: true, again: again}
		s := kind.Placed(appstore.New(fake), declared("bear@1091189122", "xcode@497799835"), func(context.Context) kind.Placing { return kind.Placing{Admin: pw} })
		err := s.Apply(t.Context(), s.Check(t.Context()))
		want := []string{"mas list --json", "sudo -n mas install 1091189122", "sudo -n mas install 1091189122", "sudo -n mas install 497799835"}
		if !again {
			want = []string{"mas list --json", "sudo -n mas install 1091189122", "mas list --json"}
		}
		if !slices.Equal(fake.Calls(), want) || pw.forgot != 1 {
			t.Errorf("given again %v: ran %q, kit found out %d times; want %q, once", again, fake.Calls(), pw.forgot, want)
		}
		if wantErr := "couldn't install bear@1091189122, xcode@497799835: needs an administrator's password: run kit apply at a terminal"; again && err != nil || !again && (err == nil || err.Error() != wantErr) {
			t.Errorf("given again %v: Apply = %v; want it installed, or %q", again, err, wantErr)
		}
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
