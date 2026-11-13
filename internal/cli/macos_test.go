package cli_test

import (
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/kind/defaults"
)

// exports scripts defaults export for domain as a dict of body.
func (w *world) exports(domain, body string) {
	w.fake.On("defaults", "export", domain, "-").Prints("<?xml version=\"1.0\"?>\n<plist version=\"1.0\">\n<dict>" + body + "</dict>\n</plist>\n")
}

// macosWorld is laptopWorld with the Dock's size declared, every watched
// domain empty but the Dock's.
func macosWorld(t *testing.T, dock string) *world {
	t.Helper()
	w := laptopWorld(t)
	w.writeSection(t, "shared", "macos settings", "# Dock\ncom.apple.dock tilesize -int 60\n")
	for _, domain := range defaults.Watched {
		w.exports(domain, "")
	}
	w.exports("com.apple.dock", dock)
	return w
}

func TestReconcileAdoptsAndPutsBackSettings(t *testing.T) {
	w := macosWorld(t, "<key>tilesize</key><integer>60</integer>")
	w.run(t, "status")
	w.exports("com.apple.dock", "<key>tilesize</key><integer>48</integer><key>autohide</key><true/>")
	w.expectSync(t, []string{"shared/declarations"}, "kit reconcile (laptop): adopt defaults:com.apple.dock:tilesize")
	if out, _, code := w.run(t, "reconcile", "defaults:com.apple.dock:tilesize", "--adopt"); code != 0 || !strings.Contains(out, "declared as it is now, in shared") {
		t.Errorf("kit reconcile --adopt printed\n%s exit %d", out, code)
	}
	if got := w.readSection(t, "shared", "macos settings"); got != "# Dock\ncom.apple.dock tilesize -int 48\n" {
		t.Errorf("[macos settings] = %q", got)
	}
	w.fake.On("defaults", "delete", "com.apple.dock", "autohide")
	w.fake.On("killall", "Dock")
	out, _, code := w.run(t, "reconcile", "defaults:com.apple.dock:autohide", "--revert")
	if code != 0 || !strings.Contains(out, "put back as it was") {
		t.Errorf("kit reconcile --revert printed\n%s exit %d", out, code)
	}
}

func TestReconcileAdoptsASettingNeverSeenAsDeclared(t *testing.T) {
	w := macosWorld(t, "<key>tilesize</key><integer>48</integer>")
	out, _, _ := w.run(t, "reconcile", "--json")
	if !strings.Contains(out, `"id": "defaults:com.apple.dock:tilesize"`) || !strings.Contains(out, `"install",`) || !strings.Contains(out, `"adopt",`) {
		t.Errorf("kit reconcile --json printed\n%s\nwant the size to set or adopt", out)
	}
	w.expectSync(t, []string{"shared/declarations"}, "kit reconcile (laptop): adopt defaults:com.apple.dock:tilesize")
	if out, _, code := w.run(t, "reconcile", "defaults:com.apple.dock:tilesize", "--adopt"); code != 0 {
		t.Errorf("kit reconcile --adopt printed\n%s exit %d", out, code)
	}
	if got := w.readSection(t, "shared", "macos settings"); got != "# Dock\ncom.apple.dock tilesize -int 48\n" {
		t.Errorf("[macos settings] = %q", got)
	}
}
