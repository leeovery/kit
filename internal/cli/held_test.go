package cli_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/runner"
)

// heldSection is the header of the section of formulae held back for the
// token their releases need.
const heldSection = "homebrew formulae --secret TOKEN"

// heldWorld is laptopWorld declaring ripgrep, and fetcher in a section held
// back for TOKEN, a secret declared for the Mac, neither formula installed
// till kit installs it; fetcher's install prints the token it's given.
func heldWorld(t *testing.T) *world {
	t.Helper()
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "homebrew formulae", "go\nripgrep\n")
	w.writeSection(t, "laptop", heldSection, "# Its releases are private.\nowner/private/fetcher\n")
	w.writeSection(t, "laptop", itemSection, "TOKEN   Section/token\n")
	f := w.fake
	before, after := "go\njq\noniguruma\nowner/tap/tool\nnode@20\nffmpeg\n", "ripgrep\nowner/private/fetcher\n"
	f.On("brew", "list", "--formula", "--full-name", "-1").Prints(before).Then().Prints(before + after)
	f.On("brew", "leaves").Prints("go\njq\nowner/tap/tool\nnode@20\nffmpeg\n").Then().Prints("go\njq\nowner/tap/tool\nnode@20\nffmpeg\n" + after)
	f.On("brew", "leaves", "--installed-on-request").Prints("go\njq\nowner/tap/tool\nffmpeg\n").Then().Prints("go\njq\nowner/tap/tool\nffmpeg\n" + after)
	f.On("brew", "tap").Prints("owner/tap\n")
	f.On("brew", "info", "--json=v2", "--formula", "ripgrep").Prints(`{"formulae": [{"name": "ripgrep", "full_name": "ripgrep", "aliases": [], "oldnames": []}], "casks": []}`)
	f.On("brew", "update", "--quiet")
	f.On("brew", "install", "--formula", "ripgrep")
	f.On("brew", "install", "--formula", "owner/private/fetcher").Prints("fetching with plain-token-value\n").PrintsToStderr("authorised as plain-token-value\n")
	return w
}

// installed is the command that installed name, as the runner ran it, the
// variables it was run with and all.
func installed(t *testing.T, w *world, name string) runner.Command {
	t.Helper()
	for _, c := range w.fake.Commands() {
		if c.String() == "brew install --formula "+name {
			return c
		}
	}
	t.Fatalf("%s wasn't installed: ran %q", name, w.fake.Calls())
	return runner.Command{}
}

// tokens are the values of TOKEN env sets.
func tokens(env []string) []string {
	var values []string
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "TOKEN="); ok {
			values = append(values, v)
		}
	}
	return values
}

// noSecretKept fails the test when what kit printed, or a run's log, holds
// the token's value.
func noSecretKept(t *testing.T, w *world, printed string) {
	t.Helper()
	if strings.Contains(printed, "plain-token-value") {
		t.Error("kit printed the token's value")
	}
	logs, _ := filepath.Glob(filepath.Join(w.home, "Library", "Logs", "kit", "*.jsonl"))
	if len(logs) == 0 {
		t.Fatal("no run's log was written")
	}
	for _, l := range logs {
		if data, _ := os.ReadFile(l); strings.Contains(string(data), "plain-token-value") {
			t.Errorf("%s holds the token's value", filepath.Base(l))
		}
	}
}

// A section held back for a secret: the rest of its kind installs at once,
// the secret's synced meanwhile, though its step comes after the formulae's,
// then the section installs as one command, the secret set for it alone,
// its value kept from the screen and the log.
func TestALinesSectionWaitsForItsSecret(t *testing.T) {
	w := heldWorld(t)
	w.fake.On("op", "whoami")
	w.fake.On("op", "read", "op://vault/Item/Section/token").Prints("plain-token-value\n")

	out, errOut, _ := w.run(t, "apply")
	calls := w.fake.Calls()
	plain, synced, held := slices.Index(calls, "brew install --formula ripgrep"), slices.Index(calls, "op read op://vault/Item/Section/token"), slices.Index(calls, "brew install --formula owner/private/fetcher")
	if plain < 0 || synced < plain || held < synced {
		t.Fatalf("ran %q: want ripgrep installed, then TOKEN synced, then fetcher installed", calls)
	}
	if got := tokens(installed(t, w, "ripgrep").Env); got != nil {
		t.Error("the formulae not held back were installed with TOKEN set")
	}
	if got := tokens(installed(t, w, "owner/private/fetcher").Env); !slices.Equal(got, []string{"plain-token-value"}) {
		t.Error("fetcher wasn't installed with TOKEN set to its synced value, once")
	}
	if !strings.Contains(out, "brew installed owner/private/fetcher, ripgrep\n") && !strings.Contains(out, "brew installed ripgrep, owner/private/fetcher\n") {
		t.Errorf("kit apply printed\n%s%s\nwant both formulae installed", out, errOut)
	}
	noSecretKept(t, w, out+errOut)
}

