package cli_test

import (
	"strings"
	"testing"
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
	w.expectSync([]string{"laptop/declarations"}, "kit reconcile (laptop): adopt npm:docx")
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
	// Adding looks twice before it installs.
	w.fake.On("npm", "ls", "--global", "--depth=0", "--json").Prints(`{"dependencies": {}}`).
		Then().Prints(`{"dependencies": {}}`).
		Then().Prints(`{"dependencies": {"typescript": {}}}`)
	w.fake.On("npm", "install", "--global", "typescript")
	w.expectSync([]string{"laptop/declarations"}, "kit npm add typescript (laptop)")

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
	w.expectSync([]string{"laptop/declarations"}, "kit npm remove typescript (laptop)")

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
