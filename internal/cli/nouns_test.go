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
	for _, want := range []string{"This Mac:", "  status ", "  apply ", "  reconcile ", "Packages:", "  brew ", "  login-item ", "Settings:", "  defaults ", "  git-config ", "  backup-exclusion ", "  spotlight-exclusion ", "Files, secrets and features:", "  file ", "  path ", "  secret ", "  feature ", "Your own:", "  check ", "  hourly ", "  manual "} {
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

// At a terminal, kit's help is its own page, in kit's look: what kit does,
// this Mac's commands with what each does, then what kit manages, a line a
// group, and the flags.
func TestHelpAtATerminal(t *testing.T) {
	w := laptopWorld(t)
	w.terminal = true
	out, _, code := w.run(t, "--help")
	at := 0
	for _, want := range []string{"│  help\n", "  kit sets a Mac up from a config repository, and keeps it that way.\n",
		"  THIS MAC\n  status  how this Mac stands against its config\n",
		"  PACKAGES  each takes add and remove\n  brew · cask · mas",
		"  SETTINGS  each takes add and remove\n  defaults · power",
		"  FILES, SECRETS AND FEATURES\n  file · path · secret · prefs · feature\n",
		"  YOUR OWN  each takes add and remove\n  check · hourly · manual · step\n",
		"  FLAGS\n  --json  print one JSON document, for scripts and agents\n"} {
		i := strings.Index(out[at:], want)
		if i < 0 {
			t.Fatalf("kit --help printed\n%s\nwant %q after what's before it", out, want)
		}
		at += i
	}
	if code != 0 || strings.Contains(out, "Usage:") {
		t.Errorf("kit --help: exit %d; want kit's own page, not cobra's", code)
	}
	out, _, _ = w.run(t, "brew", "add", "--help")
	if !strings.HasPrefix(out, "\n  kit brew add <name>... [flags]\n\n  Install formulae, and declare them.\n") || !strings.Contains(out, "  FLAGS\n  --note  why it's declared, kept after its name\n") {
		t.Errorf("kit brew add --help printed\n%s", out)
	}
}

// At a terminal, what went wrong is a row of its own, what to run about it
// under it; without one, a line.
func TestErrorsAtATerminal(t *testing.T) {
	w := laptopWorld(t)
	w.terminal = true
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"brw", "add", "jq"}, "\n  ✗ kit brw  no such command\n  │ → did you mean kit brew add jq\n"},
		{[]string{"brew", "add", "jq", "--group", "Go"}, "\n  ✗ kit brew add  unknown flag: --group\n  │ → kit brew add --help\n"},
		{[]string{"why", "nosuch"}, "\n  ▲ kit why  nosuch isn't declared for any Mac, nor installed here\n"},
	} {
		out, errOut, _ := w.run(t, tt.args...)
		if errOut != tt.want || out != "" {
			t.Errorf("kit %s printed %q, %q; want %q", strings.Join(tt.args, " "), out, errOut, tt.want)
		}
	}
	w.terminal = false
	if _, errOut, code := w.run(t, "brw", "add", "jq"); !strings.HasPrefix(errOut, `kit: unknown command "brw" for "kit"`) || code != 2 {
		t.Errorf("without a terminal: %q, exit %d", errOut, code)
	}
}
