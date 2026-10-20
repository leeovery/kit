package cli

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/askpass"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// renewing is the run's password as a sudoKeeper keeps it, for a test.
type renewing struct {
	mu    sync.Mutex
	has   bool
	again int
}

func (h *renewing) Held() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.has
}

func (h *renewing) askAgain() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.has = false
	h.again++
}

func (h *renewing) settle(has bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.has = has
}

// Renewing while the run holds the password, kit renews sudo's hold on it;
// when sudo
// has forgotten it, kit gives it the password it kept, the person asked only
// when sudo refuses that, and keeps what sudo takes, the password not held
// meanwhile; when it can't be given again, it's let go of, for what needs it
// to wait, and what asks next to ask the person rather than be given it.
func TestRenewSudo(t *testing.T) {
	for _, tc := range []struct {
		name string
		held bool
		// forgot is whether sudo has forgotten the password; asks, how many
		// times it asks for it then; refused, whether the person doesn't
		// give it.
		forgot  bool
		asks    int
		refused bool
		// given are what sudo is given; personAsked, how often the person's
		// asked; holds, whether the run still holds the password; kept, the
		// password kept for what asks next: none when "".
		given       []string
		personAsked int
		holds       bool
		kept        string
	}{
		{name: "sudo has it", held: true, holds: true, kept: "hunter2"},
		{name: "sudo forgot it", held: true, forgot: true, asks: 1, given: []string{"hunter2"}, holds: true, kept: "hunter2"},
		{name: "sudo refuses the one kept", held: true, forgot: true, asks: 2, given: []string{"hunter2", "typed"}, personAsked: 1, holds: true, kept: "typed"},
		{name: "it can't be given again", held: true, forgot: true, asks: 2, refused: true, given: []string{"hunter2"}, personAsked: 1},
		{name: "not held", forgot: true, kept: "hunter2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &renewing{has: tc.held}
			fake := runnertest.New(t)
			check := fake.On("sudo", "-n", "-v")
			if tc.forgot {
				check.Exits(1).PrintsToStderr("sudo: a password is required\n")
			}
			heldWhileGiven := false
			give := fake.On("sudo", "-S", "-p", sudoAsks, "-v").Asks(tc.asks).Does(func() { heldWhileGiven = h.Held() })
			personAsked := 0
			asks, err := askpass.Start("", "/usr/bin/true", func(context.Context, int) (string, error) {
				personAsked++
				if tc.refused {
					return "", errors.New("cancelled")
				}
				return "typed", nil
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = asks.Close() })
			asks.Keep("hunter2")

			holds := (&sudoKeeper{run: fake, asks: asks, h: h}).renew(t.Context())

			if !slices.Equal(give.Answered(), tc.given) || personAsked != tc.personAsked || holds != tc.holds || h.Held() != tc.holds {
				t.Errorf("sudo given %q, the person asked %d times, holds %v; want %q, %d, %v (ran %q)",
					give.Answered(), personAsked, h.Held(), tc.given, tc.personAsked, tc.holds, fake.Calls())
			}
			if (h.again == 1) != (tc.held && tc.forgot) || heldWhileGiven {
				t.Errorf("asked for again %v, held while sudo was given it %v; want it asked again once sudo forgot it, not held meanwhile", h.again, heldWhileGiven)
			}
			if !tc.held && len(fake.Calls()) > 0 {
				t.Errorf("ran %q, want nothing while the run doesn't hold the password", fake.Calls())
			}
			next, err := asks.Answer()(t.Context(), 0)
			if tc.kept != "" && (next != tc.kept || err != nil) || tc.kept == "" && personAsked == tc.personAsked {
				t.Errorf("what asks next is given %q, %v; want %q, the person asked when none's kept", next, err, tc.kept)
			}
		})
	}
}

// While kit asks for the password, first or again, a check takes it as
// coming, and what needs it waits for the answer, however long it takes;
// once given, nothing waits; once refused, what needs it is left out, a
// check made while kit asked having taken it as coming.
func TestAdminHold(t *testing.T) {
	for _, tc := range []struct {
		name  string
		given bool
	}{
		{name: "given", given: true},
		{name: "refused"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newAdminHold(nil, nil)
			r := &run{hold: h, placing: map[string]kind.Placing{}}
			changing := event.WithChanging(t.Context())
			for _, again := range []bool{false, true} {
				if again {
					h.askAgain()
				}
				if !h.held(t.Context()) || h.held(changing) || h.Held() {
					t.Errorf("held while asking (again %v) = %v checking, %v changing; want it coming for a check alone", again, h.held(t.Context()), h.held(changing))
				}
				admin := r.placed("app store apps").Admin
				waited := make(chan bool)
				go func() { waited <- admin.Wait(changing) }()
				select {
				case <-waited:
					t.Fatalf("what needs the password went on before it was answered (again %v)", again)
				case <-time.After(20 * time.Millisecond):
				}
				h.settle(tc.given)
				if got := <-waited; got != tc.given || h.Held() != tc.given || h.held(changing) != tc.given {
					t.Errorf("waited for the answer (again %v), got %v; want %v", again, got, tc.given)
				}
			}
		})
	}
}

