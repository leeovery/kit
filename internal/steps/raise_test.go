package steps

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/hand"
)

// sunk is a sink that keeps nothing.
type sunk struct{}

func (sunk) Emit(event.Event) {}

// A card's turn gone and back before its loop wakes is a fresh turn, its
// card never up meanwhile: its grace begins again and a look's asked for.
// Its grace ended while another card had the turn, it would otherwise
// wait for its next look to go up.
func TestATurnGoneAndBackIsFresh(t *testing.T) {
	r := &Raise{Sink: sunk{}, Now: time.Now, After: func(Wait, time.Duration) <-chan time.Time { return make(chan time.Time) }}
	u := &raising{r: r, ctx: t.Context(), pl: r.join("store", seat{}), readied: true, work: &work{wake: make(chan struct{}, 1)}}
	u.takeTurn()
	if u.turn != 1 || u.grace == nil {
		t.Fatalf("its turn come, turn %d, grace %v; want look 1 asked, its grace begun", u.turn, u.grace)
	}
	vault := r.join("vault", seat{weight: 1})
	u.draw()
	r.leave(vault, true)
	u.takeTurn()
	if u.turn != 2 || u.grace == nil || u.drawn {
		t.Errorf("its turn back, turn %d, grace %v, drawn %v; want look 2 asked, its grace begun again, not up", u.turn, u.grace, u.drawn)
	}
}

// marking is a card whose answer, marking it done, fails.
type marking struct{}

func (marking) card() event.NeedsYou                { return event.NeedsYou{Title: "Add the printer"} }
func (marking) done(context.Context) (bool, string) { return false, "" }
func (marking) answer(context.Context, hand.Answer, time.Time) (bool, string, error) {
	return true, "", errors.New("mark printer done: read-only file system")
}

// What an answer comes to once the card's down, the person having stopped
// there, is kept should it have done it, and its error, which nothing's
// left to say, the log says, as something to look at.
func TestAnAnswersErrorOnceTheCardsDown(t *testing.T) {
	var logged []event.Logged
	ctx := event.WithLogging(t.Context(), func(l event.Logged) { logged = append(logged, l) })
	w := &work{c: marking{}, out: make(chan result), halted: make(chan struct{})}
	close(w.halted)
	done, saw, err := marking{}.answer(ctx, hand.Done, time.Now())
	w.report(ctx, result{answered: true, done: done, saw: saw, err: err})
	want := []event.Logged{{Line: "Add the printer: mark printer done: read-only file system", Alert: true}}
	if !slices.Equal(logged, want) || !w.late.done {
		t.Errorf("logged %+v, kept %+v; want %+v, and it kept as done", logged, w.late, want)
	}
}

// A card's notification says, under its title, why the card's there, its
// first sentence; with no why, its first step that isn't its title nor
// heads others; else that kit waits for it; never the title again. One too
// long for a notification is cut after a whole word.
func TestANotificationSaysWhy(t *testing.T) {
	for _, tt := range []struct {
		name string
		card event.NeedsYou
		want string
	}{
		{"its why", event.NeedsYou{Title: "Open Karabiner-Elements", Why: "Its key remapping needs its driver and Input Monitoring. Opened, it asks for both.", Steps: []string{"Open Karabiner-Elements"}},
			"Its key remapping needs its driver and Input Monitoring."},
		{"no why", event.NeedsYou{Title: "Open Karabiner-Elements", Steps: []string{"Open Karabiner-Elements", "If it doesn't ask, in System Settings:", "Allow its driver"}},
			"Allow its driver"},
		{"a why that's its title", event.NeedsYou{Title: "Add the printer", Why: "Add the printer.", Steps: []string{"Plug it in"}}, "Plug it in"},
		{"nothing but its title", event.NeedsYou{Title: "Add the printer", Steps: []string{"Add the printer"}}, "kit waits for you to do this"},
		{"a long why", event.NeedsYou{Title: "Add a Time Machine disk", Why: "Time Machine keeps an hourly copy of everything on this Mac, on a disk plugged in or on the network, so nothing's lost."},
			"Time Machine keeps an hourly copy of everything on this Mac, on a disk plugged in or on…"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := noticeOf(tt.card)
			if got != tt.want {
				t.Errorf("the notification says %q, want %q", got, tt.want)
			}
			if n := utf8.RuneCountInString(got); n > 91 {
				t.Errorf("the notification says %d characters, more than a notification shows", n)
			}
		})
	}
}

// A sentence ends at a full stop, question or exclamation mark and a space,
// never at an abbreviation's.
func TestFirstSentence(t *testing.T) {
	for text, want := range map[string]string{
		"Its driver needs allowing. Then Input Monitoring.":           "Its driver needs allowing.",
		"Some apps, e.g. Moom, need it. Others don't.":                "Some apps, e.g. Moom, need it.",
		"One store, i.e. the App Store, signs in. Then kit installs.": "One store, i.e. the App Store, signs in.",
		"Is it plugged in? Plug it in.":                               "Is it plugged in?",
		"No stop at all":                                              "No stop at all",
	} {
		if got := firstSentence(text); got != want {
			t.Errorf("firstSentence(%q) = %q, want %q", text, got, want)
		}
	}
}

// Cut, text fits in n characters with its ellipsis, after a whole word: a
// word that ends where the cut falls is kept.
func TestCutAtWord(t *testing.T) {
	for _, tt := range []struct {
		text string
		n    int
		want string
	}{
		{"short", 10, "short"},
		{"exactly ten", 11, "exactly ten"},
		{"one two three", 8, "one two…"},
		{"one two three", 9, "one two…"},
		{"one two three", 7, "one…"},
		{"one, two three", 6, "one…"},
		{"abcdefghij", 5, "abcd…"},
	} {
		got := cutAtWord(tt.text, tt.n)
		if got != tt.want || utf8.RuneCountInString(got) > tt.n {
			t.Errorf("cutAtWord(%q, %d) = %q, want %q, at most %d characters", tt.text, tt.n, got, tt.want, tt.n)
		}
	}
}
