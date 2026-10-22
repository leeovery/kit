package macapp_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/macapp"
	"github.com/leeovery/kit/internal/plist"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

const terminal = "com.example.terminal"

// clock is the time a test's app is told: its notifications and their
// taking away are sent from 1000 on, in nanoseconds, each after the last.
func clock() time.Time { return time.Unix(0, 1000) }

// building scripts fake to build the app in dir as kit does: the command
// line tools found, its executable compiled, and the bundle signed. It
// returns the build's commands, in order.
func building(t *testing.T, fake *runnertest.Fake, dir string) []string {
	t.Helper()
	work := filepath.Join(dir, ".kit.app-build")
	exe := filepath.Join(work, "kit.app", "Contents", "MacOS", "kit")
	src := filepath.Join(work, "kit.swift")
	fake.On("xcode-select", "-p").Prints("/Library/Developer/CommandLineTools\n")
	fake.On("swiftc", "-O", "-o", exe, src).Does(func() {
		if err := os.WriteFile(exe, []byte("binary"), 0o755); err != nil {
			t.Error(err)
		}
	})
	fake.On("codesign", "-s", "-", "-f", filepath.Join(work, "kit.app"))
	return []string{
		"xcode-select -p",
		runner.Command{Name: "swiftc", Args: []string{"-O", "-o", exe, src}}.String(),
		runner.Command{Name: "codesign", Args: []string{"-s", "-", "-f", filepath.Join(work, "kit.app")}}.String(),
	}
}

// built returns the app in a new data directory, built.
func built(t *testing.T) (app *macapp.App, fake *runnertest.Fake) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "kit")
	if !macapp.New(runFor(t, dir), dir, terminal, clock).Ready(t.Context()) {
		t.Fatal("the app wasn't built")
	}
	fake = runnertest.New(t)
	return macapp.New(fake, dir, terminal, clock), fake
}

// runFor is a runner that builds the app in dir.
func runFor(t *testing.T, dir string) *runnertest.Fake {
	fake := runnertest.New(t)
	building(t, fake, dir)
	return fake
}

// Built, the bundle's kit's: its name, bundle identifier and icon, no Dock
// icon, notifications as Alerts; compiled and signed in a folder aside, then
// put in place in the data directory.
func TestTheAppIsBuilt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "kit")
	fake := runnertest.New(t)
	want := building(t, fake, dir)
	app := macapp.New(fake, dir, terminal, clock)

	if !app.Ready(t.Context()) {
		t.Fatalf("Ready = false, ran %q", fake.Calls())
	}
	if got := fake.Calls(); !reflect.DeepEqual(got, want) {
		t.Errorf("ran %q, want %q", got, want)
	}
	if app.Path() != filepath.Join(dir, "kit.app") {
		t.Errorf("Path = %s, want kit.app in the data directory", app.Path())
	}
	data, err := os.ReadFile(filepath.Join(app.Path(), "Contents", "Info.plist"))
	if err != nil {
		t.Fatal(err)
	}
	info, err := plist.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]any{
		"CFBundleIdentifier": macapp.BundleID, "CFBundleName": "kit", "CFBundleDisplayName": "kit",
		"CFBundleExecutable": "kit", "CFBundleIconFile": "AppIcon", "CFBundlePackageType": "APPL",
		"LSUIElement": true, "NSUserNotificationAlertStyle": "alert",
	}
	for key, value := range keys {
		if got := info.(map[string]any)[key]; got != value {
			t.Errorf("Info.plist's %s = %v, want %v", key, got, value)
		}
	}
	embedded, err := os.ReadFile("AppIcon.icns")
	if err != nil {
		t.Fatal(err)
	}
	if icon, err := os.ReadFile(filepath.Join(app.Path(), "Contents", "Resources", "AppIcon.icns")); err != nil || !bytes.Equal(icon, embedded) {
		t.Errorf("the bundle's icon (%v) isn't the icon kit embeds", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".kit.app-build")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the folder it was built in is still there (%v)", err)
	}
}

// Built from the files kit embeds, the app isn't built again: no other run
// builds it, nor asks whether it can.
func TestTheAppBuiltIsKept(t *testing.T) {
	app, fake := built(t)
	if !app.Ready(t.Context()) || len(fake.Calls()) > 0 {
		t.Errorf("ran %q, want nothing: the app's built", fake.Calls())
	}
}

