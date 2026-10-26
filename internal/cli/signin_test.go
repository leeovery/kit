package cli_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/kind/login"
	"github.com/leeovery/kit/internal/kind/secret/onepassword"
	"github.com/leeovery/kit/internal/kind/secret/onepassword/optest"
	"github.com/leeovery/kit/internal/logs"
	"github.com/leeovery/kit/internal/runner"
)

// signInApp is 1Password's app, by its bundle id, as the store's driver
// names it.
const signInApp = "com.1password.1password"

// installApp puts an app whose bundle id is id in the home's Applications
// folder, where kit looks for apps.
func (w *world) installApp(t *testing.T, name, id string) {
	t.Helper()
	w.write(t, filepath.Join("Applications", name+".app", "Contents", "Info.plist"), `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key>
	<string>`+id+`</string>
</dict>
</plist>
`)
}

// finished is the result the step called step finished with, as the log
// has it, and whether it did.
func finished(records []logs.Record, step string) (check.Result, bool) {
	i := slices.IndexFunc(records, func(r logs.Record) bool { return r.Event == "step_finished" && r.Step == step })
	if i < 0 {
		return check.Result{}, false
	}
	return *records[i].Result, true
}

// At a terminal, the store the secrets are in brings its sign-in, which kit
// raises before the secrets sync when the store says it isn't signed in: a
// card of the store's words, watched, the store's app opened as it comes
// up.
func TestApplyRaisesTheStoresSignIn(t *testing.T) {
	w := secretsWorld(t)
	w.installApp(t, "1Password", signInApp)
	w.terminal, w.size = true, [2]int{160, 64}
	kb := &keyboard{}
	w.listen = kb.listen
	f := w.fake
	f.On("op", "whoami").Exits(1)
	f.On("op", "account", "list", "--format", "json").Prints("[]")
	f.On("osascript", "-e", `display notification "Your secrets come from 1Password." with title "kit" subtitle "Sign in to 1Password"`)
	f.On("osascript", "-l", "JavaScript", "-e", login.ReadScript).Prints("[]")
	f.On("open", "-b", signInApp).Does(func() { kb.press(t, ask.Key{Name: "ctrl+c"}) })

	if _, errOut, code := w.run(t, "apply"); code != 1 {
		t.Errorf("kit apply, stopped at the card, exit %d (%s), want 1", code, errOut)
	}
	records := w.lastLog(t)
	up := cards(records)
	if len(up) != 1 || up[0].Step != "1password-sign-in" || up[0].Title != "Sign in to 1Password" || !up[0].Tells {
		t.Errorf("cards logged = %+v, want 1Password's sign-in, kit telling when it's done", up)
	}
	opened := slices.DeleteFunc(f.Calls(), func(c string) bool { return !strings.HasPrefix(c, "open ") })
	if !slices.Equal(opened, []string{"open -b " + signInApp}) {
		t.Errorf("opened %q, want 1Password's app", opened)
	}
	if slices.Contains(f.Calls(), "op inject") {
		t.Errorf("ran %q: want nothing read before the sign-in", f.Calls())
	}
}

// Signed in already, nothing's raised: the secrets sync.
func TestApplySignedInRaisesNothing(t *testing.T) {
	w := secretsWorld(t)
	w.installApp(t, "1Password", signInApp)
	w.terminal, w.size = true, [2]int{160, 64}
	f := w.fake
	f.On("op", "whoami")
	optest.Holds(f, map[string]string{"op://vault/Item/Section/token": "the value"})
	f.On("osascript", "-l", "JavaScript", "-e", login.ReadScript).Prints("[]")
	w.choose = func(string, []string) (int, error) { return 1, nil }

	w.run(t, "apply")
	records := w.lastLog(t)
	if up := cards(records); len(up) > 0 || slices.ContainsFunc(f.Calls(), func(c string) bool { return strings.HasPrefix(c, "open -b") }) {
		t.Errorf("cards %+v, ran %q: want none raised, nothing opened", up, f.Calls())
	}
	if res, ok := finished(records, "1password-sign-in"); !ok || res.State != check.OK {
		t.Errorf("the sign-in finished %+v, %v; want it found done", res, ok)
	}
	if got := w.read(t, ".secrets.zsh"); !strings.Contains(got, "export TOKEN='the value'") {
		t.Errorf("~/.secrets.zsh = %q", got)
	}
}

