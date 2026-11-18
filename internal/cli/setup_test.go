package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/backup/arq"
	"github.com/leeovery/kit/internal/backup/timemachine"
	"github.com/leeovery/kit/internal/boot/boottest"
	"github.com/leeovery/kit/internal/kind/defaults"
	"github.com/leeovery/kit/internal/runner"
)

// inUse is a MacBook Pro in use that kit doesn't look after yet: no
// config, GitHub's CLI signed in as someone, who sees repos; git with a
// setting of the person's, and macOS as it comes, nothing else of what kit
// declares on it.
func inUse(t *testing.T, repos map[string]bool) *world {
	t.Helper()
	w := newWorld(t, nil)
	w.gitHub = boottest.Serve(t, repos)
	f := w.fake
	f.On("system_profiler", "SPHardwareDataType", "-json").Prints(`{"SPHardwareDataType" : [{"_name" : "hardware_overview", "machine_name" : "MacBook Pro"}]}`)
	f.On("security", "find-generic-password", "-s", "kit-github", "-w").Exits(44)
	f.On("security", "delete-generic-password", "-s", "kit-github")
	f.On("gh", "auth", "token").Prints(boottest.Token + "\n")
	f.On("gh", "auth", "status", "--hostname", "github.com")
	f.On("gh", "extension", "list")
	f.On("git", "config", "--global", "--list", "-z").Prints("user.name\nSome One\x00")
	f.On("sw_vers", "-productVersion").Prints("26.1\n")
	for _, domain := range defaults.Watched {
		f.On("defaults", "export", domain, "-").Prints("<plist version=\"1.0\"><dict/></plist>")
		f.On("defaults", "-currentHost", "export", domain, "-").Prints("<plist version=\"1.0\"><dict/></plist>")
	}
	f.On("launchctl", "print-disabled", "system").Prints("disabled services = {\n}\n")
	f.On("defaults", "export", timemachine.Prefs, "-").Prints("<plist><dict/></plist>")
	f.On(arq.Arqc, "stats").Fails(runner.ErrNotFound)
	w.write(t, filepath.Join("Code", "kit", "main.go"), "package main\n")
	return w
}

// creates scripts GitHub's CLI creating kit-config, private, on the
// person's account, and cloning it, empty, on main and tracking it alone.
func (w *world) creates() {
	dir := filepath.Join(w.home, ".config", "kit")
	f := w.fake
	f.On("gh", "repo", "create", "kit-config", "--private").Prints("https://github.com/someone/kit-config\n")
	f.On("gh", "repo", "clone", "someone/kit-config", dir, "--", "--quiet", "--single-branch").Does(func() {
		_ = os.MkdirAll(filepath.Join(dir, ".git"), 0o700)
	})
	f.On("git", "-C", dir, "config", "credential.https://github.com.helper", "!gh auth git-credential")
	f.On("git", "-C", dir, "symbolic-ref", "HEAD", "refs/heads/main")
	f.On("git", "-C", dir, "config", "remote.origin.fetch", "+refs/heads/main:refs/remotes/origin/main")
	f.On("git", "-C", dir, "config", "branch.main.remote", "origin")
	f.On("git", "-C", dir, "config", "branch.main.merge", "refs/heads/main")
}

// setupDocument is kit setup --json's document, as an agent reads it.
type setupDocument struct {
	Machine string `json:"machine"`
	Steps   []struct {
		ID      string `json:"id"`
		State   string `json:"state"`
		Summary string `json:"summary"`
		Reason  string `json:"reason"`
	} `json:"steps"`
	Apply *struct {
		Machine string `json:"machine"`
	} `json:"apply"`
}

// stood is how each step in doc stood: its id, its state, and what it said.
func (doc setupDocument) stood() []string {
	out := make([]string, len(doc.Steps))
	for i, s := range doc.Steps {
		out[i] = s.ID + " " + s.State + " " + s.Summary + s.Reason
	}
	return out
}

func readSetup(t *testing.T, out string) setupDocument {
	t.Helper()
	var doc setupDocument
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("kit setup --json printed %q: %v", out, err)
	}
	return doc
}

