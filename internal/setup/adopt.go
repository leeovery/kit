package setup

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/check"
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
		answered: func(ctx context.Context, rec record) (string, bool) {
			if rec.Adopt == nil {
				return "", false
			}
			hand := s.handApps(ctx)
			n := s.takenOver(hand, *rec.Adopt)
			first := "none"
			if n > 0 {
				first = appCount(n)
			}
			return hand.takesOver(first, n), true
		},
		anew: func(rec record) bool { return anew(s.givenAdopt(), rec.Adopt) },
		nothing: func(ctx context.Context, _ *snapshot.Snapshot) (string, bool) {
			hand := s.handApps(ctx)
			if hand.err != nil {
				return "couldn't tell which it can take over: " + oneLine(hand.err.Error()), len(hand.offered) == 0
			}
			return hand.takesOver("none to take over", 0), len(hand.offered) == 0
		},
		ask: func(ctx context.Context, _ *snapshot.Snapshot) error {
			casks, err := s.adoptAnswer(ctx)
			if err != nil {
				return err
			}
			return s.update(func(r *record) { r.Adopt = &casks })
		},
		edited: func(ctx context.Context, rec record) string {
			if rec.Adopt == nil || len(*rec.Adopt) == 0 {
				return "with no app for Homebrew to take over"
			}
			return "with Homebrew to take over " + appCount(s.takenOver(s.handApps(ctx), *rec.Adopt))
		},
	})
}

// appCount says n apps.
func appCount(n int) string { return fmt.Sprintf("%d %s", n, look.Plural(n, "app", "apps")) }

// takenOver is how many apps installed by hand casks take over, but those
// the Mac declares already, as the snapshot found them: as many as there
// are casks, when it found none of theirs, as when --adopt names casks
// while which casks install the apps couldn't be told.
func (s *Setup) takenOver(hand *handApps, casks []string) int {
	found, _ := s.snapshot()
	n, matched := 0, false
	if found != nil {
		for _, app := range found.ByHand {
			if app.Cask != "" && slices.Contains(casks, app.Cask) {
				matched = true
				if !hand.declared[app.Cask] {
					n++
				}
			}
		}
	}
	if matched {
		return n
	}
	for _, c := range casks {
		if !hand.declared[c] {
			n++
		}
	}
	return n
}

// takesOver is the row of the apps taken over: first, then how many apps
// the Mac declares the casks of already, and what kit apply does with all
// of them, n taken over by the answer.
func (h *handApps) takesOver(first string, n int) string {
	parts := []string{first}
	if h.declaredApps > 0 {
		parts = append(parts, fmt.Sprintf("%d declared already", h.declaredApps))
	}
	if all := n + h.declaredApps; all > 0 {
		parts = append(parts, "kit apply takes "+look.Plural(all, "it", "them")+" over")
	}
	return says(parts...)
}

// handApps is what setup makes of the apps installed by hand that a cask
// installs: those offered Homebrew, a cask each; the casks the Mac declares
// already, on, by any name, whose apps the kit apply after setup takes over
// anyway, and how many apps they are; those it declares off, whose apps
// stay by hand; and why which couldn't be told, when it couldn't, as which
// casks install them, or what the Mac declares.
type handApps struct {
	offered       []offer
	declared, off map[string]bool
	declaredApps  int
	err           error
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
	h := &handApps{declared: map[string]bool{}, off: map[string]bool{}}
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
			case taken.Off(c):
				h.off[c] = true
			case taken.Taken(c):
				h.declared[c] = true
				h.declaredApps += len(appsOf(found, c))
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

// casksTaken is how m's declared casks, and those installed, take a
// cask's name (kind.Taken).
func casksTaken(ctx context.Context, m snapshot.Mac) (kind.Taking, error) {
	if m.Config == nil {
		return kind.Taking{}, errors.New("kit-config isn't read")
	}
	k := brew.CasksDescriptor.New(kindDeps(m))
	list, unread, err := kind.Declared(m.Config, k, m.Name)
	if err = cmp.Or(err, unread); err != nil {
		return kind.Taking{}, err
	}
	return kind.Taken(ctx, k, list)
}

// appIDs are the bundle ids of the apps installed by hand found that cask
// installs.
func (s *Setup) appIDs(cask string) []string {
	found, _ := s.snapshot()
	if found == nil {
		return nil
	}
	var ids []string
	for _, app := range found.ByHand {
		if app.Cask == cask && app.ID != "" {
			ids = append(ids, app.ID)
		}
	}
	return ids
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
			switch {
			case hand.off[c]:
				return nil, fmt.Errorf("this Mac declares the %s cask off, so its app stays by hand: %s", c, adoptFlags(hand.offered))
			case hand.err == nil && !hand.declared[c] && !hand.offers(c):
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
	return s.tickedAs(ctx, AdoptStep, "which apps should Homebrew take over, so a new Mac gets them?\n"+adoptMore, items,
		func(i int) string { return hand.offered[i].cask })
}

// adoptMore is what more the question says: what taking over does, and
// what becomes of the rest.
const adoptMore = "Homebrew takes each over, replacing an older copy; the rest stay as they are, and kit won't ask about them again"

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
// their bundle ids, the apps of those offered and not taken, which are
// left by hand, so kit never asks of them. When which could be taken over
// couldn't be told, the answer stands as recorded, and nothing more is
// left.
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
			left = append(left, s.appIDs(o.cask)...)
		}
	}
	return adopt, left
}

// leftByHand are the apps installed by hand found that stay so, an item
// each: all but those whose cask the Mac declares on already, and those
// whose cask was ticked and declared, not refused by the commit. One whose
// cask was offered and not ticked says the command that takes it over
// later, as its detail.
func (s *Setup) leftByHand(found *snapshot.Snapshot, hand *handApps) []check.Item {
	adopted := s.record().Adopt
	refused := s.refused()
	var left []check.Item
	for _, app := range found.ByHand {
		ticked := adopted != nil && slices.Contains(*adopted, app.Cask)
		if app.Cask != "" && (hand.declared[app.Cask] || ticked && hand.offers(app.Cask) && !refused[brew.CasksDescriptor.Name+":"+app.Cask]) {
			continue
		}
		it := check.Item{ID: ByHandStep + ":" + app.Name, Name: app.Name, State: "installed by hand"}
		if hand.offers(app.Cask) && !ticked {
			it.Detail = AddCask + app.Cask
		}
		left = append(left, it)
	}
	return left
}

// AddCask is the command, before a cask's token, that takes over an app
// installed by hand: an item of the apps left by hand says it, as its
// detail, for one a cask installs.
const AddCask = "kit cask add "

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
