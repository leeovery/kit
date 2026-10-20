package cli_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/logs"
)

// npmInstalled scripts npm as installed, with docx installed globally.
func npmInstalled(w *world) {
	w.fake.On("npm", "ls", "--global", "--depth=0", "--json").Prints(`{"dependencies": {"docx": {}}}`)
}

// Without a section of its, a kind isn't looked at, its program installed or
// not: nothing of it is drift, nor offered.
func TestAKindWithoutASectionIsntLookedAt(t *testing.T) {
	w := laptopWorld(t)
	npmInstalled(w)
	out, _, _ := w.run(t, "status")
	if strings.Contains(out, "\nnpm ") {
		t.Errorf("kit status printed\n%s\nwant no npm: nothing declares it", out)
	}
	out, _, _ = w.run(t, "reconcile")
	if strings.Contains(out, "npm:") {
		t.Errorf("kit reconcile printed\n%s\nwant nothing of npm's offered", out)
	}
	for _, call := range w.fake.Calls() {
		if strings.HasPrefix(call, "npm ") {
			t.Errorf("kit ran %s: want npm left alone", call)
		}
	}
}

// An empty section looks after its kind with nothing declared yet: what's
// installed is drift, which reconcile offers, and adopts into the section.
func TestAnEmptySectionLooksAfterItsKind(t *testing.T) {
	w := laptopWorld(t)
	npmInstalled(w)
	w.writeSection(t, "laptop", "npm packages", "")
	out, _, _ := w.run(t, "status")
	if !strings.Contains(out, "\nnpm ") {
		t.Errorf("kit status printed\n%s\nwant npm, its section declaring it", out)
	}
	out, _, _ = w.run(t, "reconcile")
	if !strings.Contains(out, "npm:docx extra") {
		t.Errorf("kit reconcile printed\n%s\nwant docx offered", out)
	}
	w.expectSync(t, []string{"laptop/declarations"}, "kit reconcile (laptop): adopt npm:docx")
	out, errOut, code := w.run(t, "reconcile", "npm:docx", "--adopt")
	if code != 0 {
		t.Fatalf("kit reconcile npm:docx --adopt printed\n%s%s exit %d", out, errOut, code)
	}
	if got := w.readSection(t, "laptop", "npm packages"); got != "docx\n" {
		t.Errorf("[npm packages] = %q, want docx adopted", got)
	}
}

// kit <kind> add, with no section of the kind's in the config, installs,
// and declares it in a section it writes: the kind's looked after from then
// on.
func TestAddWritesTheKindsSection(t *testing.T) {
	w := laptopWorld(t)
	// Adding looks once before it installs.
	w.fake.On("npm", "ls", "--global", "--depth=0", "--json").Prints(`{"dependencies": {}}`).
		Then().Prints(`{"dependencies": {"typescript": {}}}`)
	w.fake.On("npm", "install", "--global", "typescript")
	w.expectSync(t, []string{"laptop/declarations"}, "kit npm add typescript (laptop)")

	out, errOut, code := w.run(t, "npm", "add", "typescript")
	if code != 0 || !strings.Contains(out, "typescript ok installed; declared in laptop\n") {
		t.Fatalf("kit npm add typescript printed\n%s%s exit %d", out, errOut, code)
	}
	if got := w.read(t, ".config/kit/laptop/declarations"); got != "[homebrew formulae]\ngo\n\n[npm packages]\ntypescript\n" {
		t.Errorf("laptop/declarations = %q, want npm's section written", got)
	}
	out, _, _ = w.run(t, "status", "npm")
	if !strings.Contains(out, "npm ok 1 declared, all installed\n") {
		t.Errorf("kit status npm printed\n%s", out)
	}
}

// Removing a kind's last thing leaves its section, empty: the kind's still
// looked after, what's installed drift.
func TestRemovingTheLastThingKeepsTheSection(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "npm packages", "typescript\n")
	w.fake.On("npm", "ls", "--global", "--depth=0", "--json").Prints(`{"dependencies": {"typescript": {}, "docx": {}}}`).
		Then().Prints(`{"dependencies": {"docx": {}}}`)
	w.fake.On("npm", "uninstall", "--global", "typescript")
	w.expectSync(t, []string{"laptop/declarations"}, "kit npm remove typescript (laptop)")

	out, errOut, code := w.run(t, "npm", "remove", "typescript")
	if code != 0 {
		t.Fatalf("kit npm remove typescript printed\n%s%s exit %d", out, errOut, code)
	}
	if got := w.read(t, ".config/kit/laptop/declarations"); !strings.HasSuffix(got, "\n[npm packages]\n") {
		t.Errorf("laptop/declarations = %q, want npm's section left, empty", got)
	}
	out, _, _ = w.run(t, "reconcile")
	if !strings.Contains(out, "npm:docx extra") {
		t.Errorf("kit reconcile printed\n%s\nwant docx offered", out)
	}
}

