package cli_test

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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
	w.gitHub, w.repos = boottest.ServeRepos(t, repos)
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
// person's account, and cloning it, empty.
func (w *world) creates(t *testing.T) {
	w.fake.On("gh", "repo", "create", "someone/kit-config", "--private").Prints("https://github.com/someone/kit-config\n").
		Does(func() { w.repos.Add("someone/kit-config", true) })
	w.clones(t, "someone/kit-config", nil)
}

// clones scripts GitHub's CLI cloning repo into kit-config's folder,
// holding files, by name, and git there as git keeps it, kit setting and
// reading it: its origin; HEAD and what it tracks, on main and tracking it
// alone, as a single-branch clone is, or, with no files, on git's own first
// branch, tracking nothing, as an empty one is; and how it signs in.
func (w *world) clones(t *testing.T, repo string, files map[string]string) {
	dir := filepath.Join(w.home, ".config", "kit")
	git := func(args ...string) []string { return append([]string{"-C", dir}, args...) }
	var mu sync.Mutex
	has := map[string]string{}
	put := func(key, value string) func() {
		return func() {
			mu.Lock()
			defer mu.Unlock()
			has[key] = value
		}
	}
	get := func(key string) func(string) (string, error) {
		return func(string) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			if value, ok := has[key]; ok {
				return value + "\n", nil
			}
			return "", errors.New("")
		}
	}
	tracking := map[string]string{"remote.origin.fetch": "+refs/heads/main:refs/remotes/origin/main", "branch.main.remote": "origin", "branch.main.merge": "refs/heads/main"}
	for key, value := range tracking {
		w.fake.On("git", git("config", "--get", key)...).Filters(get(key))
		w.fake.On("git", git("config", key, value)...).Does(put(key, value))
	}
	w.fake.On("git", git("config", "--get", "credential.https://github.com.helper")...).Filters(get("helper"))
	w.fake.On("git", git("config", "credential.https://github.com.helper", "!gh auth git-credential")...).Does(put("helper", "!gh auth git-credential"))
	w.fake.On("git", git("symbolic-ref", "HEAD")...).Filters(get("HEAD"))
	w.fake.On("git", git("symbolic-ref", "HEAD", "refs/heads/main")...).Does(put("HEAD", "refs/heads/main"))
	w.fake.On("git", git("remote", "get-url", "origin")...).Filters(get("origin"))
	w.fake.On("git", git("remote")...).Prints("origin\n")
	w.fake.On("git", git("config", "--get", "remote.origin.url")...).Filters(get("origin"))
	w.fake.On("git", git("remote", "get-url", "--push", "--all", "origin")...).Filters(get("origin"))
	w.fake.On("gh", "repo", "view", "https://github.com/"+repo, "--json", "visibility", "--jq", ".visibility").Prints("PRIVATE\n")
	w.fake.On("git", git("for-each-ref", "--count=1", "--format=%(refname)", "refs/heads")...).Filters(func(string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if branch, ok := has["branch"]; ok {
			return branch + "\n", nil
		}
		return "", nil
	})
	heads := ""
	if len(files) > 0 {
		heads = head + "\trefs/heads/main\n"
	}
	w.fake.On("git", git("ls-remote", "--heads", "origin")...).Prints(heads)
	w.fake.On("gh", "repo", "clone", repo, dir, "--", "--quiet", "--single-branch").Does(func() {
		w.write(t, filepath.Join(".config", "kit", ".git", "HEAD"), "")
		for name, content := range files {
			w.write(t, filepath.Join(".config", "kit", name), content)
		}
		put("origin", "https://github.com/"+repo+".git")()
		if len(files) == 0 {
			put("HEAD", "refs/heads/master")()
			return
		}
		put("HEAD", "refs/heads/main")()
		put("branch", "refs/heads/main")()
		for key, value := range tracking {
			put(key, value)()
		}
	})
}

