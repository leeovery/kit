package steps

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/hand"
	"github.com/leeovery/kit/internal/notice"
)

// raised is what a card is raised for, and how to tell it's done: a step by
// hand's line, or one of kit's own to-dos. card is what its card says, but
// its step and how many follow it; done whether it's done, and what kit saw,
// in words, if it has some; answer what the person's answer does, at now,
// and likewise.
type raised interface {
	card() event.NeedsYou
	done(ctx context.Context) (bool, string)
	answer(ctx context.Context, a hand.Answer, now time.Time) (bool, string, error)
}

// readier is a raised card whose card is readied once its turn comes, as a
// choice's keys are, the date of the save one brings back said: which can
// take a while, as the first look at apps' settings on a new Mac clones
// their store.
type readier interface {
	ready(ctx context.Context) event.NeedsYou
}

// opener is a raised card that opens something as it first goes up: a step
// by hand of kit's own, its app.
type opener interface {
	open(ctx context.Context)
}

// errStopHere is why a card came down without being done: the person
// stopped where they were, at the run's to-dos, what's left staying to do.
// errDoneThenStop, that it was done, and they stopped meanwhile: it's done,
// and the rest stays to do.
var (
	errStopHere     = errors.New("stopped here")
	errDoneThenStop = fmt.Errorf("done, then %w", errStopHere)
)

// Wait is what a raised card waits for, each its own, though two can be as
// long: its grace, how long one whose turn has come waits to go up for a
// look to find it not done, so that one done by then never shows
// (WaitToGoUp, drawAfter); how long one's up before its notification goes
// out, should no look have found it not done by then (WaitToNotify,
// noticeAfter); and how long till it's looked at again (WaitToLook,
// manualPoll).
type Wait int

const (
	WaitToGoUp Wait = iota
	WaitToNotify
	WaitToLook
)

const (
	drawAfter   = 250 * time.Millisecond
	noticeAfter = 3 * time.Second
)

// manualPoll is how often a raised card is looked at, to see whether it's
// done.
var manualPoll = 3 * time.Second

// Raise is how a step by hand that's come due is raised while kit applies
// at a terminal, where someone can act on it: a card, front and centre, one
// up at a time, with the steps to do it; and a notification, in case
// they're away from it. What they answer the card with comes back on Desk.
// A card's loop waits on nothing but what comes to it: everything that
// takes time goes aside (its work: its looks, its answers and its
// readying; its app opening; its notifications, through a notice.Queue), so
// its keys act the moment they're pressed. Wait waits for all of it, as
// the run ends. After is a card's wait of d, for what w says, as
// time.After, which it is when nil.
//
// The cards come due wait in a line, the first's up: in order of how much
// of the run waits on each (engine.Holding), those holding up as much in
// the order they came, so one more of the run waits on goes up in place of
// the card up, which waits its turn again. The person can move on from the
// card up to the next waiting (hand.Next), the one left going to the end of
// the line, to come back. The to-dos at the run's end are held: each, its
// turn come, stays up till it's done.
type Raise struct {
	Sink   event.Sink
	Now    func() time.Time
	Notify notice.Notifier
	Desk   *hand.Desk
	After  func(w Wait, d time.Duration) <-chan time.Time

	notices    *notice.Queue
	noticesSet sync.Once
	aside      sync.WaitGroup

	// mu guards the line, the first's turn it is; and the card up, and
	// whose it is (shown), nil while none is.
	mu    sync.Mutex
	line  []*place
	shown *place
	card  event.NeedsYou
}

// place is a card's place in Raise's line: its step; how much of the run
// waits on it; whether it's held, never moved from its turn; how many of
// its step's own cards follow it; and moved, told each time its turn comes
// or goes. lost is how many times its turn's gone, and up the item of the
// card it last put up, which mu guards: its turn can go and come back
// before its loop wakes. notified is the item its notification's for,
// while it's posted, which only its own loop touches, posting it
// (Raise.announce) and taking it away (Raise.unnotify).
type place struct {
	step     string
	weight   int
	held     bool
	after    int
	moved    chan struct{}
	lost     int
	up       string
	notified string
}

