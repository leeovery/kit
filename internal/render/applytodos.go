package render

import (
	"fmt"
	"strings"

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
// then how many are done once some are; once they're over, that kit's
// carrying on.
func (p *Pretty) toDoState() string {
	if p.todos.Up < 0 {
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
// To do and how many; then each group, its name muted after a blank line,
// none for those in no group, its to-dos a line each: done, the done mark
// and its title muted; the one up, its title a pill; the rest white. The
// keys at its foot. Too many for the room, it starts further down, to keep
// the one up in sight.
func (p *Pretty) toDoPane(w, room int) []string {
	lines := []string{"", look.Header("To do", fmt.Sprint(len(p.todos.List)))}
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
			out = append(out, panelAt+look.Muted(l))
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
// Each is wrapped under its own words.
func (p *Pretty) toDoEnds(room int) []string {
	c := p.card
	key := func(k, does string) []string { return led(look.Urgent(k), len(k), does, room) }
	watching := "kit's watching: it moves on by itself once it's done"
	if c.Watch != "" {
		watching = "kit's watching: it moves on once " + c.Watch
	}
	switch {
	case c.Answering != "":
		return led(look.Cyan("◐"), 1, answeringWith(*c), room)
	case len(c.Keys) > 0:
		var out []string
		for _, k := range c.Keys {
			out = append(out, key(k.Key, k.Does)...)
		}
		return out
	case c.Tells:
		return led(look.Cyan("◐"), 1, watching, room)
	}
	return key(doneKey.Key, doneKey.Does)
}

// led is text after lead, a mark or a key wide cells wide, on the panel's
// margin, room wide: white, wrapped under its own words.
func led(lead string, wide int, text string, room int) []string {
	var out []string
	at := panelAt + lead + " "
	for _, l := range look.Wrap(text, room-wide-1) {
		out = append(out, at+look.White(l))
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
