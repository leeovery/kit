package render

import (
	"cmp"
	"slices"

	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/kinds"
	"github.com/leeovery/kit/internal/look"
)

// Applying's raw view (kit-look, round 12): the log is in kit's own words,
// never a tool's, but with a row chosen, r shows what the row's commands
// printed in the log's place, as they printed it, oldest first, each line
// with the time it came, on a spine in the row's colour; esc goes back to
// the log, the row still chosen.

// keptRaw is how many of the lines each step's commands print the raw view
// keeps: the newest.
const keptRaw = 5000

// rawLine is a line a step's command printed: where it came among every
// step's, when it came, dim, and the line, muted.
type rawLine struct {
	seq      int
	at, line string
}

// toolsOfRows are the tools the rows' steps run, as the person knows them,
// for the rows whose kinds all say they run the same one, and that hold no
// step of kit's own.
var toolsOfRows = func() map[string]string {
	tools := map[string]string{}
	for _, row := range applyRows {
		if _, own := rowSteps[row]; own {
			continue
		}
		var said []string
		for _, d := range kinds.All {
			if d.Row == row {
				said = append(said, d.Tool)
			}
		}
		slices.Sort(said)
		if said = slices.Compact(said); len(said) == 1 && said[0] != "" {
			tools[row] = said[0]
		}
	}
	return tools
}()

// rawKeys are the keys the raw view reads, at its heading's right end.
var rawKeys = []look.Key{{Key: "↑↓", Does: "scroll"}, {Key: "esc", Does: "back to the log"}}

// ownOutput is what row's raw view is: its tool's own output, or, without
// one, its own.
func ownOutput(row string) string {
	if tool, ok := toolsOfRows[row]; ok {
		return tool + "'s own output"
	}
	return "its own output"
}

// keepRaw keeps line, printed by e's command, for the raw view, as it
// came: its step's newest keptRaw alone.
func (p *Pretty) keepRaw(e event.Output, line string) {
	p.rawSeq++
	l := rawLine{seq: p.rawSeq, at: look.Dim(p.stamp(e.Time).Format(clockFormat)), line: look.Muted(line)}
	p.raw[e.Step] = append(lastOf(p.raw[e.Step], keptRaw-1), l)
}

// rawOf are the lines row's steps' commands printed, as they came.
func (p *Pretty) rawOf(row string) []rawLine {
	var out []rawLine
	for step, lines := range p.raw {
		if p.rowOf(step) == row {
			out = append(out, lines...)
		}
	}
	slices.SortFunc(out, func(a, b rawLine) int { return cmp.Compare(a.seq, b.seq) })
	return out
}

// hasRaw is whether row's steps' commands have printed anything.
func (p *Pretty) hasRaw(row string) bool {
	for step, lines := range p.raw {
		if len(lines) > 0 && p.rowOf(step) == row {
			return true
		}
	}
	return false
}

// rawBody is row's raw view under its heading: a line each its commands
// printed, its time, then the line on a spine in the row's colour.
func (p *Pretty) rawBody(row string) []string {
	spine := spineIn(p.rowState(row))
	lines := p.rawOf(row)
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = "  " + l.at + "  " + spine + " " + l.line
	}
	return out
}

// spineIn is the raw view's spine for a row standing as s: cyan while it
// runs, red once it's failed, else done's colour.
func spineIn(s look.State) string {
	switch s {
	case look.Running:
		return look.Cyan("│")
	case look.Failed:
		return look.Red("│")
	}
	return look.Pink("│")
}

// rowState is how the row called name stands, done when the run has none.
func (p *Pretty) rowState(name string) look.State {
	for _, r := range p.shownRows() {
		if r.name == name {
			return p.rowLook(r).State
		}
	}
	return look.Done
}

// paneBody is what the right pane shows under its heading: the chosen
// row's raw view, while it's open; else the log.
func (p *Pretty) paneBody() []string {
	if p.keys.raw {
		return p.rawBody(p.keys.chosen)
	}
	return p.logBody(p.keys.chosen)
}

// paneHeading is the right pane's heading, w wide: the raw view's, the row
// and its tool's own output, with its keys; or the log's, Log, or Log and
// the row chosen, with the key to its raw view once its commands have
// printed something.
func (p *Pretty) paneHeading(w int) string {
	row := p.keys.chosen
	switch {
	case p.keys.raw:
		return headed(look.Header(row+" · "+ownOutput(row), ""), w, rawKeys...)
	case row == "":
		return look.Header("Log", "")
	case p.hasRaw(row):
		return headed(look.Header("Log · "+row, ""), w, look.Key{Key: "r", Does: ownOutput(row)})
	}
	return look.Header("Log · "+row, "")
}

// headed is heading, then keys at the end of a line w wide, when there's
// room for both; else heading alone.
func headed(heading string, w int, keys ...look.Key) string {
	k := look.Keys(keys...) + "  "
	if ansi.StringWidth(heading)+ansi.StringWidth(k) > w {
		return heading
	}
	return spread(heading, k, w)
}

// toggleRaw opens the chosen row's raw view, following the newest, or
// closes it: nothing with no row chosen, or one whose commands have printed
// nothing.
func (p *Pretty) toggleRaw() {
	if p.keys.chosen == "" || !p.keys.raw && !p.hasRaw(p.keys.chosen) {
		return
	}
	p.keys.raw, p.keys.anchored = !p.keys.raw, false
}

// back closes the raw view, the row still chosen; with the log showing,
// it chooses everything.
func (p *Pretty) back() {
	if p.keys.raw {
		p.toggleRaw()
		return
	}
	p.choose("")
}
