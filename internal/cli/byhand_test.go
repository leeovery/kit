package cli_test

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/kind/login"
	"github.com/leeovery/kit/internal/logs"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/steps"
)

// lastLog is the run log the world's last run wrote.
func (w *world) lastLog(t *testing.T) []logs.Record {
	t.Helper()
	path, err := logs.Latest(filepath.Join(w.home, "Library", "Logs", "kit"))
	if err != nil {
		t.Fatal(err)
	}
	records, err := logs.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	return records
}

// cards are the cards of steps by hand a run's log says went up.
func cards(records []logs.Record) []logs.Record {
	var up []logs.Record
	for _, r := range records {
		if r.Event == "needs_you" {
			up = append(up, r)
		}
	}
	return up
}

// downs is the items of the cards taken down, in the order they came down.
func downs(records []logs.Record) []string {
	var down []string
	for _, r := range records {
		if r.Event == "card_down" {
			down = append(down, r.Item)
		}
	}
	return down
}

// signInWorld is a Mac with ripgrep missing, and a step by hand that comes
// after it: signing in to a tool, in a group, with notes saying why.
func signInWorld(t *testing.T) *world {
	t.Helper()
	w := missingWorld(t)
	w.writeSection(t, "laptop", "manual", "# The tool's commands need it.\n"+`sign-in "Sign in to the tool" --after brew --before gh --group "First things" --test test -f /signed-in`+"\n  Open the tool\n  Sign in\n")
	w.fake.On("brew", "update", "--quiet")
	w.fake.On("brew", "install", "--formula", "ripgrep")
	w.fake.On("test", "-f", "/signed-in").Exits(1)
	return w
}

// At a terminal, a step by hand come due goes up as a card: a notification
// posted, nothing opened, and the card in the run's log. Stopped, the run
// ends with it not done.
func TestApplyRaisesAStepByHand(t *testing.T) {
	w := signInWorld(t)
	w.terminal, w.size = true, [2]int{160, 64}
	kb := &keyboard{}
	w.listen = kb.listen
	w.fake.On("osascript", "-e", `display notification "Open the tool" with title "kit" subtitle "Sign in to the tool"`).Does(func() { kb.press(t, ask.Key{Name: "ctrl+c"}) })
	// osascript there, the login items are checked too: there are none.
	w.fake.On("osascript", "-l", "JavaScript", "-e", login.ReadScript).Prints("[]")

	if _, errOut, code := w.run(t, "apply"); code != 1 {
		t.Errorf("kit apply, stopped at the card, exit %d (%s), want 1", code, errOut)
	}
	up := cards(w.lastLog(t))
	if len(up) != 1 || up[0].Step != "sign-in" || up[0].Item != "sign-in" || up[0].Title != "Sign in to the tool" || !up[0].Tells || up[0].More != 0 {
		t.Errorf("cards logged = %+v, want sign-in's, kit telling when it's done", up)
	}
	if slices.ContainsFunc(w.fake.Calls(), func(c string) bool { return strings.HasPrefix(c, "open ") }) {
		t.Errorf("ran %q, want nothing opened", w.fake.Calls())
	}
}

// A card goes up once its grace is over, though kit's first look at it,
// as its turn came, is still under way, and its notification's posted once
// it's been up a while: kit's own waits for both, which time.After ends,
// end at once here.
func TestApplyRaisesACardOnceItsGraceIsOver(t *testing.T) {
	w := signInWorld(t)
	w.terminal, w.size = true, [2]int{160, 64}
	w.ended = []steps.Wait{steps.WaitToGoUp, steps.WaitToNotify}
	kb := &keyboard{}
	w.listen = kb.listen
	posted := make(chan struct{})
	// Its check, then the look as its turn comes, held till it's posted.
	w.fake.On("test", "-f", "/signed-in").Exits(1).Then().Exits(1).Does(func() {
		select {
		case <-posted:
		case <-time.After(5 * time.Second):
			t.Error("the card's notification wasn't posted while kit's first look at it was under way")
		}
	})
	w.fake.On("osascript", "-e", `display notification "Open the tool" with title "kit" subtitle "Sign in to the tool"`).Does(func() {
		close(posted)
		kb.press(t, ask.Key{Name: "ctrl+c"})
	})
	w.fake.On("osascript", "-l", "JavaScript", "-e", login.ReadScript).Prints("[]")

	if _, errOut, code := w.run(t, "apply"); code != 1 {
		t.Errorf("kit apply, stopped at the card, exit %d (%s), want 1", code, errOut)
	}
	if up := cards(w.lastLog(t)); len(up) != 1 || up[0].Item != "sign-in" {
		t.Errorf("cards logged = %+v, want sign-in's", up)
	}
}

