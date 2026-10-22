// Package notice posts notifications aside, one at a time in the order
// they're given, so that nothing waits for one to go out: on a new Mac, a
// notification can wait a minute while kit's app is built to post it.
package notice

import (
	"context"
	"sync"
)

// Notifier posts notifications, each under an id, replacing any posted
// under it before, and takes them away. Notify posts nothing once ctx is
// done.
type Notifier interface {
	Notify(ctx context.Context, id, title, body string)
	Clear(ctx context.Context, id string)
}

// Queue passes notifications, and their taking away, to a Notifier, aside
// and in the order it's given them, so that one is never taken away before
// it's posted. A notification taken away before it's gone out is passed on
// with its context done, so it posts nothing; one taken away as it's going
// out is taken away after.
type Queue struct {
	to Notifier

	// mu guards what waits to go out, what's going out, and idle: closed
	// once nothing waits, nil while nothing's going out.
	mu      sync.Mutex
	waiting []*send
	sending *send
	idle    chan struct{}
}

// send is a notification to post, or, clear, one to take away, under id.
type send struct {
	id, title, body string
	clear           bool
	ctx             context.Context
	cancel          context.CancelFunc
}

// New returns a queue passing notifications to to.
func New(to Notifier) *Queue { return &Queue{to: to} }

// Notify posts a notification under id, aside: ctx's values go with it,
// though it goes out whether or not ctx is done by then.
func (q *Queue) Notify(ctx context.Context, id, title, body string) {
	ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	q.add(&send{id: id, title: title, body: body, ctx: ctx, cancel: cancel})
}

// Clear takes away the notification posted under id, aside, after it's
// gone out, whether or not ctx is done by then. A notification under id
// still to go out, or going out, has its context done.
func (q *Queue) Clear(ctx context.Context, id string) {
	q.mu.Lock()
	for _, s := range q.waiting {
		s.supersede(id)
	}
	if q.sending != nil {
		q.sending.supersede(id)
	}
	q.mu.Unlock()
	q.add(&send{id: id, clear: true, ctx: context.WithoutCancel(ctx)})
}

// Wait waits till everything the queue's been given has gone out.
func (q *Queue) Wait() {
	q.mu.Lock()
	idle := q.idle
	q.mu.Unlock()
	if idle != nil {
		<-idle
	}
}

// supersede has s post nothing when it's a notification under id.
func (s *send) supersede(id string) {
	if !s.clear && s.id == id {
		s.cancel()
	}
}

// add puts s at the end of the queue, starting it going out when nothing
// is.
func (q *Queue) add(s *send) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.waiting = append(q.waiting, s)
	if q.idle == nil {
		q.idle = make(chan struct{})
		go q.work()
	}
}

// work passes on what's waiting, in turn, till nothing is.
func (q *Queue) work() {
	for s := q.next(); s != nil; s = q.next() {
		if s.clear {
			q.to.Clear(s.ctx, s.id)
			continue
		}
		q.to.Notify(s.ctx, s.id, s.title, s.body)
		s.cancel()
	}
}

// next is what goes out next, as what went out last is done: nil once
// nothing waits, the queue idle.
func (q *Queue) next() *send {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.sending = nil
	if len(q.waiting) == 0 {
		close(q.idle)
		q.idle = nil
		return nil
	}
	q.sending, q.waiting = q.waiting[0], q.waiting[1:]
	return q.sending
}
