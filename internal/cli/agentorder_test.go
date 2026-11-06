package cli_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// orderWorld is laptopWorld with apps' settings on, a secret, and steps of
// the user's own: one placed nowhere, one after a cask, one after apps'
// settings, and one needing the secret.
func orderWorld(t *testing.T) *world {
	t.Helper()
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "features", "prefs\n")
	w.writeSection(t, "laptop", "secrets", "TOKEN op://vault/item/token\n")
	w.writeSection(t, "laptop", "steps", `alone "stands alone"
uses-app "drives an app" --after cask:ghostty
after-settings "drives an app, its settings restored" --after prefs
needs-token "uses a token" --secret TOKEN
`)
	return w
}

// placed is kit agent order --json's document.
type placed struct {
	Schema                           int
	Machine, Name, Step, Thing       string
	Before, After, Unordered, Absent []struct {
		Step, Thing, Says string
	}
}

// order runs kit agent order --json for name in w, failing the test unless
// it's placed.
func (w *world) order(t *testing.T, name string) placed {
	t.Helper()
	out, errOut, code := w.run(t, "agent", "order", name, "--json")
	var doc placed
	if err := json.Unmarshal([]byte(out), &doc); err != nil || code != 0 {
		t.Fatalf("kit agent order %s --json printed\n%s%s\nexit %d: %v", name, out, errOut, code, err)
	}
	return doc
}

// names are spots' names, as kit names a step or a step's thing.
func names(spots []struct{ Step, Thing, Says string }) []string {
	var out []string
	for _, s := range spots {
		out = append(out, strings.TrimSuffix(s.Step+":"+s.Thing, ":"))
	}
	return out
}

// A step placed nowhere has nothing before it, and the secrets' sync and
// apps' settings' restore are in no order with it: either may come first,
// which it says plainly.
func TestOrderOfAStepPlacedNowhere(t *testing.T) {
	w := orderWorld(t)
	out, _, code := w.run(t, "agent", "order", "alone")
	want := `alone on a new Mac

Before it
  nothing kit installs: it can be among the first

After it
  manual  steps done by hand, at the end, once the rest is done

In no order with it: either may come first
  secret  Secrets, every one synced, once Sign in to 1Password is done
  prefs   apps' settings restored, once Homebrew casks are installed
`
	if code != 0 || out != want {
		t.Errorf("kit agent order alone printed\n%s\nexit %d, want\n%s", out, code, want)
	}
}

// A cask a step comes after is installed by then, but never opened, and its
// settings restored where the prefs step falls: here, in no order with the
// step, which waits for that cask alone, where prefs waits for them all.
func TestOrderOfAStepAfterACask(t *testing.T) {
	w := orderWorld(t)
	out, _, code := w.run(t, "agent", "order", "uses-app")
	want := `uses-app on a new Mac

Before it
  homebrew      Homebrew, done
  cask:ghostty  installed, never opened; its settings restored by prefs, which
                may come before or after it

After it
  manual        steps done by hand, at the end, once the rest is done

In no order with it: either may come first
  secret        Secrets, every one synced, once Sign in to 1Password is done
  prefs         apps' settings restored, once Homebrew casks are installed
`
	if code != 0 || out != want {
		t.Errorf("kit agent order uses-app printed\n%s\nexit %d, want\n%s", out, code, want)
	}
}

// A step after apps' settings comes after every cask, their settings
// restored before it.
func TestOrderOfAStepAfterAppsSettings(t *testing.T) {
	doc := orderWorld(t).order(t, "after-settings")
	if got, want := names(doc.Before), []string{"homebrew", "cask", "prefs"}; !slices.Equal(got, want) {
		t.Errorf("before it = %q, want %q", got, want)
	}
	if says := doc.Before[1].Says; says != "Homebrew casks, every one installed, never opened; their settings restored by prefs, before it" {
		t.Errorf("the casks before it say %q", says)
	}
	if got := names(doc.Unordered); !slices.Equal(got, []string{"secret"}) {
		t.Errorf("in no order with it = %q, want the secrets' sync alone", got)
	}
}

// A step needing a secret has that secret synced by the time it applies;
// the rest of the secrets are in no order with it, which it says.
func TestOrderOfAStepNeedingASecret(t *testing.T) {
	doc := orderWorld(t).order(t, "needs-token")
	if got := names(doc.Before); !slices.Equal(got, []string{"secret:TOKEN"}) || doc.Before[0].Says != "synced" {
		t.Errorf("before it = %+v, want TOKEN synced", doc.Before)
	}
	if got := names(doc.Unordered); !slices.Equal(got, []string{"secret", "prefs"}) || !strings.HasSuffix(doc.Unordered[0].Says, "; only those listed before it are sure to come first") {
		t.Errorf("in no order with it = %+v, want the rest of the secrets, and apps' settings", doc.Unordered)
	}
}

// A kind's thing is placed as its kind's step is, what waits for that thing
// after it, as what waits for the whole kind is.
func TestOrderOfAThing(t *testing.T) {
	doc := orderWorld(t).order(t, "cask:ghostty")
	if doc.Name != "cask:ghostty" || doc.Step != "cask" || doc.Thing != "ghostty" || doc.Machine != "laptop" || doc.Schema != 1 {
		t.Errorf("document = %+v", doc)
	}
	if got := names(doc.Before); !slices.Equal(got, []string{"homebrew"}) {
		t.Errorf("before it = %q, want Homebrew", got)
	}
	if got, want := names(doc.After), []string{"prefs", "uses-app", "after-settings", "manual"}; !slices.Equal(got, want) {
		t.Errorf("after it = %q, want %q", got, want)
	}
}

// Without secrets declared or apps' settings on, kit agent order says
// they're not in this Mac's run.
func TestOrderSaysWhatsNotInTheRun(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "steps", `alone "stands alone"`+"\n")
	out, _, code := w.run(t, "agent", "order", "alone")
	want := `
Not in this Mac's run
  secret  no secrets are declared for this Mac
  prefs   kit prefs isn't on for this Mac, so no app's settings are restored
`
	if code != 0 || !strings.HasSuffix(out, want) {
		t.Errorf("kit agent order alone printed\n%s\nexit %d, want it to end\n%s", out, code, want)
	}
}

// A name the run hasn't is an error saying what it has, as kit status says.
func TestOrderOfWhatIsntThere(t *testing.T) {
	tests := []struct{ name, want string }{
		{"nosuch", "kit: no step named nosuch on this Mac: one of homebrew, path, brew, cask, secret, prefs,"},
		{"cask:nosuch", "kit: cask:nosuch isn't declared for this Mac, or is declared off: kit list cask lists what is\n"},
		{"homebrew:jq", "kit: homebrew has no things of its own: name a step, or a kind's thing, as in cask:firefox\n"},
	}
	for _, tt := range tests {
		_, errOut, code := orderWorld(t).run(t, "agent", "order", tt.name)
		if code != 2 || !strings.HasPrefix(errOut, tt.want) {
			t.Errorf("kit agent order %s printed\n%s\nexit %d, want exit 2 and\n%s", tt.name, errOut, code, tt.want)
		}
	}
}