// poke tells pl its turn has come or gone, unless it's been told already
// and hasn't looked yet.
func (pl *place) poke() {
	select {
	case pl.moved <- struct{}{}:
	default:
	}
}

// seat is how a card takes its place in the line: by how much of the run
// waits on it, or, held, at the end; with how many of its step's own cards
// follow it; and where the person's answers come, when its step listens
// for all its cards at once, else nil for the card to listen itself.
type seat struct {
	weight  int
	held    bool
	after   int
	answers <-chan hand.Answer
}

// midRun is the seat of a card raised mid-run for the step called step: by
// how much of the run waits on it.
func midRun(ctx context.Context, step string) seat {
	return seat{weight: engine.Holding(ctx, step)}
}

// up raises c's card for the step called step, taking its place in the
// line as s says, and waits till it's done, the step's job given up
// meanwhile, for other steps to go on (engine.Release), and not taken back:
// done, a step with nothing more to do finishes without waiting for a job,
// and one with more takes it back for that, its card down by then. Its
// card goes up as its turn comes (raising), and comes down the moment it's
// done or the person stopped there, the next card's turn coming. It returns
// what kit saw as it was done, in words, if it has some; errStopHere when
// the person stopped where they were, with its work (late), what's under
// way left to finish, never cut short, to say whether it found it done
// (work.outcome); errDoneThenStop when they stopped as it was done.
func (r *Raise) up(ctx context.Context, step string, c raised, s seat) (saw string, late *work, err error) {
	answers, down := s.answers, func() {}
	if answers == nil {
		answers, down = r.Desk.Listen(step)
	}
	engine.Release(ctx)
	_, readies := c.(readier)
	u := &raising{r: r, ctx: ctx, c: c, card: c.card(), readied: !readies, answers: answers}
	u.card.Step = step
	u.pl, u.work = r.join(step, s), r.working(ctx, c)
	saw, err = u.wait()
	down()
	stopped := errors.Is(err, errStopHere)
	r.leave(u.pl, err == nil || stopped)
	r.unnotify(ctx, u.pl)
	u.work.halt()
	if !stopped || errors.Is(err, errDoneThenStop) {
		return saw, nil, err
	}
	return saw, u.work, err
}

// raising is a card raised, as its loop has it: the Raise and place it has
// in the line; what it's raised for, and what its card says; where the
// person's answers come; and its work aside. Then its turn, as the line's
// lost it so far (lost): the number of the look asked for as it came (0
// while it isn't its turn); whether a look begun since found it not done;
// whether its card's drawn, up on the screen; and the waits for its grace
// to end, and for its notification. known is whether it's known not done,
// a look having found it so or its card having been up, which it stays;
// readied whether its card's said all it will; choosing whether it's being
// answered by one of its keys.
type raising struct {
	r       *Raise
	ctx     context.Context
	pl      *place
	c       raised
	card    event.NeedsYou
	answers <-chan hand.Answer
	work    *work

	lost     int
	turn     int
	undone   bool
	drawn    bool
	grace    <-chan time.Time
	due      <-chan time.Time
	known    bool
	readied  bool
	choosing bool
}

// wait waits till the card's done, the person stopped there or the run
// stopped, taking what comes as it comes, and doing nothing that waits:
// its turn coming or going, its grace over, its notification due, what its
// work comes to, and the person's answers. As its turn comes, a look's
// asked for at once, and the card goes up when it finds it not done, or
// once its grace is over, whichever is first, so that one found done
// first never shows, nor opens anything; one known not done goes up at
// once. Its notification goes out once it's up and a look's found it not
// done, or it's been up a while.
func (u *raising) wait() (string, error) {
	for {
		u.takeTurn()
		select {
		case <-u.ctx.Done():
			return "", u.ctx.Err()
		case <-u.pl.moved:
		case <-u.grace:
			u.draw()
		case <-u.due:
			u.announce()
		case res := <-u.work.out:
			if over, saw, err := u.took(res); over {
				return saw, err
			}
		case a := <-u.answers:
			if err := u.answered(a); err != nil {
				return "", err
			}
		}
	}
}