// A Mac kit looks after already is refused: setup is for one it doesn't.
func TestSetupRefusesAMacKitLooksAfter(t *testing.T) {
	w := laptopWorld(t)
	_, errOut, code := w.run(t, "setup")
	if code != 2 || !strings.Contains(errOut, "holds a config already, so kit looks after this Mac: kit status says how it stands") {
		t.Errorf("kit setup on a Mac kit looks after printed %q, exit %d", errOut, code)
	}
}

// Without a terminal, setup creates kit-config and names the Mac as its
// flags say, takes the snapshot, and stops at the questions its flags
// don't answer, naming each flag and what it chooses from, sized as the
// disk has them, in one document. Run again with them, it carries on where it stopped: it
// declares the folders to bring back, switches apps' settings on, and
// commits and pushes kit.toml and the Mac's folder as one; then says what
// kit doesn't look after, and where secrets go.
func TestSetupWithoutATerminal(t *testing.T) {
	w := inUse(t, map[string]bool{})
	w.creates()

	out, errOut, code := w.run(t, "setup", "--json", "--create", "kit-config", "--mac", "laptop")
	doc := readSetup(t, out)
	stood := doc.stood()
	for _, want := range []string{
		"github ok someone · GitHub's CLI's sign-in",
		"kit-config ok someone/kit-config · created, private",
		"mac ok laptop",
		"snapshot ok 1: 1 git config",
		"restore failed no terminal to ask at: --restore <path> for each folder a new Mac brings back from your backups (~/Code ",
		"prefs failed no terminal to ask at: --prefs saves apps' settings every night, so a new Mac gets them back (suggested), or --no-prefs",
		"files ok none to link",
		"to-install ok nothing more",
		"commit deferred needs Your data, Apps' settings",
	} {
		if !slices.ContainsFunc(stood, func(s string) bool { return strings.HasPrefix(s, want) }) {
			t.Errorf("kit setup --json, flags wanting, stood\n%s\nwant it to hold %q", strings.Join(stood, "\n"), want)
		}
	}
	if code != 2 || !strings.Contains(errOut, "kit: no terminal to ask at: answer with --restore <path>") {
		t.Errorf("flags wanting, kit setup said %q, exit %d", errOut, code)
	}
	if got := w.read(t, filepath.Join(".config", "kit", "kit.toml")); got != "format = 1\nminimum_kit = \"0.1.0\"\nprimary = \"laptop\"\n\n[macs.laptop]\ndescription = \"MacBook Pro\"\n" {
		t.Errorf("kit.toml =\n%s", got)
	}

	w.expectCommit(t, commit{
		message: "kit setup (laptop): this Mac as it is", paths: []string{"kit.toml", "laptop"},
		files: []string{"kit.toml", "laptop/declarations"}, status: "?? kit.toml\x00?? laptop/declarations\x00",
	})
	out, errOut, code = w.run(t, "setup", "--json", "--restore", "~/Code", "--prefs", "--no-apply")
	stood = readSetup(t, out).stood()
	for _, want := range []string{
		"kit-config ok someone/kit-config · created, private",
		"restore ok 1 folder",
		"prefs ok every night",
		"commit ok 1 declared · committed and pushed kit.toml and laptop/",
		"by-hand ok none",
		"secrets ok kit keeps secrets in a store, never in kit-config: kit secret add",
	} {
		if !slices.ContainsFunc(stood, func(s string) bool { return strings.HasPrefix(s, want) }) {
			t.Errorf("kit setup --json, run again, stood\n%s\nwant it to hold %q", strings.Join(stood, "\n"), want)
		}
	}
	if code != 0 || errOut != "" {
		t.Errorf("run again, kit setup said %q, exit %d", errOut, code)
	}
	want := "[features]\nprefs\n\n[git config]\nuser.name \"Some One\"\n\n[macos settings]\n\n[github extensions]\n\n[restore]\n~/Code\n"
	if got := w.read(t, filepath.Join(".config", "kit", "laptop", "declarations")); got != want {
		t.Errorf("laptop/declarations =\n%s\nwant\n%s", got, want)
	}
	for _, call := range w.fake.Calls() {
		if strings.Count(call, "gh repo create") > 0 && call != "gh repo create kit-config --private" {
			t.Errorf("ran %s", call)
		}
	}
	if _, _, code := w.run(t, "setup"); code != 2 {
		t.Errorf("once done, kit setup exits %d; want it refused, kit looking after this Mac", code)
	}
}

