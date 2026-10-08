package render

import (
	"fmt"
	"time"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/logs"
	"github.com/leeovery/kit/internal/look"
)

// foldFrom is how many runs of a command in a row, all fine, fold into a
// line.
const foldFrom = 3

// RunList is the runs logged, newest first, as a list to pick one from: a
// day a header, a run a row, its mark, when it started, what it was of and
// how it ended; runs of a command that went fine, three or more in a row,
// folded into a line, which opens to them.
func RunList(runs []logs.Run) []ask.Line {
	var lines []ask.Line
	for i := 0; i < len(runs); {
		r := runs[i]
		if i == 0 || day(runs[i-1]) != day(r) {
			if i > 0 {
				lines = append(lines, ask.Line{})
			}
			lines = append(lines, ask.Line{Text: look.Header(r.Started.Time.Format("Mon 2 Jan"), "")})
		}
		n := 1
		for fine(r) && i+n < len(runs) && fine(runs[i+n]) && runs[i+n].Of() == r.Of() && day(runs[i+n]) == day(r) {
			n++
		}
		if n >= foldFrom {
			lines = append(lines, foldLine(runs[i:i+n]))
		} else {
			for _, r := range runs[i : i+n] {
				lines = append(lines, runLine(r))
			}
		}
		i += n
	}
	return lines
}

// day is the day a run started on.
func day(r logs.Run) string { return r.Started.Time.Format(time.DateOnly) }

// fine is whether a run finished with nothing failed, needing you, or not
// checked.
func fine(r logs.Run) bool { return r.Done() && len(tally(r.Finished.Counts)) == 0 }

// runLine is a run's row: its mark, when it started, what it was of, and how
// it ended; with the cursor on it, its when and what a pill.
func runLine(r logs.Run) ask.Line {
	at, of := r.Started.Time.Format("15:04"), r.Of()
	state, says := ended(r)
	return ask.Line{
		Text:   "  " + look.Mark(state) + " " + look.Muted(at) + " " + look.Strong(of) + "  " + says,
		Chosen: "  " + look.Chosen(at+" "+of) + " " + says,
		Value:  r.Path,
	}
}

// ended is how a run ended, as its mark and in words: what failed, needs you
// or wasn't checked, how many were fine, and how long it took.
func ended(r logs.Run) (look.State, string) {
	if !r.Done() {
		return look.Failed, look.Red("didn't finish")
	}
	counts := r.Finished.Counts
	parts := tally(counts)
	switch ok := fmt.Sprintf("%d fine", counts[check.OK]); {
	case len(parts) == 0:
		parts = append(parts, look.White(ok))
	case counts[check.OK] > 0:
		parts = append(parts, look.Muted(ok))
	}
	parts = append(parts, look.Muted(duration(r.Finished.DurationMS)))
	return counted(counts), look.Says(parts...)
}

// counted is how a run stood by its counts: failed, if anything failed, or
// needing you, if anything does.
func counted(counts map[check.State]int) look.State {
	switch {
	case counts[check.Failed] > 0:
		return look.Failed
	case counts[check.Attention] > 0:
		return look.NeedsYou
	}
	return look.Done
}

// foldLine is runs of a command, newest first, all fine, folded: how many
// and what they were of, from when to when; opened, their rows.
func foldLine(runs []logs.Run) ask.Line {
	what := fmt.Sprintf("%d runs of %s", len(runs), runs[0].Of())
	when := look.Dim(runs[len(runs)-1].Started.Time.Format("15:04") + " to " + runs[0].Started.Time.Format("15:04"))
	folds := make([]ask.Line, len(runs))
	for i, r := range runs {
		folds[i] = runLine(r)
	}
	return ask.Line{
		Text:   "  " + look.Dim("+") + " " + look.Says(look.Muted(what)+look.Dim(", all fine"), when),
		Chosen: "  " + look.Chosen(what) + " " + look.Says(look.Dim("all fine"), when),
		Folds:  folds,
	}
}
