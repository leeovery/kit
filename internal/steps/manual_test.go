package steps_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/hand"
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
	step := steps.Manual(fake, "/home/someone", state, list, nil)
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
	if step.Apply != nil {
		t.Error("the steps by hand have an apply with no one to act on them")
	}
}

func TestManualLinesRead(t *testing.T) {
	for value, want := range map[string]string{
		`--test test -d x`:                      "what to do comes after the name",
		`"Do it" test -d x`:                     `"test" isn't one of a step by hand's options: --after, --before, --group and --test`,
		`"Do it" --open "System Settings"`:      `--open is gone: kit opens nothing, so say what to open in a step, a line indented under it, as in "Open 1Password"`,
		`"Do it" --group`:                       `--group needs the group's name after it, as in --group "First things"`,
		`"Do it" --group --test true`:           "--group needs the group's name after it",
		`"Do it" --group ""`:                    "--group needs the group's name after it",
		`"Do it" --group One --group Two`:       "--group is there twice: a step by hand is in one group",
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
	m, err := steps.ParseManual("/home", config.Entry{
		Name:  "1password",
		Value: `"Sign in" --after cask:1password --after cask:tap/one/1password-cli --group "First things" --before secret --test sh -c 'op account list | grep -q .' ~/bin --open x`,
		Notes: "It unlocks every secret.",
		Steps: []string{"Scan the code", "If it doesn't ask, in its settings:", "Turn on its CLI"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !m.Ordered() || strings.Join(m.After, ",") != "cask:1password,cask:tap/one/1password-cli" || strings.Join(m.Before, ",") != "secret" ||
		m.Command.Name != "sh" || strings.Join(m.Command.Args, "|") != "-c|op account list | grep -q .|/home/bin|--open|x" {
		t.Errorf("ParseManual() = %+v, %+v", m, m.Command)
	}
	if m.Group != "First things" || m.Why != "It unlocks every secret." || strings.Join(m.Steps, "|") != "Scan the code|If it doesn't ask, in its settings:|Turn on its CLI" {
		t.Errorf("ParseManual() is in group %q, why %q, its steps %q", m.Group, m.Why, m.Steps)
	}
	// --after alone only orders a line: it's one of the rest, at the end.
	if m, err := steps.ParseManual("/home", config.Entry{Name: "x", Value: `"Do it" --after cask:jetbrains-toolbox`}); err != nil || m.Ordered() {
		t.Errorf("ParseManual(--after alone) = %+v, %v; want it not a step of its own", m, err)
	}
	if m, err := steps.ParseManual("/home", config.Entry{Name: "x", Value: `"Do it" --group setup`}); err != nil || m.Group != "setup" {
		t.Errorf("ParseManual(--group setup) = %+v, %v; want it in the group setup", m, err)
	}
}

// The steps by hand are listed with the step raising them, from the start of
// a run: the rest's, every line that reads, done or not, in order; one of
// its own, its line alone.
func TestTheStepsByHandAreListed(t *testing.T) {
	state := t.TempDir()
	if err := steps.MarkDone(state, []string{"sign-in"}, time.Now(), false); err != nil {
		t.Fatal(err)
	}
	list := config.List{Entries: []config.Entry{
		{Name: "sign-in", Value: `"Sign in to the service"`, Scope: "laptop", Line: 2},
		{Name: "broken", Value: `--test true`, Scope: "laptop", Line: 3},
		{Name: "backups", Value: `"Turn backups on" --group Mac`, Notes: "Nothing's backed up till then.", Scope: "laptop", Line: 4},
	}}
	own := steps.ManualStep{Name: "licence", Do: "Enter the app's licence", After: []string{steps.ManualName}}
	p, err := engine.New(steps.Manual(nil, "/home/someone", state, list, nil), steps.OwnManual(nil, state, own, nil))
	if err != nil {
		t.Fatal(err)
	}
	sink := &events{}
	if _, err := p.Check(t.Context(), sink, engine.Options{Jobs: 1}); err != nil {
		t.Fatal(err)
	}
	start, ok := sink.seen[0].(event.RunStarted)
	if !ok || len(start.Steps) != 2 {
		t.Fatalf("the run began with %+v, want its start, with both steps", sink.seen[0])
	}
	want := map[string][]event.Hand{
		steps.ManualName: {{Name: "sign-in", Title: "Sign in to the service"}, {Name: "backups", Title: "Turn backups on", Group: "Mac", Why: "Nothing's backed up till then."}},
		"licence":        {{Name: "licence", Title: "Enter the app's licence"}},
	}
	for _, s := range start.Steps {
		if !slices.Equal(s.ByHand, want[s.Name]) {
			t.Errorf("%s lists %+v by hand, want %+v", s.Name, s.ByHand, want[s.Name])
		}
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

// card is a run at a terminal, as steps by hand are raised in it: what's
// emitted, the desk the person answers cards on, and the programs run.
type card struct {
	raise *steps.Raise
	sink  *events
	desk  *hand.Desk
	fake  *runnertest.Fake
	state string
}

func newCard(t *testing.T) *card {
	fake := runnertest.New(t)
	c := &card{sink: &events{}, desk: hand.New(), fake: fake, state: t.TempDir()}
	c.raise = &steps.Raise{Sink: c.sink, Now: time.Now, Desk: c.desk}
	return c
}

// cards waits till n cards have gone up, as the events say, a card put up
// again among them, and returns them.
func (c *card) cards(t *testing.T, n int) []event.NeedsYou {
	t.Helper()
	for range 5000 {
		var up []event.NeedsYou
		c.sink.mu.Lock()
		for _, e := range c.sink.seen {
			if card, ok := e.(event.NeedsYou); ok {
				up = append(up, card)
			}
		}
		c.sink.mu.Unlock()
		if len(up) >= n {
			return up
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("%d cards never went up", n)
	return nil
}

// answer answers step's card with a, once it's taken.
func (c *card) answer(t *testing.T, step string, a hand.Answer) {
	t.Helper()
	for range 5000 {
		if c.desk.Answer(step, a) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("%s's card never took %v", step, a)
}

// applying applies step, as the run would, its check's finding given it.
func applying(t *testing.T, step engine.Step) <-chan error {
	found := step.Check(t.Context())
	done := make(chan error, 1)
	go func() { done <- step.Apply(t.Context(), found) }()
	return done
}

// ended waits for applying to end, and returns how it did.
func ended(t *testing.T, applying <-chan error) error {
	t.Helper()
	select {
	case err := <-applying:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("applying the step by hand never ended")
		return nil
	}
}

// A step by hand of its own, come due at a terminal, goes up as a card: its
// line's name, what to do, its group, why, its steps, whether kit tells by
// itself when it's done, and a notification posted; kit opens nothing.
// Done, with no test, marks it done on this Mac, as kit manual done does.
func TestACardGoesUpAndIsDone(t *testing.T) {
	c := newCard(t)
	c.fake.On("osascript", "-e", `display notification "Open System Settings" with title "kit" subtitle "Turn on sharing"`)
	c.raise.Notify = steps.Notify(c.fake)
	m := steps.ManualStep{Name: "sharing", Do: "Turn on sharing", Why: "For logging in from elsewhere.", Group: "Mac", After: []string{"cask"}, Steps: []string{"Open System Settings", "General › Sharing", "Turn on Remote Login"}}
	step := steps.OwnManual(c.fake, c.state, m, c.raise)
	applied := applying(t, step)

	up := c.cards(t, 1)[0]
	want := event.NeedsYou{Time: up.Time, Step: "sharing", Item: "sharing", Title: "Turn on sharing", Group: "Mac", Why: "For logging in from elsewhere.", Steps: m.Steps}
	if !reflect.DeepEqual(up, want) || !slices.Equal(c.fake.Calls(), []string{`osascript -e 'display notification "Open System Settings" with title "kit" subtitle "Turn on sharing"'`}) {
		t.Errorf("card = %+v, ran %q; want %+v, a notification posted, nothing else run", up, c.fake.Calls(), want)
	}
	c.answer(t, "sharing", hand.Done)
	if err := ended(t, applied); err != nil {
		t.Fatal(err)
	}
	if res := step.Check(t.Context()); res.State != check.OK {
		t.Errorf("after Done, check = %+v, want it marked done", res)
	}
}

// A card waiting for an answer is done, too, once it's marked done
// elsewhere, as by kit manual done in another window.
func TestACardMarkedDoneElsewhere(t *testing.T) {
	defer steps.SetManualPoll(10 * time.Millisecond)()
	c := newCard(t)
	m := steps.ManualStep{Name: "paper", Do: "Install Paper"}
	step := steps.OwnManual(c.fake, c.state, m, c.raise)
	applied := applying(t, step)
	c.cards(t, 1)
	if err := steps.MarkDone(c.state, []string{"paper"}, time.Now(), false); err != nil {
		t.Fatal(err)
	}
	if err := ended(t, applied); err != nil {
		t.Fatal(err)
	}
}

// A step by hand with a test is done when its test passes: looked at every
// few seconds, and at once for Done, the card staying up while it fails.
func TestACardWithATest(t *testing.T) {
	defer steps.SetManualPoll(time.Hour)()
	c := newCard(t)
	// Its check, its look as it goes up, Done while it still fails, Done.
	c.fake.On("test", "-f", "/signed-in").Exits(1).Then().Exits(1).Then().Exits(1).Then()
	m := steps.ManualStep{Name: "sign-in", Do: "Sign in", Before: []string{"secret"}, Command: &runner.Command{Name: "test", Args: []string{"-f", "/signed-in"}}}
	applied := applying(t, steps.OwnManual(c.fake, c.state, m, c.raise))
	if up := c.cards(t, 1)[0]; !up.Tells {
		t.Errorf("card = %+v, want it to say kit tells when it's done", up)
	}
	c.answer(t, "sign-in", hand.Done)
	c.answer(t, "sign-in", hand.Done)
	if err := ended(t, applied); err != nil {
		t.Fatal(err)
	}
	if runs := len(c.fake.Commands()); runs != 4 {
		t.Errorf("ran %q, want the test run at once for each Done", c.fake.Calls())
	}
}

// Raised with no one there, a step by hand with a test is done once it
// passes, looked at every few seconds.
func TestACardWithATestLooksAgain(t *testing.T) {
	defer steps.SetManualPoll(time.Millisecond)()
	c := newCard(t)
	c.fake.On("test", "-d", "/Applications/App.app").Exits(1).Then().Exits(1).Then().Exits(1).Then()
	m := steps.ManualStep{Name: "app", Do: "Install the app", After: []string{"cask"}, Command: &runner.Command{Name: "test", Args: []string{"-d", "/Applications/App.app"}}}
	step := steps.OwnManual(c.fake, c.state, m, c.raise)
	if err := ended(t, applying(t, step)); err != nil {
		t.Fatal(err)
	}
	if res := step.Check(t.Context()); res.State != check.OK || len(c.fake.Commands()) < 4 {
		t.Errorf("check after = %+v, ran %q: want it looked at till it passed", res, c.fake.Calls())
	}
}

// There's no putting a card off: a step by hand raised ends done, or with
// the run stopped, left to do. With no one to act, applying leaves it as it
// stands.
func TestACardIsDoneOrStopped(t *testing.T) {
	c := newCard(t)
	m := steps.ManualStep{Name: "sign-in", Do: "Sign in", Before: []string{"secret"}}
	if err := steps.OwnManual(c.fake, c.state, m, nil).Apply(t.Context(), check.Result{}); err != nil {
		t.Errorf("apply without anyone to act = %v", err)
	}
	step := steps.OwnManual(c.fake, c.state, m, c.raise)
	ctx, stop := context.WithCancel(t.Context())
	applied := make(chan error, 1)
	go func() { applied <- step.Apply(ctx, step.Check(ctx)) }()
	c.cards(t, 1)
	stop()
	if err := ended(t, applied); !errors.Is(err, context.Canceled) {
		t.Errorf("stopped while its card was up = %v, want it stopped", err)
	}
	res := step.Check(t.Context())
	if !res.ToDo() || res.Items[0].State != check.ByHand || res.Items[0].Detail != "then: kit manual done sign-in" {
		t.Errorf("check after = %+v, want it left to do", res)
	}
}

// One card is up at a time: one coming due meanwhile waits its turn,
// counted in the More of the card up, which goes up again to say so, and
// goes up once that's answered; one that stops waiting isn't counted.
func TestOneCardAtATime(t *testing.T) {
	c := newCard(t)
	own := func(name string) engine.Step {
		return steps.OwnManual(c.fake, c.state, steps.ManualStep{Name: name, Do: "Do " + name, After: []string{"cask"}}, c.raise)
	}
	first := applying(t, own("first"))
	c.cards(t, 1)
	second := applying(t, own("second"))
	ctx, stop := context.WithCancel(t.Context())
	third := make(chan error, 1)
	go func() { third <- own("third").Apply(ctx, check.Result{}) }()
	up := c.cards(t, 3)
	if up[1].Item != "first" || up[1].More != 1 || up[2].Item != "first" || up[2].More != 2 {
		t.Fatalf("cards = %+v, want the first's put up again, 1 then 2 more after it", up)
	}
	stop()
	if err := ended(t, third); !errors.Is(err, context.Canceled) {
		t.Errorf("the third, stopped while it waited = %v", err)
	}
	if up := c.cards(t, 4)[3]; up.Item != "first" || up.More != 1 {
		t.Errorf("card = %+v, want the first's, 1 more after it", up)
	}
	c.answer(t, "first", hand.Done)
	if err := ended(t, first); err != nil {
		t.Fatal(err)
	}
	if up := c.cards(t, 5)[4]; up.Item != "second" || up.More != 0 {
		t.Errorf("card = %+v, want the second's, none after it", up)
	}
	c.answer(t, "second", hand.Done)
	if err := ended(t, second); err != nil {
		t.Fatal(err)
	}
	if n := len(c.cards(t, 5)); n != 5 {
		t.Errorf("%d cards went up, want 5", n)
	}
}

// The rest of the steps by hand, raised, come up one card each, in order,
// those not done: each saying how many follow it; one done, the next comes
// up.
func TestTheRestOneCardEach(t *testing.T) {
	c := newCard(t)
	c.fake.On("test", "-d", "/Applications/Done.app")
	// Its check, its look as it goes up, Done once it's there.
	c.fake.On("test", "-d", "/Applications/App.app").Exits(1).Then().Exits(1).Then()
	list := config.List{Entries: []config.Entry{
		{Name: "app", Value: `"Install the app" --test test -d /Applications/App.app`, Scope: "laptop", Line: 2},
		{Name: "done", Value: `"Install the done app" --test test -d /Applications/Done.app`, Scope: "laptop", Line: 3},
		{Name: "sign-in", Value: `"Sign in to the service"`, Scope: "laptop", Line: 4, Steps: []string{"Open the service", "Sign in"}},
	}}
	step := steps.Manual(c.fake, "/home/someone", c.state, list, c.raise)
	applied := applying(t, step)
	if up := c.cards(t, 1)[0]; up.Step != steps.ManualName || up.Item != "app" || up.More != 1 || !up.Tells {
		t.Errorf("first card = %+v, want the app's, 1 more after it", up)
	}
	c.answer(t, steps.ManualName, hand.Done)
	if up := c.cards(t, 2)[1]; up.Item != "sign-in" || up.More != 0 || up.Tells || !slices.Equal(up.Steps, []string{"Open the service", "Sign in"}) {
		t.Errorf("second card = %+v, want the sign-in's, its steps, none after it", up)
	}
	c.answer(t, steps.ManualName, hand.Done)
	if err := ended(t, applied); err != nil {
		t.Fatal(err)
	}
	if res := step.Check(t.Context()); res.State != check.OK {
		t.Errorf("check after = %+v, want all done", res)
	}
}
