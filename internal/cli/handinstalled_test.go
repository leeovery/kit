package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/status"
)

// caskroom has brew --caskroom name Homebrew's Caskroom in the world,
// holding a folder for each of apps' casks, by token, its install receipt
// saying it installed the app named: casks without apps, or none.
func (w *world) caskroom(t *testing.T, apps map[string]string) {
	t.Helper()
	room := filepath.Join("opt", "homebrew", "Caskroom")
	w.write(t, filepath.Join(room, ".keep"), "")
	for token, app := range apps {
		w.write(t, filepath.Join(room, token, ".metadata", "INSTALL_RECEIPT.json"), `{"uninstall_artifacts": [{"app": ["`+app+`"]}]}`)
	}
	w.fake.On("brew", "--caskroom").Prints(filepath.Join(w.home, room) + "\n")
}

// handWorld is laptop with the editor installed by hand, a cask installing
// it in Homebrew's catalogue, and first seen two days ago, so it needs
// attention; and the Kubius, which no cask installs.
func handWorld(t *testing.T) *world {
	t.Helper()
	w := laptopWorld(t)
	w.homebrewCache(t, `{"protected":"e30","signature":"c2ln"}
{"casks": {"editor": {"auto_updates": true, "raw_artifacts": [[":uninstall", {":quit": "com.example.editor"}], [":app", ["Editor.app"]]]}}}`)
	w.installApp(t, "Editor", "com.example.editor")
	w.installApp(t, "Kubius", "com.example.kubius")
	w.write(t, driftFile, `{"first_seen": {"brew:ffmpeg": "2025-12-31T00:00:00Z", "brew:node@20": "2025-12-31T00:00:00Z", "cask:firefox": "2025-12-31T00:00:00Z", "cask:editor": "2025-12-31T00:00:00Z"}}`)
	w.fake.On("brew", "info", "--json=v2", "--cask", "editor").Prints(`{"formulae": [], "casks": [{"token": "editor", "full_token": "editor", "old_tokens": [], "artifacts": [{"app": ["Editor.app"]}]}]}`)
	return w
}

// An app installed by hand that a cask installs is the casks' drift, as an
// undeclared cask is: status says what it's installed as, and kit
// reconcile settles it.
func TestStatusShowsAnAppInstalledByHand(t *testing.T) {
	w := handWorld(t)
	out, _, code := w.run(t, "status")
	if want := "cask attention 1 declared, all installed\ncask extra firefox\ncask by-hand editor (installed by hand as Editor.app)\n"; !strings.Contains(out, want) || code != 0 {
		t.Errorf("kit status printed\n%s exit %d; want it to hold\n%s", out, code, want)
	}
	out, _, _ = w.run(t, "status", "--json")
	var doc status.Document
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(doc.Steps, func(s status.Step) bool { return s.ID == "cask" })
	if i < 0 {
		t.Fatalf("no cask step in %s", out)
	}
	cask := doc.Steps[i]
	it := cask.Items[len(cask.Items)-1]
	if cask.Counts["by_hand"] != 1 || it.ID != "cask:editor" || it.State != "by-hand" || it.Detail != "installed by hand as Editor.app" || it.Since.IsZero() || it.Action != "" {
		t.Errorf("cask = %+v, want the editor installed by hand, nothing applying would do", cask)
	}
}

// New, it's quiet for a day, as other drift is.
func TestAnAppInstalledByHandIsNewForADay(t *testing.T) {
	w := handWorld(t)
	w.write(t, driftFile, "{}")
	out, _, code := w.run(t, "status")
	if want := "cask by-hand:new editor (installed by hand as Editor.app)\n"; !strings.Contains(out, want) || code != 0 {
		t.Errorf("kit status printed\n%s exit %d; want it to hold %q", out, code, want)
	}
}

