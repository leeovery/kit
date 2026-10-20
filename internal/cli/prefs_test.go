package cli_test

import (
	"strings"
	"testing"
)

// kit prefs: capture waits until it's switched on; start-fresh switches it
// on; kit status says how it stands once the feature is on: fine for now,
// saving from tonight.
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
	if !strings.Contains(out, "prefs ok saving from tonight\n") || !strings.Contains(out, "full-disk-access ok") {
		t.Errorf("kit status printed\n%s", out)
	}
}

// kit prefs pending lists what waits, domains then settings files, each
// with why where the record says.
func TestPrefsPendingSaysWhy(t *testing.T) {
	w := laptopWorld(t)
	w.write(t, ".local/state/kit/prefs.json", `{"pending": {"from": "laptop", "domains": ["com.example.app", "com.example.old"], "files": ["~/.config/tool/settings.json"],
		"why": {"com.example.app": "not installed", "~/.config/tool/settings.json": "it couldn't be put in place"}}}`)
	want := "Waiting to be restored from laptop (kit prefs restore --pending):\n" +
		"  com.example.app: not installed\n" +
		"  com.example.old\n" +
		"  ~/.config/tool/settings.json: it couldn't be put in place\n"
	if out, _, _ := w.run(t, "prefs", "pending"); out != want {
		t.Errorf("kit prefs pending printed\n%s\nwant\n%s", out, want)
	}
}
