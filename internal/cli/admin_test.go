package cli

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/askpass"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// While the run holds the password, kit renews sudo's hold on it; when sudo
// has forgotten it, kit gives it the password it kept, the person asked only
// when sudo refuses that, and keeps what they give; when it can't be given
// again, it's let go of, for what needs it to wait, and what asks next to
// ask the person rather than be given it.
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
		{name: "not held", forgot: true, holds: true, kept: "hunter2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := runnertest.New(t)
			check := fake.On("sudo", "-n", "-v")
			if tc.forgot {
				check.Exits(1).PrintsToStderr("sudo: a password is required\n")
			}
			give := fake.On("sudo", "-S", "-p", sudoAsks, "-v").Asks(tc.asks)
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

			// The first renewal, then the run ends.
			ctx, cancel := context.WithCancel(t.Context())
			renewals := 0
			held := func() bool {
				if renewals++; renewals > 1 {
					cancel()
					return false
				}
				return tc.held
			}
			holds := renewSudo(ctx, fake, asks, time.Millisecond, held)

			if !slices.Equal(give.Answered(), tc.given) || personAsked != tc.personAsked || holds != tc.holds {
				t.Errorf("sudo given %q, the person asked %d times, holds %v; want %q, %d, %v (ran %q)",
					give.Answered(), personAsked, holds, tc.given, tc.personAsked, tc.holds, fake.Calls())
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

// While kit asks for the password as the run goes on, a check takes it as
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
			h := &adminHold{answered: make(chan struct{})}
			r := &run{hold: h, placing: map[string]kind.Placing{}}
			changing := event.WithChanging(t.Context())
			if !h.held(t.Context()) || h.held(changing) {
				t.Errorf("held while asking = %v checking, %v changing; want it coming for a check alone", h.held(t.Context()), h.held(changing))
			}
			admin := r.placed("app store apps").Admin
			if admin == nil {
				t.Fatal("placed while asking has nothing wait for the password")
			}
			waited := make(chan bool)
			go func() { waited <- admin(changing) }()
			select {
			case <-waited:
				t.Fatal("what needs the password went on before it was answered")
			case <-time.After(20 * time.Millisecond):
			}
			h.has.Store(tc.given)
			close(h.answered)
			if got := <-waited; got != tc.given {
				t.Errorf("waited for the answer, got %v; want %v", got, tc.given)
			}
			if after := r.placed("app store apps").Admin; tc.given != (after == nil) {
				t.Errorf("placed once answered (given %v) waits: %v; want it to wait only once refused", tc.given, after != nil)
			}
		})
	}
}

// kit gives sudo no time limit for the password; an answer that came after
// sudo's own five minutes ended it is given to sudo asked again, the person
// asked once.
func TestGiveSudoGivesALateAnswerAgain(t *testing.T) {
	fake := runnertest.New(t)
	give := fake.On("sudo", "-S", "-p", sudoAsks, "-v").Asks(1).AnswersLate().Then().Asks(1)
	asked := 0
	took := giveSudo(t.Context(), fake, func(context.Context, int) (string, error) {
		asked++
		return "typed", nil
	})
	if !took || asked != 1 || !slices.Equal(give.Answered(), []string{"typed"}) {
		t.Errorf("giveSudo() = %v, the person asked %d times, sudo given %q; want it taken, asked once", took, asked, give.Answered())
	}
	for _, c := range fake.Commands() {
		if c.Timeout != runner.Unlimited {
			t.Errorf("ran %q with a limit of %v, want none", c.String(), c.Timeout)
		}
	}
}