// takeTurn takes the card's turn as the line has it now: gone, or gone and
// back since, what came with it goes, its notification taken away; come, a
// look's asked for, what it says is readied, unless it has been, and its
// grace begins, unless it's known not done, when it goes up at once.
func (u *raising) takeTurn() {
	has, lost := u.r.state(u.pl)
	if !has || lost != u.lost {
		u.r.unnotify(u.ctx, u.pl)
		u.lost, u.turn, u.undone, u.drawn, u.grace, u.due = lost, 0, false, false, nil, nil
	}
	if !has || u.turn != 0 {
		return
	}
	if !u.known {
		u.grace = u.r.after(WaitToGoUp, drawAfter)
	}
	u.turn = u.work.look()
	if !u.readied {
		u.work.readyCard()
	}
	if u.known {
		u.draw()
	}
}

// took is what the card's work came to: done, or an answer that failed,
// ends its wait, as finish says; the card readied, it says so; an answer
// that didn't do it has the card offer its keys again; a look that found
// it not done, the card's known not done (found).
func (u *raising) took(res result) (over bool, saw string, err error) {
	switch {
	case res.done || res.err != nil:
		saw, err := finish(u.answers, res.saw, res.err)
		return true, saw, err
	case res.readied != nil:
		u.ready(*res.readied)
	case res.answered:
		u.unanswered()
	default:
		u.found(res.asked)
	}
	return false, "", nil
}

// found is the card known not done, as a look, begun once the look
// numbered asked had been asked for, found it: its turn come, it goes up,
// and, the look begun since, its notification's posted. A look begun while
// it waited its turn has it go up at once when it comes.
func (u *raising) found(asked int) {
	u.known = true
	u.undone = u.undone || u.turn > 0 && asked >= u.turn
	switch {
	case u.turn == 0:
	case !u.drawn:
		u.draw()
	case u.undone:
		u.announce()
	}
}

// ready is what the card says, readied: put up again saying it, when it's
// up.
func (u *raising) ready(card event.NeedsYou) {
	card.Step = u.card.Step
	u.card, u.readied = card, true
	if u.drawn {
		u.r.redraw(u.pl, card)
	}
}

// draw puts the card up, when it's still its turn, saying what it can,
// readied or not: its app opened, the first time, and its notification
// posted once a look begun since its turn has found it not done, or once
// it's been up a while.
func (u *raising) draw() {
	u.grace = nil
	if !u.r.show(u.ctx, u.pl, u.card) {
		return
	}
	u.drawn, u.known, u.due = true, true, u.r.after(WaitToNotify, noticeAfter)
	if o, ok := u.c.(opener); ok {
		u.r.aside.Go(func() { o.open(u.ctx) })
	}
	if u.undone {
		u.announce()
	}
}

// announce posts the card's notification, aside, while it's up.
func (u *raising) announce() {
	u.due = nil
	u.r.announce(u.ctx, u.pl)
}

// answered is what the person's answer does: Stop stops where they are
// (errStopHere); Next moves on to the next card waiting; the rest are the
// card's work to do, aside, the card saying at once that it's being
// answered by a key of its, and offering none of its keys meanwhile. An
// answer while the card isn't up, or a key while one's being done, isn't
// one it offered: it goes nowhere.
func (u *raising) answered(a hand.Answer) error {
	switch {
	case a == hand.Stop:
		return errStopHere
	case a == hand.Next:
		u.r.next(u.pl)
	case u.drawn && !u.choosing:
		u.choosing = u.r.answering(u.pl, a)
		u.work.answer(a)
	}
	return nil
}

// unanswered is the card still to do once its work's done what the person
// answered: answered by one of its keys, it offers them again, those
// pressed meanwhile dropped as they came (answered).
func (u *raising) unanswered() {
	if u.choosing {
		u.choosing = false
		u.r.answering(u.pl, "")
	}
}

