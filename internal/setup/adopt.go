package setup

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/brew"
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/snapshot"
)

// adoptStep asks which of the apps installed by hand that a cask installs
// Homebrew takes over, a row a cask (handApps), all suggested: the casks
// are declared with the snapshot, so the kit apply after setup takes the
// apps over, and a new Mac installs them; the rest are left by hand for
// good, so kit never asks of them again.
func (s *Setup) adoptStep() engine.Step {
	return s.questionStep(question{
		name: AdoptStep, title: "To Homebrew", flags: "--adopt or --no-adopt",
		answered: func(rec record) (string, bool) {
			if rec.Adopt == nil {
				return "", false
			}
			n := s.takenOver(*rec.Adopt)
			if n == 0 {
				return "none", true
			}
			return says(fmt.Sprintf("%d %s", n, look.Plural(n, "app", "apps")), "kit apply takes "+look.Plural(n, "it", "them")+" over"), true
		},
		anew: func(rec record) bool { return anew(s.givenAdopt(), rec.Adopt) },
		nothing: func(ctx context.Context, _ *snapshot.Snapshot) (string, bool) {
			hand := s.handApps(ctx)
			if hand.err != nil {
				return "couldn't tell which it can take over: " + oneLine(hand.err.Error()), len(hand.offered) == 0
			}
			return "none to take over", len(hand.offered) == 0
		},
		ask: func(ctx context.Context, _ *snapshot.Snapshot) error {
			casks, err := s.adoptAnswer(ctx)
			if err != nil {
				return err
			}
			return s.update(func(r *record) { r.Adopt = &casks })
		},
		edited: func(rec record) string {
			if rec.Adopt == nil || len(*rec.Adopt) == 0 {
				return "with no app for Homebrew to take over"
			}
			n := s.takenOver(*rec.Adopt)
			return fmt.Sprintf("with Homebrew to take over %d %s", n, look.Plural(n, "app", "apps"))
		},
	})
}

// takenOver is how many apps installed by hand casks take over, as the
// snapshot found them: as many as there are casks, when it found none of
// theirs.
func (s *Setup) takenOver(casks []string) int {
	found, _ := s.snapshot()
	n := 0
	if found != nil {
		for _, app := range found.ByHand {
			if app.Cask != "" && slices.Contains(casks, app.Cask) {
				n++
			}
		}
	}
	return cmp.Or(n, len(casks))
}

// handApps is what setup makes of the apps installed by hand that a cask
// installs: those offered Homebrew, a cask each; the casks the Mac declares
// already, by any name, whose apps the kit apply after setup takes over
// anyway; and why which couldn't be told, when it couldn't, as which casks
// install them, or what the Mac declares.
type handApps struct {
	offered  []offer
	declared map[string]bool
	err      error
}

// offer is a cask offered Homebrew, and the apps it takes over.
type offer struct {
	cask string
	apps []string
}

// offers reports whether h offers cask.
func (h *handApps) offers(cask string) bool {
	return slices.ContainsFunc(h.offered, func(o offer) bool { return o.cask == cask })
}

// handApps is what setup makes of the apps installed by hand that a cask
// installs, found once the snapshot's taken.
func (s *Setup) handApps(ctx context.Context) *handApps {
	s.mu.Lock()
	known := s.hand
	s.mu.Unlock()
	if known != nil {
		return known
	}
	found, m := s.snapshot()
	h := &handApps{declared: map[string]bool{}}
	if found == nil {
		return h
	}
	h.err = found.MatchErr
	var casks []string
	for _, app := range found.ByHand {
		if app.Cask != "" && !slices.Contains(casks, app.Cask) {
			casks = append(casks, app.Cask)
		}
	}
	if len(casks) > 0 {
		taken, err := casksTaken(ctx, m)
		if err != nil {
			h.err = fmt.Errorf("couldn't read what this Mac declares: %w", err)
		}
		for _, c := range casks {
			switch {
			case err != nil:
			case taken(c):
				h.declared[c] = true
			default:
				h.offered = append(h.offered, offer{cask: c, apps: appsOf(found, c)})
			}
		}
	}
	s.mu.Lock()
	s.hand = h
	s.mu.Unlock()
	return h
}

// casksTaken reports of a cask whether m declares it, or has it installed,
// by any name it goes by (kind.Taken).
func casksTaken(ctx context.Context, m snapshot.Mac) (func(string) bool, error) {
	if m.Config == nil {
		return nil, errors.New("kit-config isn't read")
	}
	k := brew.CasksDescriptor.New(kindDeps(m))
	list, unread, err := kind.Declared(m.Config, k, m.Name)
	if err = cmp.Or(err, unread); err != nil {
		return nil, err
	}
	return kind.Taken(ctx, k, list)
}

