package cli_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// orderWorld is laptopWorld with apps' settings on, a secret, a cask held
// for it, npm packages, a step done by hand, and steps of the user's own:
// one placed nowhere, one after a cask, one after apps' settings, one
// needing the secret, one after the held cask, one after a cask this Mac
// doesn't declare, one after what isn't a step, and one after npm's.
func orderWorld(t *testing.T) *world {
	t.Helper()
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "features", "prefs\n")
	w.writeSection(t, "laptop", "secrets", "TOKEN op://vault/item/token\n")
	w.writeSection(t, "laptop", "homebrew casks --secret TOKEN", "private-app\n")
	w.writeSection(t, "laptop", "npm packages", "typescript\n")
	w.writeSection(t, "laptop", "manual", `by-hand "Do a thing by hand"`+"\n")
	w.writeSection(t, "laptop", "steps", `alone "stands alone"
uses-app "drives an app" --after cask:ghostty
after-settings "drives an app, its settings restored" --after prefs
needs-token "uses a token" --secret TOKEN
uses-private "uses a private app" --after cask:private-app
typo "names a cask not declared" --after cask:ghostie
broken "comes after nothing" --after nothere
after-npm "after npm's packages" --after npm
`)
	return w
}

// placed is kit agent order --json's document.
type placed struct {
	Schema                            int
	Machine, Name, Step, Thing, Never string
	RaisedBy                          string `json:"raised_by"`
	Before, After, Unordered, Absent  []spot
}

