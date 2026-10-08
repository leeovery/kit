package ask

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/look"
)

func press(m tea.Model, keys ...tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	for _, k := range keys {
		m, cmd = m.Update(k)
	}
	return m, cmd
}

func view(m tea.Model) string { return ansi.Strip(m.View().Content) }

var (
	down  = tea.KeyPressMsg{Code: tea.KeyDown}
	up    = tea.KeyPressMsg{Code: tea.KeyUp}
	j     = tea.KeyPressMsg{Code: 'j', Text: "j"}
	q     = tea.KeyPressMsg{Code: 'q', Text: "q"}
	enter = tea.KeyPressMsg{Code: tea.KeyEnter}
	esc   = tea.KeyPressMsg{Code: tea.KeyEscape}
)

func question(name string) Question {
	return Question{
		About:   look.Row{State: look.NeedsYou, Name: name, Says: look.Says(look.Muted("cask"), look.Orange("installed, not declared"))},
		Answers: []look.Choice{{Label: "Adopt", Does: "declare it"}, {Label: "Remove", Does: "uninstall it"}, {Label: "Skip", Does: "not now"}},
	}
}

// One question: its row, its answers under it, the cursor moving among them;
// once answered, its screen empty, to go (Choose then writes the row with
// the answer taken).
func TestChooseMovesAndTakes(t *testing.T) {
	m, cmd := press(newWalk("", []Question{question("zoom")}), down, j, down, up)
	if w := m.(walk); w.cursor != 1 || cmd != nil {
		t.Fatalf("after down, j, down (at the end), up: cursor %d, want 1", w.cursor)
	}
	want := "\n  ▲ zoom  cask · installed, not declared\n  │   Adopt  declare it\n  │ ❯ Remove  uninstall it\n  │   Skip  not now\n\n  ↑↓ choose · enter decide · esc cancel\n"
	if got := view(m); got != want {
		t.Errorf("view =\n%s\nwant\n%s", got, want)
	}
	m, cmd = press(m, enter)
	if w := m.(walk); !slices.Equal(w.answers, []int{1}) || cmd == nil {
		t.Errorf("enter: answers %v, quitting %v; want Remove taken", w.answers, cmd != nil)
	}
	if got := view(m); got != "\n" {
		t.Errorf("once taken, the view is %q, want it empty: its screen goes", got)
	}
}

func TestChooseCancels(t *testing.T) {
	for _, k := range []tea.Msg{esc, q} {
		m, cmd := press(newWalk("", []Question{question("zoom")}), k)
		if !m.(walk).cancelled || cmd == nil || view(m) != "\n" {
			t.Errorf("%v: view %q; want it cancelled", k, view(m))
		}
	}
}

// Several questions: the header counting them, those answered with their
// answer, the one asked with its answers, those to come waiting; q stops,
// keeping what's answered; nothing stays on screen.
func TestWalk(t *testing.T) {
	w := newWalk("Reconcile", []Question{question("ffmpeg"), question("zoom"), question("wget")})
	w.lead = []string{"  the heading"}
	m, _ := press(w, enter)
	want := "\n  the heading\n\n  RECONCILE  2 of 3\n  ● ffmpeg  adopt\n  ▲ zoom  cask · installed, not declared\n  │ ❯ Adopt  declare it\n  │   Remove  uninstall it\n  │   Skip  not now\n  ○ wget  cask · installed, not declared\n\n  ↑↓ choose · enter decide · q stop · esc cancel\n"
	if got := view(m); got != want {
		t.Errorf("view =\n%s\nwant\n%s", got, want)
	}
	m, cmd := press(m, down, enter, q)
	if w := m.(walk); !w.stopped || !slices.Equal(w.answers, []int{0, 1}) || cmd == nil || view(m) != "\n  the heading\n\n" {
		t.Errorf("stopped: %+v, view %q", w, view(m))
	}
}

// A secret is typed or pasted, a dot a character, never shown.
func TestSecretField(t *testing.T) {
	m, _ := press(field{about: look.Row{State: look.NeedsYou, Name: "NPM_TOKEN", Says: look.Orange("needs its value")}, width: 80},
		tea.KeyPressMsg{Code: 'a', Text: "a"}, tea.PasteMsg{Content: "bcd\n"}, tea.KeyPressMsg{Code: tea.KeyBackspace})
	got := view(m)
	if want := "\n  ▲ NPM_TOKEN  needs its value\n  │ ❯ ••• \n\n  enter done · esc cancel\n"; got != want || strings.Contains(got, "abc") {
		t.Errorf("view =\n%q\nwant\n%q", got, want)
	}
	m, _ = press(m, enter)
	if f := m.(field); string(f.typed) != "abc" || !f.done || view(m) != "\n" {
		t.Errorf("entered: %q, done %v, view %q", string(f.typed), f.done, view(m))
	}
}
