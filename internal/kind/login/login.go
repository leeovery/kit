// Package login is the kind of login items: the apps that open at login,
// as System Events keeps them. Each is named by its app's bundle id, as in
// com.getdropbox.dropbox, which stays put when an app is renamed or moved;
// a declaration's note carries the app's name. System Events is driven by
// small JavaScript for Automation scripts, through osascript, which needs
// macOS's permission for the terminal to control System Events, asked for
// the first time it does.
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

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/hold"
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

// notInstalled is why a login item can't be added yet: its app isn't
// installed, or, a helper an app puts in place when it's first opened, as
// Figma's agent, that app hasn't been opened.
const notInstalled = "not installed: install the app, or open the app it comes with"

// helperDirs are the folders inside an app where the apps that come with
// it live, as a helper that opens at login: Keyboard Maestro's engine, in
// Contents/MacOS.
var helperDirs = []string{"Contents/MacOS", "Contents/Library/LoginItems", "Contents/Helpers"}

// Login is the login items, driven through a runner.
type Login struct {
	run runner.Runner
	// home is the user's home, where ~/Applications is.
	home string
	// AppDirs are the folders apps live in, where an app LaunchServices
	// doesn't know is looked for: /Applications, its Utilities, and
	// ~/Applications.
	AppDirs []string
}

// kindName names the kind.
const kindName = "login-item"

// Descriptor is login items' kind, as kit's shared code knows it.
var Descriptor = kind.Descriptor{
	Name:    kindName,
	Label:   "macOS login items",
	Group:   kind.Packages,
	Section: &config.Section{Kind: kindName, Header: "macos login items", Form: config.Names, Sorted: true, Held: true},
	Help: &kind.Help{
		What:   "macOS login items",
		Add:    "Add login items, by app, and declare them",
		Remove: "Remove login items, and undeclare them",
	},
	Row: "Login items",
	// Login items open the apps the casks and the App Store install.
	After: []string{"cask", "mas"},
	New:   func(d *kind.Deps) kind.Kind { return New(d.Run, d.Home) },
}

// New returns the login items, driven through run, for the user whose home
// is home.
func New(run runner.Runner, home string) *Login {
	return &Login{run: run, home: home, AppDirs: []string{"/Applications", "/Applications/Utilities", filepath.Join(home, "Applications")}}
}

func (*Login) Name() string    { return kindName }
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

// notAllowed is the error for System Events not answering kit, as macOS
// hasn't let the terminal kit runs in control it.
var notAllowed = &kind.NotAllowed{
	What:  "macOS hasn't let the terminal control System Events",
	Where: "System Settings › Privacy & Security › Automation",
}

// script runs a script with args, reading the JSON it prints into into.
func (l *Login) script(ctx context.Context, script string, into any, args ...string) error {
	res, err := l.osascript(ctx, slices.Concat([]string{"-l", "JavaScript", "-e", script}, args))
	if err != nil || into == nil {
		return err
	}
	if err := json.Unmarshal(res.Stdout, into); err != nil {
		return fmt.Errorf("read System Events' answer: %w", err)
	}
	return nil
}

// osascript runs osascript with args. The first time the terminal kit runs
// in drives System Events, macOS asks the person at the Mac whether it may,
// and System Events doesn't answer till they have: osascript gives up
// waiting after two minutes, so, when macOS was asking meanwhile, it's run
// again, and again while the question's up. Refused, or never answered,
// macOS hasn't let kit control System Events (notAllowed).
func (l *Login) osascript(ctx context.Context, args []string) (runner.Result, error) {
	for {
		var res runner.Result
		var err error
		asked := hold.During(ctx, func() { res, err = l.run.Run(ctx, runner.Command{Name: "osascript", Args: args}) })
		exit, exited := errors.AsType[*runner.ExitError](err)
		if !exited {
			return res, err
		}
		// osascript says why on its execution error line.
		said := strings.Join(slices.DeleteFunc(exit.Lines(), func(line string) bool { return !strings.Contains(line, "execution error:") }), "\n")
		switch {
		case strings.Contains(said, "(-1743)") || strings.Contains(said, "Not authorized"):
			return res, notAllowed
		case !strings.Contains(said, "(-1712)"):
			return res, err
		case !asked || ctx.Err() != nil:
			return res, notAllowed
		}
	}
}

func (l *Login) items(ctx context.Context) ([]item, error) {
	var items []item
	err := l.script(ctx, ReadScript, &items)
	return items, err
}

// identify finds the apps wanted names, by path or bundle id, in order. A
// bundle id LaunchServices doesn't know, as it doesn't an app installed a
// moment ago, or one inside another app till that's been opened, is looked
// for in the folders apps live in (lookFor).
func (l *Login) identify(ctx context.Context, wanted []string) ([]item, error) {
	apps, err := l.ask(ctx, wanted)
	if err != nil {
		return nil, err
	}
	var missed []int
	for i, app := range apps {
		if app.Path == "" && !strings.HasPrefix(wanted[i], "/") {
			missed = append(missed, i)
		}
	}
	if len(missed) == 0 {
		return apps, nil
	}
	found, err := l.lookFor(ctx)
	if err != nil {
		return nil, err
	}
	for _, i := range missed {
		if app, ok := found[l.Key(wanted[i])]; ok {
			apps[i] = app
		}
	}
	return apps, nil
}

// lookFor finds the apps in AppDirs, and the apps inside each that come
// with it (helperDirs), by bundle id, as Key has it: each read from its
// bundle, in one run of the script, the first of an id found kept.
func (l *Login) lookFor(ctx context.Context) (map[string]item, error) {
	var paths []string
	for _, dir := range l.AppDirs {
		for _, app := range appsIn(dir) {
			paths = append(paths, app)
			for _, sub := range helperDirs {
				paths = append(paths, appsIn(filepath.Join(app, sub))...)
			}
		}
	}
	apps, err := l.ask(ctx, paths)
	if err != nil {
		return nil, err
	}
	found := make(map[string]item, len(apps))
	for _, app := range apps {
		if _, seen := found[l.Key(app.ID)]; app.ID != "" && !seen {
			found[l.Key(app.ID)] = app
		}
	}
	return found, nil
}

// appsIn are the apps in dir, by path: none when it can't be read.
func appsIn(dir string) []string {
	entries, _ := os.ReadDir(dir)
	var apps []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".app") {
			apps = append(apps, filepath.Join(dir, e.Name()))
		}
	}
	return apps
}

// ask asks the script of the apps wanted names, by path or bundle id, in
// order: LaunchServices finds an app by its id.
func (l *Login) ask(ctx context.Context, wanted []string) ([]item, error) {
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
		for _, dir := range append(slices.Clone(l.AppDirs), "/System/Applications") {
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
