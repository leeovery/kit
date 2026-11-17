package cli_test

import (
	"strconv"
	"strings"
	"testing"
)

// kit's help lists the whole Mac's commands, then what kit manages, by
// group: a command for each kind of thing, which names it first.
func TestHelpGroupsTheCommands(t *testing.T) {
	out, _, code := laptopWorld(t).run(t, "--help")
	at := 0
	for _, want := range []string{"This Mac:", "  status ", "  apply ", "  reconcile ", "Packages:", "  brew ", "  login-item ", "Settings:", "  git-config ", "  defaults ", "  backup-exclusion ", "  spotlight-exclusion ", "Files, secrets and features:", "  file ", "  path ", "  secret ", "  feature ", "  backup ", "Your own:", "  check ", "  hourly ", "  manual "} {
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
		"  SETTINGS  each takes add and remove\n  git-config · defaults · power",
		"  FILES, SECRETS AND FEATURES\n  file · path · secret · prefs · feature · backup · restore\n",
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
		// A loader may have shown on the way, drawn over itself and taken down.
		if out = out[strings.LastIndex(out, "\r")+1:]; errOut != tt.want || out != "" {
			t.Errorf("kit %s printed %q, %q; want %q", strings.Join(tt.args, " "), out, errOut, tt.want)
		}
	}
	w.terminal = false
	if _, errOut, code := w.run(t, "brw", "add", "jq"); !strings.HasPrefix(errOut, `kit: unknown command "brw" for "kit"`) || code != 2 {
		t.Errorf("without a terminal: %q, exit %d", errOut, code)
	}
}

// With --json, what went wrong is a document on standard output, saying it
// and the status kit exits with, nothing on standard error; a command line
// kit couldn't read included, wherever --json is in it, and a lookup that
// found nothing, which exits 0.
func TestErrorsWithJSON(t *testing.T) {
	w := laptopWorld(t)
	for _, tt := range []struct {
		args []string
		says string
		exit int
	}{
		{[]string{"brew", "add", "jq", "--group", "Go", "--json"}, "unknown flag: --group", 2},
		{[]string{"--json", "brew", "add", "jq", "--group", "Go"}, "unknown flag: --group", 2},
		{[]string{"brw", "add", "jq", "--json"}, `unknown command \"brw\" for \"kit\"`, 2},
		{[]string{"s", "--json"}, "kit s could be kit secret, kit splash, kit spotlight-exclusion, kit status, kit step", 2},
		{[]string{"reconcile", "brew:nosuch", "--snooze", "--json"}, "no item brew:nosuch: kit reconcile lists them", 2},
		{[]string{"log", "apply", "--json"}, "no run of apply logged", 0},
	} {
		out, errOut, code := w.run(t, tt.args...)
		if want := "{\n  \"schema\": 1,\n  \"error\": \"" + tt.says + "\",\n  \"exit\": " + strconv.Itoa(tt.exit) + "\n}\n"; out != want || errOut != "" || code != tt.exit {
			t.Errorf("kit %s printed %q, %q, exit %d; want %q, exit %d", strings.Join(tt.args, " "), out, errOut, code, want, tt.exit)
		}
	}
}

// A command line kit couldn't read shows its command's usage after what
// went wrong, on standard error, without a terminal or with --plain; never
// on standard output, nor once the command line was read.
func TestUsageGoesToStandardError(t *testing.T) {
	w := laptopWorld(t)
	for _, terminal := range []bool{false, true} {
		w.terminal = terminal
		out, errOut, code := w.run(t, "brew", "add", "jq", "--group", "Go", "--plain")
		if out != "" || !strings.HasPrefix(errOut, "kit: unknown flag: --group\nUsage:\n  kit brew add <name>... [flags]\n") || code != 2 {
			t.Errorf("at a terminal %t: printed %q, %q, exit %d", terminal, out, errOut, code)
		}
	}
	w.terminal = false
	if out, errOut, code := w.run(t, "reconcile", "brew:nosuch", "--snooze"); out != "" || errOut != "kit: no item brew:nosuch: kit reconcile lists them\n" || code != 2 {
		t.Errorf("a command line read: printed %q, %q, exit %d; want no usage", out, errOut, code)
	}
}
