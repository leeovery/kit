package cli_test

import (
	"strings"
	"testing"
)

// A feature switched on brings its steps into the run; one kit doesn't
// know is refused, saying where and naming those it has, as scratch and
// oh-my-zsh are now kit has them no longer; a backup tool is refused as a
// feature, saying where it goes now and the command declaring it there, as
// its section had it.
func TestFeaturesBringTheirSteps(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "features", "remote-login\n")
	w.fake.On("launchctl", "print-disabled", "system").Prints(`disabled services = {` + "\n" + `	"com.openssh.sshd" => enabled` + "\n}\n")
	if out, _, _ := w.run(t, "status", "remote-login"); !strings.Contains(out, "remote-login ok on\n") {
		t.Errorf("kit status remote-login printed\n%s", out)
	}

	for _, tt := range []struct{ scope, header, line, want string }{
		{"laptop", "features", "time-capsule\n", "laptop/declarations:5: kit doesn't know the feature time-capsule"},
		{"laptop", "features", "scratch\n", "laptop/declarations:5: kit doesn't know the feature scratch: one of file-sharing, prefs, remote-login, touch-id-sudo\n"},
		{"shared", "features", "oh-my-zsh\n", "kit doesn't know the feature oh-my-zsh: one of file-sharing, prefs, remote-login, touch-id-sudo\n"},
		{"laptop", "features", "time-machine\n", "laptop/declarations:5: time-machine is a backup tool, so it goes in [backups] now, not [features]: kit backup add time-machine, once its line's out"},
		{"laptop", "features --after cask:arq", "arq\n", "laptop/declarations:5: arq is a backup tool, so it goes in [backups] now, not [features]: kit backup add arq --after cask:arq, once its line's out"},
		{"shared", "features --after cask:arq", "arq\n", "arq is a backup tool, so it goes in [backups] now, not [features]: kit backup add arq --after cask:arq --shared, once its line's out"},
	} {
		w := laptopWorld(t)
		w.writeSection(t, tt.scope, tt.header, tt.line)
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
	for _, name := range []string{"time-capsule", "scratch", "oh-my-zsh"} {
		if _, errOut, code := w.run(t, "feature", "on", name); !strings.Contains(errOut, "kit doesn't know the feature "+name+": one of file-sharing, prefs, remote-login, touch-id-sudo\n") || code != 2 {
			t.Errorf("kit feature on %s printed %q, exit %d", name, errOut, code)
		}
	}
	if _, errOut, code := w.run(t, "feature", "on", "time-machine"); !strings.Contains(errOut, "time-machine is a backup tool, so it goes in [backups] now, not [features]: kit backup add time-machine\n") || code != 2 {
		t.Errorf("kit feature on time-machine printed %q, exit %d", errOut, code)
	}
	if _, errOut, code := w.run(t, "feature", "off", "arq", "--shared"); !strings.Contains(errOut, "arq is a backup tool, so it goes in [backups] now, not [features]: kit backup remove arq --shared\n") || code != 2 {
		t.Errorf("kit feature off arq --shared printed %q, exit %d", errOut, code)
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
