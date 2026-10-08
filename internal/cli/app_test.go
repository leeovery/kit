package cli_test

import (
	"slices"
	"strings"
	"testing"
)

const (
	xcodeListed = `{"adamID":497799835,"name":"Xcode"}` + "\n"
	bearListed  = `{"adamID":1091189122,"name":"Bear"}` + "\n"
	sleepSearch = `{"adamID":937984704,"name":"Amphetamine"}
{"adamID":946798523,"name":"Sleep Control Center"}
`
)

// appWorld is laptopWorld with the App Store's mas installed, Xcode
// installed from it, and Bear declared but missing.
func appWorld(t *testing.T) *world {
	t.Helper()
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "app store apps", "bear@1091189122\nxcode@497799835\n")
	w.fake.On("mas", "list", "--json").Prints(xcodeListed).Then().Prints(xcodeListed).Then().Prints(xcodeListed + bearListed)
	return w
}

func TestApplyInstallsAppsWithThePasswordAskedFirst(t *testing.T) {
	w := appWorld(t)
	w.terminal = true
	w.choose = func(string, []string) (int, error) { return 1, nil }
	w.fake.On("sudo", "-v")
	w.fake.On("sudo", "-n", "mas", "install", "1091189122")

	out, errOut, _ := w.run(t, "apply")
	if !strings.Contains(out, "bear@1091189122") {
		t.Errorf("kit apply printed\n%s%s\nwant Bear installed", out, errOut)
	}
	calls := w.fake.Calls()
	if sudo, install := slices.Index(calls, "sudo -v"), slices.Index(calls, "sudo -n mas install 1091189122"); sudo < 0 || install < sudo {
		t.Errorf("ran %q, want the password asked before the install", calls)
	}
}

func TestApplyWithoutATerminalLeavesAppsWaiting(t *testing.T) {
	w := appWorld(t)
	w.fake.On("sudo", "-n", "true").Exits(1).PrintsToStderr("sudo: a password is required")

	out, _, code := w.run(t, "apply")
	if want := "mas missing:new bear@1091189122 (needs an administrator's password: run kit apply at a terminal)\n"; !strings.Contains(out, want) || code != 1 {
		t.Errorf("kit apply printed\n%s exit %d; want Bear waiting", out, code)
	}
	for _, c := range w.fake.Calls() {
		if strings.Contains(c, "mas install") {
			t.Errorf("ran %q, want nothing installed", c)
		}
	}
}

// kit mas add finds the app from its name: several matches are a choice at
// a terminal, asked before anything else; the declaration names the app and
// its id.
func TestAddAnAppByName(t *testing.T) {
	w := laptopWorld(t)
	w.terminal = true
	w.fake.On("mas", "list", "--json").Prints(xcodeListed)
	w.fake.On("mas", "search", "--json", "sleep").Prints(sleepSearch)
	var offered []string
	w.choose = func(question string, options []string) (int, error) {
		if strings.HasPrefix(question, "sleep  App Store · which is it?") {
			offered = options
			return 1, nil
		}
		return 0, nil
	}
	w.fake.On("sudo", "-v")
	w.fake.On("sudo", "-n", "mas", "install", "946798523")
	w.expectSync([]string{"laptop/declarations"}, "kit mas add sleep-control-center@946798523 (laptop)")

	if _, errOut, code := w.run(t, "mas", "add", "sleep"); code != 0 {
		t.Fatalf("kit exit add %d: %s", code, errOut)
	}
	if want := []string{"Amphetamine (937984704)", "Sleep Control Center (946798523)"}; !slices.Equal(offered, want) {
		t.Errorf("offered %q, want %q", offered, want)
	}
	if got := w.readSection(t, "laptop", "app store apps"); got != "sleep-control-center@946798523\n" {
		t.Errorf("laptop = %q", got)
	}
}

func TestAddAnAppByNameWithoutATerminalSaysWhich(t *testing.T) {
	w := laptopWorld(t)
	w.fake.On("mas", "search", "--json", "sleep").Prints(sleepSearch)
	_, errOut, code := w.run(t, "mas", "add", "sleep")
	want := "kit: sleep could be any of these: name one\n  amphetamine@937984704   Amphetamine (937984704)\n  sleep-control-center@946798523   Sleep Control Center (946798523)\n"
	if errOut != want || code != 2 {
		t.Errorf("kit mas add sleep printed %q, exit %d\nwant %q", errOut, code, want)
	}
}
