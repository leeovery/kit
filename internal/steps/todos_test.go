package steps_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/hand"
	"github.com/leeovery/kit/internal/prefs"
	"github.com/leeovery/kit/internal/runner/runnertest"
	"github.com/leeovery/kit/internal/steps"
)

// kit's own to-dos are its checks' items, in its words, one wording
// wherever they show: the to-do's title the item's name, how to do it in a
// line its detail; then why, its steps, what kit watches for and its group;
// apps' settings a choice, of bringing them back or starting afresh, that
// comes first. The backup tools' are their drivers', tested with them.
func TestKitsOwnToDos(t *testing.T) {
	state := t.TempDir()
	if err := os.WriteFile(filepath.Join(state, "prefs.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	settings := prefs.New(runnertest.New(t), t.TempDir(), state, t.TempDir(), "", "laptop", prefs.Lists{}, now)
	for _, tt := range []struct {
		step engine.Step
		want check.Item
	}{
		{steps.Prefs(settings, now), check.Item{
			ID: "prefs:paused", Name: "Bring back apps' settings", State: steps.Problem,
			Detail: "kit prefs restore, or kit prefs start-fresh",
			ToDo: &check.ToDo{
				Title: "Bring back apps' settings",
				Why:   "First, so the apps you open next start with yours. This Mac saves its own once they're back. Claude Code's come too.",
				Group: "Apps",
				Keys: []check.Key{
					{Key: "r", Does: "brings them back from this Mac's last save"},
					{Key: "f", Does: "starts afresh: nothing back, saving from now"},
				},
				First: true,
			}}},
	} {
		res := tt.step.Check(t.Context())
		if !res.ToDo() || len(res.Items) != 1 || !reflect.DeepEqual(res.Items[0], tt.want) {
			t.Errorf("%s: items = %+v\nwant %+v, %+v", tt.step.Name, res.Items, tt.want, tt.want.ToDo)
		}
	}
}

// changing is a step's check that finds something, then, once it's been
// asked again, that it's fine: as a to-do done by the time kit looks.
type changing struct {
	mu    sync.Mutex
	asked int
	found check.Result
}

func (c *changing) check(context.Context) check.Result {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.asked++
	if c.asked == 1 {
		return c.found
	}
	return check.Result{State: check.OK, Summary: "fine"}
}

// ownStep is a step called name whose check finds the item it once, as
// changing has it.
func ownStep(name string, it check.Item) engine.Step {
	c := &changing{found: check.Result{State: check.Attention, Summary: "to do", Items: []check.Item{it}}}
	return engine.Step{Name: name, Title: name, Area: steps.AreaBackups, Check: c.check}
}

// toDoItem is an item of step's, for the person to do: t.
func toDoItem(step string, t check.ToDo) check.Item {
	return check.Item{ID: step + ":none", Name: t.Title, State: steps.Problem, ToDo: &t}
}

// atTheEnd has the steps by hand at the end come after every other step,
// as a run at a terminal places them.
func atTheEnd(manual engine.Step, others ...engine.Step) []engine.Step {
	for _, s := range others {
		manual.After = append(manual.After, s.Name)
	}
	return append(others, manual)
}

// applied applies all, returning the run's report and what it emitted.
func applied(t *testing.T, c *card, all []engine.Step) engine.Report {
	t.Helper()
	p, err := engine.New(all...)
	if err != nil {
		t.Fatal(err)
	}
	report, err := p.Apply(t.Context(), c.sink, engine.Options{Command: "apply", Jobs: 4})
	if err != nil {
		t.Fatal(err)
	}
	return report
}

// toDoLists are the to-do lists the run emitted, in order.
func toDoLists(sink *events) []event.ToDos {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	var out []event.ToDos
	for _, e := range sink.seen {
		if l, ok := e.(event.ToDos); ok {
			out = append(out, l)
		}
	}
	return out
}

// finishes are how many times each step finished, as the run emitted it,
// and how each last stood.
func finishes(sink *events) (map[string]int, map[string]check.State) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	times, last := map[string]int{}, map[string]check.State{}
	for _, e := range sink.seen {
		if f, ok := e.(event.StepFinished); ok {
			times[f.Step]++
			last[f.Step] = f.Result.State
		}
	}
	return times, last
}

// At the run's end, what's left for the person comes as one list, in kit's
// order: kit's own that come first (apps' settings), then the config's
// lines not done, in order, then the rest of kit's own, in the run's order;
// grouped, each group where its first comes, one with none a group of its
// own. Each is looked at as its turn comes, and goes up only when it isn't
// done: done, the list ticks it, with what kit saw. After the last, the
// steps they held are checked again, in place of how they stood: those
// they came from, and no other.
func TestTheToDosInKitsOrder(t *testing.T) {
	c := newCard(t)
	for _, f := range []string{"/k", "/s", "/n", "/l"} {
		c.fake.On("test", "-f", f).Exits(1).Then()
	}
	list := config.List{Entries: []config.Entry{
		{Name: "karabiner", Value: `"Open Karabiner-Elements" --group Apps --test test -f /k`, Scope: "laptop", Line: 2},
		{Name: "sharing", Value: `"Allow Full Disk Access for remote users" --group "System Settings" --test test -f /s`, Scope: "laptop", Line: 3},
		{Name: "serial", Value: `"Note the serial" --test test -f /n`, Scope: "laptop", Line: 4},
		{Name: "label", Value: `"Label the backup disk" --group Backups --test test -f /l`, Scope: "laptop", Line: 5},
	}}
	report := applied(t, c, atTheEnd(steps.Manual(c.fake, "/home/someone", c.state, list, c.raise),
		ownStep("time-machine", toDoItem("time-machine", check.ToDo{Title: "Add a Time Machine disk", Group: "Backups"})),
		ownStep("arq", toDoItem("arq", check.ToDo{Title: "Set up Arq's backup plan", Group: "Backups"})),
		ownStep("prefs", toDoItem("prefs", check.ToDo{Title: "Bring back apps' settings", Group: "Apps", First: true})),
		engine.Step{Name: "disk", Title: "Disk space", Check: func(context.Context) check.Result {
			return check.Result{State: check.Attention, Summary: "nearly full", Items: []check.Item{{ID: "disk:low", Name: "nearly full", State: steps.Problem}}}
		}},
	))

	lists := toDoLists(c.sink)
	if len(lists) == 0 {
		t.Fatal("no to-dos came up")
	}
	var got [][2]string
	for _, e := range lists[0].List {
		got = append(got, [2]string{e.Group, e.Title})
	}
	want := [][2]string{
		{"Apps", "Bring back apps' settings"}, {"Apps", "Open Karabiner-Elements"},
		{"System Settings", "Allow Full Disk Access for remote users"},
		{"", "Note the serial"},
		{"Backups", "Label the backup disk"}, {"Backups", "Add a Time Machine disk"}, {"Backups", "Set up Arq's backup plan"},
	}
	if !slices.Equal(got, want) || lists[0].Up != 0 {
		t.Errorf("the to-dos came up as %q, the one up %d; want %q, the first up", got, lists[0].Up, want)
	}
	last := lists[len(lists)-1]
	if len(lists) != len(want)+1 || last.Up != -1 {
		t.Fatalf("the list was emitted %d times, last with %d up; want once as each came up and once over", len(lists), last.Up)
	}
	for _, e := range last.List {
		saw := ""
		if e.Step != steps.ManualName {
			saw = "fine"
		}
		if !e.Done || e.Saw != saw {
			t.Errorf("%s ended done %v, kit saw %q; want it done, kit having seen %q", e.Title, e.Done, e.Saw, saw)
		}
	}
	if up := c.cardsNow(); len(up) != 0 {
		t.Errorf("cards went up for %+v; want none, each done when its turn came", up)
	}
	times, stood := finishes(c.sink)
	for _, name := range []string{"prefs", "time-machine", "arq"} {
		if times[name] != 2 || stood[name] != check.OK || report.Results[name].State != check.OK {
			t.Errorf("%s finished %d times, last %s, the report %s; want it checked again once its to-do was done, ok", name, times[name], stood[name], report.Results[name].State)
		}
	}
	if times["disk"] != 1 || report.Results["disk"].State != check.Attention {
		t.Errorf("disk finished %d times, the report %s: want it left as it was", times["disk"], report.Results["disk"].State)
	}
}

// cardsNow are the cards that have gone up so far.
func (c *card) cardsNow() []event.NeedsYou {
	c.sink.mu.Lock()
	defer c.sink.mu.Unlock()
	var up []event.NeedsYou
	for _, e := range c.sink.seen {
		if card, ok := e.(event.NeedsYou); ok {
			up = append(up, card)
		}
	}
	return up
}

// waitForList waits till the to-dos have been emitted n times, and returns
// the last.
func (c *card) waitForList(t *testing.T, n int) event.ToDos {
	t.Helper()
	for range 5000 {
		if lists := toDoLists(c.sink); len(lists) >= n {
			return lists[len(lists)-1]
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("the to-dos were never emitted %d times", n)
	return event.ToDos{}
}

// A to-do that's a choice goes up with its keys, as its step readies it,
// kit not telling by itself; a key does what its step says, and, should
// that do it, it's done, what the step said it did as what kit saw. What
// goes wrong, the log says, as something to look at, and it stays up.
func TestAChoiceToDo(t *testing.T) {
	defer steps.SetManualPoll(time.Hour)()
	c := newCard(t)
	var mu sync.Mutex
	var pressed []string
	brought := false
	step := engine.Step{
		Name: "prefs", Title: "App settings", Area: steps.AreaBackups,
		Check: func(context.Context) check.Result {
			mu.Lock()
			defer mu.Unlock()
			if brought {
				return check.Result{State: check.OK, Summary: "saved"}
			}
			return check.Result{State: check.Attention, Summary: "paused", Items: []check.Item{toDoItem("prefs", check.ToDo{
				Title: "Bring back apps' settings", First: true,
				Keys: []check.Key{{Key: "r", Does: "brings them back"}, {Key: "f", Does: "starts afresh"}},
			})}}
		},
		Choice: &engine.Choice{
			Ready: func(_ context.Context, it check.Item) check.Item {
				t := *it.ToDo
				t.Keys = []check.Key{{Key: "r", Does: "brings them back, 15 Oct"}, t.Keys[1]}
				it.ToDo = &t
				return it
			},
			Answer: func(_ context.Context, _ check.Item, key string) (string, error) {
				mu.Lock()
				defer mu.Unlock()
				pressed = append(pressed, key)
				if len(pressed) == 1 {
					return "", errors.New("couldn't reach the store")
				}
				brought = true
				return "214 apps' settings, from 15 Oct", nil
			},
		},
	}
	done := make(chan engine.Report, 1)
	go func() {
		done <- applied(t, c, atTheEnd(steps.Manual(c.fake, "/home/someone", c.state, config.List{}, c.raise), step))
	}()
	up := c.cards(t, 1)[0]
	if up.Tells || !slices.Equal(up.Keys, []check.Key{{Key: "r", Does: "brings them back, 15 Oct"}, {Key: "f", Does: "starts afresh"}}) {
		t.Errorf("card = %+v, want its keys as its step readied them, kit not telling by itself", up)
	}
	c.answer(t, steps.ManualName, "r")
	alerted := false
	for range 5000 {
		c.sink.mu.Lock()
		alerted = slices.ContainsFunc(c.sink.seen, func(e event.Event) bool {
			l, ok := e.(event.Logged)
			return ok && l.Alert && l.Line == "Bring back apps' settings: couldn't reach the store"
		})
		c.sink.mu.Unlock()
		if alerted {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !alerted {
		t.Fatal("what went wrong with r never reached the log")
	}
	c.answer(t, steps.ManualName, "r")
	report := <-done
	last := toDoLists(c.sink)
	if l := last[len(last)-1]; !l.List[0].Done || l.List[0].Saw != "214 apps' settings, from 15 Oct" {
		t.Errorf("the to-do ended %+v; want it done, kit having seen what r did", l.List[0])
	}
	if !slices.Equal(pressed, []string{"r", "r"}) || report.Results["prefs"].State != check.OK {
		t.Errorf("pressed %q, prefs stands %s; want r twice, then prefs checked again, ok", pressed, report.Results["prefs"].State)
	}
}

// The person can stop at the to-dos: the one up comes down, the rest stay
// to do, nothing's checked again, and the run carries on to its end, the
// steps by hand applied without failing.
func TestStoppingAtTheToDos(t *testing.T) {
	defer steps.SetManualPoll(time.Hour)()
	c := newCard(t)
	stuck := func(name, title string) engine.Step {
		it := toDoItem(name, check.ToDo{Title: title, Group: "Backups"})
		return engine.Step{Name: name, Title: name, Check: func(context.Context) check.Result {
			return check.Result{State: check.Attention, Summary: "to do", Items: []check.Item{it}}
		}}
	}
	done := make(chan engine.Report, 1)
	go func() {
		done <- applied(t, c, atTheEnd(steps.Manual(c.fake, "/home/someone", c.state, config.List{}, c.raise),
			stuck("time-machine", "Add a Time Machine disk"), stuck("arq", "Set up Arq's backup plan")))
	}()
	c.cards(t, 1)
	c.answer(t, steps.ManualName, hand.Stop)
	report := <-done
	last := c.waitForList(t, 2)
	if last.Up != -1 || last.List[0].Done || last.List[1].Done {
		t.Errorf("stopped, the list ended %+v; want none up, none done", last)
	}
	times, _ := finishes(c.sink)
	if times["time-machine"] != 1 || times["arq"] != 1 || report.Results["time-machine"].State != check.Attention {
		t.Errorf("finishes %v; want nothing checked again, Time Machine still to do", times)
	}
	if res := report.Results[steps.ManualName]; res.State != check.OK {
		t.Errorf("the steps by hand stand %+v; want them applied, nothing declared", res)
	}
	if n := len(c.cardsNow()); n != 1 {
		t.Errorf("%d cards went up; want the one, then none", n)
	}
	want := []string{"notify card:manual:time-machine:none | Add a Time Machine disk | kit waits for you to do this", "clear card:manual:time-machine:none"}
	if got := c.notices.posted(); !slices.Equal(got, want) {
		t.Errorf("notifications %q, want %q", got, want)
	}
}
