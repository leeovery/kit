package render_test

import (
	"time"

	"bytes"
	"testing"

	"github.com/charmbracelet/colorprofile"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/logs"
	"github.com/leeovery/kit/internal/render"
)

func TestLogView(t *testing.T) {
	ok := check.Result{State: check.OK, Summary: "/opt/homebrew"}
	brew := check.Result{State: check.Attention, Summary: "6 declared, 4 installed"}
	deferred := check.Result{State: check.Deferred, Reason: "needs Homebrew"}
	records := []logs.Record{
		{Time: at, Event: "run_started", Command: "status", Machine: "laptop", Version: "0.1.0", Steps: []event.Step{
			{Name: "homebrew", Title: "Homebrew"}, {Name: "brew", Title: "Formulae"}, {Name: "cask", Title: "Casks"}, {Name: "mas", Title: "App Store"},
		}},
		{Time: at, Event: "command", Command: "sw_vers -productVersion", Exit: new(0), DurationMS: 20},
		{Time: at, Event: "step_started", Step: "homebrew"},
		{Time: at, Event: "command", Step: "homebrew", Command: "brew --prefix", Exit: new(0), DurationMS: 110},
		{Time: at, Event: "step_finished", Step: "homebrew", Result: &ok, DurationMS: 120},
		{Time: at, Event: "command", Step: "brew", Command: "brew list --formula --full-name -1", Exit: new(0), DurationMS: 540},
		{Time: at, Event: "command", Step: "brew", Command: "brew info --json=v2 jq", Exit: new(1), DurationMS: 900, Error: "brew info --json=v2 jq exited 1: Error: No available formula"},
		{Time: at, Event: "step_finished", Step: "brew", Result: &brew, DurationMS: 1460},
		{Time: at, Event: "command", Step: "cask", Command: "brew list --cask --full-name -1", Exit: new(-1), DurationMS: 120000, Error: "brew list --cask --full-name -1: context deadline exceeded"},
		{Time: at, Event: "step_finished", Step: "mas", Result: &deferred},
		{Time: at, Event: "run_finished", DurationMS: 121500, Counts: map[check.State]int{check.OK: 1, check.Attention: 1, check.Failed: 1, check.Deferred: 1}},
	}
	var out bytes.Buffer
	if err := render.LogView(&colorprofile.Writer{Forward: &out, Profile: colorprofile.NoTTY}, "/Users/someone/Library/Logs/kit/2026-01-02T030405.000-status.jsonl", records); err != nil {
		t.Fatal(err)
	}
	golden(t, "log.golden", out.String())
}

func TestLogViewOfARunThatDidntFinish(t *testing.T) {
	records := []logs.Record{
		{Time: at, Event: "run_started", Command: "status", Machine: "laptop", Steps: []event.Step{{Name: "brew", Title: "Formulae"}}},
		{Time: at, Event: "step_started", Step: "brew"},
	}
	var out bytes.Buffer
	if err := render.LogView(&colorprofile.Writer{Forward: &out, Profile: colorprofile.NoTTY}, "/x.jsonl", records); err != nil {
		t.Fatal(err)
	}
	want := "kit status · laptop · 2 Jan 2026 03:04:05\n/x.jsonl\n\n✗ Formulae   didn't finish\n\nThe run didn't finish\n"
	if out.String() != want {
		t.Errorf("printed\n%q\nwant\n%q", out.String(), want)
	}
}

// At a terminal, a run's log is a timeline: the run as a row, then each
// step after the time it started, what it ran on the line under it.
func TestLogRun(t *testing.T) {
	ok := check.Result{State: check.OK, Summary: "/opt/homebrew"}
	failed := check.Result{State: check.Failed, Reason: "couldn't apply: run apply exited 1"}
	records := []logs.Record{
		{Time: at, Event: "run_started", Command: "apply", Machine: "laptop", Steps: []event.Step{
			{Name: "homebrew", Title: "Homebrew"}, {Name: "remote-session", Title: "remote-session"}, {Name: "fonts", Title: "fonts"},
		}},
		{Time: at, Event: "step_started", Step: "homebrew"},
		{Time: at, Event: "command", Step: "homebrew", Command: "brew --prefix", Exit: new(0), DurationMS: 110},
		{Time: at, Event: "step_finished", Step: "homebrew", Result: &ok, DurationMS: 120},
		{Time: at.Add(time.Second), Event: "step_started", Step: "remote-session"},
		{Time: at, Event: "command", Step: "remote-session", Command: "/Users/someone/.config/kit/shared/steps/remote-session/run apply", Exit: new(1), DurationMS: 300, Error: "sudo: a password is required"},
		{Time: at, Event: "step_finished", Step: "remote-session", Result: &failed, DurationMS: 310},
		{Time: at, Event: "run_finished", DurationMS: 4000, Counts: map[check.State]int{check.OK: 1, check.Failed: 1}},
	}
	var out bytes.Buffer
	if err := render.LogRun(&colorprofile.Writer{Forward: &out, Profile: colorprofile.NoTTY}, 80, records); err != nil {
		t.Fatal(err)
	}
	golden(t, "log-run.golden", out.String())
}
