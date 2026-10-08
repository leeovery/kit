package cli_test

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/ask"
)

var driftFile = filepath.Join(".local", "state", "kit", "drift.json")

func TestReconcileListsTheDrift(t *testing.T) {
	out, _, code := laptopWorld(t).run(t, "reconcile")
	want := `kit reconcile · laptop
brew:ffmpeg extra since 31 Dec: --adopt, --remove or --snooze
brew:node@20 unused-dependency since 31 Dec: --remove, --adopt or --snooze
cask:firefox extra since 31 Dec: --adopt, --remove or --snooze
`
	if out != want || code != 1 {
		t.Errorf("kit reconcile printed\n%s exit %d\nwant\n%s(exit 1)", out, code, want)
	}
}

func TestReconcileJSON(t *testing.T) {
	out, _, code := laptopWorld(t).run(t, "reconcile", "--json")
	var doc struct {
		Schema  int    `json:"schema"`
		Machine string `json:"machine"`
		Items   []struct {
			ID      string   `json:"id"`
			Kind    string   `json:"kind"`
			Name    string   `json:"name"`
			State   string   `json:"state"`
			Choices []string `json:"choices"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || code != 1 {
		t.Fatalf("kit reconcile --json printed %q, exit %d: %v", out, code, err)
	}
	if doc.Schema != 1 || doc.Machine != "laptop" || len(doc.Items) != 3 {
		t.Fatalf("document = %+v", doc)
	}
	if it := doc.Items[1]; it.ID != "brew:node@20" || it.Kind != "brew" || it.State != "unused-dependency" || !slices.Equal(it.Choices, []string{"remove", "adopt", "snooze"}) {
		t.Errorf("second item = %+v", it)
	}
}

func TestReconcileAdoptsByID(t *testing.T) {
	w := laptopWorld(t)
	w.expectSync([]string{"laptop/declarations"}, "kit reconcile (laptop): adopt brew:ffmpeg: for screen recordings")
	out, errOut, code := w.run(t, "reconcile", "brew:ffmpeg", "--adopt", "--note", "for screen recordings")
	if !strings.Contains(out, "brew:ffmpeg ok already installed; declared in laptop\n") || code != 0 {
		t.Errorf("kit reconcile printed\n%s%s exit %d", out, errOut, code)
	}
	if got := w.readSection(t, "laptop", "homebrew formulae"); got != "ffmpeg   # for screen recordings\ngo\n" {
		t.Errorf("laptop = %q", got)
	}
}

// Several items, one decision: one run, and one commit.
func TestReconcileAdoptsSeveralInOneCommit(t *testing.T) {
	w := laptopWorld(t)
	w.expectSync([]string{"laptop/declarations"}, "kit reconcile (laptop): adopt brew:ffmpeg, adopt cask:firefox")
	out, errOut, code := w.run(t, "reconcile", "brew:ffmpeg", "cask:firefox", "--adopt")
	if code != 0 || !strings.Contains(out, "kit-config ok committed and pushed laptop/declarations\n") {
		t.Errorf("kit reconcile printed\n%s%s exit %d", out, errOut, code)
	}
	if got := w.readSection(t, "laptop", "homebrew formulae"); got != "ffmpeg\ngo\n" {
		t.Errorf("laptop = %q", got)
	}
	if got := w.readSection(t, "laptop", "homebrew casks"); got != "firefox\n" {
		t.Errorf("laptop = %q", got)
	}
	_, errOut, code = w.run(t, "reconcile", "brew:node@20", "brew:nosuch", "--remove")
	if errOut != "kit: no item brew:nosuch: kit reconcile lists them\n" || code != 2 {
		t.Errorf("an unknown id among several: %q, exit %d; want nothing done", errOut, code)
	}
	for _, c := range w.fake.Calls() {
		if strings.Contains(c, "uninstall") {
			t.Errorf("ran %q, want nothing done when an id isn't an item", c)
		}
	}
}

func TestReconcileRemovesByID(t *testing.T) {
	w := laptopWorld(t)
	w.fake.On("brew", "uninstall", "--formula", "node@20")
	out, _, code := w.run(t, "reconcile", "brew:node@20", "--remove")
	if !strings.Contains(out, "brew:node@20 ok uninstalled; it wasn't declared\n") || !strings.Contains(out, "kit-config ok nothing changed\n") || code != 0 {
		t.Errorf("kit reconcile printed\n%s exit %d", out, code)
	}
}

func TestReconcileSnoozesByID(t *testing.T) {
	w := laptopWorld(t)
	out, _, code := w.run(t, "reconcile", "cask:firefox", "--snooze")
	if !strings.Contains(out, "cask:firefox ok snoozed till 9 Jan\n") || code != 0 {
		t.Errorf("kit reconcile printed\n%s exit %d", out, code)
	}
	if got := w.read(t, driftFile); !strings.Contains(got, `"cask:firefox": "2026-01-09T03:04:05Z"`) {
		t.Errorf("drift.json = %s, want firefox snoozed a week", got)
	}
	out, _, _ = w.run(t, "status")
	if !strings.Contains(out, "cask ok 1 declared, all installed\n") || !strings.Contains(out, "cask extra:snoozed firefox\n") {
		t.Errorf("kit status after the snooze printed\n%s\nwant firefox quiet", out)
	}
}

func TestReconcileHasNoGroups(t *testing.T) {
	w := laptopWorld(t)
	if _, errOut, code := w.run(t, "reconcile", "brew:ffmpeg", "--adopt", "--group", "Media"); errOut != "kit: unknown flag: --group\n" || code != 2 {
		t.Errorf("kit reconcile --group: %q, exit %d; want the flag unknown", errOut, code)
	}
}

func TestReconcileRefusesWhatItCant(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{args: []string{"brew:ffmpeg"}, want: "kit: say what to do with brew:ffmpeg: --adopt, --remove or --snooze\n"},
		{args: []string{"brew:ffmpeg", "--install"}, want: "kit: brew:ffmpeg can't be installed: --adopt, --remove or --snooze\n"},
		{args: []string{"brew:ffmpeg", "--adopt", "--remove"}, want: "kit: one thing at a time: --adopt and --remove\n"},
		{args: []string{"brew:nosuch", "--snooze"}, want: "kit: no item brew:nosuch: kit reconcile lists them\n"},
	}
	for _, tt := range tests {
		w := laptopWorld(t)
		_, errOut, code := w.run(t, append([]string{"reconcile"}, tt.args...)...)
		if errOut != tt.want || code != 2 {
			t.Errorf("kit reconcile %q printed %q, exit %d; want %q, exit 2", tt.args, errOut, code, tt.want)
		}
	}
}

// answers answers kit's questions by the first of answers whose question
// holds its key.
func answers(t *testing.T, answers map[string]string) func(string, []string) (int, error) {
	return func(question string, options []string) (int, error) {
		for key, answer := range answers {
			if strings.Contains(question, key) {
				if answer == "stop" {
					return 0, ask.ErrStopped
				}
				if i := slices.Index(options, answer); i >= 0 {
					return i, nil
				}
				t.Errorf("%q isn't among %q", answer, options)
			}
		}
		t.Errorf("no answer for %q", question)
		return 0, nil
	}
}

func TestReconcileAtATerminalAsksFirstThenDoesItAll(t *testing.T) {
	w := laptopWorld(t)
	w.terminal = true
	w.choose = answers(t, map[string]string{
		"ffmpeg  brew":  "Adopt",
		"node@20  brew": "Remove",
		"firefox  cask": "Skip",
	})
	w.fake.On("brew", "uninstall", "--formula", "node@20")
	w.expectSync([]string{"laptop/declarations"}, "kit reconcile (laptop): adopt brew:ffmpeg, remove brew:node@20")

	if _, errOut, code := w.run(t, "reconcile"); code != 0 {
		t.Fatalf("kit reconcile exit %d: %s", code, errOut)
	}
	wantAsked := []string{
		"ffmpeg  brew · installed, not declared · 2 days",
		"node@20  brew · installed for something since removed, needed by nothing · 2 days",
		"firefox  cask · installed, not declared · 2 days",
	}
	if !slices.Equal(w.asked, wantAsked) {
		t.Errorf("asked\n%s\nwant\n%s", strings.Join(w.asked, "\n"), strings.Join(wantAsked, "\n"))
	}
	if got := w.readSection(t, "laptop", "homebrew formulae"); got != "ffmpeg\ngo\n" {
		t.Errorf("laptop = %q", got)
	}
}

func TestReconcileStoppedEarlyDoesNothing(t *testing.T) {
	w := laptopWorld(t)
	w.terminal = true
	w.choose = answers(t, map[string]string{"ffmpeg  brew": "stop"})
	out, _, code := w.run(t, "reconcile")
	if !strings.HasSuffix(out, "  ● Nothing decided\n") || code != 0 {
		t.Errorf("kit reconcile printed %q, exit %d", out, code)
	}
}

// Naming a kind narrows reconcile to that kind's items.
func TestReconcileOneKind(t *testing.T) {
	out, _, code := laptopWorld(t).run(t, "reconcile", "cask")
	want := "kit reconcile · laptop\ncask:firefox extra since 31 Dec: --adopt, --remove or --snooze\n"
	if out != want || code != 1 {
		t.Errorf("kit reconcile cask printed\n%s exit %d\nwant\n%s", out, code, want)
	}
}
