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
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/leeovery/kit/internal/apps"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/hold"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/state"
)

// installTimeout is how long an app's install may take: Xcode is
// gigabytes.
const installTimeout = 90 * time.Minute

// firstWord is how long, till an app's in, mas may take to say anything
// as it installs: mas 7, with no one signed in to the App Store, waits for
// good, saying nothing, rather than saying so (the sixth run, 19 Oct). Once
// it's said it's downloading, it says nothing more till it's done: its
// progress is drawn only at a terminal.
var firstWord = 5 * time.Minute

// adminWait is why an install waits, without an administrator's password.
const adminWait = "needs an administrator's password: run kit apply at a terminal"

// most is how many of a search's apps are offered.
const most = 8

// AppStore is the App Store, driven through a runner.
type AppStore struct {
	run   runner.Runner
	admin func(ctx context.Context) bool
	// AppDirs are the folders apps are installed in, where an app the App
	// Store installed that mas doesn't list yet is looked for; StateDir,
	// kit's state folder, where what the store said of each such app's
	// bundle id is kept.
	AppDirs  []string
	StateDir string
	// Now is the time, which says when to ask the store again about an app
	// it said it hasn't.
	Now func() time.Time

	// mu guards listed, what mas listed last, which Missed goes by.
	mu     sync.Mutex
	listed []app
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
	New: func(d *kind.Deps) kind.Kind {
		s := New(d.Run)
		s.AppDirs, s.StateDir = d.AppDirs, d.StateDir
		return s
	},
}

// New returns the App Store, driven through run.
func New(run runner.Runner) *AppStore {
	return &AppStore{run: run, Now: time.Now}
}

func (*AppStore) Name() string    { return kindName }
func (*AppStore) Program() string { return "mas" }

