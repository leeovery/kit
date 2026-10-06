package login_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/login"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

const items = `[{"name":"Dropbox","path":"/Applications/Dropbox.app","id":"com.getdropbox.dropbox"},` +
	`{"name":"Keyboard Maestro Engine","path":"/Applications/Keyboard Maestro.app/Contents/MacOS/Keyboard Maestro Engine.app","id":"com.stairways.keyboardmaestro.engine"},` +
	`{"name":"Gone Helper","path":"","id":""}]`

// osascript is osascript's arguments for script, given args.
func osascript(script string, args ...string) []string {
	return append([]string{"-l", "JavaScript", "-e", script}, args...)
}

func declared(names ...string) config.List {
	l := config.List{Kind: "login"}
	for _, n := range names {
		l.Entries = append(l.Entries, config.Entry{Name: n, File: "login"})
	}
	return l
}

func TestInstalled(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("osascript", osascript(login.ReadScript)...).Prints(items)
	got, err := login.New(fake, "/home").Installed(t.Context())
	want := []kind.Installed{{Name: "com.getdropbox.dropbox", Explicit: true}, {Name: "com.stairways.keyboardmaestro.engine", Explicit: true}, {Name: "Gone-Helper", Explicit: true}}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("Installed() = %+v, %v; want %+v", got, err, want)
	}
}

func TestWithoutTheAutomationPermission(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("osascript", osascript(login.ReadScript)...).Exits(1).PrintsToStderr("execution error: Not authorized to send Apple events to System Events. (-1743)")
	_, err := login.New(fake, "/home").Installed(t.Context())
	if err == nil || !strings.Contains(err.Error(), "kit needs the Automation permission for System Events") {
		t.Errorf("Installed() error = %v, want it to say what permission", err)
	}
}

// A declared login item whose app isn't installed is held up, saying so.
func TestCompare(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("osascript", osascript(login.ReadScript)...).Prints(items)
	fake.On("osascript", osascript(login.IdentifyScript, "com.example.notyet", "com.manytricks.moom")...).
		Prints(`[{"id":"","name":"","path":""},{"id":"com.manytricks.Moom","name":"Moom.app","path":"/Applications/Moom.app"}]`)
	got := kind.Compare(t.Context(), login.New(fake, "/home"), declared("Com.GetDropbox.Dropbox", "com.manytricks.moom", "com.example.notyet"))
	want := []check.Item{
		{ID: "login-item:com.example.notyet", Name: "com.example.notyet", State: kind.Missing, Detail: "not installed: install the app first"},
		{ID: "login-item:com.manytricks.moom", Name: "com.manytricks.moom", State: kind.Missing, Action: kind.Install},
		{ID: "login-item:Gone-Helper", Name: "Gone-Helper", State: kind.Extra},
		{ID: "login-item:com.stairways.keyboardmaestro.engine", Name: "com.stairways.keyboardmaestro.engine", State: kind.Extra},
	}
	if got.Summary != "3 declared, 1 installed" || !slices.Equal(got.Items, want) {
		t.Errorf("Compare() = %+v\nwant items %+v", got, want)
	}
}

func TestInstallAndRemove(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("osascript", osascript(login.IdentifyScript, "com.manytricks.Moom")...).
		Prints(`[{"id":"com.manytricks.Moom","name":"Moom.app","path":"/Applications/Moom.app"}]`)
	fake.On("osascript", osascript(login.AddScript, "/Applications/Moom.app")...)
	fake.On("osascript", osascript(login.ReadScript)...).Prints(items)
	fake.On("osascript", osascript(login.DeleteScript, "Keyboard Maestro Engine")...)
	l := login.New(fake, "/home")
	if err := l.Install(t.Context(), []string{"com.manytricks.Moom"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Remove(t.Context(), []string{"com.stairways.keyboardmaestro.engine"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Remove(t.Context(), []string{"com.example.nosuch"}); err == nil {
		t.Error("Remove() of an item there isn't = nil error")
	}
}

func TestDescribe(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("osascript", osascript(login.ReadScript)...).Prints(items)
	fake.On("osascript", osascript(login.IdentifyScript, "com.manytricks.Moom")...).
		Prints(`[{"id":"com.manytricks.Moom","name":"Moom.app","path":"/Applications/Moom.app"}]`)
	l := login.New(fake, "/home")
	for name, want := range map[string]string{"com.getdropbox.dropbox": "Dropbox", "com.manytricks.Moom": "Moom"} {
		if got := l.Describe(t.Context(), name); got != want {
			t.Errorf("Describe(%s) = %q, want %q", name, got, want)
		}
	}
}

// kit add login finds the app from its name, path or bundle id.
func TestFind(t *testing.T) {
	home := t.TempDir()
	app := filepath.Join(home, "Applications", "Tiny Tool.app")
	if err := os.MkdirAll(app, 0o700); err != nil {
		t.Fatal(err)
	}
	fake := runnertest.New(t)
	tiny := `[{"id":"com.example.tiny","name":"Tiny Tool.app","path":"` + app + `"}]`
	fake.On("osascript", osascript(login.IdentifyScript, app)...).Prints(tiny)
	fake.On("osascript", osascript(login.IdentifyScript, "com.example.tiny")...).Prints(tiny)
	l := login.New(fake, home)
	want := []kind.Found{{Name: "com.example.tiny", Label: "Tiny Tool (" + app + ")"}}
	for _, typed := range []string{"Tiny Tool", "~/Applications/Tiny Tool.app", app, "com.example.tiny"} {
		if got, err := l.Find(t.Context(), typed); err != nil || !slices.Equal(got, want) {
			t.Errorf("Find(%q) = %+v, %v; want %+v", typed, got, err, want)
		}
	}
	if _, err := l.Find(t.Context(), "Nothing Like It"); err == nil || !strings.Contains(err.Error(), "no app called Nothing Like It") {
		t.Errorf("Find() of no app = %v", err)
	}
}
