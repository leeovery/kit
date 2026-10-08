package cli_test

import (
	"strings"
	"testing"
)

func TestAddACheckRunsItOnce(t *testing.T) {
	w := laptopWorld(t)
	w.fake.On("dig", "+short", "@127.0.0.1", "example.com").Exits(1).Prints(";; connection timed out\n")
	w.expectSync([]string{"laptop/declarations"}, "kit check add dns (laptop): AdGuard answers")
	out, _, code := w.run(t, "check", "add", "dns", "--note", "AdGuard answers", "--", "dig", "+short", "@127.0.0.1", "example.com")
	if code != 0 || !strings.Contains(out, "dns ok declared in laptop; it fails now: ;; connection timed out") {
		t.Errorf("kit check add printed\n%s exit %d", out, code)
	}
	if got := w.readSection(t, "laptop", "checks"); got != "dns -- dig +short @127.0.0.1 example.com   # AdGuard answers\n" {
		t.Errorf("[checks] = %q", got)
	}
	w.expectSync([]string{"laptop/declarations"}, "kit check remove dns (laptop)")
	if out, _, code := w.run(t, "check", "remove", "dns"); code != 0 || w.readSection(t, "laptop", "checks") != "" {
		t.Errorf("kit check remove printed\n%s exit %d", out, code)
	}
}

func TestAddAJob(t *testing.T) {
	w := laptopWorld(t)
	w.expectSync([]string{"shared/declarations"}, "kit hourly add marks (laptop)")
	if out, _, code := w.run(t, "hourly", "add", "marks", "--shared", "--", "asimov", "~/Code"); code != 0 {
		t.Errorf("kit hourly add printed\n%s exit %d", out, code)
	}
	if got := w.readSection(t, "shared", "hourly"); got != "marks -- asimov ~/Code\n" {
		t.Errorf("[hourly] = %q", got)
	}
	_, errOut, code := w.run(t, "nightly", "add", "tidy", "tidy-up")
	if code != 2 || !strings.Contains(errOut, "say it as kit nightly add <name> -- <command>") {
		t.Errorf("kit nightly add without -- printed %q, exit %d", errOut, code)
	}
}

func TestStepsByHand(t *testing.T) {
	w := laptopWorld(t)
	w.expectSync([]string{"laptop/declarations"}, "kit manual add sign-in (laptop)")
	if out, _, code := w.run(t, "manual", "add", "sign-in", "Sign in to the service once"); code != 0 {
		t.Errorf("kit manual add printed\n%s exit %d", out, code)
	}
	if got := w.readSection(t, "laptop", "manual"); got != "sign-in \"Sign in to the service once\"\n" {
		t.Errorf("[manual] = %q", got)
	}
	out, _, _ := w.run(t, "status")
	if !strings.Contains(out, "manual attention 0 of 1 done\nmanual manual Sign in to the service once (then: kit manual done sign-in)\n") {
		t.Errorf("kit status printed\n%s", out)
	}
	if out, _, code := w.run(t, "manual", "done", "sign-in"); code != 0 || out != "Marked done on this Mac: [sign-in]\n" {
		t.Errorf("kit manual done printed %q, exit %d", out, code)
	}
	if out, _, _ := w.run(t, "status"); !strings.Contains(out, "manual ok all 1 done\n") {
		t.Errorf("after kit done, kit status printed\n%s", out)
	}
	if _, errOut, code := w.run(t, "manual", "done", "nothing"); code != 2 || !strings.Contains(errOut, "no step by hand named nothing") {
		t.Errorf("kit manual done nothing printed %q, exit %d", errOut, code)
	}
}

func TestAStepByHandKitChecksItself(t *testing.T) {
	w := laptopWorld(t)
	w.fake.On("test", "-d", "/Applications/Tool.app").Exits(1)
	w.expectSync([]string{"laptop/declarations"}, "kit manual add tool (laptop)")
	out, _, code := w.run(t, "manual", "add", "tool", "Install the tool from its site", "--", "test", "-d", "/Applications/Tool.app")
	if code != 0 || !strings.Contains(out, "tool ok declared in laptop; not done yet") {
		t.Errorf("kit manual add printed\n%s exit %d", out, code)
	}
	out, _, _ = w.run(t, "status")
	if !strings.Contains(out, "manual attention 0 of 1 done\nmanual manual Install the tool from its site\n") {
		t.Errorf("kit status printed\n%s", out)
	}
}