// A secret synced in an earlier run is waited for no longer: the section's
// install has it set from kit's own secrets file, whatever the environment
// kit inherited holds.
func TestALinesSectionTakesItsSecretFromKitsFile(t *testing.T) {
	w := heldWorld(t)
	w.write(t, ".secrets.zsh", "# Written by kit.\nexport TOKEN='plain-token-value'\n")
	w.env["TOKEN"] = "inherited-value"
	w.environ = []string{"TOKEN=inherited-value"}

	out, errOut, _ := w.run(t, "apply")
	if got := tokens(installed(t, w, "owner/private/fetcher").Env); !slices.Equal(got, []string{"plain-token-value"}) {
		t.Errorf("fetcher was installed with TOKEN set to %d values, want kit's own alone", len(got))
	}
	for _, c := range w.fake.Calls() {
		if strings.HasPrefix(c, "op ") {
			t.Errorf("ran %s: the secret was in place, so nothing's synced", c)
		}
	}
	if slices.ContainsFunc(w.childEnv, func(kv string) bool { return strings.HasPrefix(kv, "TOKEN=") }) {
		t.Error("kit's programs inherit TOKEN")
	}
	noSecretKept(t, w, out+errOut)
}

// A secret that never comes leaves the section's lines missing, saying
// what isn't there, while the rest of the kind installs.
func TestALinesSectionWhoseSecretNeverComes(t *testing.T) {
	w := heldWorld(t)
	w.fake.On("op", "whoami").Exits(1)
	w.fake.On("op", "account", "list", "--format", "json").Prints("[]")
	w.fake.On("brew", "list", "--formula", "--full-name", "-1").Prints("go\njq\noniguruma\nowner/tap/tool\nnode@20\nffmpeg\n").
		Then().Prints("go\njq\noniguruma\nowner/tap/tool\nnode@20\nffmpeg\nripgrep\n")

	out, errOut, code := w.run(t, "apply")
	for _, c := range w.fake.Calls() {
		if c == "brew install --formula owner/private/fetcher" {
			t.Errorf("ran %s, though TOKEN never came", c)
		}
	}
	installed(t, w, "ripgrep")
	if !strings.Contains(out, "brew missing:new owner/private/fetcher (needs TOKEN, which isn't there yet)\n") || code != 1 {
		t.Errorf("kit apply printed\n%s%s\nexit %d; want fetcher missing, saying TOKEN isn't there", out, errOut, code)
	}
}

// A section needing a secret no [secrets] section declares fails its
// kind's step, saying so.
func TestALinesSectionNeedingAnUndeclaredSecret(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", itemSection, "TOKEN   Section/token\n")
	w.writeSection(t, "laptop", "homebrew formulae --secret NOPE", "owner/private/fetcher\n")
	out, _, code := w.run(t, "status")
	if !strings.Contains(out, "brew failed laptop/declarations: [homebrew formulae --secret NOPE]: it needs NOPE, which no [secrets] section declares\n") || code != 1 {
		t.Errorf("kit status printed\n%s\nexit %d", out, code)
	}
}

