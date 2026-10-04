package ask

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func press(c chooser, keys ...tea.KeyPressMsg) (chooser, tea.Cmd) {
	var cmd tea.Cmd
	var m tea.Model = c
	for _, k := range keys {
		m, cmd = m.Update(k)
	}
	return m.(chooser), cmd
}

var (
	down  = tea.KeyPressMsg{Code: tea.KeyDown}
	up    = tea.KeyPressMsg{Code: tea.KeyUp}
	j     = tea.KeyPressMsg{Code: 'j', Text: "j"}
	enter = tea.KeyPressMsg{Code: tea.KeyEnter}
	esc   = tea.KeyPressMsg{Code: tea.KeyEscape}
)

func TestChooserMovesAndTakes(t *testing.T) {
	c := newChooser("Group for jq?", []string{"To be sorted", "Shell", "Git"})
	c, cmd := press(c, down, j, down, up)
	if c.cursor != 1 || cmd != nil {
		t.Fatalf("after down, j, down (at the end), up: cursor %d, want 1", c.cursor)
	}
	c, _ = press(c, up, up)
	if c.cursor != 0 {
		t.Errorf("up past the top: cursor %d, want 0", c.cursor)
	}
	c, cmd = press(c, down, enter)
	if !c.chosen || c.cursor != 1 || cmd == nil {
		t.Errorf("enter: chosen %v, cursor %d, quitting %v; want Shell taken", c.chosen, c.cursor, cmd != nil)
	}
	if got := c.View().Content; !strings.Contains(got, "Group for jq?") || !strings.Contains(got, "Shell") || strings.Contains(got, "Git") {
		t.Errorf("once taken, the view is %q, want the question and the answer alone", got)
	}
}

func TestChooserCancels(t *testing.T) {
	for _, k := range []tea.KeyPressMsg{esc, {Code: 'q', Text: "q"}} {
		c, cmd := press(newChooser("Group?", []string{"Shell"}), k)
		if !c.cancelled || cmd == nil || !strings.Contains(c.View().Content, "cancelled") {
			t.Errorf("%s: cancelled %v, quitting %v, view %q; want it cancelled", k, c.cancelled, cmd != nil, c.View().Content)
		}
	}
}

func TestChooserShowsTheOptions(t *testing.T) {
	c, _ := press(newChooser("Group for jq?", []string{"To be sorted", "Shell"}), down)
	got := c.View().Content
	for _, want := range []string{"Group for jq?", "  To be sorted", "› Shell", "enter to take"} {
		if !strings.Contains(got, want) {
			t.Errorf("view %q lacks %q", got, want)
		}
	}
}
