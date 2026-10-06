package cli_test

import (
	"strings"
	"testing"
)

func TestAddACheckRunsItOnce(t *testing.T) {
	w := laptopWorld(t)
	w.fake.On("dig", "+short", "@127.0.0.1", "example.com").Exits(1).Prints(";; connection timed out\n")
	w.expectSync([]string{"laptop/declarations"}, "kit add check dns (laptop): AdGuard answers")
	out, _, code := w.run(t, "add", "check", "dns", "--note", "AdGuard answers", "--", "dig", "+short", "@127.0.0.1", "example.com")
	if code != 0 || !strings.Contains(out, "dns ok declared in laptop; it fails now: ;; connection timed out") {
		t.Errorf("kit add check printed\n%s exit %d", out, code)
	}
	if got := w.readSection(t, "laptop", "checks"); got != "dns -- dig +short @127.0.0.1 example.com   # AdGuard answers\n" {
		t.Errorf("[checks] = %q", got)
	}
	w.expectSync([]string{"laptop/declarations"}, "kit remove check dns (laptop)")
	if out, _, code := w.run(t, "remove", "check", "dns"); code != 0 || w.readSection(t, "laptop", "checks") != "" {
		t.Errorf("kit remove check printed\n%s exit %d", out, code)
	}
}

func TestAddAJob(t *testing.T) {
	w := laptopWorld(t)
	w.expectSync([]string{"shared/declarations"}, "kit add hourly marks (laptop)")
	if out, _, code := w.run(t, "add", "hourly", "marks", "--shared", "--", "asimov", "~/Code"); code != 0 {
		t.Errorf("kit add hourly printed\n%s exit %d", out, code)
	}
	if got := w.readSection(t, "shared", "hourly"); got != "marks -- asimov ~/Code\n" {
		t.Errorf("[hourly] = %q", got)
	}
	_, errOut, code := w.run(t, "add", "nightly", "tidy", "tidy-up")
	if code != 2 || !strings.Contains(errOut, "say it as kit add nightly <name> -- <command>") {
		t.Errorf("kit add nightly without -- printed %q, exit %d", errOut, code)
	}
}

func TestStepsByHand(t *testing.T) {
	w := laptopWorld(t)
	w.expectSync([]string{"laptop/declarations"}, "kit add manual sign-in (laptop)")
	if out, _, code := w.run(t, "add", "manual", "sign-in", "Sign in to the service once"); code != 0 {
		t.Errorf("kit add manual printed\n%s exit %d", out, code)
	}
	if got := w.readSection(t, "laptop", "manual"); got != "sign-in \"Sign in to the service once\"\n" {
		t.Errorf("[manual] = %q", got)
	}
	out, _, _ := w.run(t, "status")
	if !strings.Contains(out, "manual attention 0 of 1 done\nmanual manual Sign in to the service once (then: kit done sign-in)\n") {
		t.Errorf("kit status printed\n%s", out)
	}
	if out, _, code := w.run(t, "done", "sign-in"); code != 0 || out != "Marked done on this Mac: [sign-in]\n" {
		t.Errorf("kit done printed %q, exit %d", out, code)
	}
	if out, _, _ := w.run(t, "status"); !strings.Contains(out, "manual ok all 1 done\n") {
		t.Errorf("after kit done, kit status printed\n%s", out)
	}
	if _, errOut, code := w.run(t, "done", "nothing"); code != 2 || !strings.Contains(errOut, "no step by hand named nothing") {
		t.Errorf("kit done nothing printed %q, exit %d", errOut, code)
	}
}

func TestAStepByHandKitChecksItself(t *testing.T) {
	w := laptopWorld(t)
	w.fake.On("test", "-d", "/Applications/Tool.app").Exits(1)
	w.expectSync([]string{"laptop/declarations"}, "kit add manual tool (laptop)")
	out, _, code := w.run(t, "add", "manual", "tool", "Install the tool from its site", "--", "test", "-d", "/Applications/Tool.app")
	if code != 0 || !strings.Contains(out, "tool ok declared in laptop; not done yet") {
		t.Errorf("kit add manual printed\n%s exit %d", out, code)
	}
	out, _, _ = w.run(t)
	if !strings.Contains(out, "Manual  Install the tool from its site  → kit status") {
		t.Errorf("bare kit printed\n%s", out)
	}
}
