// Package login is the kind of login items: the apps that open at login,
// as System Events keeps them. Each is named by its app's bundle id, as in
// com.getdropbox.dropbox, which stays put when an app is renamed or moved;
// a declaration's note carries the app's name. System Events is driven by
// small JavaScript for Automation scripts, through osascript, which needs
// the Automation permission for System Events.
package login

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/runner"
)

// The scripts kit runs, exported for tests to script osascript's answers.

// ReadScript prints the login items, each with its app's path and bundle
// id, as JSON.
const ReadScript = `ObjC.import('AppKit');
function run() {
  var se = Application('System Events');
  var names = se.loginItems.name();
  var paths = se.loginItems.path();
  return JSON.stringify(names.map(function (name, i) {
    var path = paths[i] || '';
    var bundle = path ? $.NSBundle.bundleWithPath(path) : null;
    var id = bundle && !bundle.isNil() ? ObjC.unwrap(bundle.bundleIdentifier) : '';
    return {name: name, path: path, id: id || ''};
  }));
}`

// IdentifyScript prints, for each argument (an app's path, or its bundle
// id), the app's bundle id, name and path, as JSON: empty for an app that
// isn't there.
const IdentifyScript = `ObjC.import('AppKit');
function run(argv) {
  var ws = $.NSWorkspace.sharedWorkspace;
  var fm = $.NSFileManager.defaultManager;
  return JSON.stringify(argv.map(function (wanted) {
    var path = '';
    if (wanted.charAt(0) === '/') {
      if (fm.fileExistsAtPath(wanted)) path = wanted;
    } else {
      var url = ws.URLForApplicationWithBundleIdentifier(wanted);
      if (!url.isNil()) path = ObjC.unwrap(url.path);
    }
    if (!path) return {id: '', name: '', path: ''};
    var bundle = $.NSBundle.bundleWithPath(path);
    var id = bundle.isNil() ? '' : ObjC.unwrap(bundle.bundleIdentifier) || '';
    return {id: id, name: ObjC.unwrap(fm.displayNameAtPath(path)), path: path};
  }));
}`

// AddScript adds a login item for the app at each path its arguments give.
const AddScript = `function run(argv) {
  var se = Application('System Events');
  argv.forEach(function (path) {
    se.loginItems.push(se.LoginItem({path: path, hidden: false}));
  });
  return '';
}`

// DeleteScript deletes the login items its arguments name.
const DeleteScript = `function run(argv) {
  var se = Application('System Events');
  argv.forEach(function (name) {
    se.loginItems.byName(name).delete();
  });
  return '';
}`

// notInstalled is why a login item can't be added yet.
const notInstalled = "not installed: install the app first"

// Login is the login items, driven through a runner.
type Login struct {
	run runner.Runner
	// home is the user's home, where ~/Applications is.
	home string
}

// New returns the login items, driven through run, for the user whose home
// is home.
func New(run runner.Runner, home string) *Login {
	return &Login{run: run, home: home}
}

func (*Login) Name() string    { return "login-item" }
func (*Login) Title() string   { return "Login items" }
func (*Login) Program() string { return "osascript" }

// item is a login item, or an app, as the scripts tell of it.
type item struct {
	Name string `json:"name"`
	Path string `json:"path"`
	ID   string `json:"id"`
}

// named is what kit calls a login item: its app's bundle id, or, when it
// hasn't one, its name, made a name a list can hold.
func (it item) named() string {
	if it.ID != "" {
		return it.ID
	}
	return strings.Join(strings.Fields(it.Name), "-")
}

// script runs a script with args, reading the JSON it prints into into.
func (l *Login) script(ctx context.Context, script string, into any, args ...string) error {
	res, err := l.run.Run(ctx, runner.Command{Name: "osascript", Args: slices.Concat([]string{"-l", "JavaScript", "-e", script}, args)})
	if exit, ok := errors.AsType[*runner.ExitError](err); ok && (strings.Contains(exit.Stderr, "-1743") || strings.Contains(exit.Stderr, "Not authorized")) {
		return errors.New("kit needs the Automation permission for System Events: System Settings › Privacy & Security › Automation, for the terminal kit runs in")
	}
	if err != nil || into == nil {
		return err
	}
	if err := json.Unmarshal(res.Stdout, into); err != nil {
		return fmt.Errorf("read System Events' answer: %w", err)
	}
	return nil
}

func (l *Login) items(ctx context.Context) ([]item, error) {
	var items []item
	err := l.script(ctx, ReadScript, &items)
	return items, err
}

