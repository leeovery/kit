package cli_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/kind/login"
	"github.com/leeovery/kit/internal/logs"
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
// step done by the time its card would go up doesn't go up.
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
	if started < 0 || len(cards(records)) > 0 {
		t.Fatalf("log = %+v, want the steps by hand checked, no card up, as the tool's in", records)
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
