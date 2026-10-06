package cli_test

import (
	"strings"
	"testing"
)

// gitWorld is laptopWorld with git's settings declared: the editor
// changed on the Mac since, and a setting git has that isn't declared.
func gitWorld(t *testing.T) *world {
	t.Helper()
	w := laptopWorld(t)
	w.writeSection(t, "shared", "git config", "# Core\ncore.editor micro\ncore.pager delta\n")
	w.fake.On("git", "config", "--global", "--list", "-z").Prints("core.editor\nvim\x00core.pager\ndelta\x00delta.pager\nless\x00")
	return w
}

func TestStatusShowsGitsSettings(t *testing.T) {
	out, _, _ := gitWorld(t).run(t, "status")
	if !strings.Contains(out, "git ok 2 declared, all set; 1 changed\ngit diverged:new core.editor (set to vim)\ngit extra:new delta.pager\n") {
		t.Errorf("kit status printed\n%s", out)
	}
}

func TestReconcileAdoptsASettingChangedOnTheMac(t *testing.T) {
	w := gitWorld(t)
	w.expectSync([]string{"shared/declarations"}, "kit reconcile (laptop): adopt git:core.editor")
	out, _, code := w.run(t, "reconcile", "git:core.editor", "--adopt")
	if code != 0 || !strings.Contains(out, "declared as it is now, in shared") {
		t.Errorf("kit reconcile printed\n%s exit %d", out, code)
	}
	if got := w.readSection(t, "shared", "git config"); got != "# Core\ncore.editor vim\ncore.pager delta\n" {
		t.Errorf("[git config] = %q", got)
	}
}

func TestReconcilePutsASettingBack(t *testing.T) {
	w := gitWorld(t)
	w.fake.On("git", "config", "--global", "--replace-all", "core.editor", "micro")
	out, _, code := w.run(t, "reconcile", "git:core.editor", "--revert")
	if code != 0 || !strings.Contains(out, "put back as declared") {
		t.Errorf("kit reconcile printed\n%s exit %d", out, code)
	}
}

func TestReconcileAdoptsAnUndeclaredSetting(t *testing.T) {
	w := gitWorld(t)
	w.expectSync([]string{"laptop/declarations"}, "kit reconcile (laptop): adopt git:delta.pager")
	if out, _, code := w.run(t, "reconcile", "git:delta.pager", "--adopt"); code != 0 {
		t.Errorf("kit reconcile printed\n%s exit %d", out, code)
	}
	if got := w.readSection(t, "laptop", "git config"); got != "# To be sorted\ndelta.pager less\n" {
		t.Errorf("[git config] = %q", got)
	}
}

func TestApplyLeavesASettingChangedOnTheMac(t *testing.T) {
	w := gitWorld(t)
	w.run(t, "apply", "git")
	for _, c := range w.fake.Calls() {
		if strings.Contains(c, "--replace-all") {
			t.Errorf("kit apply ran %s", c)
		}
	}
}
