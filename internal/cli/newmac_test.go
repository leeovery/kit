package cli_test

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// bootState is the boot's record, in kit's state directory.
var bootState = filepath.Join(".local", "state", "kit", "bootstrap.json")

// newMacWorld is laptopWorld as a new Mac's boot leaves it: laptop's the
// Mac the boot added, a copy of studio's, its folder and its table in
// kit.toml not committed, as git couldn't commit then, beside an edit of
// the user's own; and git's name and email declared in [git config], set
// once kit apply sets them.
func newMacWorld(t *testing.T) (w *world, identified *atomic.Bool) {
	t.Helper()
	w = laptopWorld(t)
	w.write(t, bootState, `{"new_mac": "laptop", "from": "studio"}`)
	w.write(t, filepath.Join(".config", "kit", "laptop", "notes"), "copied\n")
	w.edits(t, " M kit.toml\x00 M shared/home/.zshrc\x00?? laptop/declarations\x00?? laptop/notes\x00", "2\t0\tkit.toml\x003\t1\tshared/home/.zshrc\x00")
	w.writeSection(t, "shared", "git config", "user.email someone@example.com\nuser.name Someone\n")
	identified = &atomic.Bool{}
	w.fake.On("git", "config", "--global", "--list", "-z").Filters(func(string) (string, error) {
		if identified.Load() {
			return "user.email\nsomeone@example.com\x00user.name\nSomeone\x00", nil
		}
		return "", nil
	})
	w.fake.On("git", "config", "--global", "--replace-all", "user.email", "someone@example.com")
	w.fake.On("git", "config", "--global", "--replace-all", "user.name", "Someone").Does(func() { identified.Store(true) })
	for _, key := range []string{"user.name", "user.email"} {
		w.fake.On("git", "-C", filepath.Join(w.home, ".config", "kit"), "config", "--get", key).Filters(func(string) (string, error) {
			if identified.Load() {
				return "set\n", nil
			}
			return "", errors.New("unset")
		})
	}
	return w, identified
}

// A new Mac's apply commits its folder and its table in kit.toml once git's
// settings give it a name and email, those alone, with the message kit
// machine add writes, and pushes; the user's own edit stays as it is. The
// boot's record forgets the Mac, so the next apply has nothing of it.
func TestApplyCommitsANewMac(t *testing.T) {
	w, _ := newMacWorld(t)
	w.expectCommit(t, commit{
		message: "kit machine add laptop (laptop)", paths: []string{"kit.toml", "laptop"},
		status: " M kit.toml\x00?? laptop/declarations\x00?? laptop/notes\x00",
		update: []string{"kit.toml", "laptop/declarations", "laptop/notes"},
		staged: "kit.toml\x00laptop/declarations\x00laptop/notes\x00",
	})

	out, errOut, _ := w.run(t, "apply")
	if !strings.Contains(out, "new-mac ok committed and pushed laptop's folder\n") {
		t.Errorf("kit apply printed\n%s%s\nwant laptop's folder committed and pushed", out, errOut)
	}
	calls := w.fake.Calls()
	set := slices.IndexFunc(calls, func(c string) bool { return strings.Contains(c, "--replace-all user.name") })
	committed := slices.IndexFunc(calls, func(c string) bool { return strings.Contains(c, "commit-tree") })
	if set < 0 || committed < set {
		t.Errorf("ran\n%s\nwant git's name set, then the commit", strings.Join(calls, "\n"))
	}
	if state := w.read(t, bootState); strings.Contains(state, "laptop") || strings.Contains(state, "studio") {
		t.Errorf("the boot's record holds %s; want the new Mac forgotten", state)
	}
	if out, _, _ := w.run(t, "status"); strings.Contains(out, "new-mac") {
		t.Errorf("kit status printed\n%s\nwant nothing of the new Mac, committed", out)
	}
}

// Without git's name and email, as nothing sets them, the apply waits for
// them till the rest of the run is done, then leaves the new Mac for the
// next apply, saying why, without failing: nothing's committed, and the
// boot's record keeps it.
func TestApplyLeavesANewMacGitCantCommit(t *testing.T) {
	w, _ := newMacWorld(t)
	w.writeSection(t, "shared", "git config", "")

	out, errOut, _ := w.run(t, "apply")
	if !strings.Contains(out, "new-mac deferred needs git's name and git's email, which aren't there yet\n") {
		t.Errorf("kit apply printed\n%s%s\nwant the new Mac left, waiting for git's name and email", out, errOut)
	}
	for _, c := range w.fake.Calls() {
		if strings.Contains(c, "commit-tree") || strings.HasSuffix(c, "push --quiet") {
			t.Errorf("ran %s; want nothing committed or pushed", c)
		}
	}
	if state := w.read(t, bootState); !strings.Contains(state, `"new_mac": "laptop"`) {
		t.Errorf("the boot's record holds %s; want the new Mac kept for the next apply", state)
	}
}