// identify finds the apps wanted names, by path or bundle id, in order.
func (l *Login) identify(ctx context.Context, wanted []string) ([]item, error) {
	if len(wanted) == 0 {
		return nil, nil
	}
	var apps []item
	if err := l.script(ctx, IdentifyScript, &apps, wanted...); err != nil {
		return nil, err
	}
	if len(apps) != len(wanted) {
		return nil, fmt.Errorf("asked of %d apps, System Events told of %d", len(wanted), len(apps))
	}
	for i := range apps {
		apps[i].Name = strings.TrimSuffix(apps[i].Name, ".app")
	}
	return apps, nil
}

// Installed lists the login items, each installed for itself.
func (l *Login) Installed(ctx context.Context) ([]kind.Installed, error) {
	items, err := l.items(ctx)
	if err != nil {
		return nil, err
	}
	installed := make([]kind.Installed, len(items))
	for i, it := range items {
		installed[i] = kind.Installed{Name: it.named(), Explicit: true}
	}
	return installed, nil
}

// Key is a bundle id, in lowercase, as macOS matches them.
func (*Login) Key(name string) string {
	return strings.ToLower(name)
}

// Resolve takes every name as a bundle id: one no app has is held up.
func (*Login) Resolve(_ context.Context, names []string) (map[string]string, error) {
	resolved := make(map[string]string, len(names))
	for _, name := range names {
		resolved[name] = name
	}
	return resolved, nil
}

// Blocked holds up the login items whose apps aren't installed.
func (l *Login) Blocked(ctx context.Context, missing map[string]string, _ []kind.Installed) (map[string]string, error) {
	names := make([]string, 0, len(missing))
	for name := range missing {
		names = append(names, name)
	}
	slices.Sort(names)
	apps, err := l.identify(ctx, names)
	if err != nil {
		return nil, err
	}
	blocked := make(map[string]string)
	for i, app := range apps {
		if app.Path == "" {
			blocked[names[i]] = notInstalled
		}
	}
	return blocked, nil
}

// Install adds a login item for the app of each bundle id.
func (l *Login) Install(ctx context.Context, names []string) error {
	apps, err := l.identify(ctx, names)
	if err != nil {
		return err
	}
	paths := make([]string, len(apps))
	for i, app := range apps {
		if app.Path == "" {
			return fmt.Errorf("%s: %s", names[i], notInstalled)
		}
		paths[i] = app.Path
	}
	return l.script(ctx, AddScript, nil, paths...)
}

// Remove deletes the login items of the apps named.
func (l *Login) Remove(ctx context.Context, names []string) error {
	items, err := l.items(ctx)
	if err != nil {
		return err
	}
	var toDelete []string
	for _, name := range names {
		i := slices.IndexFunc(items, func(it item) bool { return l.Key(it.named()) == l.Key(name) })
		if i < 0 {
			return fmt.Errorf("no login item for %s", name)
		}
		toDelete = append(toDelete, items[i].Name)
	}
	return l.script(ctx, DeleteScript, nil, toDelete...)
}

// Describe is the name of the app of the bundle id name, for the note
// beside it: "" when it can't be found.
func (l *Login) Describe(ctx context.Context, name string) string {
	if items, err := l.items(ctx); err == nil {
		for _, it := range items {
			if l.Key(it.named()) == l.Key(name) {
				return it.Name
			}
		}
	}
	if apps, err := l.identify(ctx, []string{name}); err == nil && apps[0].Name != "" {
		return apps[0].Name
	}
	return ""
}

// Find finds the app typed means: a path to it, its bundle id, or its name,
// looked for in the folders apps live in.
func (l *Login) Find(ctx context.Context, typed string) ([]kind.Found, error) {
	wanted := typed
	switch {
	case strings.HasPrefix(typed, "~/"):
		wanted = filepath.Join(l.home, typed[2:])
	case strings.HasPrefix(typed, "/"):
	case strings.Contains(typed, ".") && !strings.ContainsAny(typed, " /"):
	default:
		wanted = ""
		for _, dir := range []string{"/Applications", "/Applications/Utilities", filepath.Join(l.home, "Applications"), "/System/Applications"} {
			if path := filepath.Join(dir, typed+".app"); exists(path) {
				wanted = path
				break
			}
		}
		if wanted == "" {
			return nil, fmt.Errorf("no app called %s in /Applications or ~/Applications: give its path or bundle id", typed)
		}
	}
	apps, err := l.identify(ctx, []string{wanted})
	if err != nil {
		return nil, err
	}
	if apps[0].ID == "" {
		return nil, fmt.Errorf("no app at %s", typed)
	}
	return []kind.Found{{Name: apps[0].ID, Label: fmt.Sprintf("%s (%s)", apps[0].Name, apps[0].Path)}}, nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
