package cli_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/ask"
)

// Bare kit at a terminal is kit's menu, at once: nothing is checked, Status
// first; kit becomes the command chosen, under the menu's wordmark, the line
// chosen left on screen; q leaves nothing.
func TestBareKitMenu(t *testing.T) {
	w := laptopWorld(t)
	w.terminal = true
	var menus [][]string
	var picks []string
	w.pick = func(lines []ask.Line) (string, error) {
		var shown []string
		for _, l := range lines {
			shown = append(shown, ansi.Strip(l.Text))
		}
		menus = append(menus, shown)
		if len(picks) == 0 {
			return "", ask.ErrCancelled
		}
		picked := picks[0]
		picks = picks[1:]
		return picked, nil
	}

	picks = []string{"status"}
	out, _, code := w.run(t)
	want := []string{
		"    Status  every check · kit status",
		"    Apply  install what's missing · kit apply",
		"    Reconcile  settle what differs from the config · kit reconcile",
		"    Log  past runs · kit log",
		"    Nightly  run the jobs that are due, then every check · kit nightly",
		"    App settings  save apps' settings now · kit prefs capture",
		"    Secrets  fetch them from 1Password again · kit secret sync",
		"    Declared  what's declared for this Mac · kit list",
		"    Help  every command · kit --help",
	}
	if len(menus) != 1 || !slices.Equal(menus[0], want) {
		t.Errorf("the menu =\n%s\nwant\n%s", strings.Join(slices.Concat(menus...), "\n"), strings.Join(want, "\n"))
	}
	if calls := w.fake.Calls(); len(calls) > 0 {
		t.Errorf("the menu ran %q; want nothing checked", calls)
	}
	if len(w.became) != 1 || !slices.Equal(w.became[0], []string{"--under-home", "status"}) || code != 0 {
		t.Errorf("kit became %q, exit %d; want kit --under-home status", w.became, code)
	}
	if out = ansi.Strip(out); !strings.Contains(out, "▀█▀") || !strings.HasSuffix(out, "│  Fri 2 Jan · 03:04\n  █   █  ▄█▄    █    │\n\n  ❯ Status  every check · kit status\n") {
		t.Errorf("kit printed\n%s\nwant the wordmark, then the line chosen", out)
	}

	menus, w.became, picks = nil, nil, []string{"prefs capture"}
	w.run(t)
	if len(w.became) != 1 || !slices.Equal(w.became[0], []string{"--under-home", "prefs", "capture"}) {
		t.Errorf("kit became %q; want kit --under-home prefs capture", w.became)
	}

	menus, w.became, picks = nil, nil, nil
	if out, _, code := w.run(t); len(menus) != 1 || w.became != nil || code != 0 || out != "" {
		t.Errorf("q: kit became %q, exit %d, printed %q; want nothing", w.became, code, out)
	}
}

// Without a terminal, bare kit is kit's help: kit status reports.
func TestBareKitWithoutATerminal(t *testing.T) {
	w := laptopWorld(t)
	out, errOut, code := w.run(t)
	if !strings.Contains(out, "it's kit's menu") || errOut != "" || code != 0 || len(w.fake.Calls()) > 0 {
		t.Errorf("kit printed %q, %q, exit %d, ran %q; want its help, nothing run", out, errOut, code, w.fake.Calls())
	}
}

// Run from kit's menu, a command's heading leaves the wordmark out: the
// menu's is above it.
func TestUnderTheHomeNoSecondWordmark(t *testing.T) {
	w := laptopWorld(t)
	w.terminal = true
	if out, _, _ := w.run(t, "--under-home", "status"); strings.Contains(out, "▀█▀") || !strings.Contains(ansi.Strip(out), "CONFIG") {
		t.Errorf("kit --under-home status printed\n%s\nwant the report without the wordmark", ansi.Strip(out))
	}
	if out, _, _ := w.run(t, "status"); !strings.Contains(out, "▀█▀") {
		t.Error("kit status, from the shell: want the wordmark")
	}
}

// A command is known by the start of its name when no other's starts so, at
// every level; when others' do, kit says which, and runs none.
func TestCommandsByTheStartOfTheirName(t *testing.T) {
	w := laptopWorld(t)
	full, fullErr, fullCode := w.run(t, "reconcile")
	short, shortErr, shortCode := w.run(t, "rec")
	if short != full || shortErr != fullErr || shortCode != fullCode {
		t.Errorf("kit rec printed %q, %q, exit %d; want what kit reconcile does: %q, %q, exit %d", short, shortErr, shortCode, full, fullErr, fullCode)
	}
	if _, errOut, code := w.run(t, "s"); errOut != "kit: kit s could be kit secret, kit spotlight-exclusion, kit status, kit step\n" || code != 2 {
		t.Errorf("kit s printed %q, exit %d", errOut, code)
	}
	// Under a command, one that's not known shows that command's help, its
	// own commands listed.
	if out, errOut, _ := w.run(t, "claude-mcp", "o"); errOut != "" || !strings.Contains(out, "\n  off ") || !strings.Contains(out, "\n  on ") {
		t.Errorf("kit claude-mcp o printed %q, %q; want claude-mcp's help", out, errOut)
	}
	w.terminal = true
	_, errOut, code := w.run(t, "s")
	if got := ansi.Strip(errOut); got != "\n  ✗ kit s  more than one command starts so\n  │ → kit secret · kit spotlight-exclusion · kit status · kit step\n" || code != 2 {
		t.Errorf("at a terminal, kit s printed\n%s", got)
	}
}