// app is what mas says of an app, as far as kit needs.
type app struct {
	ID     int64  `json:"adamID"`
	Bundle string `json:"bundleID"`
	Name   string `json:"name"`
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

// Installed lists the apps installed from the App Store, as mas lists them,
// each installed for itself.
func (s *AppStore) Installed(ctx context.Context) ([]kind.Installed, error) {
	apps, err := s.apps(ctx, "list", "--json")
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.listed = apps
	s.mu.Unlock()
	installed := make([]kind.Installed, len(apps))
	for i, a := range apps {
		installed[i] = kind.Installed{Name: a.named(), Explicit: true}
	}
	return installed, nil
}

// lookupsName names kit's record of what the App Store said of the bundle
// ids it was asked about: each one's app id, 0 for one it doesn't have,
// and when it said so (Missed), as it's asked again a day on.
const lookupsName = "appstore.json"

type lookups struct {
	IDs    map[string]int64     `json:"ids"`
	Missed map[string]time.Time `json:"missed,omitempty"`
}

// askAgain is how long the store saying it hasn't an app holds.
const askAgain = 24 * time.Hour

// notInStore is how mas says the App Store has no app of a bundle id.
const notInStore = "no apps found in the app store"

// Missed finds which of names, declared and not in mas's last list, which
// holds what Spotlight has indexed, are installed after all, as an app installed
// a moment ago is: an app in AppDirs carrying the App Store's receipt, its
// id the App Store's for the bundle id the app says, asked once and kept
// in kit's state, as is the store not having it, for a day.
func (s *AppStore) Missed(ctx context.Context, names []string) ([]kind.Installed, error) {
	wanted := map[int64]string{}
	for _, name := range names {
		wanted[id(name)] = name
	}
	s.mu.Lock()
	listed := s.listed
	s.mu.Unlock()
	known := map[string]bool{}
	for _, a := range listed {
		known[strings.ToLower(a.Bundle)] = true
	}
	rec, err := state.Load[lookups](s.StateDir, lookupsName)
	if err != nil {
		return nil, err
	}
	var found []kind.Installed
	asked := map[string]int64{}
	for _, bundle := range s.received() {
		if known[strings.ToLower(bundle)] {
			continue
		}
		n, kept := rec.IDs[bundle]
		if kept && n == 0 && s.Now().Sub(rec.Missed[bundle]) > askAgain {
			kept = false
		}
		if !kept {
			a, ok := s.lookUp(ctx, bundle)
			if !ok {
				continue
			}
			n, asked[bundle] = a.ID, a.ID
		}
		if name, ok := wanted[n]; ok && n != 0 {
			found = append(found, kind.Installed{Name: name, Explicit: true})
		}
	}
	if len(asked) == 0 {
		return found, nil
	}
	return found, state.Update(s.StateDir, lookupsName, func(l *lookups) {
		if l.IDs == nil {
			l.IDs = map[string]int64{}
		}
		if l.Missed == nil {
			l.Missed = map[string]time.Time{}
		}
		maps.Copy(l.IDs, asked)
		for bundle, n := range asked {
			if n == 0 {
				l.Missed[bundle] = s.Now()
			} else {
				delete(l.Missed, bundle)
			}
		}
	})
}

// received are the bundle ids of the apps in AppDirs that carry the App
// Store's receipt.
func (s *AppStore) received() []string {
	var bundles []string
	for _, dir := range s.AppDirs {
		paths, _ := filepath.Glob(filepath.Join(dir, "*.app"))
		for _, path := range paths {
			if _, err := os.Stat(filepath.Join(path, "Contents", "_MASReceipt", "receipt")); err != nil {
				continue
			}
			if bundle := apps.BundleID(path); bundle != "" {
				bundles = append(bundles, bundle)
			}
		}
	}
	return bundles
}

// lookUp asks the App Store for the app of a bundle id: one with id 0 when
// it has none, which is worth keeping; not ok when it couldn't say.
func (s *AppStore) lookUp(ctx context.Context, bundle string) (app, bool) {
	found, err := s.apps(ctx, "lookup", "--json", "--bundle", bundle)
	if says(err, notInStore) {
		return app{Bundle: bundle}, true
	}
	if err != nil || len(found) != 1 {
		return app{}, false
	}
	return found[0], true
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
// up, each once kit has the password, should it be asking for it again
// (kind.AwaitAdmin): till one's in, each is the check that someone's signed
// in to the App Store (install). No one signed in, the rest aren't tried.
func (s *AppStore) Install(ctx context.Context, names []string) error {
	var errs []error
	signedIn := false
	for _, name := range names {
		if ctx.Err() != nil || !kind.AwaitAdmin(ctx) {
			break
		}
		err := s.install(ctx, name, signedIn)
		if _, out := errors.AsType[*notSignedIn](err); out {
			return kind.Joined(append(errs, err))
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
// says no one's signed in for, or hangs, as mas 7's does with no one signed
// in (attempt), means signing in, a step by hand before the App Store's,
// wasn't done after all: it's done again (engine.Undone), then name's tried
// again. With nothing to do again, as without a terminal, it fails, naming
// the fix.
func (s *AppStore) install(ctx context.Context, name string, signedIn bool) error {
	if id(name) == 0 {
		return notAnApp(name)
	}
	for {
		var hung bool
		err := s.again(ctx, func() (err error) {
			hung, err = s.attempt(ctx, name, signedIn)
			return err
		})
		switch {
		case err == nil:
			return nil
		case forgotten(err):
			return errors.New(adminWait)
		case !hung && !signedOut(err):
			return failed(err)
		}
		back := kind.Away(ctx)
		again := engine.Undone(ctx)
		back()
		if !again {
			return &notSignedIn{err: err, hung: hung}
		}
	}
}

// attempt installs name once, reporting whether it hung: till an app's in
// (signedIn), mas not saying anything for firstWord. What's left of an
// install kit ended is stopped (stopStray).
func (s *AppStore) attempt(ctx context.Context, name string, signedIn bool) (hung bool, err error) {
	run, lines := ctx, kind.Lines(ctx)
	if !signedIn {
		first, heard, cancel := hold.UntilHeard(ctx, firstWord)
		defer cancel()
		given := lines
		run, lines = first, func(line string) {
			heard()
			if given != nil {
				given(line)
			}
		}
	}
	res, err := s.sudo(run, "install", []string{name}, installTimeout, lines)
	hung = !signedIn && errors.Is(run.Err(), context.DeadlineExceeded) && ctx.Err() == nil
	if err != nil && ctx.Err() == nil && (hung || errors.Is(err, context.DeadlineExceeded)) {
		s.stopStray(ctx, res.Group, name)
	}
	return hung, err
}

// stopStray stops what's left of kit's install of name, in group, the
// process group it ran in, once kit's ended it: sudo passes on to mas that
// it's to stop, but mas, running as root, can carry on, holding up the
// next try. What pgrep finds of it in the group is killed through sudo;
// nothing outside it, as a mas run by hand.
func (s *AppStore) stopStray(ctx context.Context, group int, name string) {
	if group == 0 {
		return
	}
	res, err := s.run.Run(ctx, runner.Command{Name: "pgrep", Args: []string{"-g", strconv.Itoa(group), "-f", fmt.Sprintf("^(/[^ ]*/)?mas install %d$", id(name))}})
	pids := strings.Fields(string(res.Stdout))
	if err != nil || len(pids) == 0 {
		return
	}
	_ = s.again(ctx, func() error {
		_, err := s.run.Run(ctx, runner.Command{Name: "sudo", Args: append([]string{"-n", "kill", "-KILL"}, pids...)})
		return err
	})
}

// again runs f, through kit's own sudo -n, and once more should sudo have
// forgotten the password meanwhile, as when the Mac slept, once kit has it
// again (kind.AdminForgotten).
func (s *AppStore) again(ctx context.Context, f func() error) error {
	err := f()
	if forgotten(err) && kind.AdminForgotten(ctx) {
		return f()
	}
	return err
}

// forgotten reports whether err is sudo -n saying it hasn't the password.
func forgotten(err error) bool {
	exit, ok := errors.AsType[*runner.ExitError](err)
	return ok && slices.ContainsFunc(exit.Lines("sudo:"), func(line string) bool { return strings.Contains(line, "a password is required") })
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
	return says(err, "not signed in")
}

// says reports whether err is mas failing, saying text, whatever its case,
// on its error line: "Error:" for its own, "[ERROR]" as the App Store's
// sign-in reaches it.
func says(err error, text string) bool {
	exit, ok := errors.AsType[*runner.ExitError](err)
	return ok && slices.ContainsFunc(exit.Lines("Error:", "[ERROR]"), func(line string) bool {
		return strings.Contains(strings.ToLower(line), text)
	})
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
	return s.again(ctx, func() error {
		_, err := s.sudo(ctx, "uninstall", names, 0, nil)
		return err
	})
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
func (s *AppStore) sudo(ctx context.Context, verb string, names []string, timeout time.Duration, lines func(string)) (runner.Result, error) {
	args := []string{"-n", "mas", verb}
	for _, name := range names {
		n := id(name)
		if n == 0 {
			return runner.Result{}, notAnApp(name)
		}
		args = append(args, strconv.FormatInt(n, 10))
	}
	return s.run.Run(ctx, runner.Command{Name: "sudo", Args: args, Timeout: timeout, Lines: lines})
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
func (s *AppStore) Blocked(ctx context.Context, missing map[string]string, _ []kind.Installed) (map[string]kind.Block, error) {
	blocked := make(map[string]kind.Block)
	if s.admin == nil || len(missing) == 0 || s.admin(ctx) {
		return blocked, nil
	}
	for name := range missing {
		blocked[name] = kind.Block{Why: adminWait}
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
