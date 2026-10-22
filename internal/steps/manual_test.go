package steps_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
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
		`"Do it" --open "System Settings"`:      `--open is gone: a step by hand of yours opens nothing, so say what to open in a step, a line indented under it, as in "Open Firefox"`,
		`"Do it" --group`:                       `--group needs the group's name after it, as in --group "First things"`,
		`"Do it" --group --test true`:           "--group needs the group's name after it",
		`"Do it" --group ""`:                    "--group needs the group's name after it",
		`"Do it" --group One --group Two`:       "--group is there twice: a step by hand is in one group",
		`"Do it" --test`:                        "--test needs the command",
		`"Do it" -- test -d x`:                  "comes after --test now",
		`"Do it" --after`:                       "--after needs a step's name after it",
		`"Do it" --before cask:1password`:       "--before a step",
		`"Do it" --after cask: --test true`:     "one of its things as in cask:firefox",
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

// events notes the events a step emits, each then given to on, when set,
// as the step emits it.
type events struct {
	mu   sync.Mutex
	seen []event.Event
	on   func(event.Event)
}

func (e *events) Emit(ev event.Event) {
	e.mu.Lock()
	e.seen = append(e.seen, ev)
	e.mu.Unlock()
	if e.on != nil {
		e.on(ev)
	}
}

// notices notes the notifications posted and taken away: a clear in a
// context that's done, so that it can't run, says so.
type notices struct {
	mu   sync.Mutex
	seen []string
}

func (n *notices) Notify(_ context.Context, id, title, body string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.seen = append(n.seen, "notify "+id+" | "+title+" | "+body)
}

func (n *notices) Clear(ctx context.Context, id string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if ctx.Err() != nil {
		id += " (in a context that's done)"
	}
	n.seen = append(n.seen, "clear "+id)
}

func (n *notices) posted() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return slices.Clone(n.seen)
}

// posted are the notifications posted and taken away, once all given have
// gone out.
func (c *card) posted() []string {
	c.raise.Wait()
	return c.notices.posted()
}

