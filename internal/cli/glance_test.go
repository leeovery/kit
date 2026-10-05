package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/status"
)

// Bare kit is the at-a-glance view: a line an area, attention exiting 1.
func TestBareKit(t *testing.T) {
	out, errOut, code := laptopWorld(t).run(t)
	want := `kit · laptop
ok        Mac     48% free · 0.3 GB swap · load 1.9
attention Drift   brew ffmpeg (not declared, 2 days), brew node@20 (unused, 2 days), cask firefox (not declared, 2 days)  → kit reconcile
ok        Config  committed and pushed · private
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
