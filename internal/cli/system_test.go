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
	w.expectPassword()
	w.fake.On("sudo", "-n", "-v")
	w.fake.On("sudo", "-n", "tee", w.home+"/etc/sudo_local")
	w.run(t, "apply", "touch-id-sudo")
	calls := strings.Join(w.fake.Calls(), "\n")
	if i, j := strings.Index(calls, askedFor), strings.Index(calls, "sudo -n tee"); i < 0 || j < i {
		t.Errorf("ran\n%s\nwant the password asked for, then the line written", calls)
	}
}

// A step whose sudo -n finds the password forgotten mid-run, as once
// Composer's cleared it, is applied once kit's given it again: the step
// has the run's password to give again (afterAdmin).
func TestASystemStepRunsAgainOnceThePasswordsGivenAgain(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "features", "touch-id-sudo\n")
	w.terminal = true
	w.expectPassword()
	w.fake.On("sudo", "-n", "-v")
	w.fake.On("sudo", "-n", "tee", w.home+"/etc/sudo_local").Exits(1).PrintsToStderr("sudo: a password is required\n").Then()
	_, _, code := w.run(t, "apply", "touch-id-sudo")
	tees := 0
	for _, call := range w.fake.Calls() {
		if strings.HasPrefix(call, "sudo -n tee") {
			tees++
		}
	}
	if code != 0 || tees != 2 {
		t.Errorf("kit apply exited %d, wrote the line %d times; want it written on the second try (ran %q)", code, tees, w.fake.Calls())
	}
}
