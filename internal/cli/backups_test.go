package cli_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/runner"
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

// Arq not installed, kit says how to install it as this Mac's config has
// it: kit apply does, its cask declared for this Mac, in its own file or
// the shared one; its cask in, but not Arq, it's to be installed again;
// else the cask's to be declared.
func TestArqNotInstalledSaysHow(t *testing.T) {
	for _, tt := range []struct{ name, scope, casks, in, do string }{
		{"its cask declared", "laptop", "arq\nzoom\n", "", "kit apply installs it"},
		{"its cask declared for every Mac", "shared", "arq\nghostty\n", "", "kit apply installs it"},
		{"its cask declared and in", "shared", "arq\nghostty\n", "arq\n", "its cask is in, but not Arq: install it again"},
		{"its cask not declared", "laptop", "zoom\n", "", "install it: kit cask add arq"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := laptopWorld(t)
			w.writeSection(t, tt.scope, "homebrew casks", tt.casks)
			w.writeSection(t, "laptop", "backups", "arq\n")
			w.fake.On("brew", "list", "--cask", "--full-name", "-1").Prints("ghostty\nfirefox\n" + tt.in)
			w.fake.On(arqc, "stats").Fails(runner.ErrNotFound)
			out, _, _ := w.run(t, "status", "arq")
			if !strings.Contains(out, "Arq isn't installed") || !strings.Contains(out, tt.do) {
				t.Errorf("kit status arq printed\n%s\nwant Arq not installed: %s", out, tt.do)
			}
		})
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
// waits for it as kit applies (kit status checks everything at once).
func TestABackupToolBeforeAnother(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "backups", "time-machine\narq --before time-machine\n")
	w.fake.On(arqc, "stats").Prints(arqStats(w.now.Add(-2 * time.Hour)))
	w.fake.On("defaults", "export", "/Library/Preferences/com.apple.TimeMachine", "-").Exits(1).PrintsToStderr("file does not exist")
	w.run(t, "apply")
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
// alone has them kept by Time Machine's list, which it follows, though Time
// Machine was never set up and has no settings at all; with no tool listed,
// they can't be kept, the kind's check saying where the tools go.
func TestBackupExclusionsThroughTheTools(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "backup exclusions", "/Applications\n")
	w.writeSection(t, "laptop", "backups", "arq\n")
	w.fake.On(arqc, "stats").Prints(arqStats(w.now.Add(-2 * time.Hour)))
	w.fake.On("defaults", "export", "/Library/Preferences/com.apple.TimeMachine", "-").Exits(1).PrintsToStderr("Domain /Library/Preferences/com.apple.TimeMachine does not exist\n")
	if out, _, _ := w.run(t, "status", "backup-exclusion"); !strings.Contains(out, "backup-exclusion missing:new /Applications") {
		t.Errorf("kit status backup-exclusion printed\n%s\nwant /Applications missing from Time Machine's list", out)
	}

	w.writeSection(t, "laptop", "backups", "")
	if out, _, _ := w.run(t, "status", "backup-exclusion"); !strings.Contains(out, "backup-exclusion failed no backup tool this Mac uses keeps them out: name the tools in [backups]") {
		t.Errorf("kit status backup-exclusion printed\n%s\nwant it failed, naming [backups]", out)
	}
}

// The backup tools' steps run in kit's order, Time Machine then Arq,
// whatever order the files list them in, shared or a Mac's.
func TestBackupToolsInKitsOrder(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "shared", "backups", "arq --after cask:arq\n")
	w.writeSection(t, "laptop", "backups", "time-machine\n")
	w.fake.On(arqc, "stats").Prints(arqStats(w.now.Add(-2 * time.Hour)))
	w.fake.On("defaults", "export", "/Library/Preferences/com.apple.TimeMachine", "-").Exits(1).PrintsToStderr("Domain /Library/Preferences/com.apple.TimeMachine does not exist\n")
	out, _, _ := w.run(t, "status")
	tm, arq := strings.Index(out, "\ntime-machine "), strings.Index(out, "\narq ")
	if tm < 0 || arq < 0 || tm > arq {
		t.Errorf("kit status printed\n%s\nwant Time Machine, then Arq", out)
	}
	var names []string
	for _, s := range runStarted(t, w).Steps {
		if s.Name == "time-machine" || s.Name == "arq" {
			names = append(names, s.Name)
		}
	}
	if !slices.Equal(names, []string{"time-machine", "arq"}) {
		t.Errorf("the run's steps are %q, want Time Machine's, then Arq's", names)
	}
}