// A step of your own needing a secret runs its script with it set, from
// kit's own secrets file; one not declared fails it, saying so.
func TestAStepNeedingASecret(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", itemSection, "TOKEN   Section/token\n")
	w.write(t, ".secrets.zsh", "export TOKEN='plain-token-value'\n")
	w.writeSection(t, "laptop", "steps", `fetch "Fetches the releases" --secret TOKEN`+"\n"+`other "Needs what isn't declared" --secret NOPE`+"\n")
	run := w.stepScript(t, "laptop", "fetch")
	w.stepScript(t, "laptop", "other")
	w.fake.On(run, "check").Exits(1).Prints("not fetched\n").Then().Prints("fetched\n")
	w.fake.On(run, "apply")

	out, errOut, _ := w.run(t, "apply", "fetch")
	if !strings.Contains(out, "fetch ok fetched\n") {
		t.Errorf("kit apply fetch printed\n%s%s", out, errOut)
	}
	for _, c := range w.fake.Commands() {
		if c.Name == run && !slices.Equal(tokens(c.Env), []string{"plain-token-value"}) {
			t.Errorf("%s ran without TOKEN set from kit's file", c)
		}
	}
	noSecretKept(t, w, out+errOut)
	if out, _, _ := w.run(t, "status", "other"); !strings.Contains(out, "other failed laptop/declarations:") || !strings.Contains(out, ": it needs NOPE, which no [secrets] section declares\n") {
		t.Errorf("kit status other printed\n%s", out)
	}
}

// Lines in a section held back are the list's like any other: checked,
// listed, and removed from whichever of its kind's sections holds them,
// which goes once it's empty.
func TestHeldLinesAreTheListsLikeAnyOther(t *testing.T) {
	w := laptopWorld(t)
	w.write(t, filepath.Join(".config", "kit", "laptop", "declarations"), "[homebrew formulae --after cask:ghostty]\n# Wants the terminal first.\ngo\n")
	out, _, _ := w.run(t, "status")
	if !strings.Contains(out, "brew attention 3 declared, all installed\n") {
		t.Errorf("kit status printed\n%s", out)
	}
	out, _, _ = w.run(t, "list", "brew", "--json")
	if !strings.Contains(out, `"name": "go"`) || !strings.Contains(out, `"installed": true`) {
		t.Errorf("kit list brew --json printed\n%s", out)
	}
	w.fake.On("brew", "uninstall", "--formula", "go")
	w.expectSync([]string{"laptop/declarations"}, "kit brew remove go (laptop)")
	out, errOut, code := w.run(t, "brew", "remove", "go")
	if !strings.Contains(out, "go ok uninstalled; out of laptop\n") || code != 0 {
		t.Errorf("kit brew remove go printed\n%s%s exit %d", out, errOut, code)
	}
	if got := w.read(t, filepath.Join(".config", "kit", "laptop", "declarations")); got != "" {
		t.Errorf("laptop/declarations = %q, want the section gone with its line", got)
	}
}

// kit step add declares what a step needs with --secret, which must be a
// secret declared.
func TestAddAStepNeedingASecret(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", itemSection, "TOKEN   Section/token\n")
	out, _, code := w.run(t, "step", "add", "other", "Needs what isn't declared", "--secret", "NOPE")
	if code == 0 || !strings.Contains(out, "it needs NOPE, which no [secrets] section declares") {
		t.Errorf("kit step add --secret NOPE printed\n%s exit %d", out, code)
	}
	w.expectSync([]string{"laptop/declarations", "laptop/steps/fetch"}, "kit step add fetch (laptop)")
	out, _, code = w.run(t, "step", "add", "fetch", "Fetches the releases", "--secret", "TOKEN")
	if code != 0 || w.readSection(t, "laptop", "steps") != `fetch "Fetches the releases" --secret TOKEN`+"\n" {
		t.Errorf("kit step add --secret TOKEN printed\n%s exit %d; [steps] = %q", out, code, w.readSection(t, "laptop", "steps"))
	}
}

// kit reconcile installs a line held back as applying would: with its
// secret set from kit's own secrets file.
func TestReconcileInstallsAHeldLineWithItsSecret(t *testing.T) {
	w := heldWorld(t)
	w.write(t, ".secrets.zsh", "export TOKEN='plain-token-value'\n")
	w.write(t, driftFile, `{"first_seen": {"brew:owner/private/fetcher": "2025-12-31T00:00:00Z"}}`)
	out, errOut, code := w.run(t, "reconcile", "brew:owner/private/fetcher", "--install")
	if !strings.Contains(out, "brew:owner/private/fetcher ok installed\n") || code != 0 {
		t.Errorf("kit reconcile printed\n%s%s exit %d", out, errOut, code)
	}
	if got := tokens(installed(t, w, "owner/private/fetcher").Env); !slices.Equal(got, []string{"plain-token-value"}) {
		t.Error("the line held back was installed without TOKEN set from kit's file")
	}
	noSecretKept(t, w, out+errOut)
}