// An app built from other files, as an older kit's, or one cut short, is
// built again.
func TestTheAppIsRebuilt(t *testing.T) {
	for name, change := range map[string]func(bundle string) error{
		"built from other files": func(bundle string) error {
			return os.WriteFile(filepath.Join(bundle, "Contents", "Resources", "built-from"), []byte("other\n"), 0o644)
		},
		"its executable gone": func(bundle string) error {
			return os.Remove(filepath.Join(bundle, "Contents", "MacOS", "kit"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			app, _ := built(t)
			if err := change(app.Path()); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Dir(app.Path())
			fake := runnertest.New(t)
			want := building(t, fake, dir)
			if !macapp.New(fake, dir, terminal, clock).Ready(t.Context()) || !reflect.DeepEqual(fake.Calls(), want) {
				t.Errorf("ran %q, want it built again: %q", fake.Calls(), want)
			}
		})
	}
}

// A build that fails isn't tried again in the run: the notification goes
// through osascript, as do those after it.
func TestABuildIsTriedOnce(t *testing.T) {
	dir := t.TempDir()
	fake := runnertest.New(t)
	building(t, fake, dir)
	fake.On("swiftc", "-O", "-o", filepath.Join(dir, ".kit.app-build", "kit.app", "Contents", "MacOS", "kit"), filepath.Join(dir, ".kit.app-build", "kit.swift")).Exits(1)
	fake.On("osascript", "-e", `display notification "Open it" with title "kit" subtitle "Sign in"`)
	app := macapp.New(fake, dir, terminal, clock)

	app.Notify(t.Context(), "card:sign-in", "Sign in", "Open it")
	app.Notify(t.Context(), "card:sign-in", "Sign in", "Open it")
	want := []string{
		"xcode-select -p",
		runner.Command{Name: "swiftc", Args: []string{"-O", "-o", filepath.Join(dir, ".kit.app-build", "kit.app", "Contents", "MacOS", "kit"), filepath.Join(dir, ".kit.app-build", "kit.swift")}}.String(),
		`osascript -e 'display notification "Open it" with title "kit" subtitle "Sign in"'`,
		`osascript -e 'display notification "Open it" with title "kit" subtitle "Sign in"'`,
	}
	if got := fake.Calls(); !reflect.DeepEqual(got, want) {
		t.Errorf("ran %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "kit.app")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a bundle that failed to build was put in place (%v)", err)
	}
}

// Without Swift's compiler, or the command line tools it needs, nothing's
// built: the notification goes through osascript, and the next looks again,
// as they may be installed meanwhile.
func TestWithoutSwift(t *testing.T) {
	script := `display notification "Answer it in the window macOS shows" with title "kit" subtitle "macOS is asking"`
	t.Run("no swiftc", func(t *testing.T) {
		fake := runnertest.New(t)
		fake.On("osascript", "-e", script)
		macapp.New(fake, t.TempDir(), terminal, clock).Notify(t.Context(), "dialog", "macOS is asking", "Answer it in the window macOS shows")
		if got := fake.Calls(); !reflect.DeepEqual(got, []string{"osascript -e '" + script + "'"}) {
			t.Errorf("ran %q, want osascript alone", got)
		}
	})
	t.Run("no command line tools", func(t *testing.T) {
		fake := runnertest.New(t)
		// Scripted, swiftc's there, as the fake has it.
		fake.On("swiftc", "--version")
		fake.On("xcode-select", "-p").Exits(2)
		fake.On("osascript", "-e", script)
		app := macapp.New(fake, t.TempDir(), terminal, clock)
		app.Notify(t.Context(), "dialog", "macOS is asking", "Answer it in the window macOS shows")
		app.Notify(t.Context(), "dialog", "macOS is asking", "Answer it in the window macOS shows")
		want := []string{"xcode-select -p", "osascript -e '" + script + "'", "xcode-select -p", "osascript -e '" + script + "'"}
		if got := fake.Calls(); !reflect.DeepEqual(got, want) {
			t.Errorf("ran %q, want %q", got, want)
		}
	})
}

// Through the app, a notification's posted by launching it as macOS
// launches apps, a new one in the background, with its id, title and body,
// and the app a click brings forward; clearing takes it away by its id.
// Each says when it was sent, each after the last though the clock hasn't
// moved, and by which run, the first to send any here, so the app keeps
// their order.
func TestNotifyAndClear(t *testing.T) {
	app, fake := built(t)
	open := []string{"-g", "-n", app.Path(), "--args"}
	notify := append(append([]string(nil), open...), "notify", "--id", "card:sign-in", "--title", "Sign in", "--body", "Open the tool", "--activate", terminal, "--at", "1000", "--run", "1")
	clear := append(append([]string(nil), open...), "clear", "--id", "card:sign-in", "--at", "1001", "--run", "1")
	fake.On("open", notify...)
	fake.On("open", clear...)

	app.Notify(t.Context(), "card:sign-in", "Sign in", "Open the tool")
	app.Clear(t.Context(), "card:sign-in")
	want := []string{runner.Command{Name: "open", Args: notify}.String(), runner.Command{Name: "open", Args: clear}.String()}
	if got := fake.Calls(); !reflect.DeepEqual(got, want) {
		t.Errorf("ran %q, want %q", got, want)
	}
}

// With no app to bring forward, as outside a terminal macOS names, the
// notification says none.
func TestNotifyWithNothingToActivate(t *testing.T) {
	app, _ := built(t)
	fake := runnertest.New(t)
	app = macapp.New(fake, filepath.Dir(app.Path()), "", clock)
	args := []string{"-g", "-n", app.Path(), "--args", "notify", "--id", "dialog", "--title", "macOS is asking", "--body", "Answer it", "--at", "1000", "--run", "1"}
	fake.On("open", args...)
	app.Notify(t.Context(), "dialog", "macOS is asking", "Answer it")
	if got, want := fake.Calls(), []string{runner.Command{Name: "open", Args: args}.String()}; !reflect.DeepEqual(got, want) {
		t.Errorf("ran %q, want %q", got, want)
	}
}

// An app that won't open loses nothing: the notification goes through
// osascript.
func TestAnAppThatWontOpen(t *testing.T) {
	app, fake := built(t)
	fake.On("open", "-g", "-n", app.Path(), "--args", "notify", "--id", "dialog", "--title", "macOS is asking", "--body", "Answer it", "--activate", terminal, "--at", "1000", "--run", "1").Exits(1)
	fake.On("osascript", "-e", `display notification "Answer it" with title "kit" subtitle "macOS is asking"`)
	app.Notify(t.Context(), "dialog", "macOS is asking", "Answer it")
	if calls := fake.Calls(); len(calls) != 2 || calls[1] != `osascript -e 'display notification "Answer it" with title "kit" subtitle "macOS is asking"'` {
		t.Errorf("ran %q, want osascript after the app wouldn't open", calls)
	}
}

// Nothing the app didn't post is cleared through it: osascript's can't be,
// and nothing's built to try.
func TestClearWithoutTheApp(t *testing.T) {
	fake := runnertest.New(t)
	macapp.New(fake, t.TempDir(), terminal, clock).Clear(t.Context(), "dialog")
	if calls := fake.Calls(); len(calls) > 0 {
		t.Errorf("ran %q, want nothing", calls)
	}
}

// osascript's notification is quoted: a quote or backslash in what it says
// stays in it.
func TestOsascriptQuotes(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("osascript", "-e", `display notification "a \"b\" \\ c" with title "kit" subtitle "Sign in"`)
	macapp.New(fake, t.TempDir(), terminal, clock).Notify(t.Context(), "x", "Sign in", `a "b" \ c`)
	if calls := fake.Calls(); len(calls) != 1 {
		t.Errorf("ran %q, want osascript once", calls)
	}
}

// swiftcIn is the compile of the app in dir, as building scripts it.
func swiftcIn(dir string) []string {
	work := filepath.Join(dir, ".kit.app-build")
	return []string{"-O", "-o", filepath.Join(work, "kit.app", "Contents", "MacOS", "kit"), filepath.Join(work, "kit.swift")}
}

// Prepare starts the build aside, returning at once; Ready waits for it.
func TestPrepareBuildsAside(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "kit")
	fake := runnertest.New(t)
	want := building(t, fake, dir)
	release := make(chan struct{})
	exe := filepath.Join(dir, ".kit.app-build", "kit.app", "Contents", "MacOS", "kit")
	fake.On("swiftc", swiftcIn(dir)...).Does(func() {
		<-release
		if err := os.WriteFile(exe, []byte("binary"), 0o755); err != nil {
			t.Error(err)
		}
	})
	app := macapp.New(fake, dir, terminal, clock)

	app.Prepare()
	close(release)
	if !app.Ready(t.Context()) || !reflect.DeepEqual(fake.Calls(), want) {
		t.Errorf("ran %q, want the app built once: %q", fake.Calls(), want)
	}
}

