package cli_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/nightly"
	"github.com/leeovery/kit/internal/steps"
)

// unscheduled is laptopWorld before kit apply's installed the hourly launch:
// no agent, never run on a schedule; kit.app built and nothing found of its
// Full Disk Access yet.
func unscheduled(t *testing.T) *world {
	t.Helper()
	w := laptopWorld(t)
	if err := os.Remove(filepath.Join(w.home, launchAgent)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(w.stateDir(), "nightly.json")); err != nil {
		t.Fatal(err)
	}
	return w
}

// A Mac kit has never run on a schedule needs attention, saying what to do:
// the hourly launch isn't installed, nothing has run, and kit.app hasn't
// Full Disk Access.
func TestStatusSaysKitNeverRanOnASchedule(t *testing.T) {
	out, _, code := unscheduled(t).run(t, "status")
	for _, want := range []string{
		"hourly-launch attention not installed\n",
		"kit-app-access attention not given\n",
		"nightly attention never run on a schedule\n",
		"nightly problem kit has never run on a schedule (kit apply installs the hourly launch)\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("kit status printed\n%s\nwant it to hold %q", out, want)
		}
	}
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
}

// kit apply installs the hourly launch, opening kit.app to run the kit that
// applies, and loads it; within the hour after, the first run is to come.
func TestApplyInstallsTheHourlyLaunch(t *testing.T) {
	w := unscheduled(t)
	w.fake.On("launchctl", "print", launchService).Exits(113).Then().Prints("loaded\n")
	agent := filepath.Join(w.home, launchAgent)
	w.fake.On("launchctl", "bootstrap", strings.TrimSuffix(launchService, "/"+steps.LaunchLabel), agent)

	out, errOut, _ := w.run(t, "apply", "hourly-launch")
	if !strings.Contains(out, "hourly-launch ok hourly, through kit.app\n") {
		t.Errorf("kit apply hourly-launch printed\n%s%s\nwant it installed", out, errOut)
	}
	want, err := steps.LaunchAgent(filepath.Join(w.home, kitApp), filepath.Join(w.home, kitPath))
	if err != nil {
		t.Fatal(err)
	}
	if got := w.read(t, launchAgent); got != string(want) {
		t.Errorf("the agent =\n%s\nwant\n%s", got, want)
	}
	w.now = w.now.Add(50 * time.Minute)
	if out, _, _ := w.run(t, "status", "nightly"); !strings.Contains(out, "nightly ok its first run comes on the hour\n") {
		t.Errorf("kit status nightly, 50 minutes on, printed\n%s\nwant the first run to come", out)
	}
	w.now = w.now.Add(90 * time.Minute)
	if out, _, _ := w.run(t, "status", "nightly"); !strings.Contains(out, "nightly problem no hourly run since its launch was loaded 2 hours ago") {
		t.Errorf("kit status nightly, over two hours on, printed\n%s\nwant no run since it was loaded", out)
	}
}

// An agent running another kit, as one moved since, is written again for
// the kit on kit's PATH, the one running, and loaded again.
func TestApplyMovesTheHourlyLaunchToTheKitOnPath(t *testing.T) {
	w := laptopWorld(t)
	old := filepath.Join(w.home, "old", "kit")
	w.write(t, filepath.Join("old", "kit"), "")
	agent, err := steps.LaunchAgent(filepath.Join(w.home, kitApp), old)
	if err != nil {
		t.Fatal(err)
	}
	w.write(t, launchAgent, string(agent))
	w.fake.On("launchctl", "bootout", launchService)
	w.fake.On("launchctl", "bootstrap", strings.TrimSuffix(launchService, "/"+steps.LaunchLabel), filepath.Join(w.home, launchAgent))
	if out, _, _ := w.run(t, "status", "hourly-launch"); !strings.Contains(out, "hourly-launch attention runs another kit ("+old+")\n") {
		t.Errorf("kit status hourly-launch printed\n%s\nwant the launch another kit's", out)
	}
	w.run(t, "apply", "hourly-launch")
	if got := w.read(t, launchAgent); !strings.Contains(got, "<string>"+filepath.Join(w.home, kitPath)+"</string>") {
		t.Errorf("the agent =\n%s\nwant it running the kit on kit's PATH", got)
	}
	calls := w.fake.Calls()
	bootout := slices.Index(calls, "launchctl bootout "+launchService)
	bootstrap := slices.IndexFunc(calls, func(c string) bool { return strings.HasPrefix(c, "launchctl bootstrap ") })
	if bootout < 0 || bootstrap < bootout {
		t.Errorf("ran %q, want it unloaded, then loaded again", calls)
	}
}

