package steps_test

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
	"github.com/leeovery/kit/internal/steps"
)

func TestManual(t *testing.T) {
	state := t.TempDir()
	fake := runnertest.New(t)
	list := config.List{Entries: []config.Entry{
		{Name: "tool", Value: `"Install the tool from its site" --test test -d /Applications/Tool.app`, Scope: "laptop", Line: 2},
		{Name: "app", Value: `"Install the app" --test test -d /Applications/App.app`, Scope: "laptop", Line: 3},
		{Name: "sign-in", Value: `"Sign in to the service"`, Scope: "laptop", Line: 4},
	}}
	fake.On("test", "-d", "/Applications/Tool.app")
	fake.On("test", "-d", "/Applications/App.app").Exits(1)
	step := steps.Manual(fake, "/home/someone", state, list)
	res := step.Check(t.Context())
	var got []string
	for _, it := range res.Items {
		got = append(got, it.ID+" | "+it.Name+" | "+it.Detail)
	}
	want := "manual:app | Install the app | \nmanual:sign-in | Sign in to the service | then: kit manual done sign-in"
	if res.State != check.Attention || res.Summary != "1 of 3 done" || strings.Join(got, "\n") != want || step.Area != steps.AreaManual {
		t.Errorf("result = %s %q\n%s", res.State, res.Summary, strings.Join(got, "\n"))
	}
	if err := steps.MarkDone(state, []string{"sign-in"}, time.Now(), false); err != nil {
		t.Fatal(err)
	}
	if res := step.Check(t.Context()); len(res.Items) != 1 || res.Items[0].ID != "manual:app" {
		t.Errorf("after kit done, items = %+v", res.Items)
	}
}

func TestManualLinesRead(t *testing.T) {
	for value, want := range map[string]string{
		`--test test -d x`:                      "what to do comes after the name",
		`"Do it" test -d x`:                     `"test" isn't one of a step by hand's options: --after, --before and --test`,
		`"Do it" --test`:                        "--test needs the command",
		`"Do it" -- test -d x`:                  "comes after --test now",
		`"Do it" --after`:                       "--after needs a step's name after it",
		`"Do it" --before cask:1password`:       "--before a step",
		`"Do it" --after cask: --test true`:     "one of its things as in cask:1password",
		`"Do it" --test test -d x --after cask`: "",
	} {
		_, err := steps.ParseManual("/home", config.Entry{Name: "x", Value: value})
		switch {
		case want == "" && err != nil:
			t.Errorf("%q: error = %v, want --after taken as the command's own word", value, err)
		case want != "" && (err == nil || !strings.Contains(err.Error(), want)):
			t.Errorf("%q: error = %v, want %q", value, err, want)
		}
	}
	m, err := steps.ParseManual("/home", config.Entry{Name: "1password", Value: `"Sign in" --after cask:1password --after cask:tap/one/1password-cli --before secret --test sh -c 'op account list | grep -q .' ~/bin`})
	if err != nil {
		t.Fatal(err)
	}
	if !m.Ordered() || strings.Join(m.After, ",") != "cask:1password,cask:tap/one/1password-cli" || strings.Join(m.Before, ",") != "secret" ||
		m.Command.Name != "sh" || strings.Join(m.Command.Args, "|") != "-c|op account list | grep -q .|/home/bin" {
		t.Errorf("ParseManual() = %+v, %+v", m, m.Command)
	}
}

// events notes the events a step emits.
type events struct {
	mu   sync.Mutex
	seen []event.Event
}

func (e *events) Emit(ev event.Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.seen = append(e.seen, ev)
}

// A step by hand of its own, applied at a terminal, is raised: in its row,
// what to do under it, and a notification; then waited on, its command run
// every few seconds, till it's done.
func TestOwnManualRaisedAndWaitedOn(t *testing.T) {
	defer steps.SetManualPoll(time.Millisecond)()
	fake := runnertest.New(t)
	fake.On("test", "-d", "/Applications/1Password.app").Exits(1).Then().Exits(1).Then()
	fake.On("osascript", "-e", `display notification "Sign in, then turn on its CLI" with title "kit" subtitle "1password"`)
	sink := &events{}
	m := steps.ManualStep{Name: "1password", Do: "Sign in, then turn on its CLI", Before: []string{"secret"},
		Command: &runner.Command{Name: "test", Args: []string{"-d", "/Applications/1Password.app"}}}
	step := steps.OwnManual(fake, t.TempDir(), m, &steps.Raise{Sink: sink, Now: time.Now, Notify: steps.Notify(fake)})
	if step.Name != "1password" || step.Area != steps.AreaManual {
		t.Errorf("step = %+v", step)
	}
	found := step.Check(t.Context())
	if found.State != check.Attention || len(found.Items) != 1 || found.Items[0].Name != "Sign in, then turn on its CLI" {
		t.Fatalf("check before = %+v", found)
	}
	if err := step.Apply(t.Context(), found); err != nil {
		t.Fatal(err)
	}
	if res := step.Check(t.Context()); res.State != check.OK {
		t.Errorf("check after = %+v", res)
	}
	var raised bool
	for _, e := range sink.seen {
		if d, ok := e.(event.Doing); ok && d.Step == "1password" && d.Says == "needs you" && d.Todo == "Sign in, then turn on its CLI" {
			raised = true
		}
	}
	if !raised || len(fake.Commands()) < 3 {
		t.Errorf("raised %v, ran %d commands: want it raised, then waited on", raised, len(fake.Commands()))
	}
}

// With no one to act, applying a step by hand leaves it as it stands; one
// without a command is raised but not waited on, as kit can't tell when
// it's done.
func TestOwnManualUnraisedOrUntested(t *testing.T) {
	fake := runnertest.New(t)
	step := steps.OwnManual(fake, t.TempDir(), steps.ManualStep{Name: "sign-in", Do: "Sign in", After: []string{"cask"}}, nil)
	if err := step.Apply(t.Context(), check.Result{}); err != nil {
		t.Errorf("apply without anyone to act = %v", err)
	}
	if res := step.Check(t.Context()); res.State != check.Attention || res.Items[0].Detail != "then: kit manual done sign-in" {
		t.Errorf("check = %+v", res)
	}
	sink := &events{}
	raised := steps.OwnManual(fake, t.TempDir(), steps.ManualStep{Name: "sign-in", Do: "Sign in", After: []string{"cask"}}, &steps.Raise{Sink: sink, Now: time.Now})
	done := make(chan error, 1)
	go func() { done <- raised.Apply(t.Context(), check.Result{}) }()
	select {
	case err := <-done:
		if err != nil || len(sink.seen) != 1 {
			t.Errorf("apply = %v, events %+v: want it raised, and left", err, sink.seen)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a step by hand without a command was waited on")
	}
}