// kit gives sudo no time limit for the password; an answer that came after
// sudo's own five minutes ended it is given to sudo asked again, the person
// asked once, and said to be what sudo took.
func TestGiveSudoGivesALateAnswerAgain(t *testing.T) {
	fake := runnertest.New(t)
	give := fake.On("sudo", "-S", "-p", sudoAsks, "-v").Asks(1).AnswersLate().Then().Asks(1)
	asked := 0
	given, took := giveSudo(t.Context(), fake, func(context.Context, int) (string, error) {
		asked++
		return "typed", nil
	})
	if !took || given != "typed" || asked != 1 || !slices.Equal(give.Answered(), []string{"typed"}) {
		t.Errorf("giveSudo() = %q, %v, the person asked %d times, sudo given %q; want typed taken, asked once", given, took, asked, give.Answered())
	}
	for _, c := range fake.Commands() {
		if c.Timeout != runner.Unlimited {
			t.Errorf("ran %q with a limit of %v, want none", c.String(), c.Timeout)
		}
	}
}

// Told sudo's forgotten the password, as a command run through sudo -n
// says, kit finds out at once: sudo has it still, and it's held; sudo
// hasn't, and it's asked for again, not held meanwhile, and held once
// given. Refused before, it isn't held, and nothing's asked.
func TestAForgottenPasswordIsFoundOutAtOnce(t *testing.T) {
	for _, tc := range []struct {
		name          string
		held, forgot  bool
		given         []string
		holds, asking bool
	}{
		{name: "sudo has it", held: true, holds: true},
		{name: "sudo forgot it", held: true, forgot: true, given: []string{"typed"}, holds: true, asking: true},
		{name: "refused before", forgot: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := runnertest.New(t)
			check := fake.On("sudo", "-n", "-v")
			if tc.forgot {
				check.Exits(1).PrintsToStderr("sudo: a password is required\n")
			}
			var h *adminHold
			askingWhileGiven := false
			give := fake.On("sudo", "-S", "-p", sudoAsks, "-v").Asks(1).Does(func() { askingWhileGiven = h.asking() && !h.Held() })
			asks, err := askpass.Start("", "/usr/bin/true", func(context.Context, int) (string, error) { return "typed", nil })
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = asks.Close() })
			r := &run{run: fake, askpass: asks}
			h = newAdminHold(fake, asks)
			h.settle(tc.held)
			if tc.held {
				defer r.keepSudo(t.Context(), h)()
			}
			if got := h.Forgotten(t.Context()); got != tc.holds || h.Held() != tc.holds {
				t.Errorf("Forgotten() = %v, held %v; want %v", got, h.Held(), tc.holds)
			}
			if !slices.Equal(give.Answered(), tc.given) || askingWhileGiven != tc.asking {
				t.Errorf("sudo given %q, asking meanwhile %v; want %q, %v (ran %q)", give.Answered(), askingWhileGiven, tc.given, tc.asking, fake.Calls())
			}
			if !tc.held && len(fake.Calls()) > 0 {
				t.Errorf("ran %q, want nothing once refused", fake.Calls())
			}
		})
	}
}

// Two renewals at once, as the minute's and an install's sudo finding the
// password forgotten, ask the person once: the second waits for the first,
// and, the password refused, asks nothing again.
func TestRenewalsAtOnceAskOnce(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("sudo", "-n", "-v").Exits(1).PrintsToStderr("sudo: a password is required\n")
	fake.On("sudo", "-S", "-p", sudoAsks, "-v").Asks(1)
	asking := make(chan struct{})
	var personAsked atomic.Int32
	asks, err := askpass.Start("", "/usr/bin/true", func(context.Context, int) (string, error) {
		if personAsked.Add(1) == 1 {
			close(asking)
			time.Sleep(100 * time.Millisecond)
		}
		return "", errors.New("cancelled")
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = asks.Close() })
	h := &renewing{has: true}
	k := &sudoKeeper{run: fake, asks: asks, h: h}
	var wg sync.WaitGroup
	wg.Go(func() { k.renew(t.Context()) })
	<-asking
	wg.Go(func() { k.renew(t.Context()) })
	wg.Wait()
	if personAsked.Load() != 1 || h.again != 1 || h.Held() {
		t.Errorf("the person was asked %d times, the password asked for again %d times, held %v; want once, once, not held", personAsked.Load(), h.again, h.Held())
	}
}
