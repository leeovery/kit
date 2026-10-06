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

	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/runner"
)

// installTimeout is how long an install may take: Xcode is gigabytes.
const installTimeout = 90 * time.Minute

// adminWait is why an install waits, without an administrator's password.
const adminWait = "needs an administrator's password: run kit apply at a terminal"

// most is how many of a search's apps are offered.
const most = 8

// AppStore is the App Store, driven through a runner.
type AppStore struct {
	run   runner.Runner
	admin func(ctx context.Context) bool
}

// New returns the App Store, driven through run.
func New(run runner.Runner) *AppStore {
	return &AppStore{run: run}
}

func (*AppStore) Name() string    { return "mas" }
func (*AppStore) Title() string   { return "App Store" }
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

// Install installs the apps names name, through sudo: mas needs root to
// install.
func (s *AppStore) Install(ctx context.Context, names []string) error {
	return s.sudo(ctx, "install", names, installTimeout)
}

// Remove uninstalls the apps names name, through sudo, as mas needs.
func (s *AppStore) Remove(ctx context.Context, names []string) error {
	return s.sudo(ctx, "uninstall", names, 0)
}

func (s *AppStore) sudo(ctx context.Context, verb string, names []string, timeout time.Duration) error {
	args := []string{"-n", "mas", verb}
	for _, name := range names {
		n := id(name)
		if n == 0 {
			return fmt.Errorf("%s isn't an App Store app's name: its name, then @ and its id", name)
		}
		args = append(args, strconv.FormatInt(n, 10))
	}
	_, err := s.run.Run(ctx, runner.Command{Name: "sudo", Args: args, Timeout: timeout})
	return err
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
