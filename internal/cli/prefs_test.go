package cli_test

import (
	"strings"
	"testing"
)

// kit prefs: capture waits until it's switched on; start-fresh switches it
// on; kit status says how it stands once the feature is on.
func TestPrefsCommands(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "features", "prefs\n")
	out, _, code := w.run(t, "prefs", "capture")
	if code != 1 || !strings.Contains(out, "prefs attention paused: capture isn't switched on for this Mac\n") {
		t.Errorf("kit prefs capture printed\n%s exit %d", out, code)
	}
	if out, _, _ := w.run(t, "prefs", "pending"); out != "Nothing is waiting to be restored.\n" {
		t.Errorf("kit prefs pending printed %q", out)
	}
	if out, _, _ := w.run(t, "prefs", "pending", "--json"); out != "{\n  \"from\": \"\",\n  \"domains\": []\n}\n" {
		t.Errorf("kit prefs pending --json printed %q", out)
	}
	if out, _, code := w.run(t, "prefs", "start-fresh"); code != 0 || !strings.Contains(out, "Capture is switched on for this Mac") {
		t.Errorf("kit prefs start-fresh printed %q, exit %d", out, code)
	}
	out, _, _ = w.run(t, "status")
	if !strings.Contains(out, "prefs attention not captured yet\n") || !strings.Contains(out, "full-disk-access ok") {
		t.Errorf("kit status printed\n%s", out)
	}
}
