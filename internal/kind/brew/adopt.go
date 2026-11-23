package brew

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/runner"
)

// adoption is how a batch of casks goes in: those that go in with --adopt,
// as their apps in the Applications folder, dir, are theirs or aren't
// there; those that go in as ever, as kit can't vet what they put in
// place; and why each refused doesn't go in.
type adoption struct {
	dir     string
	adopt   []toAdopt
	plain   []string
	refused []error
}

// toAdopt is a cask to install with --adopt: by the name given, as the
// catalogue has it, and whether its apps are there already.
type toAdopt struct {
	name  string
	cask  catalogued
	there bool
}

// adoption is how batch, casks of one tap or Homebrew's own, goes in. A
// tap's never are in the catalogue, which holds Homebrew's own alone.
// With no Applications folder, or no catalogue, every one goes in as ever.
// One whose app there its install wouldn't take over, by its version,
// doesn't go in (versioned).
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
	a := adoption{dir: c.AppDirs[0]}
	for _, name := range batch {
		k, ok := cat.cask(name)
		if !ok || k.unvetted {
			a.plain = append(a.plain, name)
			continue
		}
		there, err := k.there(a.dir, c.Home)
		if err == nil && there {
			err = cat.versioned(k, a.dir)
		}
		if err != nil {
			a.refused = append(a.refused, err)
			continue
		}
		a.adopt = append(a.adopt, toAdopt{name: name, cask: k, there: there})
	}
	return a
}

// adopting installs batch as adoption has it, replacing each app Homebrew
// didn't adopt (replace).
func (c casks) adopting(ctx context.Context, batch []string) error {
	a := c.adoption(ctx, batch)
	errs := a.refused
	if len(a.plain) > 0 {
		if err := c.installs(ctx, []string{"--cask"}, a.plain); err != nil {
			errs = append(errs, err)
		}
	}
	if len(a.adopt) > 0 {
		names := make([]string, len(a.adopt))
		for i, t := range a.adopt {
			names[i] = t.name
		}
		if err := c.installs(ctx, []string{"--cask", "--adopt"}, names); err != nil {
			errs = append(errs, c.replace(ctx, a, err)...)
		}
	}
	return kind.Joined(errs)
}

// replace installs again, alone, with --force, each of a's casks whose
// adopt failed, as failed says, while its own app is still there, and an
// older copy of the same major version (replaceable): one that isn't fails
// saying why, in place of Homebrew's line. It's an install of its own as
// Homebrew refuses --adopt beside --force, and --adopt never replaces. It
// keys on Homebrew's line saying the app differs, never on the cask being
// missing: one that failed otherwise, as its download, may yet be adopted
// in place, where --force would delete the app. It returns what's still
// failed.
func (c casks) replace(ctx context.Context, a adoption, failed error) []error {
	var errs []error
	var refused []string
	replaced := false
	for _, t := range a.notAdopted(failed) {
		if there, err := t.cask.there(a.dir, c.Home); err != nil || !there {
			continue
		}
		if err := t.cask.replaceable(a.dir); err != nil {
			errs, refused = append(errs, err), append(refused, t.cask.token)
			continue
		}
		if err := c.installs(ctx, []string{"--cask", "--force"}, []string{t.name}); err != nil {
			errs = append(errs, err)
			continue
		}
		replaced = true
	}
	if replaced {
		failed = c.stillFailed(ctx, a, failed)
	}
	if failed = unsaid(failed, refused); failed != nil {
		errs = append([]error{failed}, errs...)
	}
	return errs
}

// caskFailed is the line brew install fails one of its casks with, by its
// token; notAdoptedLine, the one it fails a cask with whose app there
// differs from its download.
var (
	caskFailed     = regexp.MustCompile(`^Error: ([^\s:]+): `)
	notAdoptedLine = regexp.MustCompile(`^Error: ([^\s:]+): It seems the existing .+ is different from the one being installed`)
)

// notAdopted are those of a's casks with an app there that err, brew
// install --adopt's failure, says Homebrew didn't adopt.
func (a adoption) notAdopted(err error) []toAdopt {
	exit, ok := errors.AsType[*runner.ExitError](err)
	if !ok {
		return nil
	}
	var not []toAdopt
	for _, line := range exit.Lines("Error:") {
		m := notAdoptedLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if i := slices.IndexFunc(a.adopt, func(t toAdopt) bool { return t.there && t.cask.token == m[1] }); i >= 0 {
			not = append(not, a.adopt[i])
		}
	}
	return not
}

// stillFailed is failed, brew install --adopt's failure, once some of a's
// casks are replaced: none when every one is in; else its lines of those
// still missing alone, as brew lists them, so it never says why one now in
// failed. As it is when brew can't list them, or it names none of them.
func (c casks) stillFailed(ctx context.Context, a adoption, failed error) error {
	listed, err := c.lines(ctx, "list", "--cask", "--full-name", "-1")
	if err != nil {
		return failed
	}
	var missing []string
	for _, t := range a.adopt {
		if !slices.Contains(listed, t.cask.token) {
			missing = append(missing, t.cask.token)
		}
	}
	exit, ok := errors.AsType[*runner.ExitError](failed)
	switch {
	case len(missing) == 0:
		return nil
	case !ok:
		return failed
	}
	said := saidOf(exit, func(token string) bool { return slices.Contains(missing, token) })
	if said == nil {
		return failed
	}
	return said
}

// unsaid is failed, brew install's failure, without why the casks refused
// failed, which they say themselves: its lines of the other casks alone,
// none when it names no other. As it is when none was refused, or it isn't
// brew's.
func unsaid(failed error, refused []string) error {
	exit, ok := errors.AsType[*runner.ExitError](failed)
	if len(refused) == 0 || !ok {
		return failed
	}
	if said := saidOf(exit, func(token string) bool { return !slices.Contains(refused, token) }); said != nil {
		return said
	}
	return nil
}

// saidOf is exit, brew install's failure, saying only the lines it fails
// a cask on whose token keep keeps: nil when it has none.
func saidOf(exit *runner.ExitError, keep func(token string) bool) *runner.ExitError {
	var said []string
	for _, line := range exit.Lines("Error:") {
		if m := caskFailed.FindStringSubmatch(line); m != nil && keep(m[1]) {
			said = append(said, line)
		}
	}
	if len(said) == 0 {
		return nil
	}
	return runner.NewExitError(exit.Command, exit.Code, []byte(strings.Join(said, "\n")))
}