// Where kit's app can be built, a run that raises cards builds it, once, in
// kit's data folder: the card's notification is posted through it, under an
// id of its step and item, a click bringing forward the terminal kit runs
// in, as the first run to send any there, and taken away as the run ends
// with the card not done.
func TestApplyNotifiesThroughKitsApp(t *testing.T) {
	w := signInWorld(t)
	w.terminal, w.size = true, [2]int{160, 64}
	w.env["__CFBundleIdentifier"] = "com.example.terminal"
	kb := &keyboard{}
	w.listen = kb.listen
	data := filepath.Join(w.home, ".local", "share", "kit")
	work := filepath.Join(data, ".kit.app-build")
	exe := filepath.Join(work, "kit.app", "Contents", "MacOS", "kit")
	app := filepath.Join(data, "kit.app")
	built := []runner.Command{
		{Name: "xcode-select", Args: []string{"-p"}},
		{Name: "swiftc", Args: []string{"-O", "-o", exe, filepath.Join(work, "kit.swift")}},
		{Name: "codesign", Args: []string{"-s", "-", "-f", filepath.Join(work, "kit.app")}},
	}
	at := w.now.UnixNano()
	notify := runner.Command{Name: "open", Args: []string{"-g", "-n", app, "--args", "notify", "--id", "card:sign-in:sign-in", "--title", "Sign in to the tool", "--body", "Open the tool", "--activate", "com.example.terminal", "--at", strconv.FormatInt(at, 10), "--run", "1"}}
	clear := runner.Command{Name: "open", Args: []string{"-g", "-n", app, "--args", "clear", "--id", "card:sign-in:sign-in", "--at", strconv.FormatInt(at+1, 10), "--run", "1"}}
	w.fake.On("xcode-select", "-p")
	w.fake.On("codesign", built[2].Args...)
	w.fake.On("swiftc", built[1].Args...).Does(func() {
		if err := os.WriteFile(exe, nil, 0o755); err != nil {
			t.Error(err)
		}
	})
	w.fake.On("open", notify.Args...).Does(func() { kb.press(t, ask.Key{Name: "ctrl+c"}) })
	w.fake.On("open", clear.Args...)

	if _, errOut, code := w.run(t, "apply"); code != 1 {
		t.Errorf("kit apply, stopped at the card, exit %d (%s), want 1", code, errOut)
	}
	var ran []string
	for _, c := range w.fake.Calls() {
		if strings.HasPrefix(c, "xcode-select") || strings.HasPrefix(c, "swiftc") || strings.HasPrefix(c, "codesign") || strings.HasPrefix(c, "open -g") {
			ran = append(ran, c)
		}
	}
	want := []string{built[0].String(), built[1].String(), built[2].String(), notify.String(), clear.String()}
	if !slices.Equal(ran, want) {
		t.Errorf("ran %q, want %q", ran, want)
	}
}