// kit backup add declares a backup tool for this Mac, or every Mac with
// --shared, taking it out of the Macs' files that had it in the same edit,
// its check placed as --after and --before say, and kit backup remove takes
// it out, each committed and pushed; one declared already, in the shared
// file or the one it goes in, a tool kit doesn't know, or one placed after
// what isn't a step is refused.
func TestBackupAddAndRemove(t *testing.T) {
	w := laptopWorld(t)
	w.expectSync(t, []string{"laptop/declarations"}, "kit backup add arq (laptop): offsite")
	out, errOut, code := w.run(t, "backup", "add", "arq", "--after", "cask:arq", "--before", "config", "--note", "offsite")
	if code != 0 || !strings.Contains(out, "arq ok declared in laptop\n") {
		t.Fatalf("kit backup add printed\n%s%s exit %d", out, errOut, code)
	}
	if got := w.readSection(t, "laptop", "backups"); got != "arq --after cask:arq --before config   # offsite\n" {
		t.Errorf("[backups] = %q", got)
	}
	w.expectSync(t, []string{"shared/declarations"}, "kit backup add time-machine (laptop)")
	if out, errOut, code := w.run(t, "backup", "add", "time-machine", "--shared"); code != 0 || !strings.Contains(out, "time-machine ok declared in shared\n") {
		t.Fatalf("kit backup add --shared printed\n%s%s exit %d", out, errOut, code)
	}
	if got := w.readSection(t, "shared", "backups"); got != "time-machine\n" {
		t.Errorf("shared [backups] = %q", got)
	}
	for _, tt := range []struct {
		args []string
		want string
		code int
	}{
		{[]string{"add", "arq"}, "arq failed declared already, in laptop/declarations:", 1},
		{[]string{"add", "time-machine", "--shared"}, "time-machine failed declared already, in shared/declarations:", 1},
		{[]string{"add", "time-capsule"}, "kit doesn't know the backup tool time-capsule: one of arq, time-machine", 2},
		{[]string{"add", "arq", "--before", "cask:arq"}, "--before a step", 2},
		{[]string{"remove", "time-machine"}, "time-machine failed declared for every Mac, in shared/declarations:", 1},
	} {
		out, errOut, code := w.run(t, append([]string{"backup"}, tt.args...)...)
		if !strings.Contains(out+errOut, tt.want) || code != tt.code {
			t.Errorf("kit backup %s printed\n%s%s exit %d; want %q, exit %d", strings.Join(tt.args, " "), out, errOut, code, tt.want, tt.code)
		}
	}
	w.writeSection(t, "studio", "backups", "arq --before config --after cask:arq\n")
	before := map[string]string{}
	for _, scope := range []string{"shared", "laptop", "studio"} {
		before[scope] = w.readSection(t, scope, "backups")
	}
	if out, _, code := w.run(t, "backup", "add", "arq", "--shared"); code != 1 || !strings.Contains(out, "arq failed laptop/declarations:") || !strings.Contains(out, "has arq with --after cask:arq and --before config, where this doesn't: give the same, or take that line out first") {
		t.Errorf("kit backup add --shared, dropping the Macs' options, printed\n%s exit %d", out, code)
	}
	for scope, was := range before {
		if got := w.readSection(t, scope, "backups"); got != was {
			t.Errorf("%s's [backups] = %q, want it untouched", scope, got)
		}
	}
	w.expectSync(t, []string{"laptop/declarations", "shared/declarations", "studio/declarations"}, "kit backup add arq (laptop)")
	if out, errOut, code := w.run(t, "backup", "add", "arq", "--shared", "--before", "config", "--after", "cask:arq"); code != 0 || !strings.Contains(out, "arq ok declared in shared, out of laptop and studio\n") {
		t.Fatalf("kit backup add --shared, declared by Macs, printed\n%s%s exit %d", out, errOut, code)
	}
	if got := w.readSection(t, "shared", "backups"); got != "arq --after cask:arq --before config   # offsite\ntime-machine\n" {
		t.Errorf("shared [backups] = %q", got)
	}
	for _, mac := range []string{"laptop", "studio"} {
		if got := w.readSection(t, mac, "backups"); got != "" {
			t.Errorf("%s's [backups] = %q, want arq out of it", mac, got)
		}
	}
	if commits := slices.DeleteFunc(w.fake.Calls(), func(call string) bool { return !strings.Contains(call, " commit-tree ") }); len(commits) != 3 {
		t.Errorf("committed %q, want one commit for each of the three adds", commits)
	}
	w.writeSection(t, "shared", "backups", "")
	if out, _, code := w.run(t, "backup", "add", "arq", "--after", "nothing"); !strings.Contains(out, "arq failed it comes after nothing, which isn't a step") || code != 1 {
		t.Errorf("kit backup add arq --after nothing printed\n%s exit %d", out, code)
	}
	w.writeSection(t, "laptop", "backups", "arq\n")
	w.expectSync(t, []string{"laptop/declarations"}, "kit backup remove arq (laptop)")
	if out, errOut, code := w.run(t, "backup", "remove", "arq"); code != 0 || !strings.Contains(out, "arq ok out of laptop\n") {
		t.Fatalf("kit backup remove printed\n%s%s exit %d", out, errOut, code)
	}
	if got := w.readSection(t, "laptop", "backups"); got != "" {
		t.Errorf("[backups] = %q, want it empty", got)
	}
}

// Applying, the backup exclusions wait for the program of the tool keeping
// them, Time Machine's tmutil for Arq alone, as another step may put it on
// kit's PATH: one that never comes leaves them deferred, saying so.
func TestBackupExclusionsWaitForTheirProgram(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "backup exclusions", "/Applications\n")
	w.writeSection(t, "laptop", "backups", "arq\n")
	w.fake.On(arqc, "stats").Prints(arqStats(w.now.Add(-2 * time.Hour)))
	w.fake.On("defaults", "export", "/Library/Preferences/com.apple.TimeMachine", "-").Prints("<plist><dict><key>SkipPaths</key><array/></dict></plist>")
	w.fake.On("sudo", "-n", "true")
	w.write(t, filepath.Join(".local", "state", "kit", "drift.json"), `{"first_seen": {"backup-exclusion:/Applications": "2025-12-31T00:00:00Z"}}`)
	out, errOut, _ := w.run(t, "apply")
	if !strings.Contains(out, "backup-exclusion deferred needs tmutil, which isn't there yet\n") {
		t.Errorf("kit apply printed\n%s%s\nwant the exclusions waiting for tmutil", out, errOut)
	}
}