// setupDocument is kit setup --json's document, as an agent reads it.
type setupDocument struct {
	Machine string `json:"machine"`
	Steps   []struct {
		ID      string `json:"id"`
		State   string `json:"state"`
		Summary string `json:"summary"`
		Reason  string `json:"reason"`
		Items   []struct {
			Name   string `json:"name"`
			State  string `json:"state"`
			Detail string `json:"detail"`
		} `json:"items"`
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
	w.creates(t)

	out, errOut, code := w.run(t, "setup", "--json", "--create", "kit-config", "--mac", "laptop")
	doc := readSetup(t, out)
	stood := doc.stood()
	for _, want := range []string{
		"github ok someone · GitHub's CLI's sign-in",
		"kit-config ok someone/kit-config · new, private",
		"mac ok laptop",
		"snapshot ok 1: 1 git config",
		"restore failed no terminal to ask at: --restore <path> for each folder a new Mac brings back from your backups (~/Code ",
		"prefs failed no terminal to ask at: --prefs saves apps' settings every night, so a new Mac gets them back (suggested), or --no-prefs",
		"files ok none to link",
		"to-install ok nothing: kit-config is new",
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
	if got, want := w.read(t, filepath.Join(".config", "kit", "shared", "declarations")), "[paths]\n"+filepath.Join(w.home, "homebrew", "bin")+"   # Homebrew\n"; got != want {
		t.Errorf("shared/declarations = %q, want %q: Homebrew's folders on every Mac's PATH", got, want)
	}

	w.expectCommit(t, commit{
		message: "kit setup (laptop): this Mac as it is", paths: []string{"kit.toml", "shared", "laptop"},
		files: []string{"kit.toml", "laptop/declarations", "shared/declarations"}, status: "?? kit.toml\x00?? laptop/declarations\x00?? shared/declarations\x00",
	})
	out, errOut, code = w.run(t, "setup", "--json", "--restore", "~/Code", "--prefs", "--no-apply")
	stood = readSetup(t, out).stood()
	for _, want := range []string{
		"kit-config ok someone/kit-config · new, private",
		"restore ok 1 folder",
		"prefs ok every night",
		"commit ok 1 declared · committed and pushed kit.toml, shared/ and laptop/",
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
		if strings.Count(call, "gh repo create") > 0 && call != "gh repo create someone/kit-config --private" {
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
	w.creates(t)
	healthyMac(w)
	w.private()
	w.expectCommit(t, commit{
		message: "kit setup (laptop): this Mac as it is", paths: []string{"kit.toml", "shared", "laptop"},
		files: []string{"kit.toml", "laptop/declarations", "shared/declarations"}, status: "?? kit.toml\x00?? laptop/declarations\x00?? shared/declarations\x00",
	})
	out, errOut, code := w.run(t, "setup", "--create", "kit-config", "--mac", "laptop", "--no-restore", "--no-prefs")
	for _, want := range []string{"kit setup · MacBook Pro\n", "commit ok 1 declared · committed and pushed kit.toml, shared/ and laptop/\n", "kit apply · laptop\n", "git-config ok 1 declared, all set\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("kit setup printed\n%s%s\nwant it to hold %q", out, errOut, want)
		}
	}
	if code != 0 {
		t.Errorf("kit setup exited %d", code)
	}
}

// A push that fails leaves setup part-way, saying a run again carries on
// and never to delete kit-config's folder: a run again declares nothing
// twice, commits nothing new, and pushes what's waiting, to GitHub's new,
// empty, repository, its branch made by the push. Then it's done. What's
// waiting, if git can't tell, is never taken for nothing to push.
func TestSetupCarriesOnFromAPushThatFailed(t *testing.T) {
	w := inUse(t, map[string]bool{})
	w.creates(t)
	dir := filepath.Join(w.home, ".config", "kit")
	git := func(args ...string) []string { return append([]string{"-C", dir}, args...) }
	paths := []string{"kit.toml", "shared", "laptop"}
	w.expectCommit(t, commit{
		message: "kit setup (laptop): this Mac as it is", paths: paths,
		files: []string{"kit.toml", "laptop/declarations", "shared/declarations"}, status: "?? kit.toml\x00?? laptop/declarations\x00?? shared/declarations\x00",
	})
	w.fake.On("git", git("rev-parse", "--verify", "--quiet", "refs/remotes/origin/main")...).Exits(1)
	w.fake.On("git", git("fetch", "--prune", "--quiet", "origin")...).Exits(128).PrintsToStderr("fatal: couldn't find remote ref refs/heads/main\n")
	w.fake.On("git", git("update-ref", "-d", "refs/remotes/origin/main")...)
	w.fake.On("git", git("push", "--quiet", "origin", "HEAD:refs/heads/main")...).Exits(128).PrintsToStderr("fatal: the remote end hung up unexpectedly\n").Then()
	flags := []string{"setup", "--json", "--create", "kit-config", "--mac", "laptop", "--restore", "~/Code", "--prefs", "--no-apply"}
	prefsRecord := filepath.Join(".local", "state", "kit", "prefs.json")

	out, _, code := w.run(t, flags...)
	stood := readSetup(t, out).stood()
	if want := "commit attention 1 declared · committed, not pushed: couldn't push: "; !slices.ContainsFunc(stood, func(s string) bool { return strings.HasPrefix(s, want) }) || code != 1 {
		t.Errorf("the push failing, kit setup stood\n%s\nexit %d; want it to hold %q", strings.Join(stood, "\n"), code, want)
	}
	if carryOn := "; kit setup again carries on; never delete ~/.config/kit part-way, as files are moved into it and linked back"; !slices.ContainsFunc(stood, func(s string) bool { return strings.HasSuffix(s, carryOn) }) {
		t.Errorf("the push failing, kit setup stood\n%s\nwant the commit's row to end %q", strings.Join(stood, "\n"), carryOn)
	}
	if got := w.read(t, prefsRecord); !strings.Contains(got, `"on": "started fresh `) {
		t.Errorf("prefs.json says\n%s\nwant capture started afresh", got)
	}

	capturing := `{"on": "started fresh 2026-11-01 09:00", "captured": "2026-11-02T01:00:00Z"}`
	w.write(t, prefsRecord, capturing)
	w.fake.On("git", w.committing([]string{"status", "--porcelain", "-z", "--untracked-files=all", "--no-renames"}, paths...)...)
	w.fake.On("git", git("log", "--format=%ct", "HEAD", "--not", "--remotes=origin")...).Exits(128).PrintsToStderr("fatal: bad object HEAD\n").
		Then().Prints("1763460000\n")
	out, _, code = w.run(t, flags...)
	stood = readSetup(t, out).stood()
	if want := "commit attention 0 declared · nothing changed; couldn't tell what's waiting to be pushed: "; !slices.ContainsFunc(stood, func(s string) bool { return strings.HasPrefix(s, want) }) || code != 1 {
		t.Errorf("what's waiting not told, kit setup stood\n%s\nexit %d; want it to hold %q, setup not done", strings.Join(stood, "\n"), code, want)
	}
	out, errOut, code := w.run(t, flags...)
	doc := readSetup(t, out)
	stood = doc.stood()
	if want := "commit ok 0 declared · nothing changed; pushed 1 commit waiting"; !slices.Contains(stood, want) || code != 0 {
		t.Errorf("run again, kit setup stood\n%s\nsaid %q, exit %d; want it to hold %q", strings.Join(stood, "\n"), errOut, code, want)
	}
	for _, s := range doc.Steps {
		if s.ID == "commit" && len(s.Items) > 0 {
			t.Errorf("run again, the commit's items are %+v: want what's declared already left as it is", s.Items)
		}
	}
	want := "[features]\nprefs\n\n[git config]\nuser.name \"Some One\"\n\n[macos settings]\n\n[github extensions]\n\n[restore]\n~/Code\n"
	if got := w.read(t, filepath.Join(".config", "kit", "laptop", "declarations")); got != want {
		t.Errorf("laptop/declarations =\n%s\nwant\n%s", got, want)
	}
	if got := w.read(t, prefsRecord); got != capturing {
		t.Errorf("prefs.json says\n%s\nwant capture left as it was", got)
	}
	if _, _, code := w.run(t, "setup"); code != 2 {
		t.Errorf("once pushed, kit setup exits %d; want it refused, kit looking after this Mac", code)
	}
}

// A commit that fails once a file's moved into kit-config and linked back
// leaves setup part-way: a run again links nothing twice, commits what the
// first run made, and ends as a setup whose commit never failed.
func TestSetupCarriesOnFromACommitThatFailed(t *testing.T) {
	flags := []string{"setup", "--json", "--create", "kit-config", "--mac", "laptop", "--no-restore", "--no-prefs", "--link", "~/.zshrc", "--no-apply"}
	made := commit{
		message: "kit setup (laptop): this Mac as it is", paths: []string{"kit.toml", "shared", "laptop"},
		files:  []string{"kit.toml", "laptop/declarations", "laptop/home/.zshrc", "shared/declarations"},
		status: "?? kit.toml\x00?? laptop/declarations\x00?? laptop/home/.zshrc\x00?? shared/declarations\x00",
	}
	mac := func(t *testing.T) *world {
		w := inUse(t, map[string]bool{})
		w.creates(t)
		w.write(t, ".zshrc", "zsh\n")
		w.fake.On("git", "-C", filepath.Join(w.home, ".config", "kit"), "check-ignore", "--verbose", "--stdin", "-z").Exits(1)
		return w
	}

	unbroken := mac(t)
	unbroken.expectCommit(t, made)
	if _, errOut, code := unbroken.run(t, flags...); code != 0 {
		t.Fatalf("unbroken, kit setup said %q, exit %d", errOut, code)
	}

	w := mac(t)
	failing := made
	failing.fails = "fatal: unable to write new index file\n"
	w.expectCommit(t, failing)
	out, _, code := w.run(t, flags...)
	stood := readSetup(t, out).stood()
	if want := "commit failed couldn't commit kit.toml, shared/ and laptop/: "; !slices.ContainsFunc(stood, func(s string) bool { return strings.HasPrefix(s, want) }) || code != 1 {
		t.Fatalf("the commit failing, kit setup stood\n%s\nexit %d; want it to hold %q", strings.Join(stood, "\n"), code, want)
	}
	w.keeps("laptop/home/.zshrc")
	w.expectCommit(t, made)
	out, errOut, code := w.run(t, flags...)
	doc := readSetup(t, out)
	if code != 0 {
		t.Errorf("run again, kit setup stood\n%s\nsaid %q, exit %d", strings.Join(doc.stood(), "\n"), errOut, code)
	}
	for _, s := range doc.Steps {
		if s.ID == "commit" && len(s.Items) > 0 {
			t.Errorf("run again, the commit's items are %+v: want what's linked already left as it is", s.Items)
		}
	}
	for _, path := range []string{
		filepath.Join(".config", "kit", "kit.toml"), filepath.Join(".config", "kit", "laptop", "declarations"),
		filepath.Join(".config", "kit", "laptop", "home", ".zshrc"), filepath.Join(".local", "state", "kit", "setup.json"),
		filepath.Join(".local", "state", "kit", "machine"),
	} {
		if got, want := w.read(t, path), unbroken.read(t, path); got != want {
			t.Errorf("run again, %s =\n%s\nwant, as unbroken,\n%s", path, got, want)
		}
	}
	if got, want := w.linksTo(".zshrc"), filepath.Join(w.home, ".config", "kit", "laptop", "home", ".zshrc"); got != want {
		t.Errorf("~/.zshrc links to %q, want %q", got, want)
	}
}
