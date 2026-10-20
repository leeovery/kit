package cli_test

import (
	"strings"
	"testing"
)

// A feature switched on brings its steps into the run; one kit doesn't
// know is refused, saying where; a backup tool is refused as a feature,
// saying where it goes now and what its line there is.
func TestFeaturesBringTheirSteps(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "features", "remote-login\n")
	w.fake.On("launchctl", "print-disabled", "system").Prints(`disabled services = {` + "\n" + `	"com.openssh.sshd" => enabled` + "\n}\n")
	if out, _, _ := w.run(t, "status", "remote-login"); !strings.Contains(out, "remote-login ok on\n") {
		t.Errorf("kit status remote-login printed\n%s", out)
	}

	for _, tt := range []struct{ header, line, want string }{
		{"features", "time-capsule\n", "laptop/declarations:5: kit doesn't know the feature time-capsule"},
		{"features", "time-machine\n", "laptop/declarations:5: time-machine is a backup tool, so it goes in [backups] now, not [features]: its line there is time-machine"},
		{"features --after cask:arq", "arq\n", "laptop/declarations:5: arq is a backup tool, so it goes in [backups] now, not [features]: its line there is arq --after cask:arq"},
	} {
		w := laptopWorld(t)
		w.writeSection(t, "laptop", tt.header, tt.line)
		if _, errOut, code := w.run(t, "status"); !strings.Contains(errOut, tt.want) || code != 2 {
			t.Errorf("[%s] %s: kit status printed %q, exit %d; want %q", tt.header, tt.line, errOut, code, tt.want)
		}
	}
}

func TestFeatureOnAndOff(t *testing.T) {
	w := laptopWorld(t)
	w.expectSync([]string{"laptop/declarations"}, "kit feature on remote-login (laptop)")
	out, errOut, code := w.run(t, "feature", "on", "remote-login")
	if code != 0 || !strings.Contains(out, "remote-login ok on, in laptop\n") {
		t.Fatalf("kit feature on printed\n%s%s exit %d", out, errOut, code)
	}
	if got := w.readSection(t, "laptop", "features"); got != "remote-login\n" {
		t.Errorf("[features] = %q", got)
	}
	w.expectSync([]string{"laptop/declarations"}, "kit feature off remote-login (laptop)")
	if _, errOut, code := w.run(t, "feature", "off", "remote-login"); code != 0 {
		t.Fatalf("kit feature off: %s exit %d", errOut, code)
	}
	if got := w.readSection(t, "laptop", "features"); got != "" {
		t.Errorf("[features] = %q, want it gone", got)
	}
	if _, errOut, code := w.run(t, "feature", "on", "time-capsule"); !strings.Contains(errOut, "kit doesn't know the feature time-capsule") || code != 2 {
		t.Errorf("kit feature on time-capsule printed %q, exit %d", errOut, code)
	}
	if _, errOut, code := w.run(t, "feature", "on", "time-machine"); !strings.Contains(errOut, "time-machine is a backup tool, so it goes in [backups] now, not [features]") || code != 2 {
		t.Errorf("kit feature on time-machine printed %q, exit %d", errOut, code)
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

// A feature's section placed after what isn't a step, or a thing of a step
// that isn't a kind's, fails the feature's steps, saying so.
func TestAFeatureWaitingForWhatIsntThere(t *testing.T) {
	for _, after := range []string{"nothing", "disk:x"} {
		t.Run(after, func(t *testing.T) {
			w := laptopWorld(t)
			w.writeSection(t, "laptop", "features --after "+after, "remote-login\n")
			out, _, code := w.run(t, "status", "remote-login")
			if want := "remote-login failed laptop/declarations: [features --after " + after + "]: it comes after " + after + ", which isn't a step\n"; !strings.Contains(out, want) || code != 1 {
				t.Errorf("kit status remote-login printed\n%s\nexit %d; want %q", out, code, want)
			}
		})
	}
}
