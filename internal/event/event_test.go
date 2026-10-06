package event_test

import (
	"context"
	"errors"
	"maps"
	"sync"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/runner"
)

type recorder struct {
	events []event.Event
}

func (r *recorder) Emit(e event.Event) { r.events = append(r.events, e) }

func TestFanoutSendsEveryEventToEverySink(t *testing.T) {
	a, b := &recorder{}, &recorder{}
	f := event.NewFanout(a, b)
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() { f.Emit(event.StepStarted{Step: "brew"}) })
	}
	wg.Wait()
	if len(a.events) != 50 || len(b.events) != 50 {
		t.Errorf("sinks got %d and %d events, want 50 each", len(a.events), len(b.events))
	}
}

func TestStepOf(t *testing.T) {
	if got := event.StepOf(t.Context()); got != "" {
		t.Errorf("StepOf() outside a step = %q, want none", got)
	}
	if got := event.StepOf(event.WithStep(t.Context(), "cask")); got != "cask" {
		t.Errorf("StepOf() = %q, want cask", got)
	}
}

func TestCommand(t *testing.T) {
	started := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	rep := runner.Report{
		Command: runner.Command{Name: "gh", Args: []string{"auth", "token"}, Input: "never logged"},
		Started: started,
		Result:  runner.Result{Stdout: []byte("gho_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123\n"), Stderr: []byte("value hunter2-value\n"), ExitCode: 1, Duration: time.Second},
		Err:     errors.New("gh auth token exited 1: value hunter2-value"),
	}

	e := event.Command(event.WithStep(context.Background(), "kit-config"), rep, "hunter2-value")
	want := event.CommandRan{
		Time: started, Step: "kit-config", Command: "gh auth token", Exit: 1, Duration: time.Second,
		Stdout: "[redacted]\n", Stderr: "value [redacted]\n", Error: "gh auth token exited 1: value [redacted]",
	}
	if e != want {
		t.Errorf("Command() = %+v\nwant %+v", e, want)
	}
}

func TestTally(t *testing.T) {
	got := event.Tally(check.Result{State: check.OK}, check.Result{State: check.OK}, check.Result{State: check.Attention}, check.Result{})
	want := map[check.State]int{check.OK: 2, check.Attention: 1, check.Failed: 1}
	if !maps.Equal(got, want) {
		t.Errorf("Tally() = %v, want %v", got, want)
	}
}

func TestASecretsOutputIsNeverKept(t *testing.T) {
	rep := runner.Report{
		Command: runner.Command{Name: "op", Args: []string{"read", "op://vault/item/field"}, Secret: true},
		Result:  runner.Result{Stdout: []byte("plain words no pattern would catch\n")},
	}
	if e := event.Command(context.Background(), rep); e.Stdout != "(a secret, 35 bytes, not kept)" {
		t.Errorf("Stdout = %q", e.Stdout)
	}
}
