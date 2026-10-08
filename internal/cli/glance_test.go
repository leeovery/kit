package cli_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/status"
)

// Bare kit is the at-a-glance view: a line an area, attention exiting 1.
func TestBareKit(t *testing.T) {
	out, errOut, code := laptopWorld(t).run(t)
	want := `kit · laptop
ok        Mac     48% free · 0.3 GB swap · load 1.9
attention Drift   brew ffmpeg (not declared, 2 days), brew node@20 (unused, 2 days), cask firefox (not declared, 2 days)  → kit reconcile
ok        Config  pushed · private
`
	if out != want || errOut != "" || code != 1 {
		t.Errorf("kit printed\n%s%q exit %d\nwant\n%s", out, errOut, code, want)
	}
}

func TestBareKitJSON(t *testing.T) {
	out, _, code := laptopWorld(t).run(t, "--json")
	var doc status.Document
	if err := json.Unmarshal([]byte(out), &doc); err != nil || code != 1 || !doc.Attention {
		t.Fatalf("kit --json printed %q, exit %d: %v", out, code, err)
	}
	areas := map[string]bool{}
	for _, s := range doc.Steps {
		areas[s.Area] = true
	}
	if !areas["Drift"] || !areas["Mac"] || !areas["Config"] {
		t.Errorf("areas = %v", areas)
	}
}

func TestKitRefusesAnUnknownCommand(t *testing.T) {
	_, errOut, code := laptopWorld(t).run(t, "nosuch")
	if !strings.Contains(errOut, `unknown command "nosuch"`) || code != 2 {
		t.Errorf("kit nosuch printed %q, exit %d", errOut, code)
	}
}

// At a terminal, kit's home offers what to run next, one list under it, the
// cursor on what's needed first; kit becomes the command chosen, under the
// home, the line chosen left on screen; q leaves the home as it is.
func TestBareKitMenu(t *testing.T) {
	w := laptopWorld(t)
	w.terminal = true
	var menus [][]string
	var picks []string
	w.pick = func(lines []ask.Line) (string, error) {
		var shown []string
		for _, l := range lines {
			text := l.Text
			if l.Start {
				text = l.Chosen
			}
			shown = append(shown, ansi.Strip(text))
		}
		menus = append(menus, shown)
		if len(picks) == 0 {
			return "", ask.ErrCancelled
		}
		picked := picks[0]
		picks = picks[1:]
		return picked, nil
	}

	picks = []string{"reconcile"}
	out, _, code := w.run(t)
	want := []string{
		"  ❯ Reconcile  decide on 3 things · kit reconcile",
		"    Apply  install what's missing · kit apply",
		"    Status  every check · kit status",
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
	if len(w.became) != 1 || !slices.Equal(w.became[0], []string{"--under-home", "reconcile"}) || code != 1 {
		t.Errorf("kit became %q, exit %d; want kit --under-home reconcile", w.became, code)
	}
	if out = ansi.Strip(out); !strings.HasSuffix(out, "▲ Config  ffmpeg installed, not declared · 2 days · 2 more\n\n  ❯ Reconcile  decide on 3 things · kit reconcile\n") {
		t.Errorf("kit printed\n%s\nwant the home, then the line chosen", out)
	}

	menus, w.became, picks = nil, nil, []string{"prefs capture"}
	w.run(t)
	if len(w.became) != 1 || !slices.Equal(w.became[0], []string{"--under-home", "prefs", "capture"}) {
		t.Errorf("kit became %q; want kit --under-home prefs capture", w.became)
	}

	menus, w.became, picks = nil, nil, nil
	if out, _, code := w.run(t); len(menus) != 1 || w.became != nil || code != 1 || strings.Contains(ansi.Strip(out), "❯") {
		t.Errorf("q: kit became %q, exit %d, printed\n%s\nwant the home left as it is", w.became, code, ansi.Strip(out))
	}
}

// Run from the home's menu, a command's heading leaves the wordmark out: the
// home's is above it.
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