// Prepared as a run starts, the app numbers the run, after the last run to
// send notifications, so that it keeps each run's after the last's: the
// order of the last run's kept, and the lock it's kept under beside it,
// never in the folder, as the app's instances can still be using both.
// Each run's number is taken once, as it starts or as it first sends one.
func TestPrepareNumbersTheRun(t *testing.T) {
	app, _ := built(t)
	dir := filepath.Dir(app.Path())
	mark := filepath.Join(dir, "notices", "0a1b")
	if err := os.MkdirAll(filepath.Dir(mark), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mark, []byte("1000 clear"), 0o644); err != nil {
		t.Fatal(err)
	}
	app.Prepare()
	app.Prepare()
	if run := read(t, filepath.Join(dir, "notices", "run")); run != "1\n" {
		t.Errorf("the run's number is %q, want 1, the first", run)
	}
	if got := read(t, mark); got != "1000 clear" {
		t.Errorf("the last run's order is %q, want it kept", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "notices.lock")); err != nil {
		t.Errorf("no lock beside the order kept: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "notices", ".lock")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a lock in the folder (%v), want it beside", err)
	}

	fake := runnertest.New(t)
	next := macapp.New(fake, dir, terminal, clock)
	args := []string{"-g", "-n", app.Path(), "--args", "clear", "--id", "dialog", "--at", "1000", "--run", "2"}
	fake.On("open", args...)
	next.Prepare()
	next.Clear(t.Context(), "dialog")
	if got := fake.Calls(); !reflect.DeepEqual(got, []string{runner.Command{Name: "open", Args: args}.String()}) {
		t.Errorf("the next run ran %q, want its clear sent as run 2", got)
	}
}