// Once it's committed, setup carries on as kit apply, as the command
// would, in the same run: its steps, built from the config setup made.
func TestSetupThenApplies(t *testing.T) {
	w := inUse(t, map[string]bool{})
	w.creates()
	healthyMac(w)
	w.private()
	w.expectCommit(t, commit{
		message: "kit setup (laptop): this Mac as it is", paths: []string{"kit.toml", "laptop"},
		files: []string{"kit.toml", "laptop/declarations"}, status: "?? kit.toml\x00?? laptop/declarations\x00",
	})
	out, errOut, code := w.run(t, "setup", "--create", "kit-config", "--mac", "laptop", "--no-restore", "--no-prefs")
	for _, want := range []string{"kit setup · MacBook Pro\n", "commit ok 1 declared · committed and pushed kit.toml and laptop/\n", "kit apply · laptop\n", "git-config ok 1 declared, all set\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("kit setup printed\n%s%s\nwant it to hold %q", out, errOut, want)
		}
	}
	if code != 0 {
		t.Errorf("kit setup exited %d", code)
	}
}

// At a terminal, setup asks what its flags don't answer, each with its
// answer suggested: the person's kit-config offered to join; which of its
// Macs this is, or a new one, named as its model suggests; the folders to
// bring back, one by one; apps' settings.
func TestSetupAsksAtATerminal(t *testing.T) {
	w := inUse(t, map[string]bool{"someone/kit-config": true, "someone/notes": true})
	w.terminal = true
	dir := filepath.Join(w.home, ".config", "kit")
	w.fake.On("gh", "repo", "clone", "someone/kit-config", dir, "--", "--quiet", "--single-branch").Does(func() {
		w.write(t, filepath.Join(".config", "kit", ".git", "HEAD"), "ref: refs/heads/main\n")
		w.write(t, filepath.Join(".config", "kit", "kit.toml"), twoMacs)
		w.write(t, filepath.Join(".config", "kit", "laptop", "declarations"), "")
		w.write(t, filepath.Join(".config", "kit", "studio", "declarations"), "")
	})
	w.fake.On("git", "-C", dir, "config", "credential.https://github.com.helper", "!gh auth git-credential")
	w.choose = func(question string, options []string) (int, error) {
		switch {
		case strings.Contains(question, "which of your Macs"):
			return slices.Index(options, "A new one"), nil
		case strings.HasPrefix(question, "~/etc"), strings.HasPrefix(question, "Apps' settings"):
			return 1, nil
		}
		return 0, nil
	}
	w.expectCommit(t, commit{
		message: "kit setup (macbook-pro): this Mac as it is", paths: []string{"kit.toml", "macbook-pro"},
		files: []string{"kit.toml", "macbook-pro/declarations"}, status: " M kit.toml\x00?? macbook-pro/declarations\x00",
	})
	out, errOut, code := w.run(t, "setup", "--no-apply")
	want := []string{
		"kit-config  someone has kit-config",
		"This Mac  which of your Macs is this?",
		"This Mac  what will kit-config call it?",
		"This Mac  where does it start from?",
		"~/Code",
		"~/etc",
		"Apps' settings  save them every night, so a new Mac gets them back?",
	}
	if len(w.asked) != len(want) {
		t.Fatalf("asked\n%s\nwant\n%s", strings.Join(w.asked, "\n"), strings.Join(want, "\n"))
	}
	for i, q := range want {
		if !strings.HasPrefix(w.asked[i], q) {
			t.Errorf("asked %q, want %q", w.asked[i], q)
		}
	}
	for _, want := range []string{"kit-config ok someone/kit-config · joined · has laptop and studio\n", "mac ok macbook-pro · new\n", "restore ok 1 folder · ", "prefs ok not saved\n", "commit ok "} {
		if !strings.Contains(out, want) {
			t.Errorf("kit setup printed\n%s%s\nwant it to hold %q", out, errOut, want)
		}
	}
	if !strings.HasSuffix(w.read(t, filepath.Join(".config", "kit", "kit.toml")), "[macs.macbook-pro]\ndescription = \"MacBook Pro\"\n") || code != 0 {
		t.Errorf("kit.toml =\n%s\nexit %d", w.read(t, filepath.Join(".config", "kit", "kit.toml")), code)
	}
}