// spot is a step or thing in kit agent order --json's lists; App says a
// thing of no step is an app; Through names the step it's ahead only
// through.
type spot struct {
	Step, Thing, Says, Through string
	App                        bool
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

// names are spots' names, as kit agent order names them.
func names(spots []spot) []string {
	var out []string
	for _, s := range spots {
		switch {
		case s.Step == "" && s.App:
			out = append(out, s.Thing+" (app)")
		case s.Step == "":
			out = append(out, s.Thing+" (on PATH)")
		default:
			out = append(out, strings.TrimSuffix(s.Step+":"+s.Thing, ":"))
		}
	}
	return out
}

// A step placed nowhere has nothing before it, and the secrets' sync and
// apps' settings' restore are in no order with it: either may come first,
// which it says plainly. The host key's record, committed to kit-config,
// comes after it, as it may bring the key git signs with.
func TestOrderOfAStepPlacedNowhere(t *testing.T) {
	w := orderWorld(t)
	out, _, code := w.run(t, "agent", "order", "alone")
	want := `alone on a new Mac

Before it
  nothing: it can be among the first

After it
  host-key  SSH host key
  config    kit-config edits
  manual    steps done by hand, at the end, once the rest is done

In no order with it: either may come first
  secret    Secrets: each synced unless it fails, once Sign in to 1Password is
            done
  prefs     apps' settings restored, once Homebrew casks are installed
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
  homebrew        Homebrew, finished
  cask:ghostty    installed, likely never opened yet; its settings restored by
                  prefs, which may come before or after it
  brew (on PATH)  a program, waited for till it's on kit's PATH, whatever
                  installs it

After it
  host-key        SSH host key
  config          kit-config edits
  manual          steps done by hand, at the end, once the rest is done

In no order with it: either may come first
  secret          Secrets: each synced unless it fails, once Sign in to
                  1Password is done
  prefs           apps' settings restored, once Homebrew casks are installed
`
	if code != 0 || out != want {
		t.Errorf("kit agent order uses-app printed\n%s\nexit %d, want\n%s", out, code, want)
	}
}

// A step after apps' settings comes after every cask, their settings
// restored before it.
func TestOrderOfAStepAfterAppsSettings(t *testing.T) {
	doc := orderWorld(t).order(t, "after-settings")
	if got, want := names(doc.Before), []string{"homebrew", "cask", "secret:TOKEN", "prefs", "1password-sign-in", "brew (on PATH)"}; !slices.Equal(got, want) {
		t.Errorf("before it = %q, want %q", got, want)
	}
	if says := doc.Before[1].Says; says != "Homebrew casks: each installed unless it fails; likely never opened yet; their settings restored by prefs, before it" {
		t.Errorf("the casks before it say %q", says)
	}
	if token := doc.Before[2]; token.Through != "prefs" || token.Says != "synced (through prefs: may not be there)" {
		t.Errorf("the secret before it, through prefs, = %+v, want it said to be through prefs", token)
	}
	if doc.Before[3].Through != "" {
		t.Errorf("prefs before it = %+v, want it sure, a step it comes after itself", doc.Before[3])
	}
	if got := names(doc.Unordered); !slices.Equal(got, []string{"secret"}) {
		t.Errorf("in no order with it = %q, want the secrets' sync alone", got)
	}
}

// A step needing a secret has that secret synced by the time it applies;
// the rest of the secrets are in no order with it, which it says.
func TestOrderOfAStepNeedingASecret(t *testing.T) {
	doc := orderWorld(t).order(t, "needs-token")
	if got := names(doc.Before); !slices.Equal(got, []string{"secret:TOKEN", "1password-sign-in"}) || doc.Before[0].Says != "synced" {
		t.Errorf("before it = %+v, want TOKEN synced, after the password manager's sign-in", doc.Before)
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
	if got := names(doc.Before); !slices.Equal(got, []string{"homebrew", "brew (on PATH)"}) {
		t.Errorf("before it = %q, want Homebrew, and brew on kit's PATH", got)
	}
	if got, want := names(doc.After), []string{"prefs", "uses-app", "after-settings", "host-key", "config", "manual"}; !slices.Equal(got, want) {
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
  secret    no secrets are declared for this Mac
  prefs     kit prefs isn't on for this Mac, so no app's settings are restored
`
	if code != 0 || !strings.HasSuffix(out, want) {
		t.Errorf("kit agent order alone printed\n%s\nexit %d, want it to end\n%s", out, code, want)
	}
}

// A name the run hasn't is an error saying what it has, as kit status says.
func TestOrderOfWhatIsntThere(t *testing.T) {
	tests := []struct{ name, want string }{
		{"nosuch", "kit: no step named nosuch on this Mac: one of homebrew, path, brew, cask, npm, secret, prefs,"},
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

// The password manager's sign-in comes before any secret syncs: before a
// secret, before the secrets' sync, and so before what waits for either.
func TestOrderPlacesTheSignInBeforeTheSecrets(t *testing.T) {
	if doc := orderWorld(t).order(t, "secret:TOKEN"); !slices.Equal(names(doc.Before), []string{"1password-sign-in"}) {
		t.Errorf("before secret:TOKEN = %q, want the password manager's sign-in", names(doc.Before))
	}
	if doc := orderWorld(t).order(t, "secret"); !slices.Equal(names(doc.Before), []string{"1password-sign-in"}) {
		t.Errorf("before the secrets' sync = %q, want the password manager's sign-in", names(doc.Before))
	}
	doc := orderWorld(t).order(t, "1password-sign-in")
	if got := names(doc.After); !slices.Contains(got, "secret") || !slices.Contains(got, "needs-token") || len(doc.Unordered) != 0 {
		t.Errorf("after the sign-in = %q, in no order %q: want the secrets' sync and what needs a secret after it", got, names(doc.Unordered))
	}
}

// The password manager's sign-in waits for its program on kit's PATH and
// its app installed, whatever installs them: it's not among the first.
func TestOrderOfTheSignInWaitsForItsProgramAndApp(t *testing.T) {
	w := orderWorld(t)
	doc := w.order(t, "1password-sign-in")
	if got, want := names(doc.Before), []string{"op (on PATH)", "1Password (app)"}; !slices.Equal(got, want) {
		t.Fatalf("before the sign-in = %q, want %q", got, want)
	}
	if says := doc.Before[1].Says; says != "an app, waited for till it's installed, whatever installs it" {
		t.Errorf("1Password before it says %q", says)
	}
	out, _, code := w.run(t, "agent", "order", "1password-sign-in")
	if code != 0 || strings.Contains(out, "among the first") || !strings.Contains(out, "\nBefore it\n  op (on PATH) ") || !strings.Contains(out, "\n  1Password (app) ") {
		t.Errorf("kit agent order 1password-sign-in printed\n%s\nexit %d, want op and 1Password before it", out, code)
	}
}

// A step after a cask held for a secret has that secret, and the sign-in
// before it, in place first, as the cask is.
func TestOrderOfAStepAfterAHeldThing(t *testing.T) {
	doc := orderWorld(t).order(t, "uses-private")
	if got, want := names(doc.Before), []string{"homebrew", "cask:private-app", "secret:TOKEN", "1password-sign-in", "brew (on PATH)"}; !slices.Equal(got, want) {
		t.Errorf("before it = %q, want %q", got, want)
	}
}

// A kind waits for its program on kit's PATH, whatever installs it: it's
// not among the first.
func TestOrderOfAKindWaitingForItsProgram(t *testing.T) {
	w := orderWorld(t)
	out, _, code := w.run(t, "agent", "order", "npm")
	want := "Before it\n  npm (on PATH)  a program, waited for till it's on kit's PATH, whatever\n                 installs it\n"
	if code != 0 || !strings.Contains(out, want) || strings.Contains(out, "among the first") {
		t.Errorf("kit agent order npm printed\n%s\nexit %d, want it to say\n%s", out, code, want)
	}
	if doc := w.order(t, "after-npm"); !slices.Equal(names(doc.Before), []string{"npm", "npm (on PATH)"}) {
		t.Errorf("before a step after npm = %q, want npm's packages and npm on kit's PATH", names(doc.Before))
	}
}

// A step after a thing this Mac doesn't declare never applies: kit never
// installs that thing, so the step waits for it every run.
func TestOrderOfAStepAfterAThingNotDeclared(t *testing.T) {
	doc := orderWorld(t).order(t, "typo")
	if doc.Never != "it waits for cask:ghostie, which isn't declared for this Mac, so it's never installed" {
		t.Errorf("never = %q", doc.Never)
	}
	i := slices.Index(names(doc.Before), "cask:ghostie")
	if i < 0 || doc.Before[i].Says != "not declared for this Mac: never installed" {
		t.Errorf("before it = %+v, want cask:ghostie never installed", doc.Before)
	}
}

// A step whose line is wrong fails at once, and kit agent order says why,
// and nothing about where it falls.
func TestOrderOfABrokenStep(t *testing.T) {
	out, _, code := orderWorld(t).run(t, "agent", "order", "broken")
	said := strings.Join(strings.Fields(out), " ")
	if code != 0 || !strings.HasPrefix(said, "broken on a new Mac It fails at once, and never applies: laptop/declarations:") || !strings.HasSuffix(said, ": it comes after nothere, which isn't a step") || strings.Contains(out, "Before it") {
		t.Errorf("kit agent order broken printed\n%s\nexit %d, want it to say it fails, and why", out, code)
	}
}

// A step done by hand that nothing comes after is raised with the rest, at
// the end, and placed as they are.
func TestOrderOfAStepDoneByHandAtTheEnd(t *testing.T) {
	w := orderWorld(t)
	doc := w.order(t, "by-hand")
	if doc.Step != "manual" || doc.RaisedBy != "manual" || len(doc.After) != 0 || !slices.Contains(names(doc.Before), "alone") {
		t.Errorf("document = %+v, want it placed as manual, after the rest", doc)
	}
	out, _, _ := w.run(t, "agent", "order", "by-hand")
	if heading := "by-hand on a new Mac: a step done by hand, raised with the rest by manual\n"; !strings.HasPrefix(out, heading) {
		t.Errorf("kit agent order by-hand printed\n%s\nwant it to open\n%s", out, heading)
	}
}

// Only what a step waits for itself keeps it from applying: a step it comes
// after has only to finish, so what that step waited for, never installed,
// is said to be through it, and doesn't stop this one; nor does what a held
// section of a kind it comes after waits for.
func TestOrderOfAStepAfterOneThatNeverApplies(t *testing.T) {
	w := orderWorld(t)
	w.writeSection(t, "laptop", "homebrew casks --after brew:nosuch", "held-app\n")
	w.writeSection(t, "laptop", "steps", `first "after a cask not declared" --after cask:ghostie
then "after first" --after first
after-casks "after every cask" --after cask
`)
	doc := w.order(t, "then")
	i := slices.Index(names(doc.Before), "cask:ghostie")
	if doc.Never != "" || i < 0 || doc.Before[i].Through != "first" || doc.Before[i].Says != "not declared for this Mac: never installed (through first: may not be there)" {
		t.Errorf("never = %q, before it = %+v: want it to apply, cask:ghostie only through first", doc.Never, doc.Before)
	}
	if doc := w.order(t, "first"); doc.Never == "" {
		t.Errorf("first's never is empty, want it never applying: it waits for cask:ghostie itself")
	}
	doc = w.order(t, "after-casks")
	i = slices.Index(names(doc.Before), "brew:nosuch")
	if doc.Never != "" || i < 0 || doc.Before[i].Through != "cask" {
		t.Errorf("never = %q, before it = %+v: want it to apply, brew:nosuch only through cask", doc.Never, doc.Before)
	}
}