// kit reconcile lists it with its choices: adopt, leave by hand, snooze;
// never remove, as kit deletes nothing it didn't install.
func TestReconcileListsAnAppInstalledByHand(t *testing.T) {
	w := handWorld(t)
	out, _, code := w.run(t, "reconcile", "cask")
	want := "kit reconcile · laptop\ncask:firefox extra since 31 Dec: --adopt, --remove or --snooze\n" +
		"cask:editor by-hand since 31 Dec (installed by hand as Editor.app): --adopt, --leave or --snooze\n"
	if out != want || code != 0 {
		t.Errorf("kit reconcile cask printed\n%s exit %d\nwant\n%s", out, code, want)
	}
	out, _, _ = w.run(t, "reconcile", "cask", "--json")
	var doc struct {
		Items []struct {
			ID      string   `json:"id"`
			State   string   `json:"state"`
			Detail  string   `json:"detail"`
			Choices []string `json:"choices"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || len(doc.Items) != 2 {
		t.Fatalf("kit reconcile --json printed %s: %v", out, err)
	}
	if it := doc.Items[1]; it.ID != "cask:editor" || it.State != "by-hand" || it.Detail != "installed by hand as Editor.app" || !slices.Equal(it.Choices, []string{"adopt", "leave", "snooze"}) {
		t.Errorf("its item = %+v", it)
	}
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"cask:editor", "--remove"}, "kit: cask:editor can't be removed: --adopt, --leave or --snooze\n"},
		{[]string{"cask:firefox", "--leave"}, "kit: cask:firefox can't be left: --adopt, --remove or --snooze\n"},
	} {
		if _, errOut, code := w.run(t, append([]string{"reconcile"}, tt.args...)...); errOut != tt.want || code != 2 {
			t.Errorf("kit reconcile %q printed %q, exit %d; want %q, exit 2", tt.args, errOut, code, tt.want)
		}
	}
}

// Adopting it declares its cask, for this Mac or every Mac, and installs
// it, Homebrew taking the app over: in one run, and one commit.
func TestReconcileAdoptsAnAppInstalledByHand(t *testing.T) {
	for _, tt := range []struct {
		args  []string
		scope string
	}{
		{[]string{"cask:editor", "--adopt"}, "laptop"},
		{[]string{"cask:editor", "--adopt", "--shared"}, "shared"},
	} {
		t.Run(tt.scope, func(t *testing.T) {
			w := handWorld(t)
			f := w.fake
			f.On("brew", "update", "--quiet")
			f.On("brew", "install", "--cask", "--adopt", "editor").Does(func() {
				f.On("brew", "list", "--cask", "--full-name", "-1").Prints("ghostty\nfirefox\neditor\n")
			})
			file := filepath.Join(tt.scope, "declarations")
			w.expectSync(t, []string{file}, "kit reconcile (laptop): adopt cask:editor")
			out, errOut, code := w.run(t, append([]string{"reconcile"}, tt.args...)...)
			if want := "cask:editor ok installed; declared in " + tt.scope + "\n"; !strings.Contains(out, want) || code != 0 {
				t.Errorf("kit reconcile printed\n%s%s exit %d; want it to hold %q", out, errOut, code, want)
			}
			if got := w.readSection(t, tt.scope, "homebrew casks"); !strings.Contains(got, "editor\n") {
				t.Errorf("%s's casks = %q, want the editor declared", tt.scope, got)
			}
			if !slices.Contains(f.Calls(), "brew install --cask --adopt editor") {
				t.Errorf("ran %q, want the editor's app adopted", f.Calls())
			}
		})
	}
}

// At a terminal, kit reconcile asks about it, saying what it's installed
// as: adopt it, for this Mac or every Mac, or leave it by hand, for good;
// left, it's never asked about again, even of every item, and status
// counts it with what's quiet.
func TestReconcileLeavesAnAppByHand(t *testing.T) {
	w := handWorld(t)
	w.terminal = true
	var offered []string
	w.choose = func(question string, options []string) (int, error) {
		if !strings.HasPrefix(question, "editor  cask") {
			return slices.Index(options, "Skip"), nil
		}
		offered = options
		return slices.Index(options, "Leave by hand"), nil
	}
	if _, errOut, code := w.run(t, "reconcile", "cask"); code != 0 {
		t.Fatalf("kit reconcile exit %d: %s", code, errOut)
	}
	if want := []string{"firefox  cask · installed, not declared · 2 days", "editor  cask · installed by hand as Editor.app, not declared · 2 days"}; !slices.Equal(w.asked, want) {
		t.Errorf("asked\n%s\nwant\n%s", strings.Join(w.asked, "\n"), strings.Join(want, "\n"))
	}
	if want := []string{"Adopt", "Adopt everywhere", "Leave by hand", "Snooze", "Skip"}; !slices.Equal(offered, want) {
		t.Errorf("offered %q, want %q", offered, want)
	}
	if got := w.read(t, driftFile); !strings.Contains(got, `"left_by_hand": {`+"\n"+`    "cask:editor": "2026-01-02T03:04:05Z"`) {
		t.Errorf("drift.json = %s, want the editor left by hand now", got)
	}

	w.asked = nil
	if _, errOut, code := w.run(t, "reconcile", "cask", "--all"); code != 0 || slices.ContainsFunc(w.asked, func(q string) bool { return strings.HasPrefix(q, "editor") }) {
		t.Errorf("kit reconcile --all asked %q, exit %d (%s); want the editor, left by hand, never asked about", w.asked, code, errOut)
	}
	w.terminal = false
	out, _, code := w.run(t, "status")
	if want := "cask by-hand:left editor (installed by hand as Editor.app)\n"; !strings.Contains(out, want) || code != 0 {
		t.Errorf("kit status printed\n%s exit %d; want it to hold %q", out, code, want)
	}
}

// Left by hand, by its id, nothing's committed; once kit cask add has
// declared its cask, adopting the app, it's drift no more, and kit forgets
// it was left.
func TestALeaveByHandEndsOnceItsCaskIsDeclared(t *testing.T) {
	w := handWorld(t)
	out, _, code := w.run(t, "reconcile", "cask:editor", "--leave")
	if !strings.Contains(out, "cask:editor ok left installed by hand\n") || !strings.Contains(out, "kit-config ok nothing changed\n") || code != 0 {
		t.Errorf("kit reconcile printed\n%s exit %d", out, code)
	}
	if got := w.read(t, driftFile); !strings.Contains(got, `"cask:editor": "2026-01-02T03:04:05Z"`) {
		t.Errorf("drift.json = %s, want the editor left by hand", got)
	}
	f := w.fake
	f.On("brew", "update", "--quiet")
	f.On("brew", "install", "--cask", "--adopt", "editor").Does(func() {
		f.On("brew", "list", "--cask", "--full-name", "-1").Prints("ghostty\nfirefox\neditor\n")
	})
	w.expectSync(t, []string{"laptop/declarations"}, "kit cask add editor (laptop)")
	if out, errOut, code := w.run(t, "cask", "add", "editor"); code != 0 {
		t.Fatalf("kit cask add printed\n%s%s exit %d", out, errOut, code)
	}
	if _, _, code := w.run(t, "status"); code != 0 {
		t.Fatalf("kit status exit %d", code)
	}
	if got := w.read(t, driftFile); strings.Contains(got, "cask:editor") {
		t.Errorf("drift.json = %s, want the editor forgotten once declared", got)
	}
}

// A leave is this Mac's alone, so --shared is refused; and the last answer
// holds: snoozed, a left item is left no more, but snoozed; adopted, it's
// declared and installed, and left no more.
func TestALeftAppAnsweredAgain(t *testing.T) {
	w := handWorld(t)
	if _, errOut, code := w.run(t, "reconcile", "cask:editor", "--leave", "--shared"); errOut != "kit: cask:editor is left by hand on this Mac only: drop --shared\n" || code != 2 {
		t.Errorf("kit reconcile --leave --shared printed %q, exit %d; want it refused, exit 2", errOut, code)
	}
	leave := func() {
		t.Helper()
		if _, errOut, code := w.run(t, "reconcile", "cask:editor", "--leave"); code != 0 {
			t.Fatalf("kit reconcile --leave exit %d: %s", code, errOut)
		}
	}
	leave()
	out, _, code := w.run(t, "reconcile", "cask:editor", "--snooze")
	if got := w.read(t, driftFile); !strings.Contains(out, "cask:editor ok snoozed till 9 Jan\n") || code != 0 || strings.Contains(got, "left_by_hand") || !strings.Contains(got, `"snoozed_until": {`) {
		t.Errorf("kit reconcile --snooze printed\n%s exit %d, drift.json %s; want the editor snoozed, left no more", out, code, got)
	}
	leave()
	f := w.fake
	f.On("brew", "update", "--quiet")
	f.On("brew", "install", "--cask", "--adopt", "editor").Does(func() {
		f.On("brew", "list", "--cask", "--full-name", "-1").Prints("ghostty\nfirefox\neditor\n")
	})
	w.expectSync(t, []string{"laptop/declarations"}, "kit reconcile (laptop): adopt cask:editor")
	out, _, code = w.run(t, "reconcile", "cask:editor", "--adopt")
	if got := w.read(t, driftFile); !strings.Contains(out, "cask:editor ok installed; declared in laptop\n") || code != 0 || strings.Contains(got, "left_by_hand") {
		t.Errorf("kit reconcile --adopt printed\n%s exit %d, drift.json %s; want the editor adopted, left no more", out, code, got)
	}
}

// When what's installed by hand can't be told, as Homebrew's catalogue is
// gone, a leave and when the app was first seen are kept, for when it can
// be told again.
func TestALeaveOutlastsALookThatFails(t *testing.T) {
	w := handWorld(t)
	if _, errOut, code := w.run(t, "reconcile", "cask:editor", "--leave"); code != 0 {
		t.Fatalf("kit reconcile --leave exit %d: %s", code, errOut)
	}
	if err := os.RemoveAll(filepath.Join(w.home, "Library", "Caches", "Homebrew", "api")); err != nil {
		t.Fatal(err)
	}
	if _, _, code := w.run(t, "status"); code != 0 {
		t.Fatalf("kit status exit %d", code)
	}
	if got := w.read(t, driftFile); !strings.Contains(got, `"left_by_hand": {`+"\n"+`    "cask:editor"`) || !strings.Contains(got, `"cask:editor": "2025-12-31T00:00:00Z"`) {
		t.Errorf("drift.json = %s, want the editor still left, first seen as before", got)
	}
}