// A kit that isn't the one on kit's PATH, as a build of kit's own, leaves
// the launch as it stands: its check looks at the agent there, and kit
// apply writes nothing, saying why.
func TestAnotherKitLeavesTheHourlyLaunch(t *testing.T) {
	w := laptopWorld(t)
	w.self = filepath.Join(w.home, "Code", "kit", "kit")
	w.write(t, filepath.Join("Code", "kit", "kit"), "a build")
	if out, _, _ := w.run(t, "status", "hourly-launch"); !strings.Contains(out, "hourly-launch ok hourly, through kit.app\n") {
		t.Errorf("kit status hourly-launch printed\n%s\nwant the launch in place, as the agent stands", out)
	}

	if err := os.Remove(filepath.Join(w.home, launchAgent)); err != nil {
		t.Fatal(err)
	}
	out, _, _ := w.run(t, "apply", "hourly-launch")
	if want := "hourly-launch deferred this kit, " + w.self + ", isn't the one on kit's PATH, " + filepath.Join(w.home, kitPath) + ": the hourly launch is left for that kit's apply\n"; !strings.Contains(out, want) {
		t.Errorf("kit apply hourly-launch printed\n%s\nwant it to hold %q", out, want)
	}
	if w.read(t, launchAgent) != "" || slices.ContainsFunc(w.fake.Calls(), func(c string) bool { return strings.HasPrefix(c, "launchctl bootstrap") }) {
		t.Errorf("wrote the agent or loaded it (ran %q); want it left", w.fake.Calls())
	}
}

// Only the hourly launch's runs record the hourly run: one by hand runs the
// same, and never hides a launch that's stopped.
func TestOnlyTheLaunchRecordsTheHourlyRun(t *testing.T) {
	w := laptopWorld(t)
	w.now = w.now.Add(3 * time.Hour)
	w.run(t, "nightly")
	if out, _, _ := w.run(t, "status", "nightly"); !strings.Contains(out, "nightly problem the hourly run last ran 3 hours ago") {
		t.Errorf("after kit nightly by hand, kit status nightly printed\n%s\nwant the launch's last run still the last", out)
	}
	w.run(t, "nightly", "--scheduled")
	if out, _, _ := w.run(t, "status", "nightly"); !strings.Contains(out, "nightly ok last ran: hourly 06:04") {
		t.Errorf("after the launch's run, kit status nightly printed\n%s\nwant it recorded", out)
	}
}

// The hourly run, kit.app's child, records whether kit.app has Full Disk
// Access, by reading what only that shows: denied, kit status says so, and
// the step by hand giving it is to do again. With none of it on the Mac to
// read, it records nothing.
func TestTheHourlyRunRecordsKitAppsAccess(t *testing.T) {
	w := unscheduled(t)
	w.run(t, "nightly", "--scheduled")
	if rec, err := nightly.Load(w.stateDir()); err != nil || !rec.Access.At.IsZero() {
		t.Errorf("recorded %+v (%v), with nothing protected to read; want nothing", rec.Access, err)
	}

	safari := filepath.Join(w.home, "Library", "Safari")
	if err := os.MkdirAll(safari, 0o700); err != nil {
		t.Fatal(err)
	}
	w.run(t, "nightly", "--scheduled")
	if out, _, _ := w.run(t, "status", "kit-app-access"); !strings.Contains(out, "kit-app-access ok granted\n") {
		t.Errorf("kit status printed\n%s\nwant kit.app's access granted", out)
	}

	if err := os.Chmod(safari, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(safari, 0o700) })
	w.now = w.now.Add(time.Hour)
	w.run(t, "nightly", "--scheduled")
	rec, err := nightly.Load(w.stateDir())
	if err != nil || rec.Access.Granted || !rec.Access.At.Equal(w.now) {
		t.Errorf("recorded %+v (%v), want access denied now", rec.Access, err)
	}
	out, _, code := w.run(t, "status", "kit-app-access")
	if !strings.Contains(out, "kit-app-access attention not granted: the hourly run at 04:04 couldn't read what it protects\n") || code != 1 {
		t.Errorf("kit status printed\n%s exit %d; want kit.app's access not granted", out, code)
	}
}

// The launch's runs leave the terminal's Full Disk Access out: kit.app's is
// kit-app-access's. A run at a terminal checks it.
func TestTheLaunchsRunsLeaveTheTerminalsAccessOut(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "features", "prefs\n")
	if out, _, _ := w.run(t, "nightly", "--scheduled"); strings.Contains(out, "full-disk-access") || !strings.Contains(out, "kit-app-access ") {
		t.Errorf("the launch's run printed\n%s\nwant the terminal's Full Disk Access left out", out)
	}
	if out, _, _ := w.run(t, "status", "full-disk-access"); !strings.Contains(out, "full-disk-access ok") {
		t.Errorf("kit status printed\n%s\nwant the terminal's Full Disk Access checked", out)
	}
}

// kit.app built again, from another kit's files, is another app to macOS:
// the launch needs it built from this kit's, and Full Disk Access given to
// it again.
func TestARebuiltKitAppNeedsItsAccessAgain(t *testing.T) {
	w := laptopWorld(t)
	w.write(t, filepath.Join(kitApp, "Contents", "Resources", "built-from"), "an older kit's\n")
	out, _, code := w.run(t, "status", "hourly-launch", "kit-app-access")
	for _, want := range []string{"hourly-launch attention kit.app not built from this kit\n", "kit-app-access attention not given to this build of kit.app\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("kit status printed\n%s\nwant it to hold %q", out, want)
		}
	}
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
}

// kit.app's Full Disk Access is given once the hourly launch has built it.
func TestKitAppsAccessAfterTheHourlyLaunch(t *testing.T) {
	w := orderWorld(t)
	if before := names(w.order(t, "kit-app-access").Before); !slices.Contains(before, "hourly-launch") {
		t.Errorf("before kit-app-access = %q; want hourly-launch", before)
	}
}
