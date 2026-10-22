package macapp_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
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
// moved, so the app keeps their order.
func TestNotifyAndClear(t *testing.T) {
	app, fake := built(t)
	open := []string{"-g", "-n", app.Path(), "--args"}
	notify := append(append([]string(nil), open...), "notify", "--id", "card:sign-in", "--title", "Sign in", "--body", "Open the tool", "--activate", terminal, "--at", "1000")
	clear := append(append([]string(nil), open...), "clear", "--id", "card:sign-in", "--at", "1001")
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
	args := []string{"-g", "-n", app.Path(), "--args", "notify", "--id", "dialog", "--title", "macOS is asking", "--body", "Answer it", "--at", "1000"}
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
	fake.On("open", "-g", "-n", app.Path(), "--args", "notify", "--id", "dialog", "--title", "macOS is asking", "--body", "Answer it", "--activate", terminal, "--at", "1000").Exits(1)
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

// A wait for the build under way is given up once its context is done, as
// when the notification waiting has been taken away: that notification
// posts nothing, and the build goes on. Closing the app stops the build and
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
	app.Prepare()
	<-compiling

	ctx, stop := context.WithCancel(t.Context())
	stop()
	if app.Ready(ctx) {
		t.Error("Ready = true with the build under way")
	}
	app.Notify(ctx, "card:sign-in", "Sign in", "Open it")
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
// app ready or not.
func TestNotifyOnceItsContextIsDone(t *testing.T) {
	ctx, stop := context.WithCancel(t.Context())
	stop()
	app, fake := built(t)
	app.Notify(ctx, "dialog", "macOS is asking", "Answer it")
	macapp.New(fake, t.TempDir(), terminal, clock).Notify(ctx, "dialog", "macOS is asking", "Answer it")
	if calls := fake.Calls(); len(calls) > 0 {
		t.Errorf("ran %q, want nothing", calls)
	}
}
