package cli

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/askpass"
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
