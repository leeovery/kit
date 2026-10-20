// Package appstore is the App Store's kind: apps installed from the Mac App
// Store, through mas. An app is named by its name and its id, as in
// xcode@497799835, so a list sorts by name and says what each is, while
// matching goes by the id alone, which the store never changes.
package appstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/runner"
)

// How long an app's install may take: once one app's in, an hour and a
// half, as Xcode is gigabytes; before, firstTimeout, as mas 7, with no one
// signed in to the App Store, waits for good rather than saying so (the
// sixth run, 19 Oct).
const (
	installTimeout = 90 * time.Minute
	firstTimeout   = 5 * time.Minute
)

// adminWait is why an install waits, without an administrator's password.
const adminWait = "needs an administrator's password: run kit apply at a terminal"

// most is how many of a search's apps are offered.
const most = 8

// AppStore is the App Store, driven through a runner.
type AppStore struct {
	run   runner.Runner
	admin func(ctx context.Context) bool
}

// kindName names the kind.
const kindName = "mas"

// Descriptor is the App Store's apps' kind, as kit's shared code knows it.
var Descriptor = kind.Descriptor{
	Name:    kindName,
	Label:   "App Store apps",
	Group:   kind.Packages,
	Section: &config.Section{Kind: kindName, Header: "app store apps", Form: config.Names, Sorted: true, Held: true},
	Help: &kind.Help{
		What:   "App Store apps",
		Add:    "Install App Store apps, by name or id, and declare them",
		Remove: "Uninstall App Store apps, and undeclare them",
	},
	Row: "App Store apps",
	New: func(d *kind.Deps) kind.Kind { return New(d.Run) },
}

// New returns the App Store, driven through run.
func New(run runner.Runner) *AppStore {
	return &AppStore{run: run}
}

func (*AppStore) Name() string    { return kindName }
func (*AppStore) Program() string { return "mas" }

// app is what mas says of an app, as far as kit needs.
type app struct {
	ID   int64  `json:"adamID"`
	Name string `json:"name"`
}

// named is the app's name as kit declares it.
func (a app) named() string {
	return Name(a.Name, a.ID)
}

// Name is an app's name as kit declares it: its name, in lowercase letters,
// digits and dashes, then @ and its id.
func Name(name string, id int64) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
		default:
			dash = true
		}
	}
	if b.Len() == 0 {
		b.WriteString("app")
	}
	return b.String() + "@" + strconv.FormatInt(id, 10)
}

// declared is a declared name: anything, then @ and the id.
var declared = regexp.MustCompile(`^[^@]+@([0-9]+)$`)

