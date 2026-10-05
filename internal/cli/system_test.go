package cli_test

import (
	"strings"
	"testing"
)

func TestTouchIDForSudoAsksForThePasswordFirst(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "features", "touch-id-sudo\n")
	out, _, _ := w.run(t, "status")
	if !strings.Contains(out, "touch-id-sudo ok off\ntouch-id-sudo missing:new Touch ID for sudo (sudo asks for the password, not a fingerprint) (to install)\n") {
		t.Errorf("kit status printed\n%s", out)
	}

	// Without a terminal, kit never asks: it waits.
	w.fake.On("sudo", "-n", "true").Exits(1)
	out, _, _ = w.run(t, "apply", "touch-id-sudo")
	if !strings.Contains(out, "waiting for an administrator's password") {
		t.Errorf("kit apply printed\n%s", out)
	}

	w.terminal = true
	w.fake.On("sudo", "-v")
	w.fake.On("sudo", "-n", "-v")
	w.fake.On("sudo", "-n", "tee", w.home+"/etc/sudo_local")
	w.run(t, "apply", "touch-id-sudo")
	calls := strings.Join(w.fake.Calls(), "\n")
	if i, j := strings.Index(calls, "sudo -v"), strings.Index(calls, "sudo -n tee"); i < 0 || j < i {
		t.Errorf("ran\n%s\nwant the password asked for, then the line written", calls)
	}
}