// finish is how a card's wait ends, done, what kit saw, or with err: a
// Stop the person gave meanwhile, waiting unread, stops there all the same,
// once it's done (errDoneThenStop). What else they answered is dropped,
// meant for the card now down.
func finish(answers <-chan hand.Answer, saw string, err error) (string, error) {
	switch {
	case err != nil:
		return "", err
	case stoppedMeanwhile(answers):
		return saw, errDoneThenStop
	}
	return saw, nil
}

// stoppedMeanwhile empties answers of what came while kit was busy, without
// waiting, and reports whether the person stopped where they were
// meanwhile, which isn't dropped.
func stoppedMeanwhile(answers <-chan hand.Answer) bool {
	stop := false
	for {
		select {
		case a := <-answers:
			stop = stop || a == hand.Stop
		default:
			return stop
		}
	}
}

// after is a wait of d, for what w says, as After has it.
func (r *Raise) after(w Wait, d time.Duration) <-chan time.Time {
	if r.After == nil {
		return time.After(d)
	}
	return r.After(w, d)
}

// state is pl's card as the line has it: has, its turn come; lost, how
// many times its turn's gone.
func (r *Raise) state(pl *place) (has bool, lost int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.hasLocked(pl), pl.lost
}

// show puts card up for pl, when it's still pl's turn and the run hasn't
// stopped, reporting whether it did: its More counting the cards waiting
// after it, its step's own and those in the line.
func (r *Raise) show(ctx context.Context, pl *place, card event.NeedsYou) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.hasLocked(pl) || ctx.Err() != nil {
		return false
	}
	card.Time, card.More = r.Now(), r.moreLocked(pl)
	r.card, r.shown, pl.up = card, pl, card.Item
	r.Sink.Emit(card)
	return true
}

// redraw puts pl's card up again saying card, readied, while it's up: as
// it stands, being answered or not, and how many after it.
func (r *Raise) redraw(pl *place, card event.NeedsYou) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.shown != pl {
		return
	}
	card.Time, card.More, card.Answering = r.Now(), r.card.More, r.card.Answering
	r.card = card
	r.Sink.Emit(card)
}

// answering puts pl's card up again, answered by key while kit does what it
// says, or, "", waiting for an answer again: reporting whether it did, as
// it does only while pl's card is up and key is one of its keys, or "".
func (r *Raise) answering(pl *place, key hand.Answer) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.shown != pl || key != "" && !slices.ContainsFunc(r.card.Keys, func(k check.Key) bool { return hand.Answer(k.Key) == key }) {
		return false
	}
	r.card.Time, r.card.Answering = r.Now(), string(key)
	r.Sink.Emit(r.card)
	return true
}

// announce posts the notification of pl's card, while it's up, once each
// time it goes up.
func (r *Raise) announce(ctx context.Context, pl *place) {
	r.mu.Lock()
	defer r.mu.Unlock()
	q := r.queue()
	if q == nil || r.shown != pl || pl.notified != "" {
		return
	}
	pl.notified = r.card.Item
	q.Notify(ctx, noticeID(pl.step, r.card.Item), r.card.Title, noticeOf(r.card))
}

// unnotify takes the notification of pl's card away, once it's down: set
// aside, done, or the run stopped.
func (r *Raise) unnotify(ctx context.Context, pl *place) {
	if q := r.queue(); q != nil && pl.notified != "" {
		q.Clear(ctx, noticeID(pl.step, pl.notified))
		pl.notified = ""
	}
}

// queue is where the cards' notifications go out from, through Notify:
// nil with nothing to post them.
func (r *Raise) queue() *notice.Queue {
	r.noticesSet.Do(func() {
		if r.Notify != nil {
			r.notices = notice.New(r.Notify)
		}
	})
	return r.notices
}

// Wait waits till what the cards did aside is done: their work, their apps
// opened, and their notifications posted and taken away.
func (r *Raise) Wait() {
	r.aside.Wait()
	if q := r.queue(); q != nil {
		q.Wait()
	}
}