// kit list of a kind the config has no section of refuses, as kit status of
// it does, looking at nothing.
func TestListOfAKindWithoutASection(t *testing.T) {
	w := laptopWorld(t)
	_, errOut, code := w.run(t, "list", "npm")
	if code != 2 || !strings.Contains(errOut, "no kind named npm on this Mac: one of brew, cask") {
		t.Errorf("kit list npm printed %q, exit %d; want it refused", errOut, code)
	}
}

// A temporary install of a kind the config has no section of writes the
// kind's section, empty, so the kind's looked after, and reconcile asks once
// the throwaway's time is up.
func TestAddTemporarilyWritesTheKindsSection(t *testing.T) {
	w := laptopWorld(t)
	w.fake.On("npm", "ls", "--global", "--depth=0", "--json").Prints(`{"dependencies": {}}`).
		Then().Prints(`{"dependencies": {"left-pad": {}}}`)
	w.fake.On("npm", "install", "--global", "left-pad")
	w.expectSync(t, []string{"laptop/declarations"}, "kit npm add left-pad (laptop)")

	out, errOut, code := w.run(t, "npm", "add", "left-pad", "--temp")
	if code != 0 || !strings.Contains(out, "left-pad ok installed, for now: not declared, quiet for 7 days, then kit reconcile asks; [npm packages] added to laptop, empty\n") {
		t.Fatalf("kit npm add left-pad --temp printed\n%s%s exit %d", out, errOut, code)
	}
	if got := w.read(t, ".config/kit/laptop/declarations"); got != "[homebrew formulae]\ngo\n\n[npm packages]\n" {
		t.Errorf("laptop/declarations = %q, want npm's section, empty", got)
	}
	w.now = w.now.Add(8 * 24 * time.Hour)
	out, _, _ = w.run(t, "reconcile")
	if !strings.Contains(out, "npm:left-pad extra") {
		t.Errorf("kit reconcile a week on printed\n%s\nwant left-pad asked about", out)
	}
}

// With nothing declared, a kind whose program isn't installed doesn't wait
// for it as kit applies, while another step is at work: it's fine at once.
func TestApplyDoesntWaitForAnEmptyKindsProgram(t *testing.T) {
	w := missingWorld(t)
	w.writeSection(t, "laptop", "app store apps", "")
	w.fake.On("mas", "list", "--json").Fails(errNotFound)
	w.fake.On("brew", "update", "--quiet")
	w.fake.On("brew", "install", "--formula", "ripgrep").Does(func() { time.Sleep(300 * time.Millisecond) })

	out, errOut, _ := w.run(t, "apply")
	if !strings.Contains(out, "mas ok none declared; mas isn't installed\n") {
		t.Errorf("kit apply printed\n%s%s\nwant the App Store's apps fine", out, errOut)
	}
	masDone, installed := -1, -1
	for i, r := range w.lastLog(t) {
		switch {
		case r.Event == "step_finished" && r.Step == "mas":
			masDone = i
		case r.Event == "command" && r.Command == "brew install --formula ripgrep":
			installed = i
		}
	}
	if masDone < 0 || installed < 0 || masDone > installed {
		t.Errorf("the App Store's apps finished at %d, ripgrep's install at %d: want them done without waiting for the install", masDone, installed)
	}
}

// What the drift record holds of a kind the config has no section of goes:
// once its section's back, its things are new again, not old drift.
func TestDriftOfAKindWithoutASectionIsForgotten(t *testing.T) {
	w := laptopWorld(t)
	w.write(t, driftFile, `{"first_seen": {"brew:ffmpeg": "2025-12-31T00:00:00Z", "npm:docx": "2025-12-01T00:00:00Z"}, "snoozed_until": {"npm:left-pad": "2026-01-05T00:00:00Z"}}`)
	w.run(t, "status")
	if got := w.read(t, driftFile); strings.Contains(got, "npm:") || !strings.Contains(got, "brew:ffmpeg") {
		t.Errorf("drift.json = %s, want npm's gone and Homebrew's kept", got)
	}
	npmInstalled(w)
	w.writeSection(t, "laptop", "npm packages", "")
	out, _, _ := w.run(t, "status", "npm")
	if !strings.Contains(out, "npm ok") {
		t.Errorf("kit status npm printed\n%s\nwant docx new, not needing attention", out)
	}
}

