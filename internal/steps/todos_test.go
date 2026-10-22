package steps_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
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
				Why:   "First, so the apps you open next start with yours. Bringing them back opens some apps once: their windows, notifications and permission prompts may follow. This Mac saves its own once they're back.",
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
// own. Each found done by its first look as its turn comes never goes up:
// the list ticks it, with what kit saw. After the last, the steps they held
// are checked again, in place of how they stood: those they came from, and
// no other.
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
	if up, down := c.cardsNow(), c.downs(); len(up) > 0 || len(down) > 0 {
		t.Errorf("cards went up %+v, and down for %q; want none, each found done by its first look", up, down)
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
// kit not telling by itself; a key does what its step says, the card
// offering none of its keys meanwhile, and, should that do it, it's done,
// what the step said it did as what kit saw. What goes wrong, the log says,
// as something to look at, and it stays up, its keys offered again, a key
// pressed while the first was being done dropped.
func TestAChoiceToDo(t *testing.T) {
	defer steps.SetManualPoll(time.Hour)()
	c := newCard(t)
	var mu sync.Mutex
	var pressed, answering []string
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
				shown := c.cardsNow()
				answering = append(answering, shown[len(shown)-1].Answering)
				if len(pressed) == 1 {
					// f, pressed before the screen showed r being done.
					if !c.desk.Answer(steps.ManualName, "f") {
						t.Error("f, pressed while r was being done, wasn't passed on")
					}
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
	if !slices.Equal(answering, []string{"r", "r"}) {
		t.Errorf("while r was being done, the card was answered by %q; want r, its keys not offered", answering)
	}
	var shown []string
	for _, card := range c.cardsNow() {
		shown = append(shown, card.Answering)
	}
	if !slices.Equal(shown, []string{"", "r", "", "r"}) {
		t.Errorf("the card went up answered by %q; want its keys, then r while it worked, its keys again once it failed, then r", shown)
	}
}

// A to-do of kit's own done, the to-dos its step's check finds then come
// next, as apps to open once bringing back their settings left: a watched
// one, kit offering no keys, done once its check finds it gone, settling
// what it waited for, what that did as what kit saw.
func TestAToDoLeavesAnother(t *testing.T) {
	defer steps.SetManualPoll(5 * time.Millisecond)()
	c := newCard(t)
	var mu sync.Mutex
	brought, opened := false, false
	var settled []string
	step := engine.Step{
		Name: "prefs", Title: "App settings", Area: steps.AreaBackups,
		Check: func(context.Context) check.Result {
			mu.Lock()
			defer mu.Unlock()
			switch {
			case !brought:
				return check.Result{State: check.Attention, Summary: "paused", Items: []check.Item{toDoItem("prefs", check.ToDo{
					Title: "Bring back apps' settings", First: true, Keys: []check.Key{{Key: "r", Does: "brings them back"}},
				})}}
			case !opened:
				return check.Result{State: check.Attention, Summary: "1 app to open once", Items: []check.Item{{ID: "prefs:open", Name: "Open Boxed App once and quit it", State: steps.Problem,
					ToDo: &check.ToDo{Title: "Open Boxed App once and quit it", Watch: "Boxed App has its settings folder", First: true}}}}
			}
			return check.Result{State: check.OK, Summary: "saving from tonight"}
		},
		Choice: &engine.Choice{
			Answer: func(context.Context, check.Item, string) (string, error) {
				mu.Lock()
				defer mu.Unlock()
				brought = true
				return "1 app's settings; 1 app to open once", nil
			},
			Settle: func(_ context.Context, it check.Item) string {
				mu.Lock()
				defer mu.Unlock()
				settled = append(settled, it.ID)
				if it.ID == "prefs:open" {
					return "1 app's settings"
				}
				return ""
			},
		},
	}
	done := make(chan engine.Report, 1)
	go func() {
		done <- applied(t, c, atTheEnd(steps.Manual(c.fake, "/home/someone", c.state, config.List{}, c.raise), step))
	}()
	c.cards(t, 1)
	c.answer(t, steps.ManualName, "r")
	var up event.NeedsYou
	for range 5000 {
		if shown := c.cardsNow(); shown[len(shown)-1].Item == "prefs:open" {
			up = shown[len(shown)-1]
			break
		}
		time.Sleep(time.Millisecond)
	}
	if up.Title != "Open Boxed App once and quit it" || !up.Tells || len(up.Keys) != 0 {
		t.Fatalf("after r, the card up is %+v; want the apps to open, watched", up)
	}
	mu.Lock()
	opened = true
	mu.Unlock()
	report := <-done
	last := toDoLists(c.sink)
	l := last[len(last)-1]
	if len(l.List) != 2 || l.List[1].Title != "Open Boxed App once and quit it" || !l.List[1].Done || l.List[1].Saw != "1 app's settings" {
		t.Errorf("the to-dos ended %+v; want the apps to open next, done, kit having seen their settings back", l.List)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Contains(settled, "prefs:open") || report.Results["prefs"].State != check.OK {
		t.Errorf("settled %q, prefs stands %s; want the apps to open settled, then prefs ok", settled, report.Results["prefs"].State)
	}
}

// q's Stop, pressed while kit did what a key said, which did it, after the
// key was pressed twice, the second waiting unread: the Stop's taken all
// the same, the to-do is done, the to-do it left joins the list, and the
// run stops there, the rest staying to do.
func TestStoppingWhileAKeyIsDone(t *testing.T) {
	defer steps.SetManualPoll(time.Hour)()
	c := newCard(t)
	var mu sync.Mutex
	brought := false
	step := engine.Step{
		Name: "prefs", Title: "App settings", Area: steps.AreaBackups,
		Check: func(context.Context) check.Result {
			mu.Lock()
			defer mu.Unlock()
			if brought {
				return check.Result{State: check.Attention, Summary: "1 app to open once", Items: []check.Item{{ID: "prefs:open", Name: "Open Boxed App once and quit it", State: steps.Problem,
					ToDo: &check.ToDo{Title: "Open Boxed App once and quit it", Watch: "Boxed App has its settings folder", First: true}}}}
			}
			return check.Result{State: check.Attention, Summary: "paused", Items: []check.Item{toDoItem("prefs", check.ToDo{
				Title: "Bring back apps' settings", First: true, Keys: []check.Key{{Key: "r", Does: "brings them back"}},
			})}}
		},
		Choice: &engine.Choice{
			Answer: func(context.Context, check.Item, string) (string, error) {
				if !c.desk.Answer(steps.ManualName, "r") || !c.desk.Answer(steps.ManualName, hand.Stop) {
					t.Error("r again, then q's Stop, pressed while r was being done, weren't both passed on")
				}
				mu.Lock()
				defer mu.Unlock()
				brought = true
				return "214 apps' settings", nil
			},
		},
	}
	disk := ownStep("time-machine", toDoItem("time-machine", check.ToDo{Title: "Add a Time Machine disk", Group: "Backups"}))
	done := make(chan engine.Report, 1)
	go func() {
		done <- applied(t, c, atTheEnd(steps.Manual(c.fake, "/home/someone", c.state, config.List{}, c.raise), step, disk))
	}()
	c.cards(t, 1)
	c.answer(t, steps.ManualName, "r")
	over(t, done)
	last := toDoLists(c.sink)
	l := last[len(last)-1]
	var ids []string
	for _, e := range l.List {
		ids = append(ids, e.ID)
	}
	if l.Up != -1 || !slices.Equal(ids, []string{"prefs:none", "prefs:open", "time-machine:none"}) ||
		!l.List[0].Done || l.List[0].Saw != "214 apps' settings" || l.List[1].Done || l.List[2].Done {
		t.Errorf("the to-dos ended %+v; want the first done, the app to open after it, the run stopped there, the rest to do", l)
	}
	if got := len(c.cardsNow()); got != 2 {
		t.Errorf("%d cards went up, want the first, then it answered by r, and no more", got)
	}
}

// q's Stop, pressed while an answer waits unread as kit looks whether the
// to-do up is done: the Stop's taken all the same, and the run stops there.
func TestStoppingWhileKitLooks(t *testing.T) {
	defer steps.SetManualPoll(5 * time.Millisecond)()
	c := newCard(t)
	looking, carryOn := make(chan struct{}), make(chan struct{})
	var once sync.Once
	c.fake.On("test", "-f", "/printer").Exits(1).Then().Exits(1).Then().Exits(1).Does(func() {
		once.Do(func() {
			close(looking)
			<-carryOn
		})
	})
	list := config.List{Entries: []config.Entry{
		{Name: "printer", Value: `"Add the printer" --test test -f /printer`, Scope: "laptop", Line: 2},
		{Name: "scanner", Value: `"Add the scanner"`, Scope: "laptop", Line: 3},
	}}
	applied := applying(t, steps.Manual(c.fake, "/home/someone", c.state, list, c.raise))
	<-looking
	if !c.desk.Answer(steps.ManualName, hand.Done) || !c.desk.Answer(steps.ManualName, hand.Stop) {
		t.Error("space, then q's Stop, pressed while kit looked, weren't both passed on")
	}
	close(carryOn)
	if err := ended(t, applied); err != nil {
		t.Fatal(err)
	}
	if got := c.ups(); !slices.Equal(got, []string{"manual:printer"}) {
		t.Errorf("cards up %q, want the printer's alone", got)
	}
	if todos := c.lastToDos(); len(todos) != 2 || todos[0].Done || todos[1].Done {
		t.Errorf("the to-dos ended %+v, want both left to do", todos)
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
	c.noticed(t, 1)
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
	if got := c.posted(); !slices.Equal(got, want) {
		t.Errorf("notifications %q, want %q", got, want)
	}
}

// A choice to-do answered while a mid-run card waits behind it: the card
// shows the key being done, still counting the card waiting; done, the
// card waiting goes up next, then the to-do its step's check finds now,
// which follows on.
func TestAChoiceDoneWithACardWaiting(t *testing.T) {
	defer steps.SetManualPoll(5 * time.Millisecond)()
	c := newCard(t)
	gate := make(chan struct{})
	var mu sync.Mutex
	brought, opened := false, false
	var answering event.NeedsYou
	prefsStep := engine.Step{
		Name: "prefs", Title: "App settings", Area: steps.AreaBackups,
		Check: func(context.Context) check.Result {
			mu.Lock()
			defer mu.Unlock()
			switch {
			case !brought:
				return check.Result{State: check.Attention, Summary: "paused", Items: []check.Item{toDoItem("prefs", check.ToDo{
					Title: "Bring back apps' settings", First: true, Keys: []check.Key{{Key: "r", Does: "brings them back"}},
				})}}
			case !opened:
				return check.Result{State: check.Attention, Summary: "1 app to open once", Items: []check.Item{{ID: "prefs:open", Name: "Open Boxed App once and quit it", State: steps.Problem,
					ToDo: &check.ToDo{Title: "Open Boxed App once and quit it", Watch: "Boxed App has its settings folder", First: true}}}}
			}
			return check.Result{State: check.OK, Summary: "saving from tonight"}
		},
		Choice: &engine.Choice{
			Answer: func(context.Context, check.Item, string) (string, error) {
				shown := c.cardsNow()
				mu.Lock()
				defer mu.Unlock()
				answering, brought = shown[len(shown)-1], true
				return "1 app's settings; 1 app to open once", nil
			},
		},
	}
	vault := steps.OwnManual(c.fake, c.state, steps.ManualStep{Name: "vault", Do: "Sign in to the vault"}, c.raise)
	vault.After, vault.Before = []string{"cask"}, []string{"secret"}
	manual := steps.Manual(c.fake, "/home/someone", c.state, config.List{}, c.raise)
	manual.After = []string{"prefs"}
	p, err := engine.New(manual, prefsStep, vault, gated("cask", gate), fine("secret"))
	if err != nil {
		t.Fatal(err)
	}
	ran := running(t, c, p)
	c.upNow(t, "prefs:none")
	close(gate)
	c.cards(t, 2)
	c.answer(t, steps.ManualName, "r")
	c.upNow(t, "vault")
	c.answer(t, "vault", hand.Done)
	c.upNow(t, "prefs:open")
	mu.Lock()
	opened = true
	mu.Unlock()
	over(t, ran)

	mu.Lock()
	defer mu.Unlock()
	if answering.Item != "prefs:none" || answering.Answering != "r" || answering.More != 1 {
		t.Errorf("as r was done, the card up was %+v; want the to-do, answered by r, the vault's card counted after it", answering)
	}
	if got, want := c.ups(), []string{"prefs:none", "vault", "prefs:open"}; !slices.Equal(got, want) {
		t.Errorf("cards up %q, want %q", got, want)
	}
	if l := c.lastToDos(); len(l) != 2 || !l[0].Done || l[0].Saw != "1 app's settings; 1 app to open once" || l[1].ID != "prefs:open" || !l[1].Done {
		t.Errorf("the to-dos ended %+v; want r's done, what it did as what kit saw, then the app to open, done", l)
	}
}

// The run stopped while kit did what a key said, which didn't do it: the
// card isn't offered with its keys again.
func TestARunStoppedWhileAKeyIsDone(t *testing.T) {
	defer steps.SetManualPoll(time.Hour)()
	c := newCard(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	step := engine.Step{
		Name: "prefs", Title: "App settings", Area: steps.AreaBackups,
		Check: func(context.Context) check.Result {
			return check.Result{State: check.Attention, Summary: "paused", Items: []check.Item{toDoItem("prefs", check.ToDo{
				Title: "Bring back apps' settings", First: true, Keys: []check.Key{{Key: "r", Does: "brings them back"}},
			})}}
		},
		Choice: &engine.Choice{
			Answer: func(context.Context, check.Item, string) (string, error) {
				cancel()
				return "", nil
			},
		},
	}
	p, err := engine.New(atTheEnd(steps.Manual(c.fake, "/home/someone", c.state, config.List{}, c.raise), step)...)
	if err != nil {
		t.Fatal(err)
	}
	ran := make(chan engine.Report, 1)
	go func() {
		report, _ := p.Apply(ctx, c.sink, engine.Options{Command: "apply", Jobs: 4})
		ran <- report
	}()
	c.cards(t, 1)
	c.answer(t, steps.ManualName, "r")
	over(t, ran)
	var answering []string
	for _, card := range c.cardsNow() {
		answering = append(answering, card.Answering)
	}
	if !slices.Equal(answering, []string{"", "r"}) {
		t.Errorf("the card went up answered by %q; want its keys, then r while it worked, and its keys never again", answering)
	}
}

// settings is a step as apps' settings are: its check finds them paused, a
// choice, r bringing them back; then, brought back, an app to open once,
// watched, till it's opened; then saved. Each of its parts first does what
// the test has it do, if anything: as its check starts (checking), as r is
// done (bringing), as the card's readied, its r saying the save's date
// (readying, given what readying runs in), and as opening the app is
// settled (settling), cut short should the run have stopped by the time
// it's done (cut).
type settings struct {
	mu                           sync.Mutex
	brought, opened, cut         bool
	checking, bringing, settling func()
	readying                     func(ctx context.Context)
}

func (s *settings) step() engine.Step {
	return engine.Step{
		Name: "prefs", Title: "App settings", Area: steps.AreaBackups,
		Check: func(context.Context) check.Result {
			if f := s.part(func() func() { return s.checking }); f != nil {
				f()
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			switch {
			case !s.brought:
				return check.Result{State: check.Attention, Summary: "paused", Items: []check.Item{toDoItem("prefs", check.ToDo{
					Title: "Bring back apps' settings", First: true, Keys: []check.Key{{Key: "r", Does: "brings them back"}},
				})}}
			case !s.opened:
				return check.Result{State: check.Attention, Summary: "1 app to open once", Items: []check.Item{{ID: "prefs:open", Name: "Open Boxed App once and quit it", State: steps.Problem,
					ToDo: &check.ToDo{Title: "Open Boxed App once and quit it", Watch: "Boxed App has its settings folder", First: true}}}}
			}
			return check.Result{State: check.OK, Summary: "saved"}
		},
		Choice: &engine.Choice{
			Ready: func(ctx context.Context, it check.Item) check.Item {
				s.mu.Lock()
				readying := s.readying
				s.mu.Unlock()
				if readying != nil {
					readying(ctx)
				}
				if it.ToDo == nil || len(it.ToDo.Keys) == 0 {
					return it
				}
				t := *it.ToDo
				t.Keys = []check.Key{{Key: "r", Does: "brings them back, 15 Oct"}}
				it.ToDo = &t
				return it
			},
			Answer: func(context.Context, check.Item, string) (string, error) {
				if f := s.part(func() func() { return s.bringing }); f != nil {
					f()
				}
				s.mu.Lock()
				defer s.mu.Unlock()
				s.brought = true
				return "214 apps' settings", nil
			},
			Settle: func(ctx context.Context, it check.Item) string {
				if f := s.part(func() func() { return s.settling }); f != nil && it.ID == "prefs:open" {
					f()
					s.set(func(s *settings) { s.cut = ctx.Err() != nil })
				}
				return ""
			},
		},
	}
}

// part is the part of s get says, as it stands now.
func (s *settings) part(get func() func()) func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	return get()
}

// set has s's state, or a part, changed by change.
func (s *settings) set(change func(s *settings)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	change(s)
}

// held is a part that, its first time, says it's started (begun), and waits
// till it's let through (through).
func held() (part func(), begun, through chan struct{}) {
	begun, through = make(chan struct{}), make(chan struct{})
	var once sync.Once
	return func() {
		first := false
		once.Do(func() { first = true })
		if first {
			close(begun)
			<-through
		}
	}, begun, through
}

// endingWith applies other, then the steps by hand at the end, with none of
// their own, aside, the run's report coming once it ends.
func endingWith(t *testing.T, c *card, other engine.Step) <-chan engine.Report {
	done := make(chan engine.Report, 1)
	go func() {
		done <- applied(t, c, atTheEnd(steps.Manual(c.fake, "/home/someone", c.state, config.List{}, c.raise), other))
	}()
	return done
}

// stopped closes when the to-dos are emitted stopped, none up, as c's sink
// sees them.
func (c *card) stopped() <-chan struct{} {
	stopped := make(chan struct{})
	var once sync.Once
	c.sink.on = func(e event.Event) {
		if l, ok := e.(event.ToDos); ok && l.Up == -1 {
			once.Do(func() { close(stopped) })
		}
	}
	return stopped
}

// q's Stop while kit does what a key said acts at once, however long that
// takes: the card comes down and the list stops, the key's work left to
// finish, never cut short; done by it, the list says so after, with the
// to-do it leaves, left to do.
func TestAStopWhileAKeyIsDone(t *testing.T) {
	defer steps.SetManualPoll(time.Hour)()
	c := newCard(t)
	stopped := c.stopped()
	s := &settings{}
	bringing, begun, through := held()
	s.bringing = bringing
	done := endingWith(t, c, s.step())
	c.cards(t, 1)
	c.answer(t, steps.ManualName, "r")
	within(t, begun, "r was never done")
	c.answer(t, steps.ManualName, hand.Stop)
	within(t, stopped, "the list wasn't stopped while r was being done")
	if l := c.lastToDos(); l[0].Done || !slices.Equal(c.downs(), []string{"prefs:none"}) {
		t.Errorf("stopped, the to-dos stood %+v, cards down %q; want the card down, not done yet", l, c.downs())
	}
	close(through)
	over(t, done)
	l := c.lastToDos()
	if len(l) != 2 || !l[0].Done || l[0].Saw != "214 apps' settings" || l[1].ID != "prefs:open" || l[1].Done {
		t.Errorf("the to-dos ended %+v; want r's done, what it did as what kit saw, the app to open after it, left to do", l)
	}
}

// q's Stop acts at once while kit readies what the to-do up says, as the
// date of the last save, which takes cloning their store on a new Mac: its
// card's up once its grace is over, saying what it can meanwhile. Stopped
// there, readying's cut short, as it can never find the to-do done, and the
// run ends without waiting for it.
func TestAStopWhileTheCardIsReadied(t *testing.T) {
	defer steps.SetManualPoll(time.Hour)()
	c := newCard(t)
	stopped := c.stopped()
	s := &settings{}
	begun, cut := make(chan struct{}), make(chan struct{})
	s.readying = func(ctx context.Context) {
		close(begun)
		<-ctx.Done()
		close(cut)
	}
	done := endingWith(t, c, s.step())
	within(t, begun, "the card was never readied")
	if up := c.cardsNow(); len(up) > 0 {
		t.Fatalf("cards up %+v before the card's grace was over, its look not done", up)
	}
	c.waits.waiting(t, steps.DrawAfter, 1)
	c.waits.end(steps.DrawAfter)
	if up := c.cards(t, 1)[0]; !slices.Equal(up.Keys, []check.Key{{Key: "r", Does: "brings them back"}}) {
		t.Errorf("card = %+v, want it up with its keys as they stand", up)
	}
	c.answer(t, steps.ManualName, hand.Stop)
	within(t, stopped, "the list wasn't stopped while the card was readied")
	over(t, done)
	within(t, cut, "readying the card wasn't cut short as the person stopped")
	if up := c.cardsNow(); len(up) != 1 {
		t.Errorf("cards up %+v; want the one, never again once it came down", up)
	}
}

// A card readied once it's up, its look being quick and readying it slow,
// goes up again saying so.
func TestACardReadiedOnceUp(t *testing.T) {
	defer steps.SetManualPoll(time.Hour)()
	c := newCard(t)
	s := &settings{}
	readying, begun, through := held()
	s.readying = func(context.Context) { readying() }
	done := endingWith(t, c, s.step())
	within(t, begun, "the card was never readied")
	c.waits.waiting(t, steps.DrawAfter, 1)
	c.waits.end(steps.DrawAfter)
	c.cards(t, 1)
	close(through)
	if up := c.cards(t, 2)[1]; up.Item != "prefs:none" || !slices.Equal(up.Keys, []check.Key{{Key: "r", Does: "brings them back, 15 Oct"}}) || up.Answering != "" {
		t.Errorf("readied, the card = %+v; want it up again, its r dated", up)
	}
	c.answer(t, steps.ManualName, hand.Stop)
	over(t, done)
}

// q's Stop acts at once while kit looks for what a to-do done leaves: the
// list stops there, the one done kept done, and what the look finds joins
// it after, left to do.
func TestAStopWhileKitLooksForWhatFollows(t *testing.T) {
	defer steps.SetManualPoll(time.Hour)()
	c := newCard(t)
	stopped := c.stopped()
	s := &settings{}
	checking, begun, through := held()
	var checks atomic.Int32
	s.bringing = func() {
		// After r, its look finding it done, then the look for what follows,
		// held.
		s.set(func(s *settings) {
			s.checking = func() {
				if checks.Add(1) == 2 {
					checking()
				}
			}
		})
	}
	done := endingWith(t, c, s.step())
	c.cards(t, 1)
	c.answer(t, steps.ManualName, "r")
	within(t, begun, "kit never looked for what bringing them back left")
	c.answer(t, steps.ManualName, hand.Stop)
	within(t, stopped, "the list wasn't stopped while kit looked for what followed")
	if l := c.lastToDos(); len(l) != 1 || !l[0].Done {
		t.Errorf("stopped, the to-dos stood %+v; want r's done", l)
	}
	close(through)
	over(t, done)
	if l := c.lastToDos(); len(l) != 2 || !l[0].Done || l[1].ID != "prefs:open" || l[1].Done {
		t.Errorf("the to-dos ended %+v; want r's done, then the app to open, left to do", l)
	}
}

// q's Stop at the to-dos acts at once while the card's notification waits
// to go out, as one waits for kit's app to be built on a new Mac.
func TestAStopWhileItsNotificationWaits(t *testing.T) {
	c, h := heldCard(t)
	stopped := c.stopped()
	applied := applying(t, steps.Manual(c.fake, "/home/someone", c.state, printerAndScanner, c.raise))
	h.posting(t, "card:manual:manual:printer")
	c.answer(t, steps.ManualName, hand.Stop)
	within(t, stopped, "the list wasn't stopped while the card's notification waited")
	if err := ended(t, applied); err != nil {
		t.Fatal(err)
	}
	close(h.through)
	want := []string{"notify card:manual:manual:printer | Add the printer | kit waits for you to do this (taken away first)", "clear card:manual:manual:printer"}
	if got := c.posted(); !slices.Equal(got, want) {
		t.Errorf("notifications %q, want %q", got, want)
	}
}

// Once the person's stopped at the to-dos, no look begins, though one's
// due as they stop: the one up stays as it was.
func TestNoLookOnceStopped(t *testing.T) {
	defer steps.SetManualPoll(time.Hour)()
	for range 20 {
		c := newCard(t)
		c.waits = newWaits(time.Hour)
		c.raise.After = c.waits.after
		stopped := c.stopped()
		// Its check, then its look as its turn comes.
		c.fake.On("test", "-f", "/printer").Exits(1)
		list := config.List{Entries: []config.Entry{{Name: "printer", Value: `"Add the printer" --test test -f /printer`, Scope: "laptop", Line: 2}}}
		applied := applying(t, steps.Manual(c.fake, "/home/someone", c.state, list, c.raise))
		c.cards(t, 1)
		// The look as its turn came, then the wait for its next.
		c.waits.waiting(t, time.Hour, 2)
		c.answer(t, steps.ManualName, hand.Stop)
		<-stopped
		c.waits.end(time.Hour)
		if err := ended(t, applied); err != nil {
			t.Fatal(err)
		}
		c.raise.Wait()
		if runs := len(c.fake.Commands()); runs != 2 {
			t.Fatalf("ran %q; want its check and its look as its turn came, and none once stopped", c.fake.Calls())
		}
	}
}

// A key pressed while kit looks whether the to-do's done, that look finding
// it done, isn't done: the to-do's done, what the look saw as what kit saw.
func TestAKeyBehindALookThatFindsItDone(t *testing.T) {
	defer steps.SetManualPoll(time.Millisecond)()
	c := newCard(t)
	s := &settings{}
	checking, begun, through := held()
	s.bringing = func() { t.Error("r was done though the to-do was found done") }
	taken := make(chan struct{})
	var once sync.Once
	c.sink.on = func(e event.Event) {
		if up, ok := e.(event.NeedsYou); ok && up.Answering == "r" {
			once.Do(func() { close(taken) })
		}
	}
	done := endingWith(t, c, s.step())
	c.cards(t, 1)
	s.set(func(s *settings) { s.checking = checking })
	within(t, begun, "kit never looked again whether the to-do was done")
	c.answer(t, steps.ManualName, "r")
	within(t, taken, "the card never said it was being answered with r")
	s.set(func(s *settings) { s.brought, s.opened = true, true })
	close(through)
	over(t, done)
	if l := c.lastToDos(); len(l) != 1 || !l[0].Done || l[0].Saw != "saved" {
		t.Errorf("the to-dos ended %+v; want it done, kit having seen it saved", l)
	}
}

// The run stopped at the to-dos while kit settles what the one up waited
// for: the list stops at once, its card down, the settling left to finish,
// never cut short, and waited for as the run ends.
func TestARunStoppedWhileKitSettles(t *testing.T) {
	defer steps.SetManualPoll(time.Millisecond)()
	c := newCard(t)
	stopped := c.stopped()
	s := &settings{brought: true}
	settling, begun, through := held()
	s.settling = settling
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	p, err := engine.New(atTheEnd(steps.Manual(c.fake, "/home/someone", c.state, config.List{}, c.raise), s.step())...)
	if err != nil {
		t.Fatal(err)
	}
	ran := make(chan engine.Report, 1)
	go func() {
		report, _ := p.Apply(ctx, c.sink, engine.Options{Command: "apply", Jobs: 4})
		ran <- report
	}()
	c.cards(t, 1)
	s.set(func(s *settings) { s.opened = true })
	within(t, begun, "kit never settled what the to-do waited for")
	cancel()
	within(t, stopped, "the list wasn't stopped as the run was, while kit settled")
	over(t, ran)
	close(through)
	c.raise.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cut {
		t.Error("settling was cut short as the run stopped")
	}
}