// appsOf are the names of the apps installed by hand found that cask
// installs.
func appsOf(found *snapshot.Snapshot, cask string) []string {
	var names []string
	for _, app := range found.ByHand {
		if app.Cask == cask {
			names = append(names, app.Name)
		}
	}
	return names
}

func (s *Setup) adoptAnswer(ctx context.Context) ([]string, error) {
	hand := s.handApps(ctx)
	switch {
	case s.Given.Adopt != nil:
		casks := *s.givenAdopt()
		for _, c := range casks {
			if hand.err == nil && !hand.declared[c] && !hand.offers(c) {
				return nil, fmt.Errorf("no app installed by hand here is the %s cask's to take over: %s", c, adoptFlags(hand.offered))
			}
		}
		return casks, nil
	case s.Ask == nil:
		return nil, s.unanswered(adoptFlags(hand.offered))
	}
	items := make([]Item, len(hand.offered))
	for i, o := range hand.offered {
		items[i] = Item{Name: look.InWords(o.apps), Ticked: true}
		if len(o.apps) > 1 || o.cask != slug(o.apps[0]) {
			items[i].More = o.cask
		}
	}
	return s.tickedAs(ctx, AdoptStep, "which apps should Homebrew take over, so a new Mac gets them?\nthe rest stay as they are, and kit won't ask about them again", items,
		func(i int) string { return hand.offered[i].cask })
}

// givenAdopt are the casks --adopt gives, each once: nil when it's not
// given.
func (s *Setup) givenAdopt() *[]string {
	if s.Given.Adopt == nil {
		return nil
	}
	casks := []string{}
	for _, c := range *s.Given.Adopt {
		if !slices.Contains(casks, c) {
			casks = append(casks, c)
		}
	}
	return &casks
}

// slug is an app's name as a cask's token most often has it: lower case,
// dashes for spaces.
func slug(name string) string {
	return strings.ReplaceAll(strings.ToLower(name), " ", "-")
}

// adoptFlags is what the flags answer, naming each cask offered, and the
// apps it takes over.
func adoptFlags(offered []offer) string {
	if len(offered) == 0 {
		return "--no-adopt, as Homebrew can take over none of the apps installed by hand"
	}
	said := make([]string, len(offered))
	for i, o := range offered {
		said[i] = o.cask + " for " + look.InWords(o.apps)
	}
	return "--adopt <cask> for each app installed by hand that Homebrew takes over, so a new Mac gets it (" + strings.Join(said, ", ") +
		", all suggested), or --no-adopt leaves them as they are, for good"
}

// adopting are the casks rec's answer has Homebrew take over apps
// installed by hand with, but those the Mac declares already; and, by
// their drift's ids, those offered and not taken, which are left by hand,
// so kit never asks of them. When which could be taken over couldn't be
// told, the answer stands as recorded, and nothing more is left.
func (s *Setup) adopting(ctx context.Context, rec record) (adopt, left []string) {
	if rec.Adopt == nil {
		return nil, nil
	}
	hand := s.handApps(ctx)
	if hand.err != nil {
		for _, c := range *rec.Adopt {
			if !hand.declared[c] {
				adopt = append(adopt, c)
			}
		}
		return adopt, nil
	}
	for _, o := range hand.offered {
		if slices.Contains(*rec.Adopt, o.cask) {
			adopt = append(adopt, o.cask)
		} else {
			left = append(left, brew.CasksDescriptor.Name+":"+o.cask)
		}
	}
	return adopt, left
}

// leftByHand are the apps installed by hand found that stay so: all but
// those whose cask the Mac declares already, and those whose cask was
// ticked and declared, not refused by the commit.
func (s *Setup) leftByHand(found *snapshot.Snapshot, hand *handApps) []brew.ByHand {
	adopted := s.record().Adopt
	refused := s.refused()
	var left []brew.ByHand
	for _, app := range found.ByHand {
		taken := adopted != nil && slices.Contains(*adopted, app.Cask) && hand.offers(app.Cask) && !refused[brew.CasksDescriptor.Name+":"+app.Cask]
		if app.Cask == "" || !hand.declared[app.Cask] && !taken {
			left = append(left, app)
		}
	}
	return left
}

// refused are the things setup's commit refused to declare, by id.
func (s *Setup) refused() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]bool{}
	if s.declared == nil {
		return out
	}
	for _, it := range s.declared.Items {
		if it.State == Refused {
			out[it.ID] = true
		}
	}
	return out
}
