package cli_test

import (
	"strings"
	"testing"
)

// A feature switched on brings its steps into the run; one kit doesn't
// know is refused, saying where and naming those it has, as scratch and
// oh-my-zsh are now kit has them no longer.
func TestFeaturesBringTheirSteps(t *testing.T) {
	for feature, label := range map[string]string{"remote-login": "com.openssh.sshd", "file-sharing": "com.apple.smbd", "screen-sharing": "com.apple.screensharing"} {
		w := laptopWorld(t)
		w.writeSection(t, "laptop", "features", feature+"\n")
		w.fake.On("launchctl", "print-disabled", "system").Prints("disabled services = {\n\t\"" + label + "\" => enabled\n}\n")
		if feature == "screen-sharing" {
			w.fake.On("/usr/bin/sqlite3", "-readonly", "/Library/Application Support/com.apple.TCC/TCC.db", "select service, auth_value from access where client='com.apple.screensharing.agent'").Prints("kTCCServicePostEvent|2\nkTCCServiceScreenCapture|2\n")
		}
		if out, _, _ := w.run(t, "status", feature); !strings.Contains(out, feature+" ok on\n") {
			t.Errorf("kit status %s printed\n%s", feature, out)
		}
	}

	for _, tt := range []struct{ scope, header, line, want string }{
		{"laptop", "features", "time-capsule\n", "laptop/declarations:5: kit doesn't know the feature time-capsule"},
		{"laptop", "features", "scratch\n", "laptop/declarations:5: kit doesn't know the feature scratch: one of file-sharing, prefs, remote-login, screen-sharing, touch-id-sudo\n"},
		{"shared", "features", "oh-my-zsh\n", "kit doesn't know the feature oh-my-zsh: one of file-sharing, prefs, remote-login, screen-sharing, touch-id-sudo\n"},
	} {
		w := laptopWorld(t)
		w.writeSection(t, tt.scope, tt.header, tt.line)
		if _, errOut, code := w.run(t, "status"); !strings.Contains(errOut, tt.want) || code != 2 {
			t.Errorf("[%s] %s: kit status printed %q, exit %d; want %q", tt.header, tt.line, errOut, code, tt.want)
		}
	}
}

// Screen Sharing whose viewers can only watch needs attention at once, as
// a to-do, never drift new for a day, and kit reconcile has nothing to
// settle of it: only System Settings grants control.
func TestScreenSharingIsAToDoNotDrift(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "features", "screen-sharing\n")
	w.fake.On("launchctl", "print-disabled", "system").Prints("disabled services = {\n\t\"com.apple.screensharing\" => enabled\n}\n")
	w.fake.On("/usr/bin/sqlite3", "-readonly", "/Library/Application Support/com.apple.TCC/TCC.db", "select service, auth_value from access where client='com.apple.screensharing.agent'").Prints("kTCCServicePostEvent|0\nkTCCServiceScreenCapture|2\n")
	out, _, _ := w.run(t, "status", "screen-sharing")
	if want := "screen-sharing attention on, but viewers can only watch\nscreen-sharing problem Turn Screen Sharing off and on again (System Settings › General › Sharing: turn Screen Sharing off, then on again)\n"; !strings.Contains(out, want) {
		t.Errorf("kit status screen-sharing printed\n%s\nwant\n%s", out, want)
	}
	if out, errOut, code := w.run(t, "reconcile", "--json"); code != 0 || strings.Contains(out, "screen-sharing") {
		t.Errorf("kit reconcile printed\n%s%s exit %d; want nothing of screen-sharing", out, errOut, code)
	}
}

func TestFeatureOnAndOff(t *testing.T) {
	w := laptopWorld(t)
	w.expectSync(t, []string{"laptop/declarations"}, "kit feature on remote-login (laptop)")
	out, errOut, code := w.run(t, "feature", "on", "remote-login")
	if code != 0 || !strings.Contains(out, "remote-login ok on, in laptop\n") {
		t.Fatalf("kit feature on printed\n%s%s exit %d", out, errOut, code)
	}
	if got := w.readSection(t, "laptop", "features"); got != "remote-login\n" {
		t.Errorf("[features] = %q", got)
	}
	w.expectSync(t, []string{"laptop/declarations"}, "kit feature off remote-login (laptop)")
	if _, errOut, code := w.run(t, "feature", "off", "remote-login"); code != 0 {
		t.Fatalf("kit feature off: %s exit %d", errOut, code)
	}
	if got := w.readSection(t, "laptop", "features"); got != "" {
		t.Errorf("[features] = %q, want it gone", got)
	}
	for _, name := range []string{"time-capsule", "scratch", "oh-my-zsh"} {
		if _, errOut, code := w.run(t, "feature", "on", name); !strings.Contains(errOut, "kit doesn't know the feature "+name+": one of file-sharing, prefs, remote-login, screen-sharing, touch-id-sudo\n") || code != 2 {
			t.Errorf("kit feature on %s printed %q, exit %d", name, errOut, code)
		}
	}
}

// Checks of the user's own run as a step of their own, each failing one an
// item.
func TestOwnChecksRun(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "checks", "dns -- dig +short @127.0.0.1 example.com   # the network's lookups\n")
	w.fake.On("dig", "+short", "@127.0.0.1", "example.com").Exits(9).Prints(";; connection timed out; no servers could be reached\n")
	out, _, code := w.run(t, "status", "checks")
	if !strings.Contains(out, "checks attention 1 of 1 failing\nchecks problem dns: ;; connection timed out; no servers could be reached (your check, laptop/declarations:5)\n") || code != 0 {
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
