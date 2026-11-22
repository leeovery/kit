package brew

import (
	"context"
	"errors"
	"regexp"
	"slices"

	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/runner"
)

// adoption is how a batch of casks goes in, as the apps already in the
// Applications folder have it.
type adoption struct {
	// dir is the Applications folder casks put their apps in.
	dir string
	// adopt go in with --adopt: the catalogue's casks, by the names given,
	// whose apps there, if any are, are theirs. casks are each as the
	// catalogue has it; there, whether its apps are there.
	adopt []string
	casks map[string]catalogued
	there map[string]bool
	// plain go in as ever, without --adopt: those the catalogue doesn't
	// know, as another tap's, whose apps there can't be told to be theirs.
	// Homebrew fails one whose app is there.
	plain []string
	// refused don't go in, each saying why: an app there isn't the cask's.
	refused []error
}

// adoption is how batch, casks of one tap or Homebrew's own, goes in. With
// no Applications folder to look in, or no catalogue to tell whose an app
// there is, every one goes in as ever; a tap's without the catalogue read,
// as it holds Homebrew's own casks alone.
func (c casks) adoption(ctx context.Context, batch []string) adoption {
	all := adoption{plain: batch}
	if _, tapped := tapOf(batch[0]); tapped || len(c.AppDirs) == 0 {
		return all
	}
	cat, err := c.catalogue(ctx)
	if err != nil {
		event.Log(ctx, event.Logged{Line: "kit can't tell whose an app already there is, so casks install without adopting one: " + err.Error()})
		return all
	}
	a := adoption{dir: c.AppDirs[0], casks: map[string]catalogued{}, there: map[string]bool{}}
	for _, name := range batch {
		k, ok := cat.cask(name)
		if !ok {
			a.plain = append(a.plain, name)
			continue
		}
		there, err := k.there(a.dir, c.Home)
		if err != nil {
			a.refused = append(a.refused, err)
			continue
		}
		a.adopt = append(a.adopt, name)
		a.casks[name], a.there[name] = k, len(there) > 0
	}
	return a
}

// adopting installs batch, casks of one tap or Homebrew's own, as adoption
// has it: the catalogue's with --adopt, so the cask's own app already in
// the Applications folder, by its bundle id, is taken over in place, as
// Homebrew does unchecked for a cask that updates itself, and for any other
// only when its bundle versions are the download's; one Homebrew didn't
// adopt replaced (replace). A cask whose app there isn't its own isn't
// installed, saying so: kit never takes over, nor deletes, an app that may
// be another's, as a fork of the cask's under its name.
func (c casks) adopting(ctx context.Context, batch []string) error {
	a := c.adoption(ctx, batch)
	errs := a.refused
	if len(a.plain) > 0 {
		if err := c.installs(ctx, []string{"--cask"}, a.plain); err != nil {
			errs = append(errs, err)
		}
	}
	if len(a.adopt) > 0 {
		if err := c.installs(ctx, []string{"--cask", "--adopt"}, a.adopt); err != nil {
			errs = append(errs, c.replace(ctx, a, err)...)
		}
	}
	return kind.Joined(errs)
}

// replace installs again, alone, with --force, each of a's casks that
// failed, brew install --adopt's failure, says Homebrew didn't adopt
// (notAdopted), while its own app, by its bundle id, is still there:
// Homebrew deletes that app, then moves the cask's in. --force never goes
// in first, as it would replace an app Homebrew adopts in place. It
// returns what failed: each replacing that failed, and failed itself,
// unless every one of a's casks to adopt is in now.
func (c casks) replace(ctx context.Context, a adoption, failed error) []error {
	var errs []error
	replaced := false
	for _, name := range a.notAdopted(failed) {
		if there, err := a.casks[name].there(a.dir, c.Home); err != nil || len(there) == 0 {
			continue
		}
		if err := c.installs(ctx, []string{"--cask", "--force"}, []string{name}); err != nil {
			errs = append(errs, err)
			continue
		}
		replaced = true
	}
	if !replaced || !c.allIn(ctx, a) {
		errs = append([]error{failed}, errs...)
	}
	return errs
}

// notAdoptedLine is the line brew install --adopt fails a cask with, by
// its token, when the app at its target differs from the cask's: its
// bundle versions aren't the download's.
var notAdoptedLine = regexp.MustCompile(`^Error: (\S+): It seems the existing .+ is different from the one being installed`)

// notAdopted are those of a's casks with an app there that err, brew
// install --adopt's failure, says Homebrew didn't adopt.
func (a adoption) notAdopted(err error) []string {
	exit, ok := errors.AsType[*runner.ExitError](err)
	if !ok {
		return nil
	}
	var names []string
	for _, line := range exit.Lines("Error:") {
		m := notAdoptedLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		for _, name := range a.adopt {
			if a.there[name] && a.casks[name].token == m[1] && !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}
	return names
}

// allIn reports whether every one of a's casks to adopt is installed, as
// brew lists them, by token.
func (c casks) allIn(ctx context.Context, a adoption) bool {
	listed, err := c.lines(ctx, "list", "--cask", "--full-name", "-1")
	if err != nil {
		return false
	}
	return !slices.ContainsFunc(a.adopt, func(name string) bool { return !slices.Contains(listed, a.casks[name].token) })
}
