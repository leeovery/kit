package steps_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/nightly"
	"github.com/leeovery/kit/internal/runner/runnertest"
	"github.com/leeovery/kit/internal/steps"
)

// kitApp stands in for kit.app: built from this kit's files or not, and
// whether it can be built.
type kitApp struct {
	path       string
	current    bool
	buildable  bool
	readyCalls int
}

func (a *kitApp) Path() string  { return a.path }
func (a *kitApp) Current() bool { return a.current }
func (a *kitApp) Ready(context.Context) bool {
	a.readyCalls++
	if a.buildable {
		a.current = true
	}
	return a.current
}

// launchAt is when the tests' launch is loaded.
var launchAt = time.Date(2026, 11, 12, 14, 30, 0, 0, time.UTC)

// homeWith is a Mac's home of a test's own, with a kit in it at each of kits,
// from the home.
func homeWith(t *testing.T, kits ...string) string {
	t.Helper()
	home := t.TempDir()
	for _, k := range kits {
		path := filepath.Join(home, k)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

// launching is the hourly launch in home for the kit at kit, with kit.app
// app: its LaunchAgents folder and kit's state there.
func launching(home string, fake *runnertest.Fake, app *kitApp, kit string) steps.Launching {
	return steps.Launching{
		Run: fake, Dir: filepath.Join(home, "Library", "LaunchAgents"), Domain: "gui/501",
		Kit: kit, App: app, StateDir: filepath.Join(home, "state"), Now: func() time.Time { return launchAt },
		Sleep: func(time.Duration) {},
	}
}

// writeAgent puts the agent for the kit at kit in l's folder, returning
// its path.
func writeAgent(t *testing.T, l steps.Launching, kit string) string {
	t.Helper()
	agent := filepath.Join(l.Dir, "com.leeovery.kit.nightly.plist")
	data, err := steps.LaunchAgent(l.App.Path(), kit)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(l.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agent, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return agent
}

// The hourly launch's agent: launchd opens kit.app on the hour, waiting till
// it quits, a new instance each time, for it to run the kit named nightly.
func TestLaunchAgent(t *testing.T) {
	got, err := steps.LaunchAgent("/Users/someone/.local/share/kit/kit.app", "/opt/homebrew/bin/kit")
	if err != nil {
		t.Fatal(err)
	}
	want := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>com.leeovery.kit.nightly</string>
	<key>ProgramArguments</key>
	<array>
		<string>/usr/bin/open</string>
		<string>-W</string>
		<string>-g</string>
		<string>-n</string>
		<string>/Users/someone/.local/share/kit/kit.app</string>
		<string>--args</string>
		<string>nightly</string>
		<string>/opt/homebrew/bin/kit</string>
	</array>
	<key>StartCalendarInterval</key>
	<dict>
		<key>Minute</key>
		<integer>0</integer>
	</dict>
</dict>
</plist>
`
	if string(got) != want {
		t.Errorf("LaunchAgent() =\n%s\nwant\n%s", got, want)
	}
}

// The kit the launch runs is the one on kit's PATH, named as it's found
// there, when it's the kit running; another kit on PATH, or none and a
// build that won't last, is none to name.
func TestLaunchKit(t *testing.T) {
	home := homeWith(t, "bin/kit", "Cellar/kit/1.0/bin/kit", "elsewhere/kit", "Code/kit/.claude/worktrees/one/kit", "cache/go-build123/b001/exe/kit", "tmp/built/kit")
	if err := os.Symlink(filepath.Join(home, "Cellar/kit/1.0/bin/kit"), filepath.Join(home, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "brew"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "Cellar/kit/1.0/bin/kit"), filepath.Join(home, "brew", "kit")); err != nil {
		t.Fatal(err)
	}
	temp := filepath.Join(home, "tmp")
	at := func(p string) string { return filepath.Join(home, p) }
	for _, tt := range []struct {
		name, self string
		path       []string
		kit        string
		why        string
	}{
		{"the kit on PATH, running", at("bin/kit"), []string{at("bin")}, at("bin/kit"), ""},
		{"the kit on PATH, Homebrew's link to it running", at("Cellar/kit/1.0/bin/kit"), []string{at("brew")}, at("brew/kit"), ""},
		{"another kit than PATH's", at("elsewhere/kit"), []string{at("bin")}, "", "this kit, " + at("elsewhere/kit") + ", isn't the one on kit's PATH, " + at("bin/kit") + ": the hourly launch is left for that kit's apply"},
		{"none on PATH, a lasting kit", at("elsewhere/kit"), []string{at("nothing")}, at("elsewhere/kit"), ""},
		{"none on PATH, a worktree's build", at("Code/kit/.claude/worktrees/one/kit"), nil, "", "this kit, " + at("Code/kit/.claude/worktrees/one/kit") + ", is a build that won't last: the hourly launch is left for the kit on kit's PATH"},
		{"none on PATH, go run's build", at("cache/go-build123/b001/exe/kit"), nil, "", "this kit, " + at("cache/go-build123/b001/exe/kit") + ", is a build that won't last: the hourly launch is left for the kit on kit's PATH"},
		{"none on PATH, in the temporary folder", at("tmp/built/kit"), nil, "", "this kit, " + at("tmp/built/kit") + ", is a build that won't last: the hourly launch is left for the kit on kit's PATH"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			kit, why := steps.LaunchKit(tt.self, tt.path, temp)
			if kit != tt.kit || why != tt.why {
				t.Errorf("LaunchKit() = %q, %q; want %q, %q", kit, why, tt.kit, tt.why)
			}
		})
	}
	if _, why := steps.LaunchKit(at("linked"), nil, at("Cellar")); why == "" {
		t.Error("a link into the temporary folder: want it a build that won't last, its link followed")
	}
}

// Not installed, kit apply builds kit.app, writes the agent and loads it,
// recording when, for the check on the runs to give its first run its
// hour; then it stands ok.
func TestHourlyLaunchInstalled(t *testing.T) {
	home := homeWith(t, "bin/kit")
	fake := runnertest.New(t)
	app := &kitApp{path: "/Users/someone/.local/share/kit/kit.app", buildable: true}
	l := launching(home, fake, app, filepath.Join(home, "bin/kit"))
	step := steps.HourlyLaunch(l)
	res := step.Check(t.Context())
	if res.State != check.Attention || res.Items[0].ID != "hourly-launch:hourly-launch" || res.Items[0].Action != steps.ActionInstall || res.Summary != "not installed" {
		t.Fatalf("not installed = %+v", res)
	}
	agent := filepath.Join(l.Dir, "com.leeovery.kit.nightly.plist")
	fake.On("launchctl", "print", "gui/501/com.leeovery.kit.nightly").Exits(113).Then()
	fake.On("launchctl", "bootstrap", "gui/501", agent)
	if err := step.Apply(t.Context(), res); err != nil {
		t.Fatal(err)
	}
	want, _ := steps.LaunchAgent(app.path, l.Kit)
	if got, err := os.ReadFile(agent); err != nil || string(got) != string(want) {
		t.Errorf("the agent written = %q (%v), want LaunchAgent's", got, err)
	}
	if info, err := os.Stat(l.Dir); err != nil || info.Mode().Perm() != 0o755 {
		t.Errorf("LaunchAgents made %v (%v), want it as macOS makes it, 0755", info.Mode().Perm(), err)
	}
	if app.readyCalls != 1 {
		t.Errorf("kit.app readied %d times, want it built first", app.readyCalls)
	}
	if calls := fake.Calls(); !slices.Equal(calls, []string{"launchctl print gui/501/com.leeovery.kit.nightly", "launchctl bootstrap gui/501 " + agent}) {
		t.Errorf("ran %q, want it loaded", calls)
	}
	if rec, err := nightly.Load(l.StateDir); err != nil || !rec.Scheduled.Equal(launchAt) {
		t.Errorf("recorded loaded at %v (%v), want %v", rec.Scheduled, err, launchAt)
	}
	if res := step.Check(t.Context()); res.State != check.OK || res.Summary != "hourly, through kit.app" {
		t.Errorf("installed = %+v", res)
	}
}

// Each way it can stand: what the check finds, and what applying runs, the
// agent in place first as the case has it.
func TestHourlyLaunchStands(t *testing.T) {
	home := homeWith(t, "bin/kit", "old/kit")
	kit, other := filepath.Join(home, "bin/kit"), filepath.Join(home, "old/kit")
	for _, tt := range []struct {
		name string
		// written is the kit the agent in place runs.
		written string
		current bool
		loaded  bool
		summary string
		// ran is what applying runs after its look at whether it's loaded.
		ran []string
	}{
		{"in place and loaded", kit, true, true, "hourly, through kit.app", nil},
		{"not loaded", kit, true, false, "not loaded", []string{"bootstrap"}},
		{"another kit, loaded", other, true, true, "runs another kit (" + other + ")", []string{"bootout", "bootstrap"}},
		{"another kit, not loaded", other, true, false, "runs another kit (" + other + ")", []string{"bootstrap"}},
		{"kit.app built from other files", kit, false, true, "kit.app not built from this kit", nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake := runnertest.New(t)
			app := &kitApp{path: "/Users/someone/.local/share/kit/kit.app", current: tt.current, buildable: true}
			l := launching(t.TempDir(), fake, app, kit)
			agent := writeAgent(t, l, tt.written)
			service := "gui/501/com.leeovery.kit.nightly"
			if tt.loaded {
				fake.On("launchctl", "print", service).Prints("com.leeovery.kit.nightly = {\n}\n")
			} else {
				fake.On("launchctl", "print", service).Exits(113).PrintsToStderr("Could not find service")
			}
			fake.On("launchctl", "bootout", service)
			fake.On("launchctl", "bootstrap", "gui/501", agent)
			step := steps.HourlyLaunch(l)
			res := step.Check(t.Context())
			if res.Summary != tt.summary || (res.State == check.OK) != (tt.summary == "hourly, through kit.app") {
				t.Fatalf("check = %+v, want %q", res, tt.summary)
			}
			before := len(fake.Calls())
			if err := step.Apply(t.Context(), res); err != nil {
				t.Fatal(err)
			}
			var ran []string
			for _, c := range fake.Commands()[before:] {
				if c.Args[0] != "print" {
					ran = append(ran, c.Args[0])
				}
			}
			if !slices.Equal(ran, tt.ran) {
				t.Errorf("applying ran launchctl %q, want %q", ran, tt.ran)
			}
			want, _ := steps.LaunchAgent(app.path, kit)
			if got, _ := os.ReadFile(agent); string(got) != string(want) {
				t.Errorf("the agent = %s, want this kit's", got)
			}
			rec, _ := nightly.Load(l.StateDir)
			if loaded := len(tt.ran) > 0; rec.Scheduled.IsZero() == loaded {
				t.Errorf("recorded loaded at %v; want it recorded only as it's loaded", rec.Scheduled)
			}
		})
	}
}

// launchd can refuse to load the launch while the job it unloaded a moment
// before is torn down: kit asks once more, a moment later, and only then
// fails, saying so.
func TestHourlyLaunchLoadsAgain(t *testing.T) {
	for _, tt := range []struct {
		name    string
		refused int
		err     string
	}{
		{"refused once", 1, ""},
		{"refused twice", 2, "launchd refused to load the hourly launch, so the next kit apply tries again"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := homeWith(t, "bin/kit", "old/kit")
			fake := runnertest.New(t)
			l := launching(home, fake, &kitApp{path: "/Users/someone/.local/share/kit/kit.app", current: true}, filepath.Join(home, "bin/kit"))
			slept := 0
			l.Sleep = func(time.Duration) { slept++ }
			agent := writeAgent(t, l, filepath.Join(home, "old/kit"))
			fake.On("launchctl", "print", "gui/501/com.leeovery.kit.nightly")
			fake.On("launchctl", "bootout", "gui/501/com.leeovery.kit.nightly")
			bootstrap := fake.On("launchctl", "bootstrap", "gui/501", agent)
			for range tt.refused {
				bootstrap = bootstrap.Exits(5).PrintsToStderr("Bootstrap failed: 5: Input/output error").Then()
			}
			step := steps.HourlyLaunch(l)
			err := step.Apply(t.Context(), step.Check(t.Context()))
			switch {
			case tt.err == "" && err != nil:
				t.Errorf("Apply() = %v, want it loaded the second time", err)
			case tt.err != "" && (err == nil || !strings.Contains(err.Error(), tt.err)):
				t.Errorf("Apply() = %v, want it to say %q", err, tt.err)
			}
			if slept != 1 {
				t.Errorf("waited %d times before asking again, want once", slept)
			}
			rec, _ := nightly.Load(l.StateDir)
			if rec.Scheduled.IsZero() != (tt.err != "") {
				t.Errorf("recorded loaded at %v; want it recorded only once loaded", rec.Scheduled)
			}
		})
	}
}

// With no kit to name, as from a build of kit's own, the check looks at the
// agent as it stands, the kit it names, and applying leaves it, saying why.
func TestHourlyLaunchLeftForAnotherKit(t *testing.T) {
	home := homeWith(t, "bin/kit")
	fake := runnertest.New(t)
	l := launching(home, fake, &kitApp{path: "/Users/someone/.local/share/kit/kit.app", current: true, buildable: true}, "")
	l.Why = "this kit, /tmp/kit, is a build that won't last: the hourly launch is left for the kit on kit's PATH"
	writeAgent(t, l, filepath.Join(home, "bin/kit"))
	fake.On("launchctl", "print", "gui/501/com.leeovery.kit.nightly")
	step := steps.HourlyLaunch(l)
	if res := step.Check(t.Context()); res.State != check.OK {
		t.Errorf("the agent as it stands, its kit there = %+v, want ok", res)
	}

	gone := filepath.Join(home, "gone/kit")
	agent := writeAgent(t, l, gone)
	written, _ := os.ReadFile(agent)
	res := step.Check(t.Context())
	if res.Summary != "runs a kit that's gone ("+gone+")" {
		t.Errorf("the agent naming a kit that's gone = %+v", res)
	}
	if err := step.Apply(t.Context(), res); !engine.IsNotYet(err) || err.Error() != l.Why {
		t.Errorf("Apply() = %v, want it left, saying why", err)
	}
	if now, _ := os.ReadFile(agent); string(now) != string(written) || slices.Contains(fake.Calls(), "launchctl bootstrap") {
		t.Errorf("the agent rewritten, or loaded (ran %q); want it left", fake.Calls())
	}
}

// Without Swift's compiler kit.app can't be built, and nothing's written:
// the next kit apply tries again.
func TestHourlyLaunchWaitsForKitApp(t *testing.T) {
	home := homeWith(t, "bin/kit")
	fake := runnertest.New(t)
	l := launching(home, fake, &kitApp{path: "/Users/someone/.local/share/kit/kit.app"}, filepath.Join(home, "bin/kit"))
	step := steps.HourlyLaunch(l)
	err := step.Apply(t.Context(), step.Check(t.Context()))
	if !engine.IsNotYet(err) {
		t.Fatalf("Apply() = %v, want not yet", err)
	}
	if _, statErr := os.Stat(filepath.Join(l.Dir, "com.leeovery.kit.nightly.plist")); !os.IsNotExist(statErr) || len(fake.Calls()) > 0 {
		t.Errorf("wrote the agent (%v) or ran %q; want nothing done", statErr, fake.Calls())
	}
}