// git set to sign its commits, and unable to sign this one, as the key it
// signs with comes later, the new Mac's left for the next apply, saying so,
// without failing: the boot's record keeps it.
func TestApplyLeavesANewMacGitCantSign(t *testing.T) {
	w, _ := newMacWorld(t)
	w.expectCommit(t, commit{
		message: "kit machine add laptop (laptop)", paths: []string{"kit.toml", "laptop"},
		status: " M kit.toml\x00?? laptop/declarations\x00?? laptop/notes\x00",
		update: []string{"kit.toml", "laptop/declarations", "laptop/notes"},
		staged: "kit.toml\x00laptop/declarations\x00laptop/notes\x00", unsigned: true,
	})

	out, errOut, _ := w.run(t, "apply")
	if !strings.Contains(out, "new-mac deferred git couldn't sign the commit yet: the next kit apply commits it\n") {
		t.Errorf("kit apply printed\n%s%s\nwant the new Mac left for the next apply", out, errOut)
	}
	if state := w.read(t, bootState); !strings.Contains(state, `"new_mac": "laptop"`) {
		t.Errorf("the boot's record holds %s; want the new Mac kept for the next apply", state)
	}
}

// A Mac the boot didn't add, or added under another name, has no step
// committing it; one whose folder and table are committed already, as kit
// reconcile adopts them, is done, saying so, and forgotten.
func TestApplyHasNothingOfAMacThatIsntNew(t *testing.T) {
	for _, tt := range []struct {
		name, state string
	}{
		{name: "no record"},
		{name: "another Mac's", state: `{"new_mac": "studio"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := laptopWorld(t)
			if tt.state != "" {
				w.write(t, bootState, tt.state)
			}
			if out, _, _ := w.run(t, "apply"); strings.Contains(out, "new-mac") {
				t.Errorf("kit apply printed\n%s\nwant nothing of a new Mac", out)
			}
		})
	}

	t.Run("committed already", func(t *testing.T) {
		w := laptopWorld(t)
		w.write(t, bootState, `{"new_mac": "laptop", "from": "studio"}`)
		dir := filepath.Join(w.home, ".config", "kit")
		w.fake.On("git", "-C", dir, "config", "--get", "user.name").Prints("Someone\n")
		w.fake.On("git", "-C", dir, "config", "--get", "user.email").Prints("someone@example.com\n")
		w.fake.On("git", "-C", dir, "--literal-pathspecs", "status", "--porcelain", "-z", "--untracked-files=all", "--no-renames", "--", "kit.toml", "laptop")

		out, _, _ := w.run(t, "apply")
		if !strings.Contains(out, "new-mac ok laptop's folder committed\n") {
			t.Errorf("kit apply printed\n%s\nwant the new Mac done", out)
		}
		if state := w.read(t, bootState); strings.Contains(state, "laptop") {
			t.Errorf("the boot's record holds %s; want the new Mac forgotten", state)
		}
	})
}

// The commit waits for the steps of your own, as one can bring the key git
// signs it with.
func TestApplyCommitsANewMacAfterYourSteps(t *testing.T) {
	w, identified := newMacWorld(t)
	identified.Store(true)
	w.writeSection(t, "laptop", "steps", `signing-key "Brings the key git signs with"`+"\n")
	run := w.stepScript(t, "laptop", "signing-key")
	w.fake.On(run, "check").Exits(1).Prints("no key\n").Then().Prints("key in place\n")
	var applied atomic.Bool
	w.fake.On(run, "apply").Does(func() {
		time.Sleep(50 * time.Millisecond)
		applied.Store(true)
	})
	w.expectCommit(t, commit{
		message: "kit machine add laptop (laptop)", paths: []string{"kit.toml", "laptop"},
		status: " M kit.toml\x00?? laptop/declarations\x00?? laptop/notes\x00",
		update: []string{"kit.toml", "laptop/declarations", "laptop/notes"},
		staged: "kit.toml\x00laptop/declarations\x00laptop/notes\x00",
	}, func() {
		if !applied.Load() {
			t.Error("kit committed before your step was applied")
		}
	})

	if out, errOut, _ := w.run(t, "apply"); !strings.Contains(out, "new-mac ok committed and pushed laptop's folder\n") {
		t.Errorf("kit apply printed\n%s%s\nwant laptop's folder committed and pushed", out, errOut)
	}
}