// Closed, a run numbered is kept as ended, after those before it, the last
// 32 of them, so that the app posts nothing it sent that's still to post:
// what it sends after says its run all the same. A run never numbered
// isn't, nor numbered once it's closed.
func TestCloseKeepsTheRunEnded(t *testing.T) {
	app, fake := built(t)
	dir := filepath.Dir(app.Path())
	ended := filepath.Join(dir, "notices", "ended")
	var before []string
	for n := range 32 {
		before = append(before, strconv.Itoa(1000+n))
	}
	if err := os.MkdirAll(filepath.Dir(ended), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ended, []byte(strings.Join(before, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	app.Prepare()
	app.Close()
	if got, want := read(t, ended), strings.Join(append(before[1:], "1"), "\n")+"\n"; got != want {
		t.Errorf("runs ended = %q, want %q", got, want)
	}
	args := []string{"-g", "-n", app.Path(), "--args", "clear", "--id", "dialog", "--at", "1000", "--run", "1"}
	fake.On("open", args...)
	app.Clear(t.Context(), "dialog")
	if got := fake.Calls(); !reflect.DeepEqual(got, []string{runner.Command{Name: "open", Args: args}.String()}) {
		t.Errorf("ran %q once closed, want its clear sent as run 1", got)
	}

	fake = runnertest.New(t)
	never := macapp.New(fake, dir, terminal, clock)
	if !never.Ready(t.Context()) {
		t.Fatal("the app built isn't ready")
	}
	never.Close()
	args = []string{"-g", "-n", app.Path(), "--args", "clear", "--id", "dialog", "--at", "1000"}
	fake.On("open", args...)
	never.Clear(t.Context(), "dialog")
	if got := read(t, filepath.Join(dir, "notices", "run")); got != "1\n" {
		t.Errorf("the last run's number is %q once closed unnumbered, want 1 still", got)
	}
	if got := fake.Calls(); !reflect.DeepEqual(got, []string{runner.Command{Name: "open", Args: args}.String()}) {
		t.Errorf("ran %q, want a clear saying no run", got)
	}
}

// With the lock held long, as by an instance of the app while macOS posts a
// notification, a run starting doesn't wait on it: it goes unnumbered, and
// what it sends meanwhile says no run, the app keeping its order by time
// alone; once the lock's let go, the next it sends numbers it. Its end,
// numbered, is kept with the lock held all the same.
func TestTheLockHeldLong(t *testing.T) {
	app, fake := built(t)
	dir := filepath.Dir(app.Path())
	held := lockHeld(t, dir)
	app.Prepare()
	unnumbered := []string{"-g", "-n", app.Path(), "--args", "clear", "--id", "dialog", "--at", "1000"}
	numbered := []string{"-g", "-n", app.Path(), "--args", "clear", "--id", "dialog", "--at", "1001", "--run", "1"}
	fake.On("open", unnumbered...)
	fake.On("open", numbered...)
	app.Clear(t.Context(), "dialog")
	held()
	app.Clear(t.Context(), "dialog")
	want := []string{runner.Command{Name: "open", Args: unnumbered}.String(), runner.Command{Name: "open", Args: numbered}.String()}
	if got := fake.Calls(); !reflect.DeepEqual(got, want) {
		t.Errorf("ran %q, want a clear saying no run, then, the lock let go, one saying run 1", got)
	}

	held = lockHeld(t, dir)
	defer held()
	app.Close()
	if got := read(t, filepath.Join(dir, "notices", "ended")); got != "1\n" {
		t.Errorf("runs ended = %q, want run 1's end kept without the lock", got)
	}
}

// lockHeld holds the lock on the app's marks in dir, as an instance of the
// app would, till what it returns is called.
func lockHeld(t *testing.T, dir string) (release func()) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, "notices.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	return func() { _ = f.Close() }
}

// read is the file at path, failing the test should it not read.
func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// A notification waiting for the build under way gives up its wait once its
// context is done, as when it's been taken away: it posts nothing, through
// the app or osascript, and the build goes on. Closing the app stops the build and
// waits till it has; what's posted after goes through osascript, nothing
// built again.
func TestAWaitForTheBuildIsGivenUp(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "kit")
	fake := runnertest.New(t)
	building(t, fake, dir)
	compiling := make(chan struct{})
	fake.On("swiftc", swiftcIn(dir)...).Does(func() { close(compiling) }).Hangs()
	script := `display notification "Open it" with title "kit" subtitle "Sign in"`
	fake.On("osascript", "-e", script)
	app := macapp.New(fake, dir, terminal, clock)
	ctx, stop := context.WithCancel(t.Context())
	notified := make(chan struct{})
	go func() {
		defer close(notified)
		app.Notify(ctx, "card:sign-in", "Sign in", "Open it")
	}()
	<-compiling
	stop()
	<-notified
	if app.Ready(ctx) {
		t.Error("Ready = true with the build under way")
	}
	compiled := []string{"xcode-select -p", runner.Command{Name: "swiftc", Args: swiftcIn(dir)}.String()}
	if got := fake.Calls(); !reflect.DeepEqual(got, compiled) {
		t.Errorf("ran %q, want the build alone: nothing posted once its wait was given up", got)
	}

	app.Close()
	app.Notify(t.Context(), "card:sign-in", "Sign in", "Open it")
	if got, want := fake.Calls(), append(compiled, "osascript -e '"+script+"'"); !reflect.DeepEqual(got, want) {
		t.Errorf("ran %q, want %q", got, want)
	}
	if _, err := os.Stat(app.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a build stopped was put in place (%v)", err)
	}
}

// Taken away before it could be posted, a notification posts nothing, the
// app ready or not, and starts no build, nor asks whether one can be.
func TestNotifyOnceItsContextIsDone(t *testing.T) {
	ctx, stop := context.WithCancel(t.Context())
	stop()
	app, fake := built(t)
	app.Notify(ctx, "dialog", "macOS is asking", "Answer it")
	dir := filepath.Join(t.TempDir(), "kit")
	building(t, fake, dir)
	macapp.New(fake, dir, terminal, clock).Notify(ctx, "dialog", "macOS is asking", "Answer it")
	if calls := fake.Calls(); len(calls) > 0 {
		t.Errorf("ran %q, want nothing", calls)
	}
}

// The app's compile has ten minutes, as a slow Mac's can take more than the
// runner's usual two.
func TestTheCompileHasTenMinutes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "kit")
	fake := runnertest.New(t)
	building(t, fake, dir)
	if !macapp.New(fake, dir, terminal, clock).Ready(t.Context()) {
		t.Fatal("the app wasn't built")
	}
	for _, c := range fake.Commands() {
		if c.Name == "swiftc" && c.Timeout != 10*time.Minute {
			t.Errorf("swiftc's time limit = %s, want 10m", c.Timeout)
		}
	}
}
