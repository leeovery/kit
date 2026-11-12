package steps_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
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

// launching is a Mac's hourly launch for the kit at kit, with kit.app app,
// its LaunchAgents folder and kit's state of its own.
func launching(t *testing.T, fake *runnertest.Fake, app *kitApp, kit string) steps.Launching {
	t.Helper()
	home := t.TempDir()
	return steps.Launching{
		Run: fake, Dir: filepath.Join(home, "Library", "LaunchAgents"), Domain: "gui/501",
		Kit: kit, App: app, StateDir: filepath.Join(home, "state"), Now: func() time.Time { return launchAt },
	}
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

// Not installed, kit apply builds kit.app, writes the agent and loads it,
// recording when, for the check on the runs to give its first run its
// hour; then it stands ok.
func TestHourlyLaunchInstalled(t *testing.T) {
	fake := runnertest.New(t)
	app := &kitApp{path: "/Users/someone/.local/share/kit/kit.app", buildable: true}
	l := launching(t, fake, app, "/opt/homebrew/bin/kit")
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
	const kit, other = "/opt/homebrew/bin/kit", "/Users/someone/.local/bin/kit"
	for _, tt := range []struct {
		name string
		// written is the kit the agent in place runs: none when "".
		written string
		current bool
		loaded  bool
		summary string
		// ran is what applying runs after its look at whether it's loaded.
		ran []string
	}{
		{"in place and loaded", kit, true, true, "hourly, through kit.app", nil},
		{"not loaded", kit, true, false, "not loaded", []string{"bootstrap"}},
		{"a kit at another path, loaded", other, true, true, "not this kit's", []string{"bootout", "bootstrap"}},
		{"a kit at another path, not loaded", other, true, false, "not this kit's", []string{"bootstrap"}},
		{"kit.app built from other files", kit, false, true, "kit.app not built from this kit", nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake := runnertest.New(t)
			app := &kitApp{path: "/Users/someone/.local/share/kit/kit.app", current: tt.current, buildable: true}
			l := launching(t, fake, app, kit)
			agent := filepath.Join(l.Dir, "com.leeovery.kit.nightly.plist")
			written, _ := steps.LaunchAgent(app.path, tt.written)
			if err := os.MkdirAll(l.Dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(agent, written, 0o644); err != nil {
				t.Fatal(err)
			}
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
			if res.State == check.OK {
				return
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

// Without Swift's compiler kit.app can't be built, and nothing's written:
// the next kit apply tries again.
func TestHourlyLaunchWaitsForKitApp(t *testing.T) {
	fake := runnertest.New(t)
	l := launching(t, fake, &kitApp{path: "/Users/someone/.local/share/kit/kit.app"}, "/opt/homebrew/bin/kit")
	step := steps.HourlyLaunch(l)
	err := step.Apply(t.Context(), step.Check(t.Context()))
	if !engine.IsNotYet(err) {
		t.Fatalf("Apply() = %v, want not yet", err)
	}
	if _, statErr := os.Stat(filepath.Join(l.Dir, "com.leeovery.kit.nightly.plist")); !os.IsNotExist(statErr) || len(fake.Calls()) > 0 {
		t.Errorf("wrote the agent (%v) or ran %q; want nothing done", statErr, fake.Calls())
	}
}