// noticed waits till n notifications have been posted: a card's goes once
// a look finds it not done, after it went up.
func (c *card) noticed(t *testing.T, n int) {
	t.Helper()
	for range 5000 {
		var got int
		for _, p := range c.notices.posted() {
			if strings.HasPrefix(p, "notify ") {
				got++
			}
		}
		if got >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("%d notifications never posted", n)
}

// card is a run at a terminal, as steps by hand are raised in it: what's
// emitted, the desk the person answers cards on, the programs run, and the
// notifications posted.
type card struct {
	// held is whether its notifications wait to go out (heldCard).
	held    bool
	raise   *steps.Raise
	sink    *events
	desk    *hand.Desk
	fake    *runnertest.Fake
	notices *notices
	state   string
}

func newCard(t *testing.T) *card {
	fake := runnertest.New(t)
	c := &card{sink: &events{}, desk: hand.New(), fake: fake, notices: &notices{}, state: t.TempDir()}
	c.raise = &steps.Raise{Sink: c.sink, Now: time.Now, Notify: c.notices, Desk: c.desk}
	return c
}

// cards waits till n cards have gone up, as the events say, a card put up
// again among them, and returns them, their notifications gone out, unless
// they're held.
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
			if !c.held {
				steps.NoticesSent(c.raise)
			}
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
// itself when it's done, and a notification posted: its title what to do,
// its body the first step; kit opens nothing, and runs nothing. Done, with
// no test, marks it done on this Mac, as kit manual done does, takes its
// card down, and takes the notification away.
func TestACardGoesUpAndIsDone(t *testing.T) {
	c := newCard(t)
	m := steps.ManualStep{Name: "sharing", Do: "Turn on sharing", Why: "For logging in from elsewhere.", Group: "Mac", After: []string{"cask"}, Steps: []string{"Open System Settings", "General › Sharing", "Turn on Remote Login"}}
	step := steps.OwnManual(c.fake, c.state, m, c.raise)
	applied := applying(t, step)

	up := c.cards(t, 1)[0]
	want := event.NeedsYou{Time: up.Time, Step: "sharing", Item: "sharing", Title: "Turn on sharing", Group: "Mac", Why: "For logging in from elsewhere.", Steps: m.Steps}
	if !reflect.DeepEqual(up, want) || len(c.fake.Calls()) != 0 {
		t.Errorf("card = %+v, ran %q; want %+v, nothing run", up, c.fake.Calls(), want)
	}
	c.noticed(t, 1)
	c.answer(t, "sharing", hand.Done)
	if err := ended(t, applied); err != nil {
		t.Fatal(err)
	}
	if got := c.downs(); !slices.Equal(got, []string{"sharing"}) {
		t.Errorf("cards down %q, want its card taken down as it was done", got)
	}
	if res := step.Check(t.Context()); res.State != check.OK {
		t.Errorf("after Done, check = %+v, want it marked done", res)
	}
	notified := []string{"notify card:sharing:sharing | Turn on sharing | Open System Settings", "clear card:sharing:sharing"}
	if got := c.posted(); !slices.Equal(got, notified) {
		t.Errorf("notifications %q, want %q", got, notified)
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

// A step by hand of kit's own, as a store's sign-in, is told done as its
// Done says: done, it's never raised; not done, its card goes up, watched,
// kit opening its app as it first comes up, and comes down by itself once
// Done says so. Raised again, the app isn't opened again.
func TestKitsOwnCardIsWatchedAndOpensItsApp(t *testing.T) {
	defer steps.SetManualPoll(time.Millisecond)()
	c := newCard(t)
	var signedIn atomic.Bool
	opened := make(chan struct{}, 2)
	c.fake.On("open", "-b", "com.example.vault").Does(func() { opened <- struct{}{} })
	m := steps.ManualStep{Name: "vault-sign-in", Do: "Sign in to the vault", Why: "Your secrets come from it.", Steps: []string{"Scan the code"}, Before: []string{"secret"},
		Done: func(context.Context) bool { return signedIn.Load() }, Open: "com.example.vault"}
	step := steps.OwnManual(c.fake, c.state, m, c.raise)
	if res := step.Check(t.Context()); res.State != check.Attention || res.Items[0].Detail != "" {
		t.Errorf("check = %+v, want it not done, kit telling when it is", res)
	}
	applied := applying(t, step)
	up := c.cards(t, 1)[0]
	if up.Title != "Sign in to the vault" || up.Why != "Your secrets come from it." || !slices.Equal(up.Steps, m.Steps) || !up.Tells {
		t.Errorf("card = %+v, want what the step says, watched", up)
	}
	<-opened
	signedIn.Store(true)
	if err := ended(t, applied); err != nil {
		t.Fatal(err)
	}
	if res := step.Check(t.Context()); res.State != check.OK {
		t.Errorf("check once signed in = %+v", res)
	}
	signedIn.Store(false)
	again := make(chan error, 1)
	go func() { again <- step.Again(t.Context()) }()
	c.cards(t, 2)
	signedIn.Store(true)
	if err := ended(t, again); err != nil {
		t.Fatal(err)
	}
	if got := c.fake.Calls(); !slices.Equal(got, []string{"open -b com.example.vault"}) {
		t.Errorf("ran %q, want the app opened once", got)
	}

	done := steps.OwnManual(c.fake, c.state, m, c.raise)
	if err := ended(t, applying(t, done)); err != nil || !slices.Equal(c.downs(), []string{"vault-sign-in", "vault-sign-in", "vault-sign-in"}) {
		t.Errorf("signed in already: applied = %v, cards down %q; want its card straight down, its first look finding it done", err, c.downs())
	}
}

// downs are the items of the cards taken down, as the events say, in order.
func (c *card) downs() []string {
	c.sink.mu.Lock()
	defer c.sink.mu.Unlock()
	var out []string
	for _, e := range c.sink.seen {
		if d, ok := e.(event.CardDown); ok {
			out = append(out, d.Item)
		}
	}
	return out
}

// There's no putting a card off: a step by hand raised ends done, or with
// the run stopped, left to do, its notification, saying kit waits for it
// as it has no steps, taken away all the same. With no one to act, applying
// leaves it as it stands.
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
	c.noticed(t, 1)
	stop()
	if err := ended(t, applied); !errors.Is(err, context.Canceled) {
		t.Errorf("stopped while its card was up = %v, want it stopped", err)
	}
	res := step.Check(t.Context())
	if !res.ToDo() || res.Items[0].State != check.ByHand || res.Items[0].Detail != "then: kit manual done sign-in" {
		t.Errorf("check after = %+v, want it left to do", res)
	}
	want := []string{"notify card:sign-in:sign-in | Sign in | kit waits for you to do this", "clear card:sign-in:sign-in"}
	if got := c.posted(); !slices.Equal(got, want) {
		t.Errorf("notifications %q, want %q", got, want)
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

// turns are the cards up, as "up <item> <how many after>", and each step
// saying its card waits its turn, as "queued <step>", in the order emitted.
func (c *card) turns() []string {
	c.sink.mu.Lock()
	defer c.sink.mu.Unlock()
	var out []string
	for _, e := range c.sink.seen {
		switch e := e.(type) {
		case event.NeedsYou:
			out = append(out, fmt.Sprintf("up %s %d", e.Item, e.More))
		case event.Doing:
			if e.Says == event.Queued {
				out = append(out, "queued "+e.Step)
			}
		}
	}
	return out
}

// gated is a step whose applying ends once gate's closed: for a step by
// hand after it to come due then.
func gated(name string, gate <-chan struct{}) engine.Step {
	return engine.Step{Name: name,
		Check: func(context.Context) check.Result {
			return check.Result{State: check.Attention, Items: []check.Item{{ID: name + ":it", Name: "it", State: "missing", Action: "install"}}}
		},
		Apply: func(context.Context, check.Result) error {
			<-gate
			return nil
		}}
}

// fine is a step that stands ok, after those named.
func fine(name string, after ...string) engine.Step {
	return engine.Step{Name: name, After: after, Check: func(context.Context) check.Result { return check.Result{State: check.OK, Summary: "fine"} }}
}

// running applies p, its steps by hand raised on c, and returns its report
// once it ends.
func running(t *testing.T, c *card, p *engine.Pipeline) <-chan engine.Report {
	ran := make(chan engine.Report, 1)
	go func() {
		report, _ := p.Apply(t.Context(), c.sink, engine.Options{Jobs: 4})
		ran <- report
	}()
	return ran
}

// over waits for the run to end, and returns its report.
func over(t *testing.T, ran <-chan engine.Report) engine.Report {
	t.Helper()
	select {
	case report := <-ran:
		return report
	case <-time.After(5 * time.Second):
		t.Fatal("the run never ended")
		return engine.Report{}
	}
}

// Cards come up in order of how much of the run waits on them, through the
// steps that wait on those too: one coming due that more waits on goes up
// in place of the card up, whose step says again it waits its turn first,
// and which comes back once that's done. Tab moves on from the card up to
// the next waiting, the one left staying to do and coming back. Each card's
// notification is posted each time it comes up, and taken away each time
// it goes down, set aside or done, so only the card up has one: the
// store's taken away and posted again though its card's set aside and back
// before its step looks, held looking whether it's done, as space on a
// card kit watches has it look at once.
func TestCardsComeUpByWhatWaitsOnThem(t *testing.T) {
	defer steps.SetManualPoll(time.Hour)()
	c := newCard(t)
	gate, looking, looked := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var hold, signedIn atomic.Bool
	store := steps.OwnManual(c.fake, c.state, steps.ManualStep{Name: "store", Do: "Sign in to the store", Done: func(context.Context) bool {
		if hold.CompareAndSwap(true, false) {
			close(looking)
			<-looked
		}
		return signedIn.Load()
	}}, c.raise)
	store.Before = []string{"apps"}
	vault := steps.OwnManual(c.fake, c.state, steps.ManualStep{Name: "vault", Do: "Sign in to the vault"}, c.raise)
	vault.After, vault.Before = []string{"cask"}, []string{"secret"}
	p, err := engine.New(store, vault, gated("cask", gate), fine("apps"), fine("secret"), fine("git", "secret"), fine("tool", "git"))
	if err != nil {
		t.Fatal(err)
	}
	ran := running(t, c, p)
	if up := c.cards(t, 1)[0]; up.Item != "store" {
		t.Fatalf("first card = %+v, want the store's, the only one come due", up)
	}
	c.noticed(t, 1)
	hold.Store(true)
	c.answer(t, "store", hand.Done)
	<-looking
	close(gate)
	c.cards(t, 2)
	c.noticed(t, 2)
	c.answer(t, "vault", hand.Next)
	c.turnsSeen(t, 6)
	close(looked)
	c.cards(t, 3)
	c.noticed(t, 3)
	signedIn.Store(true)
	c.answer(t, "store", hand.Done)
	c.cards(t, 4)
	c.answer(t, "vault", hand.Done)
	report := over(t, ran)

	want := []string{"queued store", "up store 0", "queued vault", "queued store", "up vault 1", "queued vault", "up store 1", "up vault 0"}
	if got := c.turns(); !slices.Equal(got, want) {
		t.Errorf("turns %q, want %q", got, want)
	}
	if report.Results["store"].State != check.OK || report.Results["vault"].State != check.OK {
		t.Errorf("store %+v, vault %+v; want both done", report.Results["store"], report.Results["vault"])
	}
	for _, item := range []string{"store", "vault"} {
		id := "card:" + item + ":" + item
		var got []string
		for _, n := range c.posted() {
			if f := strings.Fields(n); f[1] == id {
				got = append(got, f[0])
			}
		}
		if want := []string{"notify", "clear", "notify", "clear"}; !slices.Equal(got, want) {
			t.Errorf("%s's notifications %q, want %q: posted as it comes up, taken away as it goes down", item, got, want)
		}
	}
}

// upNow waits till the card up, as the events last say, is item's.
func (c *card) upNow(t *testing.T, item string) {
	t.Helper()
	for range 5000 {
		c.sink.mu.Lock()
		last := ""
		for _, e := range c.sink.seen {
			if card, ok := e.(event.NeedsYou); ok {
				last = card.Item
			}
		}
		c.sink.mu.Unlock()
		if last == item {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("%s's card never went up", item)
}

// turnsSeen waits till n turns have been taken, as turns has them.
func (c *card) turnsSeen(t *testing.T, n int) {
	t.Helper()
	for range 5000 {
		if len(c.turns()) >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("%d turns never taken: %q", n, c.turns())
}

// ups are the cards that went up, by item, in order, one put up again
// straight after itself counted once.
func (c *card) ups() []string {
	var out []string
	for _, turn := range c.turns() {
		if f := strings.Fields(turn); f[0] == "up" && (len(out) == 0 || out[len(out)-1] != f[1]) {
			out = append(out, f[1])
		}
	}
	return out
}

// finishedAt is where the step called step finishing is among the events,
// -1 for not yet.
func (c *card) finishedAt(step string) int {
	c.sink.mu.Lock()
	defer c.sink.mu.Unlock()
	return slices.IndexFunc(c.sink.seen, func(e event.Event) bool {
		f, ok := e.(event.StepFinished)
		return ok && f.Step == step
	})
}

// storeAndVault is a run whose store's sign-in comes due first, holding up
// the App Store's apps; then, once gate's closed, the vault's, holding up
// more: the secrets, and what waits on them. vault is the vault's line.
func storeAndVault(c *card, gate <-chan struct{}, vault steps.ManualStep) *engine.Pipeline {
	store := steps.OwnManual(c.fake, c.state, steps.ManualStep{Name: "store", Do: "Sign in to the store"}, c.raise)
	store.Before = []string{"apps"}
	v := steps.OwnManual(c.fake, c.state, vault, c.raise)
	v.After, v.Before = []string{"cask"}, []string{"secret"}
	p, err := engine.New(store, v, gated("cask", gate), fine("apps"), fine("secret"), fine("git", "secret"), fine("tool", "git"))
	if err != nil {
		panic(err)
	}
	return p
}

// A card coming due that more waits on, done by the time its turn comes,
// goes up, and straight down as its first look finds it done, leaving the
// line, with no notification posted, and the card it set aside goes up
// again: none is lost, its run carrying on.
func TestACardSetAsideComesBackWhenTheOneBeforeItIsDone(t *testing.T) {
	defer steps.SetManualPoll(time.Hour)()
	for range 50 {
		c := newCard(t)
		gate := make(chan struct{})
		// Its check finds it not done; done by the time its turn comes.
		c.fake.On("test", "-f", "/signed-in").Exits(1).Then()
		ran := running(t, c, storeAndVault(c, gate, steps.ManualStep{Name: "vault", Do: "Sign in to the vault", Command: &runner.Command{Name: "test", Args: []string{"-f", "/signed-in"}}}))
		c.upNow(t, "store")
		close(gate)
		c.cards(t, 3)
		c.answer(t, "store", hand.Done)
		over(t, ran)
		want := []string{"queued store", "up store 0", "queued vault", "queued store", "up vault 1", "up store 0"}
		if got := c.turns(); !slices.Equal(got, want) {
			t.Fatalf("turns %q, want %q", got, want)
		}
		if got := c.downs(); !slices.Equal(got, []string{"vault", "store"}) {
			t.Fatalf("cards down %q, want the vault's, then the store's", got)
		}
		for _, n := range c.posted() {
			if strings.Contains(n, "card:vault:vault") {
				t.Fatalf("posted %q: want no notification for a card found done as it went up", n)
			}
		}
	}
}

// Tab moving on to a card done meanwhile, it goes up, and straight down as
// its first look finds it done, the card left going up again at once.
func TestTabToACardDoneMeanwhile(t *testing.T) {
	defer steps.SetManualPoll(time.Hour)()
	for range 50 {
		c := newCard(t)
		gate := make(chan struct{})
		ran := running(t, c, storeAndVault(c, gate, steps.ManualStep{Name: "vault", Do: "Sign in to the vault"}))
		c.upNow(t, "store")
		close(gate)
		c.upNow(t, "vault")
		if err := steps.MarkDone(c.state, []string{"store"}, time.Now(), false); err != nil {
			t.Fatal(err)
		}
		c.answer(t, "vault", hand.Next)
		c.cards(t, 4)
		c.upNow(t, "vault")
		c.answer(t, "vault", hand.Done)
		over(t, ran)
		want := []string{"queued store", "up store 0", "queued vault", "queued store", "up vault 1", "queued vault", "up store 1", "up vault 0"}
		if got := c.turns(); !slices.Equal(got, want) {
			t.Fatalf("turns %q, want %q", got, want)
		}
	}
}

// A card set aside is still looked at every few seconds: done meanwhile,
// it leaves the line, its step finishing and what waits on it carrying on
// at once, while the card up stays up, counting one fewer after it.
func TestACardSetAsideDoneMeanwhile(t *testing.T) {
	defer steps.SetManualPoll(5 * time.Millisecond)()
	c := newCard(t)
	gate := make(chan struct{})
	ran := running(t, c, storeAndVault(c, gate, steps.ManualStep{Name: "vault", Do: "Sign in to the vault"}))
	c.upNow(t, "store")
	close(gate)
	c.upNow(t, "vault")
	if err := steps.MarkDone(c.state, []string{"store"}, time.Now(), false); err != nil {
		t.Fatal(err)
	}
	for range 5000 {
		if c.finishedAt("apps") >= 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if c.finishedAt("apps") < 0 {
		t.Fatal("the App Store's apps were still held with the store's sign-in done")
	}
	c.answer(t, "vault", hand.Done)
	report := over(t, ran)
	want := []string{"queued store", "up store 0", "queued vault", "queued store", "up vault 1", "up vault 0"}
	if got := c.turns(); !slices.Equal(got, want) {
		t.Errorf("turns %q, want %q", got, want)
	}
	if report.Results["store"].State != check.OK {
		t.Errorf("the store's sign-in stands %+v; want it done", report.Results["store"])
	}
}

// The to-dos at the run's end are held: one up stays up till it's done,
// though a card more of the run waits on comes due meanwhile, and tab
// doesn't move on from it. That card goes up once the to-do up is done,
// before the next to-do, as it holds up the run.
func TestAToDoUpStaysUp(t *testing.T) {
	c := newCard(t)
	gate := make(chan struct{})
	list := config.List{Entries: []config.Entry{
		{Name: "printer", Value: `"Add the printer"`, Scope: "laptop", Line: 2},
		{Name: "scanner", Value: `"Add the scanner"`, Scope: "laptop", Line: 3},
	}}
	vault := steps.OwnManual(c.fake, c.state, steps.ManualStep{Name: "vault", Do: "Sign in to the vault"}, c.raise)
	vault.After, vault.Before = []string{"cask"}, []string{"secret"}
	p, err := engine.New(steps.Manual(c.fake, "/home/someone", c.state, list, c.raise), vault, gated("cask", gate), fine("secret"), fine("git", "secret"))
	if err != nil {
		t.Fatal(err)
	}
	ran := running(t, c, p)
	c.upNow(t, "manual:printer")
	close(gate)
	c.cards(t, 2)
	c.answer(t, steps.ManualName, hand.Next)
	c.answer(t, steps.ManualName, hand.Done)
	c.upNow(t, "vault")
	c.answer(t, "vault", hand.Done)
	c.upNow(t, "manual:scanner")
	c.answer(t, steps.ManualName, hand.Done)
	over(t, ran)

	if got := c.turns()[:4]; !slices.Equal(got, []string{"queued manual", "up manual:printer 1", "queued vault", "up manual:printer 2"}) {
		t.Errorf("turns start %q; want the printer's to-do up, the vault's card waiting after it", got)
	}
	if got, want := c.ups(), []string{"manual:printer", "vault", "manual:scanner"}; !slices.Equal(got, want) {
		t.Errorf("cards up %q, want %q", got, want)
	}
}

// The last to-do done, a card waiting behind it goes up, and the run ends
// once that's done.
func TestACardAfterTheLastToDo(t *testing.T) {
	for range 50 {
		c := newCard(t)
		gate := make(chan struct{})
		list := config.List{Entries: []config.Entry{{Name: "printer", Value: `"Add the printer"`, Scope: "laptop", Line: 2}}}
		vault := steps.OwnManual(c.fake, c.state, steps.ManualStep{Name: "vault", Do: "Sign in to the vault"}, c.raise)
		vault.After, vault.Before = []string{"cask"}, []string{"secret"}
		p, err := engine.New(steps.Manual(c.fake, "/home/someone", c.state, list, c.raise), vault, gated("cask", gate), fine("secret"))
		if err != nil {
			t.Fatal(err)
		}
		ran := running(t, c, p)
		c.upNow(t, "manual:printer")
		close(gate)
		c.cards(t, 2)
		c.answer(t, steps.ManualName, hand.Done)
		c.upNow(t, "vault")
		c.answer(t, "vault", hand.Done)
		over(t, ran)
		if got, want := c.ups(), []string{"manual:printer", "vault"}; !slices.Equal(got, want) {
			t.Fatalf("cards up %q, want %q", got, want)
		}
	}
}

// lastToDos are the to-dos as the events last list them.
func (c *card) lastToDos() []event.ToDo {
	c.sink.mu.Lock()
	defer c.sink.mu.Unlock()
	var list []event.ToDo
	for _, e := range c.sink.seen {
		if td, ok := e.(event.ToDos); ok {
			list = td.List
		}
	}
	return list
}

// A Stop given while kit looks whether the to-do up is done stops there at
// once, its card down and the list stopped, the look left to finish; found
// done, the to-do's kept as done, the list saying so after, the next never
// coming up.
func TestAStopAsTheToDoIsDone(t *testing.T) {
	defer steps.SetManualPoll(5 * time.Millisecond)()
	c := newCard(t)
	looking, carryOn, stopped := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once, stoppedOnce sync.Once
	c.sink.on = func(e event.Event) {
		if l, ok := e.(event.ToDos); ok && l.Up == -1 {
			stoppedOnce.Do(func() { close(stopped) })
		}
	}
	// Its check, its look as it comes up, then its next look, done, kept
	// till the Stop's taken.
	c.fake.On("test", "-f", "/printer").Exits(1).Then().Exits(1).Then().Does(func() {
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
	c.answer(t, steps.ManualName, hand.Stop)
	within(t, stopped, "the list wasn't stopped while kit's look was under way")
	if todos := c.lastToDos(); todos[0].Done || !slices.Equal(c.downs(), []string{"manual:printer"}) {
		t.Errorf("stopped, the to-dos stood %+v, cards down %q; want the printer's card down, not done yet", todos, c.downs())
	}
	close(carryOn)
	if err := ended(t, applied); err != nil {
		t.Fatal(err)
	}
	if got := c.ups(); !slices.Equal(got, []string{"manual:printer"}) {
		t.Errorf("cards up %q, want the printer's alone", got)
	}
	if todos := c.lastToDos(); len(todos) != 2 || !todos[0].Done || todos[1].Done {
		t.Errorf("the to-dos ended %+v, want the printer's done, the scanner's left", todos)
	}
}

// A Stop given between two to-dos, once the first's Done is taken and the
// list has moved on to the next, before its card goes up, stops there: the
// first kept as done, the next left to do, its card never up.
func TestAStopBetweenToDos(t *testing.T) {
	defer steps.SetManualPoll(time.Hour)()
	c := newCard(t)
	c.sink.on = func(ev event.Event) {
		if todos, ok := ev.(event.ToDos); ok && todos.Up == 1 && !c.desk.Answer(steps.ManualName, hand.Stop) {
			t.Error("the Stop between the to-dos wasn't taken")
		}
	}
	applied := applying(t, steps.Manual(c.fake, "/home/someone", c.state, printerAndScanner, c.raise))
	c.upNow(t, "manual:printer")
	c.answer(t, steps.ManualName, hand.Done)
	if err := ended(t, applied); err != nil {
		t.Fatal(err)
	}
	if todos := c.lastToDos(); !todos[0].Done || todos[1].Done {
		t.Errorf("the to-dos ended %+v, want the printer's done, the scanner's left", todos)
	}
	if ups := c.ups(); !slices.Equal(ups, []string{"manual:printer"}) {
		t.Errorf("cards up = %q, want the printer's alone: the scanner's never", ups)
	}
}

// A Done and a Stop given back to back are both taken, the Done first,
// whether it was taken before the Stop came or still waited: the to-do up
// kept as done, the next left to do.
func TestADoneThenAStopBackToBack(t *testing.T) {
	defer steps.SetManualPoll(time.Hour)()
	for range 20 {
		c := newCard(t)
		applied := applying(t, steps.Manual(c.fake, "/home/someone", c.state, printerAndScanner, c.raise))
		c.upNow(t, "manual:printer")
		c.answer(t, steps.ManualName, hand.Done)
		c.answer(t, steps.ManualName, hand.Stop)
		if err := ended(t, applied); err != nil {
			t.Fatal(err)
		}
		if todos := c.lastToDos(); !todos[0].Done || todos[1].Done {
			t.Fatalf("the to-dos ended %+v, want the printer's done, the scanner's left", todos)
		}
	}
}

// printerAndScanner are two steps by hand, neither with a command to test
// it by.
var printerAndScanner = config.List{Entries: []config.Entry{
	{Name: "printer", Value: `"Add the printer"`, Scope: "laptop", Line: 2},
	{Name: "scanner", Value: `"Add the scanner"`, Scope: "laptop", Line: 3},
}}

// The run stopped while a card's up, the card waiting behind it never goes
// up.
func TestNoCardGoesUpOnceTheRunsStopped(t *testing.T) {
	defer steps.SetManualPoll(time.Hour)()
	for range 50 {
		c := newCard(t)
		ctx, stop := context.WithCancel(t.Context())
		own := func(name string) <-chan error {
			step := steps.OwnManual(c.fake, c.state, steps.ManualStep{Name: name, Do: "Do " + name}, c.raise)
			done := make(chan error, 1)
			go func() { done <- step.Apply(ctx, check.Result{}) }()
			return done
		}
		first := own("first")
		c.upNow(t, "first")
		second := own("second")
		c.cards(t, 2)
		stop()
		for _, applied := range []<-chan error{first, second} {
			if err := ended(t, applied); !errors.Is(err, context.Canceled) {
				t.Fatalf("stopped, applying ended %v; want it stopped", err)
			}
		}
		if got := c.ups(); !slices.Equal(got, []string{"first"}) {
			t.Fatalf("cards up %q, want the first's alone", got)
		}
	}
}

// The rest of the steps by hand, raised, come up one card each, in order,
// those not done: each saying how many follow it; one done, the next comes
// up, its notification posted once the last one's taken away.
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
	if up := c.cards(t, 1)[0]; up.Step != steps.ManualName || up.Item != "manual:app" || up.More != 1 || !up.Tells {
		t.Errorf("first card = %+v, want the app's, 1 more after it", up)
	}
	c.noticed(t, 1)
	c.answer(t, steps.ManualName, hand.Done)
	if up := c.cards(t, 2)[1]; up.Item != "manual:sign-in" || up.More != 0 || up.Tells || !slices.Equal(up.Steps, []string{"Open the service", "Sign in"}) {
		t.Errorf("second card = %+v, want the sign-in's, its steps, none after it", up)
	}
	c.noticed(t, 2)
	c.answer(t, steps.ManualName, hand.Done)
	if err := ended(t, applied); err != nil {
		t.Fatal(err)
	}
	if res := step.Check(t.Context()); res.State != check.OK {
		t.Errorf("check after = %+v, want all done", res)
	}
	want := []string{
		"notify card:manual:manual:app | Install the app | kit waits for you to do this", "clear card:manual:manual:app",
		"notify card:manual:manual:sign-in | Sign in to the service | Open the service", "clear card:manual:manual:sign-in",
	}
	if got := c.posted(); !slices.Equal(got, want) {
		t.Errorf("notifications %q, want %q", got, want)
	}
}

// A step by hand of its own that a step it comes before finds wasn't done
// after all (engine.Undone), as the App Store's first install finds no one
// signed in, goes up again: marked done no more, its card and notification
// as the first time; done again, the step carries on. One whose test still
// passes has nothing to raise; with no one to act, none can go up again.
func TestACardGoesUpAgain(t *testing.T) {
	c := newCard(t)
	m := steps.ManualStep{Name: "appstore", Do: "Sign in to the App Store", Before: []string{"mas"}, Why: "The App Store's apps install once you're signed in.", Steps: []string{"Open the App Store", "Store › Sign In, with your Apple Account"}}
	signIn := steps.OwnManual(c.fake, c.state, m, c.raise)
	signIn.Before = m.Before
	var undone bool
	mas := engine.Step{Name: "mas", AlwaysApply: true, Check: func(context.Context) check.Result { return check.Result{State: check.OK, Summary: "all installed"} },
		Apply: func(ctx context.Context, _ check.Result) error {
			undone = engine.Undone(ctx)
			return nil
		}}
	p, err := engine.New(signIn, mas)
	if err != nil {
		t.Fatal(err)
	}
	ran := make(chan engine.Report, 1)
	go func() {
		report, _ := p.Apply(t.Context(), c.sink, engine.Options{Jobs: 1})
		ran <- report
	}()
	first := c.cards(t, 1)[0]
	c.noticed(t, 1)
	c.answer(t, "appstore", hand.Done)
	again := c.cards(t, 2)[1]
	c.noticed(t, 2)
	first.Time, again.Time = time.Time{}, time.Time{}
	if !reflect.DeepEqual(again, first) {
		t.Errorf("the card again = %+v, want it as the first time, %+v", again, first)
	}
	if res := signIn.Check(t.Context()); res.State == check.OK {
		t.Errorf("with its card up again, its check = %+v; want it marked done no more", res)
	}
	c.answer(t, "appstore", hand.Done)
	var report engine.Report
	select {
	case report = <-ran:
	case <-time.After(5 * time.Second):
		t.Fatal("the run never ended")
	}
	if !undone || report.Results["appstore"].State != check.OK {
		t.Errorf("undone %v, the step by hand stands %+v; want it done again", undone, report.Results["appstore"])
	}
	posted := []string{
		"notify card:appstore:appstore | Sign in to the App Store | Open the App Store", "clear card:appstore:appstore",
		"notify card:appstore:appstore | Sign in to the App Store | Open the App Store", "clear card:appstore:appstore",
	}
	if got := c.posted(); !slices.Equal(got, posted) {
		t.Errorf("notifications %q, want %q", got, posted)
	}

	c = newCard(t)
	c.fake.On("test", "-f", "/signed-in")
	tested := steps.OwnManual(c.fake, c.state, steps.ManualStep{Name: "sign-in", Do: "Sign in", Before: []string{"secret"}, Command: &runner.Command{Name: "test", Args: []string{"-f", "/signed-in"}}}, c.raise)
	if err := tested.Again(t.Context()); err == nil || len(c.posted()) != 0 {
		t.Errorf("again, its test passing = %v, posted %q; want it refused, nothing raised", err, c.posted())
	}
	if steps.OwnManual(c.fake, c.state, m, nil).Again != nil {
		t.Error("with no one to act, a step by hand can be done again")
	}
}

// A card up between two to-dos counts every to-do still to come after it,
// not the next alone.
func TestACardBetweenToDosCountsThemAll(t *testing.T) {
	c := newCard(t)
	gate := make(chan struct{})
	list := config.List{Entries: []config.Entry{
		{Name: "printer", Value: `"Add the printer"`, Scope: "laptop", Line: 2},
		{Name: "scanner", Value: `"Add the scanner"`, Scope: "laptop", Line: 3},
		{Name: "copier", Value: `"Add the copier"`, Scope: "laptop", Line: 4},
	}}
	vault := steps.OwnManual(c.fake, c.state, steps.ManualStep{Name: "vault", Do: "Sign in to the vault"}, c.raise)
	vault.After, vault.Before = []string{"cask"}, []string{"secret"}
	p, err := engine.New(steps.Manual(c.fake, "/home/someone", c.state, list, c.raise), vault, gated("cask", gate), fine("secret"))
	if err != nil {
		t.Fatal(err)
	}
	ran := running(t, c, p)
	c.upNow(t, "manual:printer")
	close(gate)
	c.cards(t, 2)
	c.answer(t, steps.ManualName, hand.Done)
	for i := 0; !slices.Contains(c.turns(), "up vault 2"); i++ {
		if i == 5000 {
			t.Fatalf("turns %q; want the vault's card up, the scanner and the copier after it", c.turns())
		}
		time.Sleep(time.Millisecond)
	}
	c.answer(t, "vault", hand.Done)
	c.upNow(t, "manual:scanner")
	c.answer(t, steps.ManualName, hand.Done)
	c.upNow(t, "manual:copier")
	c.answer(t, steps.ManualName, hand.Done)
	over(t, ran)
}

// heldNotices is a Notifier whose notifications each wait to go out till
// through is closed, as one waits for kit's app to be built on a new Mac:
// going says which is going out. It notes what it's passed, a notification
// whose context was done by then, so that it posts nothing, saying so.
type heldNotices struct {
	going   chan string
	through chan struct{}
	notices
}

func newHeldNotices() *heldNotices {
	return &heldNotices{going: make(chan string, 8), through: make(chan struct{})}
}

func (h *heldNotices) Notify(ctx context.Context, id, title, body string) {
	h.going <- id
	<-h.through
	if ctx.Err() != nil {
		body += " (taken away first)"
	}
	h.notices.Notify(ctx, id, title, body)
}

// heldCard is a run at a terminal, as newCard has it, whose notifications
// wait to go out.
func heldCard(t *testing.T) (*card, *heldNotices) {
	c := newCard(t)
	h := newHeldNotices()
	c.held, c.notices = true, &h.notices
	c.raise = &steps.Raise{Sink: c.sink, Now: time.Now, Notify: h, Desk: c.desk}
	return c, h
}

// posting waits till the notification under id is going out.
func (h *heldNotices) posting(t *testing.T, id string) {
	t.Helper()
	if got := <-h.going; got != id {
		t.Fatalf("notification %s went out, want %s", got, id)
	}
}

// A card takes space at once, its notification still waiting to go out:
// it's done, and down, before that notification's posted. The notification
// goes out after, taken away first, so it posts nothing, then is taken
// away: none is left.
func TestACardIsAnsweredWhileItsNotificationWaits(t *testing.T) {
	c, h := heldCard(t)
	m := steps.ManualStep{Name: "store", Do: "Sign in to the store", Steps: []string{"Open the store"}}
	step := steps.OwnManual(c.fake, c.state, m, c.raise)
	applied := applying(t, step)
	h.posting(t, "card:store:store")
	if !c.desk.Answer("store", hand.Done) {
		t.Fatal("the card didn't take space while its notification waited")
	}
	if err := ended(t, applied); err != nil {
		t.Fatal(err)
	}
	if res := step.Check(t.Context()); res.State != check.OK {
		t.Errorf("after space, check = %+v, want it done", res)
	}
	close(h.through)
	want := []string{"notify card:store:store | Sign in to the store | Open the store (taken away first)", "clear card:store:store"}
	if got := c.posted(); !slices.Equal(got, want) {
		t.Errorf("notifications %q, want %q", got, want)
	}
}

// Tab moves on at once, the card up's notification still waiting to go
// out, and space on the next card, and on the first come back. The first
// notification goes out after, taken away as it went out, so it posts
// nothing, then is taken away; the rest, taken away before they went out,
// never go out.
func TestTabWhileANotificationWaits(t *testing.T) {
	c, h := heldCard(t)
	gate := make(chan struct{})
	store := steps.OwnManual(c.fake, c.state, steps.ManualStep{Name: "store", Do: "Sign in to the store"}, c.raise)
	store.After, store.Before = []string{"cask"}, []string{"apps"}
	vault := steps.OwnManual(c.fake, c.state, steps.ManualStep{Name: "vault", Do: "Sign in to the vault"}, c.raise)
	vault.Before = []string{"secret"}
	p, err := engine.New(store, vault, gated("cask", gate), fine("apps"), fine("secret"), fine("git", "secret"))
	if err != nil {
		t.Fatal(err)
	}
	ran := running(t, c, p)
	h.posting(t, "card:vault:vault")
	close(gate)
	c.cards(t, 2)
	c.answer(t, "vault", hand.Next)
	if up := c.cards(t, 3)[2]; up.Item != "store" {
		t.Fatalf("after tab, card = %+v, want the store's", up)
	}
	c.answer(t, "store", hand.Done)
	if up := c.cards(t, 4)[3]; up.Item != "vault" {
		t.Fatalf("after the store's done, card = %+v, want the vault's back", up)
	}
	c.answer(t, "vault", hand.Done)
	over(t, ran)

	close(h.through)
	want := []string{"notify card:vault:vault | Sign in to the vault | kit waits for you to do this (taken away first)", "clear card:vault:vault"}
	if got := c.posted(); !slices.Equal(got, want) {
		t.Errorf("notifications %q, want %q: the vault's first, going out as it was taken away, then taken away; the rest never going out", got, want)
	}
}

// A choice's key acts at once, the to-do's notification still waiting to go
// out.
func TestAChoiceWhileItsNotificationWaits(t *testing.T) {
	c, h := heldCard(t)
	var mu sync.Mutex
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
				Title: "Bring back apps' settings", First: true, Keys: []check.Key{{Key: "r", Does: "brings them back"}},
			})}}
		},
		Choice: &engine.Choice{
			Answer: func(context.Context, check.Item, string) (string, error) {
				mu.Lock()
				defer mu.Unlock()
				brought = true
				return "brought back", nil
			},
		},
	}
	done := make(chan engine.Report, 1)
	go func() {
		done <- applied(t, c, atTheEnd(steps.Manual(c.fake, "/home/someone", c.state, config.List{}, c.raise), step))
	}()
	h.posting(t, "card:manual:prefs:none")
	if !c.desk.Answer(steps.ManualName, "r") {
		t.Fatal("the to-do didn't take r while its notification waited")
	}
	var report engine.Report
	select {
	case report = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the run never ended")
	}
	if report.Results["prefs"].State != check.OK {
		t.Errorf("prefs stands %s, want it done by r", report.Results["prefs"].State)
	}
	close(h.through)
	want := []string{"notify card:manual:prefs:none | Bring back apps' settings | kit waits for you to do this (taken away first)", "clear card:manual:prefs:none"}
	if got := c.posted(); !slices.Equal(got, want) {
		t.Errorf("notifications %q, want %q", got, want)
	}
}

// A card takes space at once while kit opens its app, the app opening
// aside.
func TestACardIsAnsweredWhileItsAppOpens(t *testing.T) {
	c := newCard(t)
	opening, opened := make(chan struct{}), make(chan struct{})
	c.fake.On("open", "-b", "com.example.store").Does(func() {
		close(opening)
		<-opened
	})
	step := steps.OwnManual(c.fake, c.state, steps.ManualStep{Name: "store", Do: "Sign in to the store", Open: "com.example.store"}, c.raise)
	applied := applying(t, step)
	<-opening
	if !c.desk.Answer("store", hand.Done) {
		t.Fatal("the card didn't take space while its app opened")
	}
	if err := ended(t, applied); err != nil {
		t.Fatal(err)
	}
	close(opened)
	c.raise.Wait()
	if got := c.fake.Calls(); !slices.Equal(got, []string{"open -b com.example.store"}) {
		t.Errorf("ran %q, want the app opened", got)
	}
}

// within waits for ch to close, failing the test as what didn't happen
// once a few seconds pass.
func within(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal(what)
	}
}

// A card done while every job is busy finishes its step at once, without
// waiting for a job, as it has nothing more to do: its card down, as the
// step's finishing says, and its answers no longer taken, while the step
// holding the run's one job is still at work.
func TestACardDoneWithEveryJobBusy(t *testing.T) {
	c := newCard(t)
	gate, busy, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var busyOnce, finishedOnce sync.Once
	c.sink.on = func(e event.Event) {
		switch e := e.(type) {
		case event.StepStarted:
			if e.Step == "cask" && e.Doing == "applying" {
				busyOnce.Do(func() { close(busy) })
			}
		case event.StepFinished:
			if e.Step == "store" {
				finishedOnce.Do(func() { close(finished) })
			}
		}
	}
	store := steps.OwnManual(c.fake, c.state, steps.ManualStep{Name: "store", Do: "Sign in to the store"}, c.raise)
	p, err := engine.New(store, gated("cask", gate))
	if err != nil {
		t.Fatal(err)
	}
	ran := make(chan engine.Report, 1)
	go func() {
		report, _ := p.Apply(t.Context(), c.sink, engine.Options{Jobs: 1})
		ran <- report
	}()
	c.cards(t, 1)
	within(t, busy, "the cask step never took the job the card gave up")
	if !c.desk.Answer("store", hand.Done) {
		t.Fatal("the card didn't take space")
	}
	within(t, finished, "the card's step never finished while every job was busy")
	if c.desk.Answer("store", hand.Done) {
		t.Error("the card's answers were still taken once it was done")
	}
	close(gate)
	if report := over(t, ran); report.Results["store"].State != check.OK {
		t.Errorf("store %+v, want it done", report.Results["store"])
	}
}

// slowLook is a step by hand of kit's own whose looks wait, once armed, to
// be let through, saying they've started on looking; done once signedIn.
type slowLook struct {
	hold, signedIn  atomic.Bool
	looking, looked chan struct{}
}

func newSlowLook() *slowLook {
	return &slowLook{looking: make(chan struct{}), looked: make(chan struct{})}
}

func (l *slowLook) done(ctx context.Context) bool {
	if l.hold.CompareAndSwap(true, false) {
		close(l.looking)
		select {
		case <-l.looked:
		case <-ctx.Done():
		}
	}
	return l.signedIn.Load()
}

// Tab moves on at once while kit looks whether the card up is done, the
// look under way aside; and the card tabbed to goes up at once, though its
// own look as it comes up is slow. Each card found done by a look is done.
func TestTabWhileKitLooks(t *testing.T) {
	defer steps.SetManualPoll(time.Millisecond)()
	c := newCard(t)
	gate := make(chan struct{})
	vaultLook, storeLook := newSlowLook(), newSlowLook()
	vault := steps.OwnManual(c.fake, c.state, steps.ManualStep{Name: "vault", Do: "Sign in to the vault", Done: vaultLook.done}, c.raise)
	vault.Before = []string{"secret"}
	store := steps.OwnManual(c.fake, c.state, steps.ManualStep{Name: "store", Do: "Sign in to the store", Done: storeLook.done}, c.raise)
	store.After, store.Before = []string{"cask"}, []string{"apps"}
	p, err := engine.New(vault, store, gated("cask", gate), fine("apps"), fine("secret"), fine("git", "secret"))
	if err != nil {
		t.Fatal(err)
	}
	ran := running(t, c, p)
	c.cards(t, 1)
	vaultLook.hold.Store(true)
	within(t, vaultLook.looking, "kit never looked whether the vault's card was done")
	close(gate)
	c.cards(t, 2)
	storeLook.hold.Store(true)
	c.answer(t, "vault", hand.Next)
	if up := c.cards(t, 3)[2]; up.Item != "store" {
		t.Fatalf("after tab, card = %+v, want the store's, at once", up)
	}
	within(t, storeLook.looking, "kit never looked whether the store's card was done")
	close(vaultLook.looked)
	close(storeLook.looked)
	storeLook.signedIn.Store(true)
	c.cards(t, 4)
	vaultLook.signedIn.Store(true)
	if report := over(t, ran); report.Results["vault"].State != check.OK || report.Results["store"].State != check.OK {
		t.Errorf("vault %+v, store %+v; want both done", report.Results["vault"], report.Results["store"])
	}
}

// A choice's key is taken at once while kit looks whether its to-do is
// done, the card saying it's being answered with it; what it says is done
// once that look is over, one look, or answer, at a time, so the two never
// settle the to-do at once.
func TestAChoiceWhileKitLooks(t *testing.T) {
	defer steps.SetManualPoll(time.Millisecond)()
	c := newCard(t)
	looking, looked, taken := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var hold, lookOver atomic.Bool
	var takenOnce sync.Once
	c.sink.on = func(e event.Event) {
		if up, ok := e.(event.NeedsYou); ok && up.Answering == "r" {
			takenOnce.Do(func() { close(taken) })
		}
	}
	var mu sync.Mutex
	brought := false
	step := engine.Step{
		Name: "prefs", Title: "App settings", Area: steps.AreaBackups,
		Check: func(ctx context.Context) check.Result {
			if hold.CompareAndSwap(true, false) {
				close(looking)
				<-looked
				lookOver.Store(true)
			}
			mu.Lock()
			defer mu.Unlock()
			if brought {
				return check.Result{State: check.OK, Summary: "saved"}
			}
			return check.Result{State: check.Attention, Summary: "paused", Items: []check.Item{toDoItem("prefs", check.ToDo{
				Title: "Bring back apps' settings", First: true, Keys: []check.Key{{Key: "r", Does: "brings them back"}},
			})}}
		},
		Choice: &engine.Choice{
			Answer: func(context.Context, check.Item, string) (string, error) {
				if !lookOver.Load() {
					t.Error("r was done while kit's look was under way")
				}
				mu.Lock()
				defer mu.Unlock()
				brought = true
				return "brought back", nil
			},
		},
	}
	done := make(chan engine.Report, 1)
	go func() {
		done <- applied(t, c, atTheEnd(steps.Manual(c.fake, "/home/someone", c.state, config.List{}, c.raise), step))
	}()
	c.cards(t, 1)
	hold.Store(true)
	within(t, looking, "kit never looked whether the to-do was done")
	if !c.desk.Answer(steps.ManualName, "r") {
		t.Fatal("the to-do didn't take r while kit looked")
	}
	within(t, taken, "the card never said it was being answered with r while kit looked")
	close(looked)
	select {
	case report := <-done:
		if report.Results["prefs"].State != check.OK {
			t.Errorf("prefs stands %s, want it done by r", report.Results["prefs"].State)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("r was never done")
	}
}

// A card raised again by the step it comes before, done while every job is
// busy, comes down at once, its step checked again and its answers no
// longer taken: the step that raised it waits for a job to go on with, its
// card down first, saying it's applying, no longer waiting for the card.
func TestACardAgainDoneWithEveryJobBusy(t *testing.T) {
	c := newCard(t)
	gate, busy, rechecked, applying := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var busyOnce, applyingOnce sync.Once
	var finishes atomic.Int32
	c.sink.on = func(e event.Event) {
		switch e := e.(type) {
		case event.StepStarted:
			if e.Step == "cask" && e.Doing == "applying" {
				busyOnce.Do(func() { close(busy) })
			}
		case event.StepFinished:
			if e.Step == "appstore" && finishes.Add(1) == 2 {
				close(rechecked)
			}
		case event.Doing:
			if e.Step == "mas" && e.Says == "applying" && finishes.Load() == 2 {
				applyingOnce.Do(func() { close(applying) })
			}
		}
	}
	m := steps.ManualStep{Name: "appstore", Do: "Sign in to the App Store", Before: []string{"mas"}}
	signIn := steps.OwnManual(c.fake, c.state, m, c.raise)
	signIn.Before = m.Before
	var tried atomic.Int32
	mas := engine.Step{Name: "mas", AlwaysApply: true, Check: func(context.Context) check.Result { return check.Result{State: check.OK} },
		Apply: func(ctx context.Context, _ check.Result) error {
			if tried.Add(1) == 1 {
				engine.Undone(ctx)
			}
			return nil
		}}
	cask := gated("cask", gate)
	cask.After = []string{"appstore"}
	p, err := engine.New(signIn, mas, cask)
	if err != nil {
		t.Fatal(err)
	}
	ran := make(chan engine.Report, 1)
	go func() {
		report, _ := p.Apply(t.Context(), c.sink, engine.Options{Jobs: 1})
		ran <- report
	}()
	c.cards(t, 1)
	c.answer(t, "appstore", hand.Done)
	c.cards(t, 2)
	within(t, busy, "the cask step never took the job the card gave up")
	if !c.desk.Answer("appstore", hand.Done) {
		t.Fatal("the card raised again didn't take space")
	}
	within(t, rechecked, "the card raised again never came down while every job was busy")
	if c.desk.Answer("appstore", hand.Done) {
		t.Error("the card's answers were still taken once it was done")
	}
	within(t, applying, "the step that raised the card never said it was applying while it waited for a job")
	close(gate)
	if report := over(t, ran); report.Results["appstore"].State != check.OK || report.Results["mas"].State != check.OK {
		t.Errorf("appstore %+v, mas %+v; want both done", report.Results["appstore"], report.Results["mas"])
	}
}
