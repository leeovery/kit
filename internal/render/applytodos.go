package render

import (
	"fmt"
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/look"
)

// At the run's end, what only the person at the Mac can do is worked
// through on applying's screen, one to-do at a time, in kit's order
// (kit-look, round 12): the left pane lists them, grouped, the one up a
// pill; the right has the one up as a panel, ruled off over the log, as a
// card's is; the state line counts what's left and done. Once they're
// over, kit carries on by itself, the list ticked, the log the whole side.

// The keys at the left pane's foot while the to-dos have it: q stops there
// while they're being worked through; once they're over, or stopped there,
// it stops the run.
var (
	toDoKeys     = []look.Key{{Key: "q", Does: "stop here"}}
	toDoOverKeys = []look.Key{{Key: "q", Does: "stop"}}
)

// toDoState is the state line while the to-dos are up: how many are left,
// then how many are done once some are; once they're over, what kit's
// finishing, the first still settling, if any, else that it's carrying on.
func (p *Pretty) toDoState() string {
	if p.todos.Up < 0 {
		if i := slices.IndexFunc(p.todos.List, func(t event.ToDo) bool { return t.Settling }); i >= 0 {
			return look.Cyan("◐ finishing " + p.todos.List[i].Title)
		}
		return look.Cyan("◐ carrying on")
	}
	done := 0
	for _, t := range p.todos.List {
		if t.Done {
			done++
		}
	}
	parts := []string{look.White(fmt.Sprintf("△ %d to do", len(p.todos.List)-done))}
	if done > 0 {
		parts = append(parts, look.Pink(fmt.Sprintf("● %d done", done)))
	}
	return look.Says(parts...)
}

// toDoPane is the left pane while the to-dos are up, w wide and room lines:
// To do and how many; what they are, and how to work through them
// (toDoLead); then each group, its name muted after a blank line,
// none for those in no group, its to-dos a line each: done, the done mark
// and its title muted; left for later, the skipped mark and its title
// muted; the one up, its title a pill; the rest white. The
// keys at its foot. Too many for the room, it starts further down, to keep
// the one up in sight.
func (p *Pretty) toDoPane(w, room int) []string {
	lines := []string{"", look.Header("To do", fmt.Sprint(len(p.todos.List))), ""}
	for _, l := range look.Wrap(p.toDoLead(), w-2) {
		lines = append(lines, "  "+look.Muted(l))
	}
	up, group := -1, "\x00"
	for i, t := range p.todos.List {
		if t.Group != group {
			group = t.Group
			lines = append(lines, "")
			if group != "" {
				lines = append(lines, "  "+look.Muted(group))
			}
		}
		l := "  " + look.Mark(look.ToDo) + " " + look.White(t.Title)
		switch {
		case t.Done:
			l = "  " + look.Mark(look.Done) + " " + look.Muted(t.Title)
		case t.Skipped:
			l = "  " + look.Mark(look.Skipped) + " " + look.Muted(t.Title)
		case i == p.todos.Up:
			l, up = "  "+look.Mark(look.ToDo)+look.Pill(t.Title, look.Done), len(lines)
		}
		lines = append(lines, look.Cut(l, w))
	}
	foot := p.leftFoot(w)
	room = max(room-len(foot), 0)
	if up >= room {
		lines = lines[up-room+1:]
	}
	lines = lines[:min(len(lines), room)]
	p.keys.choices = nil
	return append(append(lines, make([]string, room-len(lines))...), foot...)
}

// toDoLead is what the to-dos are, over their list: that kit can't do
// them, and how to work through them; first, that everything else is done,
// unless a step failed.
func (p *Pretty) toDoLead() string {
	lead := "kit can't do these itself: work through them one at a time, as the card on the right says."
	if p.failedSteps() == 0 {
		lead = "Everything else is done. " + lead
	}
	return lead
}

// toDoPanel is the to-do up as the panel over the log has it, w wide: its
// title, bold, and where it is in the list at the right; why it's there,
// muted and wrapped; its steps (panelSteps); then how it ends (toDoEnds).
func (p *Pretty) toDoPanel(w int) []string {
	c := p.card
	title := "  " + look.Mark(look.ToDo) + " " + look.Strong(c.Title)
	at := look.Muted(fmt.Sprintf("%d of %d", p.todos.Up+1, len(p.todos.List)))
	out := []string{"", spread(title, at+"  ", w), ""}
	room := w - len(panelAt)
	if c.Why != "" {
		for _, l := range look.Wrap(c.Why, room) {
			out = append(out, panelAt+look.Muted(look.Linked(l)))
		}
		out = append(out, "")
	}
	out = append(out, panelSteps(c.Steps, room)...)
	if len(c.Steps) > 0 {
		out = append(out, "")
	}
	return append(append(out, p.toDoEnds(room)...), "")
}

// toDoEnds is how the to-do up ends, as its panel says it, room wide after
// its margin: a choice being answered, what its key does, turning; a
// choice, its keys, a line each; one kit tells by itself, that kit's
// watching, and what for, when it says; else the key that says it's done.
// Then, but while a choice is being answered, the key leaving it for later
// (skipKey). Each is wrapped under its own words.
func (p *Pretty) toDoEnds(room int) []string {
	c := p.card
	key := func(k look.Key) []string { return led(look.Urgent(k.Key), len(k.Key), k.Does, room) }
	watching := "kit's watching: it moves on by itself once it's done"
	if c.Watch != "" {
		watching = "kit's watching: it moves on once " + c.Watch
	}
	var out []string
	switch {
	case c.Answering != "":
		return led(look.Cyan("◐"), 1, answeringWith(*c), room)
	case len(c.Keys) > 0:
		for _, k := range c.Keys {
			out = append(out, key(look.Key{Key: k.Key, Does: k.Does})...)
		}
	case c.Tells:
		out = led(look.Cyan("◐"), 1, watching, room)
	default:
		out = key(doneKey)
	}
	return append(out, key(skipKey(*c))...)
}

// skipLetters are the letters the key leaving a to-do for later is from,
// in order: s, then on round the alphabet, but q, which stops, and r, a
// row's raw view.
const skipLetters = "stuvwxyzabcdefghijklmnop"

// skipKey is the key that leaves the to-do c up for later, the next coming
// up: the first of skipLetters c's own keys haven't.
func skipKey(c event.NeedsYou) look.Key {
	letter := skipLetters[:1]
	for _, l := range skipLetters {
		if !slices.ContainsFunc(c.Keys, func(own check.Key) bool { return own.Key == string(l) }) {
			letter = string(l)
			break
		}
	}
	return look.Key{Key: letter, Does: "skips it for now: kit apply brings it back"}
}

// led is text after lead, a mark or a key wide cells wide, on the panel's
// margin, room wide: white, wrapped under its own words.
func led(lead string, wide int, text string, room int) []string {
	var out []string
	at := panelAt + lead + " "
	for _, l := range look.Wrap(text, room-wide-1) {
		out = append(out, at+look.White(look.Linked(l)))
		at = panelAt + strings.Repeat(" ", wide+1)
	}
	return out
}

// answeringWith is what the key card c's being answered with does, as its
// keys say.
func answeringWith(c event.NeedsYou) string {
	for _, k := range c.Keys {
		if k.Key == c.Answering {
			return k.Does
		}
	}
	return ""
}