// noticeID is the id of the notification for the card of item, of step.
func noticeID(step, item string) string {
	return "card:" + step + ":" + item
}

// noticeOf is what a card's notification says under its title: its first
// step, else that kit waits for it.
func noticeOf(card event.NeedsYou) string {
	if len(card.Steps) > 0 {
		return card.Steps[0]
	}
	return "kit waits for you to do this"
}

// hasLocked reports whether it's pl's turn. mu is held.
func (r *Raise) hasLocked(pl *place) bool { return len(r.line) > 0 && r.line[0] == pl }

// join gives a card for step its place in the line, as s says, the card up
// set aside should it go first: its step says it waits its turn, unless
// it's first, so that one found done before it goes up never says it.
func (r *Raise) join(step string, s seat) *place {
	pl := &place{step: step, weight: s.weight, held: s.held, after: s.after, moved: make(chan struct{}, 1)}
	r.mu.Lock()
	defer r.mu.Unlock()
	at := r.placeOf(pl)
	switch {
	case at == 0 && len(r.line) > 0:
		r.setAside()
	case at > 0:
		r.Sink.Emit(event.Doing{Time: r.Now(), Step: step, Says: event.Queued})
	}
	r.line = slices.Insert(r.line, at, pl)
	r.line[0].poke()
	r.recount()
	return pl
}

// placeOf is where pl goes in the line: before the first card holding up
// less of the run, but for a held card up; held, at the end. mu is held.
func (r *Raise) placeOf(pl *place) int {
	if pl.held {
		return len(r.line)
	}
	from := 0
	if len(r.line) > 0 && r.line[0].held {
		from = 1
	}
	at := slices.IndexFunc(r.line[from:], func(o *place) bool { return o.weight < pl.weight })
	if at < 0 {
		return len(r.line)
	}
	return from + at
}

// next sets pl's card aside for the next card waiting, pl going to the end
// of the line: nothing when it isn't pl's turn, pl's held, or none waits.
func (r *Raise) next(pl *place) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.line) < 2 || r.line[0] != pl || pl.held {
		return
	}
	r.setAside()
	r.line = append(r.line[1:], pl)
	r.line[0].poke()
}

// leave takes pl out of the line, done or the run stopped, the turn going
// to the next card waiting: its card, should it ever have gone up, said to
// come down (event.CardDown) when down says so, as it's done or the person
// stopped there; the run stopping, its step finishing says so. It's said
// as pl's card stops being the card up, under mu, so that no card joining
// meanwhile puts it up again.
func (r *Raise) leave(pl *place, down bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	i := slices.Index(r.line, pl)
	if i < 0 {
		return
	}
	r.line = slices.Delete(r.line, i, i+1)
	if r.shown == pl {
		r.shown = nil
	}
	if down && pl.up != "" {
		r.Sink.Emit(event.CardDown{Time: r.Now(), Step: pl.step, Item: pl.up})
	}
	if i == 0 && len(r.line) > 0 {
		r.line[0].poke()
	}
	r.recount()
}

// setAside has the card whose turn it is lose it, told so, its step saying
// it waits its turn, before any other card goes up. mu is held.
func (r *Raise) setAside() {
	up := r.line[0]
	up.lost++
	if r.shown == up {
		r.shown = nil
	}
	r.Sink.Emit(event.Doing{Time: r.Now(), Step: up.step, Says: event.Queued})
	up.poke()
}

// recount puts the card up up again when how many wait after it has
// changed. mu is held.
func (r *Raise) recount() {
	if r.shown == nil {
		return
	}
	if more := r.moreLocked(r.shown); more != r.card.More {
		r.card.Time, r.card.More = r.Now(), more
		r.Sink.Emit(r.card)
	}
}

// moreLocked is how many cards wait after pl's: its step's own to follow
// it, and each other in the line with those to follow it. mu is held.
func (r *Raise) moreLocked(pl *place) int {
	more := pl.after
	for _, o := range r.line {
		if o != pl {
			more += 1 + o.after
		}
	}
	return more
}