// id is the id in a declared name, or 0 when it hasn't one.
func id(name string) int64 {
	m := declared.FindStringSubmatch(name)
	if m == nil {
		return 0
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// apps runs mas with args, and reads the apps it prints, a JSON object a
// line.
func (s *AppStore) apps(ctx context.Context, args ...string) ([]app, error) {
	res, err := s.run.Run(ctx, runner.Command{Name: "mas", Args: args})
	if err != nil {
		return nil, err
	}
	var apps []app
	for line := range strings.Lines(string(res.Stdout)) {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		var a app
		if err := json.Unmarshal([]byte(line), &a); err != nil {
			return nil, fmt.Errorf("read mas %s's answer: %w", args[0], err)
		}
		apps = append(apps, a)
	}
	return apps, nil
}

// Installed lists the apps installed from the App Store, each installed for
// itself.
func (s *AppStore) Installed(ctx context.Context) ([]kind.Installed, error) {
	apps, err := s.apps(ctx, "list", "--json")
	if err != nil {
		return nil, err
	}
	installed := make([]kind.Installed, len(apps))
	for i, a := range apps {
		installed[i] = kind.Installed{Name: a.named(), Explicit: true}
	}
	return installed, nil
}

// Key is an app's id: apps match by it, whatever their names are now.
func (*AppStore) Key(name string) string {
	if n := id(name); n != 0 {
		return strconv.FormatInt(n, 10)
	}
	return name
}

// Resolve leaves each name with an id as it is, and leaves out a name
// without one, which isn't an app's.
func (*AppStore) Resolve(_ context.Context, names []string) (map[string]string, error) {
	resolved := make(map[string]string, len(names))
	for _, name := range names {
		if id(name) != 0 {
			resolved[name] = name
		}
	}
	return resolved, nil
}

// Install installs the apps names name, through sudo, as mas needs root to
// install, one at a time, so one that fails or hangs holds none of the rest
// up: till one's in, each is the check that someone's signed in to the App
// Store (install). No one signed in, the rest aren't tried.
func (s *AppStore) Install(ctx context.Context, names []string) error {
	var errs []error
	signedIn := false
	for _, name := range names {
		if ctx.Err() != nil {
			break
		}
		err := s.install(ctx, name, signedIn)
		if _, out := errors.AsType[*notSignedIn](err); out {
			return err
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		signedIn = true
	}
	return kind.Joined(errs)
}

// install installs name alone. Till an app's in (signedIn), an install mas
// says no one's signed in for, or that runs past firstTimeout, as mas 7's
// does with no one signed in, means signing in, a step by hand before the
// App Store's, wasn't done after all: it's done again (engine.Undone), then
// name's tried again. With nothing to do again, as without a terminal, it
// fails, naming the fix.
func (s *AppStore) install(ctx context.Context, name string, signedIn bool) error {
	if id(name) == 0 {
		return notAnApp(name)
	}
	limit := installTimeout
	if !signedIn {
		limit = firstTimeout
	}
	for {
		err := s.sudo(ctx, "install", []string{name}, limit, kind.Lines(ctx))
		hung := !signedIn && errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil
		switch {
		case err == nil:
			return nil
		case !hung && !signedOut(err):
			return failed(err)
		case !engine.Undone(ctx):
			return &notSignedIn{err: err, hung: hung}
		}
	}
}

// failed is err, an app's install failing, in kit's words: what mas said,
// as the run's reason reads it from its exit; else how it ended, never the
// command, which the run's log has.
func failed(err error) error {
	if _, exited := errors.AsType[*runner.ExitError](err); exited {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("mas didn't finish in %d minutes", int(installTimeout.Minutes()))
	}
	return errors.New("mas was stopped")
}

// signedOut reports whether err is mas saying no one's signed in to the App
// Store, as in "account is not signed in": mas has no way to say who is.
func signedOut(err error) bool {
	exit, ok := errors.AsType[*runner.ExitError](err)
	return ok && strings.Contains(strings.ToLower(exit.Stderr), "not signed in")
}

// notSignedIn is mas failing for want of someone signed in to the App Store,
// saying so, or hanging, in kit's words, naming the fix: what mas said is in
// the run's log.
type notSignedIn struct {
	err  error
	hung bool
}

func (n *notSignedIn) Error() string {
	if n.hung {
		return "the App Store didn't answer: sign in to the App Store, then try again"
	}
	return "no one's signed in: sign in to the App Store, then try again"
}

func (n *notSignedIn) Unwrap() error { return n.err }

// Remove uninstalls the apps names name, through sudo, as mas needs.
func (s *AppStore) Remove(ctx context.Context, names []string) error {
	return s.sudo(ctx, "uninstall", names, 0, nil)
}

// installedLine is what mas prints as it's installed an app, by its name,
// its version after it or not: ==> Installed Xcode (16.0).
var installedLine = regexp.MustCompile(`^==> Installed (.+?)(?: \([^()]*\))?$`)

// Done is which of names line, from mas install, says is installed: the
// one declared by the name mas gives it.
func (*AppStore) Done(line string, names []string) (string, bool) {
	m := installedLine.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return "", false
	}
	said, _, _ := strings.Cut(Name(m[1], 0), "@")
	for _, name := range names {
		if declaredAs, _, _ := strings.Cut(name, "@"); declaredAs == said {
			return name, true
		}
	}
	return "", false
}

// sudo runs mas's verb of names through sudo, given timeout, its lines to
// lines as they come.
func (s *AppStore) sudo(ctx context.Context, verb string, names []string, timeout time.Duration, lines func(string)) error {
	args := []string{"-n", "mas", verb}
	for _, name := range names {
		n := id(name)
		if n == 0 {
			return notAnApp(name)
		}
		args = append(args, strconv.FormatInt(n, 10))
	}
	_, err := s.run.Run(ctx, runner.Command{Name: "sudo", Args: args, Timeout: timeout, Lines: lines})
	return err
}

// notAnApp refuses name, which isn't an App Store app's.
func notAnApp(name string) error {
	return fmt.Errorf("%s isn't an App Store app's name: its name, then @ and its id", name)
}

// NeedsAdmin is names: mas needs root for every install.
func (s *AppStore) NeedsAdmin(_ context.Context, names []string) ([]string, error) {
	return slices.Clone(names), nil
}

// SetAdmin is how the App Store finds out whether an administrator's
// password is at hand.
func (s *AppStore) SetAdmin(held func(ctx context.Context) bool) {
	s.admin = held
}

// Blocked holds up every app missing, when kit is installing without an
// administrator's password.
func (s *AppStore) Blocked(ctx context.Context, missing map[string]string, _ []kind.Installed) (map[string]string, error) {
	blocked := make(map[string]string)
	if s.admin == nil || len(missing) == 0 || s.admin(ctx) {
		return blocked, nil
	}
	for name := range missing {
		blocked[name] = adminWait
	}
	return blocked, nil
}

// Find finds the app typed means: an id, looked up; a declared name, as it
// is; else a name, searched for in the store, an exact match alone, or the
// apps the search found, to choose from.
func (s *AppStore) Find(ctx context.Context, typed string) ([]kind.Found, error) {
	if id(typed) != 0 {
		return []kind.Found{{Name: typed}}, nil
	}
	if _, err := strconv.ParseInt(typed, 10, 64); err == nil {
		apps, err := s.apps(ctx, "lookup", "--json", typed)
		if _, exited := errors.AsType[*runner.ExitError](err); exited || err == nil && len(apps) == 0 {
			return nil, fmt.Errorf("no App Store app has the id %s", typed)
		}
		if err != nil {
			return nil, err
		}
		return []kind.Found{found(apps[0])}, nil
	}
	apps, err := s.apps(ctx, "search", "--json", typed)
	if _, exited := errors.AsType[*runner.ExitError](err); exited || err == nil && len(apps) == 0 {
		return nil, fmt.Errorf("no App Store app matches %s", typed)
	}
	if err != nil {
		return nil, err
	}
	var exact []kind.Found
	for _, a := range apps {
		if strings.EqualFold(a.Name, typed) {
			exact = append(exact, found(a))
		}
	}
	if len(exact) == 1 {
		return exact, nil
	}
	choices := make([]kind.Found, 0, most)
	for _, a := range apps[:min(len(apps), most)] {
		choices = append(choices, found(a))
	}
	return choices, nil
}

func found(a app) kind.Found {
	return kind.Found{Name: a.named(), Label: fmt.Sprintf("%s (%d)", a.Name, a.ID)}
}

var _ interface {
	kind.Kind
	kind.Keyed
	kind.Counter
	kind.Admin
	kind.Blocker
	kind.Finder
} = (*AppStore)(nil)
