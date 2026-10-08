package render_test

import (
	"slices"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/logs"
	"github.com/leeovery/kit/internal/render"
)

// loggedRun is a run as its log has it: of command, started at, ended with
// counts, or not ended when counts is nil.
func loggedRun(command string, at time.Time, counts map[check.State]int) logs.Run {
	r := logs.Run{Path: command + at.Format("1504"), Started: logs.Record{Event: "run_started", Time: at, Command: command}}
	if counts != nil {
		r.Finished = logs.Record{Event: "run_finished", DurationMS: 1500, Counts: counts}
	}
	return r
}

// The runs, newest first, a day a header; a command's fine runs, three or
// more in a row, folded, opening to them; a run that didn't finish, and one
// that needs you, never folded.
func TestRunList(t *testing.T) {
	day := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)
	fine := map[check.State]int{check.OK: 23}
	runs := []logs.Run{
		loggedRun("status", day.Add(14*time.Hour+20*time.Minute), map[check.State]int{check.OK: 23, check.Attention: 1}),
		loggedRun("apply", day.Add(14*time.Hour), nil),
		loggedRun("status", day.Add(11*time.Hour+12*time.Minute), fine),
		loggedRun("status", day.Add(10*time.Hour), fine),
		loggedRun("status", day.Add(8*time.Hour+51*time.Minute), fine),
		loggedRun("", day.Add(8*time.Hour), fine),
		loggedRun("brew add jq", day.Add(-time.Hour), map[check.State]int{check.OK: 2}),
		loggedRun("status", day.Add(-2*time.Hour), fine),
	}
	lines := render.RunList(runs)
	var got, chosen []string
	for _, l := range lines {
		got, chosen = append(got, ansi.Strip(l.Text)), append(chosen, ansi.Strip(l.Chosen))
	}
	want := []string{
		"  WED 7 OCT",
		"  ▲ 14:20 status  1 needs you · 23 fine · 1.5s",
		"  ✗ 14:00 apply  didn't finish",
		"  + 3 runs of status, all fine · 08:51 to 11:12",
		"  ● 08:00 kit  23 fine · 1.5s",
		"",
		"  TUE 6 OCT",
		"  ● 23:00 brew add jq  2 fine · 1.5s",
		"  ● 22:00 status  23 fine · 1.5s",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("RunList =\n%q\nwant\n%q", got, want)
	}
	if chosen[1] != "  ❯ 14:20 status  1 needs you · 23 fine · 1.5s" || chosen[3] != "  ❯ 3 runs of status  all fine · 08:51 to 11:12" || chosen[0] != "" {
		t.Errorf("chosen = %q", chosen)
	}
	fold := lines[3].Folds
	if len(fold) != 3 || ansi.Strip(fold[0].Text) != "  ● 11:12 status  23 fine · 1.5s" || fold[2].Value != "status0851" || lines[2].Value != "apply1400" {
		t.Errorf("the fold opens to %+v", fold)
	}
}

// Runs of a command that each ended needing you the same way fold too,
// saying how; a run that ended otherwise isn't folded with them.
func TestRunListFoldsRunsThatEndedAlike(t *testing.T) {
	day := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)
	needsYou := map[check.State]int{check.OK: 42, check.Attention: 1}
	runs := []logs.Run{
		loggedRun("status", day.Add(14*time.Hour+20*time.Minute), needsYou),
		loggedRun("status", day.Add(13*time.Hour+20*time.Minute), map[check.State]int{check.OK: 41, check.Attention: 1}),
		loggedRun("status", day.Add(12*time.Hour+20*time.Minute), needsYou),
		loggedRun("status", day.Add(11*time.Hour+20*time.Minute), map[check.State]int{check.OK: 41, check.Attention: 2}),
	}
	lines := render.RunList(runs)
	var got []string
	for _, l := range lines {
		got = append(got, ansi.Strip(l.Text))
	}
	want := []string{
		"  WED 7 OCT",
		"  + 3 runs of status, each 1 needs you · 12:20 to 14:20",
		"  ▲ 11:20 status  2 need you · 41 fine · 1.5s",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("RunList =\n%q\nwant\n%q", got, want)
	}
	if chosen := ansi.Strip(lines[1].Chosen); chosen != "  ❯ 3 runs of status  each 1 needs you · 12:20 to 14:20" || len(lines[1].Folds) != 3 {
		t.Errorf("the fold, chosen: %q, opening to %d runs", chosen, len(lines[1].Folds))
	}
}