// With only Homebrew's casks declared, Homebrew's step is in the run; with
// formulae too, it's there once.
func TestHomebrewsStepComesWithItsKinds(t *testing.T) {
	w := laptopWorld(t)
	w.write(t, ".config/kit/shared/declarations", "[paths]\n~/.local/bin\n\n[homebrew casks]\nghostty\n")
	w.write(t, ".config/kit/laptop/declarations", "")
	out, _, _ := w.run(t, "status")
	if strings.Count(out, "\nhomebrew ok") != 1 || strings.Contains(out, "\nbrew ") || !strings.Contains(out, "\ncask ") {
		t.Errorf("kit status printed\n%s\nwant Homebrew's step with the casks alone", out)
	}
	out, _, _ = laptopWorld(t).run(t, "status")
	if strings.Count(out, "\nhomebrew ") != 1 {
		t.Errorf("kit status printed\n%s\nwant Homebrew's step once", out)
	}
}

// A step of your own that comes after Homebrew comes after it when the run
// has it, and is placed without it when it hasn't.
func TestPlacedAfterHomebrewOrWithoutIt(t *testing.T) {
	for _, tt := range []struct {
		name, laptop string
		homebrew     bool
	}{
		{"with a Homebrew kind", "[homebrew formulae]\ngo\n", true},
		{"without one", "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := laptopWorld(t)
			w.write(t, ".config/kit/shared/declarations", "[steps]\nfonts \"the fonts\" --after homebrew\n")
			w.write(t, ".config/kit/laptop/declarations", tt.laptop)
			w.write(t, ".config/kit/shared/steps/fonts/run", "#!/bin/sh\n")
			w.fake.On(filepath.Join(w.home, ".config/kit/shared/steps/fonts/run"), "check")
			_, errOut, code := w.run(t, "status")
			if code == 2 {
				t.Fatalf("kit status: %s exit 2", errOut)
			}
			waits := map[string][]string{}
			for _, s := range runStarted(t, w).Steps {
				waits[s.Name] = s.Waits
			}
			_, inRun := waits["homebrew"]
			if inRun != tt.homebrew || slices.Contains(waits["fonts"], "homebrew") != tt.homebrew {
				t.Errorf("the run's steps wait for %v; want Homebrew's step and the waits for it %v", waits, tt.homebrew)
			}
		})
	}
}

// runStarted is the start of the world's last run, as its log has it.
func runStarted(t *testing.T, w *world) logs.Record {
	t.Helper()
	for _, r := range w.lastLog(t) {
		if r.Event == "run_started" {
			return r
		}
	}
	t.Fatal("the run's log has no start")
	return logs.Record{}
}

// kit apply and the nightly run leave a kind the config has no section of
// alone, as status does.
func TestApplyAndNightlyLeaveAKindWithoutASection(t *testing.T) {
	for _, command := range []string{"apply", "nightly"} {
		t.Run(command, func(t *testing.T) {
			w := laptopWorld(t)
			npmInstalled(w)
			out, _, _ := w.run(t, command)
			if strings.Contains(out, "\nnpm ") {
				t.Errorf("kit %s printed\n%s\nwant no npm", command, out)
			}
			for _, call := range w.fake.Calls() {
				if strings.HasPrefix(call, "npm ") {
					t.Errorf("kit %s ran %s: want npm left alone", command, call)
				}
			}
		})
	}
}

// tmux's plugins, with no plugin and no TPM in tmux's config, are left out
// of a run; TPM started, they're looked after.
func TestTmuxWithoutPluginsOrTPMIsLeftOut(t *testing.T) {
	w := laptopWorld(t)
	w.write(t, ".tmux.conf", "set -g mouse on\n")
	out, _, _ := w.run(t, "status")
	if strings.Contains(out, "\ntmux ") {
		t.Errorf("kit status printed\n%s\nwant no tmux", out)
	}
	w.write(t, ".tmux.conf", "set -g mouse on\nrun '~/.tmux/plugins/tpm/tpm'\n")
	out, _, _ = w.run(t, "status")
	if !strings.Contains(out, "\ntmux ok") {
		t.Errorf("kit status printed\n%s\nwant tmux looked after, TPM started", out)
	}
}
