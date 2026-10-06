package cli_test

import (
	"strings"
	"testing"
)

// kit's help lists the whole Mac's commands, then what kit manages, by
// group: a command for each kind of thing, which names it first.
func TestHelpGroupsTheCommands(t *testing.T) {
	out, _, code := laptopWorld(t).run(t, "--help")
	at := 0
	for _, want := range []string{"This Mac:", "  status ", "  apply ", "  reconcile ", "Packages:", "  brew ", "  login-item ", "Settings:", "  defaults ", "  git-config ", "  backup-exclusion ", "  spotlight-exclusion ", "  feature ", "Files and secrets:", "  file ", "  path ", "  secret ", "Your own:", "  check ", "  hourly ", "  manual "} {
		i := strings.Index(out[at:], want)
		if i < 0 {
			t.Fatalf("kit --help printed\n%s\nwant %q after what's before it", out, want)
		}
		at += i
	}
	if code != 0 || strings.Contains(out, "\n  add ") || strings.Contains(out, "\n  done ") {
		t.Errorf("kit --help printed\n%s exit %d; want no add, remove or done of its own", out, code)
	}
}

// kit nightly runs jobs by name, so none is named add or remove.
func TestANightlyJobIsntNamedAdd(t *testing.T) {
	w := laptopWorld(t)
	_, errOut, code := w.run(t, "nightly", "add", "add", "--", "true")
	if code != 2 || !strings.Contains(errOut, "a nightly job can't be named add: kit nightly add is the command") {
		t.Errorf("printed %q, exit %d", errOut, code)
	}
	if got := w.readSection(t, "laptop", "nightly"); got != "" {
		t.Errorf("[nightly] = %q, want nothing declared", got)
	}
}