// Signed in already, the sign-in waits for nothing: without the store's app
// it's found done, nothing raised, and the secrets sync.
func TestApplySignedInWaitsForNoApp(t *testing.T) {
	w := secretsWorld(t)
	w.terminal, w.size = true, [2]int{160, 64}
	f := w.fake
	f.On("op", "whoami")
	optest.Holds(f, map[string]string{"op://vault/Item/Section/token": "the value"})
	f.On("osascript", "-l", "JavaScript", "-e", login.ReadScript).Prints("[]")
	w.choose = func(string, []string) (int, error) { return 1, nil }

	w.run(t, "apply")
	records := w.lastLog(t)
	if res, ok := finished(records, "1password-sign-in"); !ok || res.State != check.OK || len(cards(records)) > 0 {
		t.Errorf("the sign-in finished %+v, %v, cards %+v: want it found done, nothing raised", res, ok, cards(records))
	}
	if res, ok := finished(records, "secret"); !ok || res.State != check.OK {
		t.Errorf("the secrets finished %+v, %v", res, ok)
	}
	if got := w.read(t, ".secrets.zsh"); !strings.Contains(got, "export TOKEN='the value'") {
		t.Errorf("~/.secrets.zsh = %q", got)
	}
}

// Without a terminal, no one's there to sign in: nothing's raised, and the
// secrets don't sync, saying why in the store's words.
func TestApplyPlainRaisesNoSignIn(t *testing.T) {
	w := secretsWorld(t)
	w.installApp(t, "1Password", signInApp)
	f := w.fake
	f.On("op", "whoami").Exits(1)
	f.On("op", "account", "list", "--format", "json").Prints("[]")

	out, _, _ := w.run(t, "apply")
	records := w.lastLog(t)
	if len(cards(records)) > 0 || strings.Contains(out, "1password-sign-in") {
		t.Errorf("kit apply printed\n%s\nwant nothing raised", out)
	}
	if res, ok := finished(records, "secret"); !ok || res.State != check.Deferred || res.Reason != (onepassword.Driver{}).About().SignedOut {
		t.Errorf("the secrets finished %+v, %v: want them not yet, in 1Password's words alone", res, ok)
	}
}

// The sign-in waits for the store's program and app: without the app,
// nothing's raised, and the sign-in says what it needed.
func TestApplySignInWaitsForTheApp(t *testing.T) {
	w := secretsWorld(t)
	w.terminal, w.size = true, [2]int{160, 64}
	f := w.fake
	f.On("op", "whoami").Exits(1)
	f.On("op", "account", "list", "--format", "json").Prints("[]")
	f.On("osascript", "-l", "JavaScript", "-e", login.ReadScript).Prints("[]")
	w.choose = func(string, []string) (int, error) { return 1, nil }

	w.run(t, "apply")
	records := w.lastLog(t)
	res, ok := finished(records, "1password-sign-in")
	if len(cards(records)) > 0 || !ok || res.State != check.Deferred || res.Reason != "needs 1Password, which isn't there yet" {
		t.Errorf("the sign-in finished %+v, %v, cards %+v: want it waiting for the app, nothing raised", res, ok, cards(records))
	}
}

// The sign-in waits for the store's program too: without it, nothing's
// raised, and the sign-in and the secrets say what they need.
func TestApplySignInWaitsForTheProgram(t *testing.T) {
	w := secretsWorld(t)
	w.installApp(t, "1Password", signInApp)
	w.terminal, w.size = true, [2]int{160, 64}
	f := w.fake
	f.On("op", "whoami").Fails(runner.ErrNotFound)
	f.On("op", "account", "list", "--format", "json").Fails(runner.ErrNotFound)
	f.On("osascript", "-l", "JavaScript", "-e", login.ReadScript).Prints("[]")
	w.choose = func(string, []string) (int, error) { return 1, nil }

	w.run(t, "apply")
	records := w.lastLog(t)
	for _, step := range []string{"1password-sign-in", "secret"} {
		if res, ok := finished(records, step); !ok || res.State != check.Deferred || res.Reason != "needs op, which isn't there yet" {
			t.Errorf("%s finished %+v, %v: want it waiting for op", step, res, ok)
		}
	}
	if up := cards(records); len(up) > 0 || slices.ContainsFunc(f.Calls(), func(c string) bool { return strings.HasPrefix(c, "open -b") }) {
		t.Errorf("cards %+v, ran %q: want none raised, nothing opened", up, f.Calls())
	}
}