// A run stopped while kit's app is still being built ends without waiting
// for the build, which it stops, and posts no notification: none waiting
// for the build goes through osascript in its place.
func TestApplyEndsWhileKitsAppIsBuilt(t *testing.T) {
	w := signInWorld(t)
	w.terminal, w.size = true, [2]int{160, 64}
	kb := &keyboard{}
	w.listen = kb.listen
	work := filepath.Join(w.home, ".local", "share", "kit", ".kit.app-build")
	w.fake.On("xcode-select", "-p")
	w.fake.On("swiftc", "-O", "-o", filepath.Join(work, "kit.app", "Contents", "MacOS", "kit"), filepath.Join(work, "kit.swift")).Hangs()
	var stop sync.Once
	w.fake.On("test", "-f", "/signed-in").Exits(1).Then().Exits(1).Does(func() {
		stop.Do(func() { kb.press(t, ask.Key{Name: "ctrl+c"}) })
	})

	if _, errOut, code := w.run(t, "apply"); code != 1 {
		t.Errorf("kit apply, stopped at the card, exit %d (%s), want 1", code, errOut)
	}
	calls := w.fake.Calls()
	if !slices.ContainsFunc(calls, func(c string) bool { return strings.HasPrefix(c, "swiftc ") }) {
		t.Errorf("ran %q, want kit's app being built", calls)
	}
	if slices.ContainsFunc(calls, func(c string) bool { return strings.HasPrefix(c, "open ") || strings.HasPrefix(c, "osascript -e") }) {
		t.Errorf("ran %q, want no notification posted", calls)
	}
}

// Without a terminal, no one's there to act: nothing's raised, and the step
// by hand stands not done.
func TestApplyPlainRaisesNothing(t *testing.T) {
	w := signInWorld(t)
	out, _, _ := w.run(t, "apply")
	if !strings.Contains(out, "sign-in attention not done\n") || len(cards(w.lastLog(t))) > 0 || slices.Contains(w.fake.Calls(), "open -a Tool") {
		t.Errorf("kit apply printed\n%s\nran %q: want sign-in not done, nothing raised", out, w.fake.Calls())
	}
}

// At a terminal, the steps by hand with no place of their own come last,
// once every other step has finished, but a step that comes after them; a
// step done by the time its turn comes never goes up, nor posts a
// notification.
func TestApplyRaisesTheRestLast(t *testing.T) {
	w := missingWorld(t)
	w.writeSection(t, "laptop", "manual", `tool "Install the tool" --test test -d /Applications/Tool.app`+"\n")
	w.writeSection(t, "laptop", "steps", `tidy "Tidies up after" --after manual`+"\n")
	run := w.stepScript(t, "laptop", "tidy")
	w.fake.On(run, "check").Prints("tidy\n")
	w.fake.On("brew", "update", "--quiet")
	w.fake.On("brew", "install", "--formula", "ripgrep")
	w.fake.On("test", "-d", "/Applications/Tool.app").Exits(1).Then()
	w.terminal, w.size = true, [2]int{160, 64}
	w.choose = func(string, []string) (int, error) { return 1, nil }

	if _, errOut, code := w.run(t, "apply"); code == 2 {
		t.Fatalf("kit apply exit 2: %s", errOut)
	}
	records := w.lastLog(t)
	started := slices.IndexFunc(records, func(r logs.Record) bool { return r.Event == "step_started" && r.Step == "manual" })
	finished := slices.IndexFunc(records, func(r logs.Record) bool { return r.Event == "step_finished" && r.Step == "manual" })
	if started < 0 || len(cards(records)) > 0 || len(downs(records)) > 0 {
		t.Fatalf("log = %+v, want the steps by hand checked, the tool's card never up, as the tool's in", records)
	}
	for _, c := range w.fake.Calls() {
		if strings.HasPrefix(c, "osascript") {
			t.Errorf("ran %q: want no notification for a card found done as it went up", c)
		}
	}
	for i, r := range records {
		switch {
		case r.Event != "step_finished" || r.Step == "manual":
		case r.Step == "tidy" && i < finished:
			t.Errorf("tidy finished before the steps by hand, want it after them")
		case r.Step != "tidy" && i > started:
			t.Errorf("%s finished after the steps by hand started, want them last", r.Step)
		}
	}
}
