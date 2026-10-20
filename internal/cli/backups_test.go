package cli_test

import (
	"strings"
	"testing"
	"time"
)

// arqStats is what arqc stats says of one daily plan backed up at last.
func arqStats(last time.Time) string {
	return `{"backupPlans": [{"name": "User data", "schedule": {"type": "Daily"}, "lastBackedUp": "` + last.UTC().Format(time.RFC3339) + `"}]}`
}

const arqc = "/Applications/Arq.app/Contents/Resources/arqc"

// A backup tool listed in [backups] brings its check into the run, the
// shared file's as a Mac's; one not listed has none; one kit doesn't know is
// refused, saying where and naming those it has.
func TestBackupsBringTheirChecks(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "shared", "backups", "arq\n")
	w.fake.On(arqc, "stats").Prints(arqStats(w.now.Add(-2 * time.Hour)))
	out, _, _ := w.run(t, "status")
	if !strings.Contains(out, "arq ok last backup 01:04\n") {
		t.Errorf("kit status printed\n%s", out)
	}
	if strings.Contains(out, "\ntime-machine ") {
		t.Errorf("time-machine checked though not listed:\n%s", out)
	}

	w.writeSection(t, "laptop", "backups", "time-capsule\n")
	_, errOut, code := w.run(t, "status")
	if !strings.Contains(errOut, "laptop/declarations:5: kit doesn't know the backup tool time-capsule: one of arq, time-machine") || code != 2 {
		t.Errorf("kit status printed %q, exit %d", errOut, code)
	}
}

// A Mac with no [backups] section has no backup steps.
func TestNoBackupsNoSteps(t *testing.T) {
	w := laptopWorld(t)
	out, _, _ := w.run(t, "status")
	for _, step := range []string{"time-machine", "arq"} {
		if strings.Contains(out, "\n"+step+" ") {
			t.Errorf("kit status checked %s, which no [backups] lists:\n%s", step, out)
		}
	}
}

// A backup tool's line can say what its check waits for: applying, Arq's
// check waits till its cask is in, which installs first, rather than
// reading "not installed" while the cask waits its turn.
func TestABackupToolWaitsForWhatItChecks(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "homebrew casks", "arq\nzoom\n")
	w.writeSection(t, "laptop", "backups", "arq --after cask:arq\n")
	f := w.fake
	f.On("brew", "list", "--cask", "--full-name", "-1").Prints("ghostty\nfirefox\n").Does(func() { time.Sleep(50 * time.Millisecond) })
	f.On("brew", "info", "--json=v2", "--cask", "arq", "zoom").Prints(`{"formulae": [], "casks": [{"token": "arq", "full_token": "arq", "artifacts": [{"app": ["Arq.app"]}]}, {"token": "zoom", "full_token": "zoom", "artifacts": [{"app": ["zoom.us.app"]}]}]}`)
	f.On("sudo", "-n", "true").Exits(1)
	f.On("brew", "update", "--quiet")
	f.On("brew", "install", "--cask", "arq")
	f.On("brew", "install", "--cask", "zoom")
	f.On(arqc, "stats").Prints(arqStats(w.now.Add(-2 * time.Hour)))

	out, errOut, _ := w.run(t, "apply")
	calls := w.fake.Calls()
	if !before(calls, "brew install --cask arq", "brew install --cask zoom") || !before(calls, "brew install --cask arq", arqc+" stats") {
		t.Errorf("ran %q: want arq's cask installed on its own, first, then Arq checked", calls)
	}
	if !strings.Contains(out, "arq ok last backup 01:04\n") {
		t.Errorf("kit apply printed\n%s%s\nwant Arq checked once its cask was in", out, errOut)
	}
}

// A backup tool's line can place its check before another step, which then
// waits for it.
func TestABackupToolBeforeAnother(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "backups", "time-machine\narq --before time-machine\n")
	w.fake.On(arqc, "stats").Prints(arqStats(w.now.Add(-2 * time.Hour)))
	w.fake.On("defaults", "export", "/Library/Preferences/com.apple.TimeMachine", "-").Exits(1).PrintsToStderr("file does not exist")
	w.run(t, "status")
	if calls := w.fake.Calls(); !before(calls, arqc+" stats", "defaults export /Library/Preferences/com.apple.TimeMachine -") {
		t.Errorf("ran %q: want Arq checked before Time Machine", calls)
	}
}

// A backup tool's line placed after or before what isn't a step, or after
// a thing of a step that isn't a kind's, fails its check, saying where.
func TestABackupToolWaitingForWhatIsntThere(t *testing.T) {
	for _, tt := range []struct{ options, why string }{
		{"--after nothing", "it comes after nothing, which isn't a step"},
		{"--after disk:x", "it comes after disk:x, which isn't a step"},
		{"--before nothing", "it comes before nothing, which isn't a step"},
	} {
		t.Run(tt.options, func(t *testing.T) {
			w := laptopWorld(t)
			w.writeSection(t, "laptop", "backups", "arq "+tt.options+"\n")
			out, _, code := w.run(t, "status", "arq")
			if want := "arq failed laptop/declarations:5: " + tt.why + "\n"; !strings.Contains(out, want) || code != 1 {
				t.Errorf("kit status arq printed\n%s\nexit %d; want %q", out, code, want)
			}
		})
	}
}

// The backup exclusions are written through the tools the Mac lists: Arq
// alone has them kept by Time Machine's list, which it follows; with no
// tool listed, they can't be kept, the kind's check saying where the tools
// go.
func TestBackupExclusionsThroughTheTools(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "backup exclusions", "/Applications\n")
	w.writeSection(t, "laptop", "backups", "arq\n")
	w.fake.On(arqc, "stats").Prints(arqStats(w.now.Add(-2 * time.Hour)))
	w.fake.On("defaults", "export", "/Library/Preferences/com.apple.TimeMachine", "-").Prints("<plist><dict><key>SkipPaths</key><array/></dict></plist>")
	if out, _, _ := w.run(t, "status", "backup-exclusion"); !strings.Contains(out, "backup-exclusion missing:new /Applications") {
		t.Errorf("kit status backup-exclusion printed\n%s\nwant /Applications missing from Time Machine's list", out)
	}

	w.writeSection(t, "laptop", "backups", "")
	if out, _, _ := w.run(t, "status", "backup-exclusion"); !strings.Contains(out, "backup-exclusion failed no backup tool this Mac uses keeps them out: name the tools in [backups]") {
		t.Errorf("kit status backup-exclusion printed\n%s\nwant it failed, naming [backups]", out)
	}
}
