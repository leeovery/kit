package cli_test

import (
	"strings"
	"testing"
	"time"
)

// A feature switched on brings its check into the run; one kit doesn't know
// is refused, saying where.
func TestFeaturesBringTheirChecks(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "shared", "features", "arq\n")
	w.fake.On("/Applications/Arq.app/Contents/Resources/arqc", "stats").Prints(`{"backupPlans": [{"name": "User data", "schedule": {"type": "Daily"}, "lastBackedUp": "` + w.now.Add(-2*time.Hour).UTC().Format(time.RFC3339) + `"}]}`)
	out, _, _ := w.run(t, "status", "arq")
	if !strings.Contains(out, "arq ok last backup 01:04\n") {
		t.Errorf("kit status arq printed\n%s", out)
	}
	if out, _, _ := w.run(t, "status", "time-machine"); strings.Contains(out, "time-machine ok") {
		t.Errorf("time-machine checked though not switched on:\n%s", out)
	}

	w.writeSection(t, "laptop", "features", "time-capsule\n")
	_, errOut, code := w.run(t, "status")
	if !strings.Contains(errOut, "laptop/declarations:5: kit doesn't know the feature time-capsule") || code != 2 {
		t.Errorf("kit status printed %q, exit %d", errOut, code)
	}
}

func TestFeatureOnAndOff(t *testing.T) {
	w := laptopWorld(t)
	w.expectSync([]string{"laptop/declarations"}, "kit feature on time-machine (laptop)")
	out, errOut, code := w.run(t, "feature", "on", "time-machine")
	if code != 0 || !strings.Contains(out, "time-machine ok on, in laptop\n") {
		t.Fatalf("kit feature on printed\n%s%s exit %d", out, errOut, code)
	}
	if got := w.readSection(t, "laptop", "features"); got != "time-machine\n" {
		t.Errorf("[features] = %q", got)
	}
	w.expectSync([]string{"laptop/declarations"}, "kit feature off time-machine (laptop)")
	if _, errOut, code := w.run(t, "feature", "off", "time-machine"); code != 0 {
		t.Fatalf("kit feature off: %s exit %d", errOut, code)
	}
	if got := w.readSection(t, "laptop", "features"); got != "" {
		t.Errorf("[features] = %q, want it gone", got)
	}
	if _, errOut, code := w.run(t, "feature", "on", "time-capsule"); !strings.Contains(errOut, "kit doesn't know the feature time-capsule") || code != 2 {
		t.Errorf("kit feature on time-capsule printed %q, exit %d", errOut, code)
	}
}

// Checks of the user's own run as a step of their own, each failing one an
// item.
func TestOwnChecksRun(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "checks", "dns -- dig +short @127.0.0.1 example.com   # the network's lookups\n")
	w.fake.On("dig", "+short", "@127.0.0.1", "example.com").Exits(9).Prints(";; connection timed out; no servers could be reached\n")
	out, _, code := w.run(t, "status", "checks")
	if !strings.Contains(out, "checks attention 1 of 1 failing\nchecks problem dns: ;; connection timed out; no servers could be reached (your check, laptop/declarations:5)\n") || code != 1 {
		t.Errorf("kit status checks printed\n%s exit %d", out, code)
	}
}
