package cli_test

import (
	"strings"
	"testing"
)

func TestAddACheckRunsItOnce(t *testing.T) {
	w := laptopWorld(t)
	w.fake.On("dig", "+short", "@127.0.0.1", "example.com").Exits(1).Prints(";; connection timed out\n")
	w.expectSync(t, []string{"laptop/declarations"}, "kit check add dns (laptop): AdGuard answers")
	out, _, code := w.run(t, "check", "add", "dns", "--note", "AdGuard answers", "--", "dig", "+short", "@127.0.0.1", "example.com")
	if code != 0 || !strings.Contains(out, "dns ok declared in laptop; it fails now: ;; connection timed out") {
		t.Errorf("kit check add printed\n%s exit %d", out, code)
	}
	if got := w.readSection(t, "laptop", "checks"); got != "dns -- dig +short @127.0.0.1 example.com   # AdGuard answers\n" {
		t.Errorf("[checks] = %q", got)
	}
	w.expectSync(t, []string{"laptop/declarations"}, "kit check remove dns (laptop)")
	if out, _, code := w.run(t, "check", "remove", "dns"); code != 0 || w.readSection(t, "laptop", "checks") != "" {
		t.Errorf("kit check remove printed\n%s exit %d", out, code)
	}
}

func TestAddAJob(t *testing.T) {
	w := laptopWorld(t)
	w.expectSync(t, []string{"shared/declarations"}, "kit hourly add marks (laptop)")
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

// A step by hand the Macs declare, given its name alone with --shared,
// moves to the shared file whole: its options, note, comments and steps as
// written. Given what to do without the options a Mac's line has, it's
// refused, naming both lines; and the name alone, no Mac declaring it.
func TestStepByHandMovedWhole(t *testing.T) {
	block := "# its permissions\nhammerspoon \"Open Hammerspoon\" --group Apps --after cask:hammerspoon   # automation\n  Allow it Accessibility\n"
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "manual", block)
	w.writeSection(t, "studio", "manual", block)
	out, _, code := w.run(t, "manual", "add", "hammerspoon", "Open Hammerspoon", "--group", "Apps", "--shared")
	if want := `laptop/declarations:6 has hammerspoon "Open Hammerspoon" --group Apps --after cask:hammerspoon, where this gives hammerspoon "Open Hammerspoon" --group Apps: give only its name to move it as written, or take that line out first`; code != 1 || !strings.Contains(out, want) {
		t.Errorf("kit manual add hammerspoon \"Open Hammerspoon\" --shared printed\n%s exit %d; want %q", out, code, want)
	}
	w.expectSync(t, []string{"laptop/declarations", "shared/declarations", "studio/declarations"}, "kit manual add hammerspoon (laptop)")
	if out, _, code := w.run(t, "manual", "add", "hammerspoon", "--shared"); code != 0 || !strings.Contains(out, "hammerspoon ok declared in shared, out of laptop and studio") {
		t.Fatalf("kit manual add hammerspoon --shared printed\n%s exit %d", out, code)
	}
	if got := w.readSection(t, "shared", "manual"); got != block {
		t.Errorf("shared [manual] = %q, want the Macs' block whole", got)
	}
	for _, mac := range []string{"laptop", "studio"} {
		if got := w.readSection(t, mac, "manual"); got != "" {
			t.Errorf("%s's [manual] = %q, want hammerspoon out of it", mac, got)
		}
	}
	if _, errOut, code := w.run(t, "manual", "add", "nothing", "--shared"); code != 2 || !strings.Contains(errOut, "no Mac declares nothing to move: say it as kit manual add <name>") {
		t.Errorf("kit manual add nothing --shared printed %q, exit %d", errOut, code)
	}
}

func TestStepsByHand(t *testing.T) {
	w := laptopWorld(t)
	w.expectSync(t, []string{"laptop/declarations"}, "kit manual add sign-in (laptop)")
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

// kit manual add --group puts the step by hand in its group, on its line
// before its command.
func TestAStepByHandInAGroup(t *testing.T) {
	w := laptopWorld(t)
	w.fake.On("test", "-d", "/Applications/Tool.app").Exits(1)
	w.expectSync(t, []string{"laptop/declarations"}, "kit manual add tool (laptop)")
	if out, _, code := w.run(t, "manual", "add", "tool", "Install the tool from its site", "--group", "First things", "--", "test", "-d", "/Applications/Tool.app"); code != 0 {
		t.Errorf("kit manual add --group printed\n%s exit %d", out, code)
	}
	if got := w.readSection(t, "laptop", "manual"); got != "tool \"Install the tool from its site\" --group \"First things\" --test test -d /Applications/Tool.app\n" {
		t.Errorf("[manual] = %q", got)
	}
}

func TestAStepByHandKitChecksItself(t *testing.T) {
	w := laptopWorld(t)
	w.fake.On("test", "-d", "/Applications/Tool.app").Exits(1)
	w.expectSync(t, []string{"laptop/declarations"}, "kit manual add tool (laptop)")
	out, _, code := w.run(t, "manual", "add", "tool", "Install the tool from its site", "--", "test", "-d", "/Applications/Tool.app")
	if code != 0 || !strings.Contains(out, "tool ok declared in laptop; not done yet") {
		t.Errorf("kit manual add printed\n%s exit %d", out, code)
	}
	out, _, _ = w.run(t, "status")
	if !strings.Contains(out, "manual attention 0 of 1 done\nmanual manual Install the tool from its site\n") {
		t.Errorf("kit status printed\n%s", out)
	}
}
