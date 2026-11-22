package setup

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/boot"
	"github.com/leeovery/kit/internal/boot/boottest"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/kind/brew"
	"github.com/leeovery/kit/internal/kinds"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
	"github.com/leeovery/kit/internal/snapshot"
	kitstate "github.com/leeovery/kit/internal/state"
)

// asker answers setup's questions as a test scripts them, in turn, noting
// each: a text not scripted takes what's suggested, and a tick list as it's
// ticked to start with.
type asker struct {
	mu      sync.Mutex
	asked   []string
	chooses []int
	texts   []string
}

func (a *asker) note(s string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.asked = append(a.asked, s)
}

func (a *asker) Pick(context.Context, string, []boot.Key) (string, error) {
	return "", errors.New("signing in, which the test doesn't")
}

func (a *asker) Choose(_ context.Context, step, question string, answers []boot.Answer) (int, error) {
	labels := make([]string, len(answers))
	for i, an := range answers {
		labels[i] = an.Label
	}
	a.note(step + ": " + question + ": " + strings.Join(labels, ", "))
	a.mu.Lock()
	defer a.mu.Unlock()
	i := a.chooses[0]
	a.chooses = a.chooses[1:]
	return i, nil
}

func (a *asker) Text(_ context.Context, step, question, suggested string) (string, error) {
	a.note(step + ": " + question + " [" + suggested + "]")
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.texts) == 0 {
		return suggested, nil
	}
	text := a.texts[0]
	a.texts = a.texts[1:]
	return text, nil
}

func (a *asker) Tick(_ context.Context, step, question string, items []Item) ([]bool, error) {
	kept := make([]bool, len(items))
	said := make([]string, len(items))
	for i, it := range items {
		kept[i], said[i] = it.Ticked, strings.TrimSpace(it.Name+" "+it.More)
		if it.Ticked {
			said[i] += " (ticked)"
		}
	}
	a.note(step + ": " + question + ": " + strings.Join(said, ", "))
	return kept, nil
}

type sink struct{}

func (sink) Emit(event.Event) {}

// helper is the setting git signs in to GitHub through, for the config
// repository alone.
const helper = "credential.https://github.com.helper"

// gitIn is git in the config folder's clone, as a test has it: its origin,
// HEAD, its branch, and the settings git config keeps, as git sets and
// reads them; and, on GitHub, the branches each repository has, and
// whether it's private.
type gitIn struct {
	mu      sync.Mutex
	has     map[string]string
	heads   map[string]string
	private map[string]bool
}

// printed is git printing key's value, or nothing when it's not set.
func (g *gitIn) printed(key string) func(string) (string, error) {
	return func(string) (string, error) {
		g.mu.Lock()
		defer g.mu.Unlock()
		if value, ok := g.has[key]; ok {
			return value + "\n", nil
		}
		return "", nil
	}
}

// remotes is git listing the clone's remotes: origin, once it's cloned,
// else failing, as outside a repository.
func (g *gitIn) remotes(string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.has["origin"]; ok {
		return "origin\n", nil
	}
	return "", errors.New("fatal: not a git repository")
}

// lsRemote is git listing the branches GitHub has of the repository the
// clone's origin names.
func (g *gitIn) lsRemote(string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.heads[g.has["origin"]], nil
}

// visibility is gh saying whether the repository the clone pushes to is
// private.
func (g *gitIn) visibility(string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.private[g.has["origin"]] {
		return "PRIVATE\n", nil
	}
	return "PUBLIC\n", nil
}

// pushURL is where git pushes the clone: its pushurl, else its origin.
func (g *gitIn) pushURL(string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return cmp.Or(g.has["pushurl"], g.has["origin"]) + "\n", nil
}

func (g *gitIn) put(key, value string) func() {
	return func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.has[key] = value
	}
}

// get is git printing key's value, or exiting 1 when it's not set.
func (g *gitIn) get(key string) func(string) (string, error) {
	return func(string) (string, error) {
		g.mu.Lock()
		defer g.mu.Unlock()
		value, ok := g.has[key]
		if !ok {
			return "", errors.New("")
		}
		return value + "\n", nil
	}
}

func (g *gitIn) settings() map[string]string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return maps.Clone(g.has)
}

// mac is a MacBook Pro in use, kit 0.1.2, for setup's runs: GitHub's CLI
// signed in as someone, who sees GitHub's repositories; git in the config
// folder's clone; what the snapshot finds on it; and, setup committing,
// what each commit declared, and how each push goes, in turn, OK once
// they've run out.
type mac struct {
	home     string
	dirs     config.Dirs
	run      *runnertest.Fake
	gitHub   boot.GitHub
	repos    *boottest.Repos
	git      *gitIn
	cloning  map[string]func()
	ask      Asker
	mu       sync.Mutex
	declared []Declaring
	pushes   []check.State
}

func newMac(t *testing.T, repos map[string]bool) *mac {
	t.Helper()
	home := t.TempDir()
	dirs, err := config.Locate(home, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	m := &mac{home: home, dirs: dirs, run: runnertest.New(t), cloning: map[string]func(){}}
	m.git = &gitIn{has: map[string]string{}, heads: map[string]string{}, private: map[string]bool{}}
	m.gitHub, m.repos = boottest.ServeRepos(t, repos)
	run := m.run
	run.On("security", "find-generic-password", "-s", "kit-github", "-w").Exits(44)
	run.On("gh", "auth", "token").Prints(boottest.Token + "\n")
	run.On("gh", "auth", "status", "--hostname", "github.com")
	run.On("security", "delete-generic-password", "-s", "kit-github")
	for key, value := range map[string]string{
		helper:                boot.GitCredentials,
		"remote.origin.fetch": "+refs/heads/main:refs/remotes/origin/main",
		"branch.main.remote":  "origin",
		"branch.main.merge":   "refs/heads/main",
	} {
		run.On("git", m.inClone("config", "--get", key)...).Filters(m.git.get(key))
		run.On("git", m.inClone("config", key, value)...).Does(m.git.put(key, value))
	}
	run.On("git", m.inClone("symbolic-ref", "HEAD")...).Filters(m.git.get("HEAD"))
	run.On("git", m.inClone("symbolic-ref", "HEAD", "refs/heads/main")...).Does(m.git.put("HEAD", "refs/heads/main"))
	run.On("git", m.inClone("remote")...).Filters(m.git.remotes)
	run.On("git", m.inClone("config", "--get", "remote.origin.url")...).Filters(m.git.get("origin"))
	run.On("git", m.inClone("remote", "get-url", "--push", "--all", "origin")...).Filters(m.git.pushURL)
	run.On("git", m.inClone("for-each-ref", "--count=1", "--format=%(refname)", "refs/heads")...).Filters(m.git.printed("branch"))
	run.On("git", m.inClone("ls-remote", "--heads", "origin")...).Filters(m.git.lsRemote)
	for _, args := range [][]string{{"list", "--formula", "--full-name", "-1"}, {"leaves"}, {"leaves", "--installed-on-request"}} {
		run.On("brew", args...).Fails(runner.ErrNotFound)
	}
	return m
}

func (m *mac) inClone(args ...string) []string {
	return append([]string{"-C", m.dirs.Config}, args...)
}

// cloneArgs is GitHub's CLI cloning repo into the config folder.
func (m *mac) cloneArgs(repo string) []string {
	return []string{"repo", "clone", repo, m.dirs.Config, "--", "--quiet", "--single-branch"}
}

// holds has GitHub hold repo, private or not, with files, by name: gh
// repo clone clones them into the config folder, git there on main and
// tracking it alone, as a single-branch clone is, or, with no files, on
// git's own first branch, tracking nothing, as an empty one is.
func (m *mac) holds(t *testing.T, repo string, private bool, files map[string]string) {
	m.repos.Add(repo, private)
	m.onGitHub(repo, private, len(files) > 0)
	m.clones(t, repo, files)
}

// onGitHub has GitHub hold repo, private or not, with main or no branch,
// as gh and git ask it.
func (m *mac) onGitHub(repo string, private, branch bool) {
	url := "https://github.com/" + repo + ".git"
	m.git.mu.Lock()
	m.git.private[url] = private
	if branch {
		m.git.heads[url] = "0e0bf17a399109574ffb00e33300280a33f81ab7\trefs/heads/main\n"
	}
	m.git.mu.Unlock()
	m.run.On("gh", "repo", "view", "https://github.com/"+repo, "--json", "visibility", "--jq", ".visibility").Filters(m.git.visibility)
}

// clones has gh repo clone clone repo, holding files, as holds says: a
// clone of its own, git's settings in it new.
func (m *mac) clones(t *testing.T, repo string, files map[string]string) {
	m.cloning[repo] = func() {
		m.git.mu.Lock()
		clear(m.git.has)
		m.git.mu.Unlock()
		m.write(t, ".git/HEAD", "")
		for name, content := range files {
			m.write(t, name, content)
		}
		m.git.put("origin", "https://github.com/"+repo+".git")()
		if len(files) == 0 {
			m.git.put("HEAD", "refs/heads/master")()
			return
		}
		for key, value := range map[string]string{"HEAD": "refs/heads/main", "branch": "refs/heads/main", "remote.origin.fetch": "+refs/heads/main:refs/remotes/origin/main", "branch.main.remote": "origin", "branch.main.merge": "refs/heads/main"} {
			m.git.put(key, value)()
		}
	}
	m.run.On("gh", m.cloneArgs(repo)...).Does(m.cloning[repo])
}

// cutShort is a clone of repo killed before git fetched it: its git folder
// and origin, nothing else.
func (m *mac) cutShort(t *testing.T, repo string) func() {
	return func() {
		m.git.mu.Lock()
		clear(m.git.has)
		m.git.mu.Unlock()
		m.write(t, ".git/HEAD", "")
		m.git.put("origin", "https://github.com/"+repo+".git")()
	}
}

// creates has GitHub's CLI create repo, private and empty, on GitHub.
func (m *mac) creates(t *testing.T, repo string) {
	m.clones(t, repo, nil)
	m.onGitHub(repo, true, false)
	m.run.On("gh", "repo", "create", repo, "--private").Does(func() { m.repos.Add(repo, true) })
}

func (m *mac) write(t *testing.T, name, content string) {
	path := filepath.Join(m.dirs.Config, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Error(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Error(err)
	}
}

// setup is setup on the Mac, given answers, as a run of kit setup makes it:
// committing, it notes what's declared, and goes as the pushes say.
func (m *mac) setup(given Answers) *Setup {
	s := &Setup{
		Run: m.run, Sink: sink{}, GitHub: m.gitHub, Now: time.Now, Dirs: m.dirs, Kinds: kinds.Sections(),
		Getenv: func(string) string { return "" }, Version: "0.1.2", Model: "MacBook Pro", Given: given, Ask: m.ask,
		Snapshot: func(_ context.Context, cfg *config.Config, name string) (snapshot.Snapshot, snapshot.Mac, error) {
			return found, snapshot.Mac{Name: name, Config: cfg, Run: m.run, Dirs: m.dirs, Home: m.home}, nil
		},
		Declare: func(_ context.Context, d Declaring) check.Result {
			m.mu.Lock()
			defer m.mu.Unlock()
			m.declared = append(m.declared, d)
			state := check.OK
			if len(m.pushes) > 0 {
				state, m.pushes = m.pushes[0], m.pushes[1:]
			}
			switch state {
			case check.OK:
				return check.Result{State: state, Summary: "committed and pushed"}
			case check.Failed:
				return check.Result{State: state, Reason: "couldn't commit"}
			}
			return check.Result{State: state, Summary: "committed, not pushed"}
		},
	}
	return s
}

// runSetup runs setup on the Mac, given answers, as kit setup does, only
// the steps named and what they need when any are: how each stood.
func (m *mac) runSetup(t *testing.T, given Answers, only ...string) (*Setup, engine.Report) {
	t.Helper()
	s := m.setup(given)
	if err := s.Begin(t.Context()); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	p, err := engine.New(s.Steps()...)
	if err != nil {
		t.Fatal(err)
	}
	report, err := p.Apply(t.Context(), s.Sink, engine.Options{Command: "setup", Jobs: 1, Only: only})
	if err != nil {
		t.Fatal(err)
	}
	return s, report
}

// said is how each of steps stood: its state, and what it said.
func said(report engine.Report, steps ...string) []string {
	out := make([]string, len(steps))
	for i, name := range steps {
		res := report.Results[name]
		out[i] = fmt.Sprintf("%s %s %s", name, res.State, res.Summary+res.Reason)
	}
	return out
}

// ended is how the Mac ends, setup run: setup's record; kit.toml, and the
// declarations file of the Mac it names; kit's name for it; git in the
// clone; what the last commit declared, but what the snapshot found; and
// how often GitHub's CLI created a repository.
type ended struct {
	Record              record
	Toml, Decl, Machine string
	Git                 map[string]string
	Declared            Declaring
	Creates             int
}

func (m *mac) ended(t *testing.T) ended {
	t.Helper()
	rec, err := kitstate.Load[record](m.dirs.State, recordName)
	if err != nil {
		t.Fatal(err)
	}
	e := ended{Record: rec, Git: m.git.settings()}
	toml, _ := os.ReadFile(filepath.Join(m.dirs.Config, config.File))
	decl, _ := os.ReadFile(filepath.Join(m.dirs.Config, config.DeclFile(rec.Mac)))
	e.Toml, e.Decl = string(toml), string(decl)
	e.Machine, _ = config.ReadMachine(m.dirs.State)
	if n := len(m.declared); n > 0 {
		e.Declared = m.declared[n-1]
		e.Declared.Found = nil
	}
	for _, call := range m.run.Calls() {
		if strings.HasPrefix(call, "gh repo create ") {
			e.Creates++
		}
	}
	return e
}

// At a terminal, a person with no kit-config is offered one, created
// private on the account setup signed in to, and this Mac named as its
// model suggests: the config's first files say this kit is the oldest
// that reads it, this Mac is the primary, and, run in Ghostty, it's the
// terminal a new Mac's boot hands over to; Homebrew's folders are on every
// Mac's PATH, as kit's runs find brew. The clone's on main, tracking
// main alone, though GitHub has no branch yet, and git signs in through
// GitHub's CLI.
func TestSetupCreatesAConfig(t *testing.T) {
	m := newMac(t, map[string]bool{"someone/notes": true})
	ask := &asker{chooses: []int{0}}
	m.ask = ask
	m.creates(t, "someone/kit-config")
	s := m.setup(Answers{})
	s.Getenv = func(name string) string { return map[string]string{"TERM_PROGRAM": "ghostty"}[name] }
	s.Homebrew = config.HomebrewPaths
	p, _ := engine.New(s.Steps()...)
	report, _ := p.Apply(t.Context(), s.Sink, engine.Options{Jobs: 1, Only: []string{MacStep}})
	want := []string{
		"github ok someone · GitHub's CLI's sign-in",
		"kit-config ok someone/kit-config · new, private",
		"mac ok macbook-pro",
	}
	if got := said(report, GitHubStep, ConfigStep, MacStep); !slices.Equal(got, want) {
		t.Errorf("steps stood\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	wantAsked := []string{
		"kit-config: someone has no repository named kit-config: Create kit-config, Join one of yours, Another name",
		"mac: what will kit-config call it? [macbook-pro]",
	}
	if !slices.Equal(ask.asked, wantAsked) {
		t.Errorf("asked\n%s\nwant\n%s", strings.Join(ask.asked, "\n"), strings.Join(wantAsked, "\n"))
	}
	e := m.ended(t)
	if want := "format = 1\nminimum_kit = \"0.1.2\"\nprimary = \"macbook-pro\"\nterminal = \"ghostty\"\n\n[macs.macbook-pro]\ndescription = \"MacBook Pro\"\n"; e.Toml != want {
		t.Errorf("kit.toml =\n%s\nwant\n%s", e.Toml, want)
	}
	if _, err := os.Stat(filepath.Join(m.dirs.Config, config.DeclFile("macbook-pro"))); err != nil {
		t.Errorf("the Mac's declarations file: %v", err)
	}
	const homebrew = "[paths]\n/opt/homebrew/bin   # Homebrew\n/opt/homebrew/sbin   # Homebrew\n"
	if got, err := os.ReadFile(filepath.Join(m.dirs.Config, config.DeclFile(config.Shared))); err != nil || string(got) != homebrew {
		t.Errorf("shared/declarations = %q, %v; want %q", got, err, homebrew)
	}
	if e.Machine != "macbook-pro" {
		t.Errorf("this Mac is named %q, want macbook-pro", e.Machine)
	}
	wantGit := map[string]string{
		"origin": "https://github.com/someone/kit-config.git", "HEAD": "refs/heads/main", helper: boot.GitCredentials,
		"remote.origin.fetch": "+refs/heads/main:refs/remotes/origin/main", "branch.main.remote": "origin", "branch.main.merge": "refs/heads/main",
	}
	if !maps.Equal(e.Git, wantGit) {
		t.Errorf("git in the clone is %v\nwant %v", e.Git, wantGit)
	}
}

// joinedConfig is a config repository to join: laptop and studio, laptop
// declaring a formula.
var joinedConfig = map[string]string{
	config.File:           "format = 1\nprimary = \"laptop\"\n\n[macs.laptop]\ndescription = \"MacBook Air\"\n\n[macs.studio]\n",
	"laptop/declarations": "[homebrew formulae]\njq\n",
	"studio/declarations": "",
}

// Joining a config takes one of the person's repositories, private, and
// one of its Macs, or a new one, added as kit machine add adds one: a copy
// of another's with --from. A repository that's public, or can't be seen,
// is refused; one holding no kit.toml is refused once it's looked at, its
// clone deleted, and setup starts again; an empty one starts kit-config on
// this Mac, as one created does. Creating one that's there, private, takes
// it only while it's empty.
func TestSetupJoinsAConfig(t *testing.T) {
	notes := map[string]string{"README.md": "notes\n"}
	for _, tt := range []struct {
		name    string
		private bool
		files   map[string]string
		given   Answers
		want    []string
		toml    string
		copied  string
		cleared bool
	}{
		{"a Mac it knows", true, joinedConfig, Answers{Join: "someone/kit-config", Mac: "studio"},
			[]string{"kit-config ok someone/kit-config · joined · has laptop and studio", "mac ok studio"}, joinedConfig[config.File], "", false},
		{"a new Mac, a copy of another", true, joinedConfig, Answers{Join: "someone/kit-config", Mac: "mini", From: "laptop"},
			[]string{"kit-config ok someone/kit-config · joined · has laptop and studio", "mac ok mini · new"},
			joinedConfig[config.File] + "\n[macs.mini]\ndescription = \"MacBook Pro\"\nfrom = \"laptop\"\n", "[homebrew formulae]\njq\n", false},
		{"a public repository", false, joinedConfig, Answers{Join: "someone/kit-config", Mac: "studio"},
			[]string{"kit-config failed someone/kit-config is public on GitHub, and a config repository is always private: make it private, then run kit setup again", "mac deferred needs kit-config"}, "", "", true},
		{"one it can't see", true, joinedConfig, Answers{Join: "other/kit-config", Mac: "studio"},
			[]string{"kit-config failed other/kit-config isn't on GitHub, or this sign-in can't see it", "mac deferred needs kit-config"}, "", "", true},
		{"one that's no config", true, notes, Answers{Join: "someone/kit-config", Mac: "studio"},
			[]string{"kit-config failed someone/kit-config holds no kit.toml, so it's no config repository: join yours, or create a new one", "mac deferred needs kit-config"}, "", "", true},
		{"an empty one", true, nil, Answers{Join: "someone/kit-config", Mac: "laptop"},
			[]string{"kit-config ok someone/kit-config · private, empty", "mac ok laptop"},
			"format = 1\nminimum_kit = \"0.1.2\"\nprimary = \"laptop\"\n\n[macs.laptop]\ndescription = \"MacBook Pro\"\n", "", false},
		{"one to create, there and empty", true, nil, Answers{Create: "kit-config", Mac: "laptop"},
			[]string{"kit-config ok someone/kit-config · new, private", "mac ok laptop"},
			"format = 1\nminimum_kit = \"0.1.2\"\nprimary = \"laptop\"\n\n[macs.laptop]\ndescription = \"MacBook Pro\"\n", "", false},
		{"one to create, there and holding files", true, joinedConfig, Answers{Create: "kit-config", Mac: "laptop"},
			[]string{"kit-config failed someone/kit-config is on GitHub already, holding files: join it with --join someone/kit-config, or create one named otherwise", "mac deferred needs kit-config"}, "", "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := newMac(t, nil)
			m.holds(t, "someone/kit-config", tt.private, tt.files)
			_, report := m.runSetup(t, tt.given, MacStep)
			if got := said(report, ConfigStep, MacStep); !slices.Equal(got, tt.want) {
				t.Errorf("steps stood\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
			}
			e := m.ended(t)
			if e.Toml != tt.toml {
				t.Errorf("kit.toml =\n%s\nwant\n%s", e.Toml, tt.toml)
			}
			if tt.copied != "" && e.Decl != tt.copied {
				t.Errorf("%s's declarations = %q, want the copy %q", tt.given.Mac, e.Decl, tt.copied)
			}
			if _, err := os.Stat(m.dirs.Config); tt.cleared && (err == nil || e.Record != record{}) {
				t.Errorf("refused, the config folder's there (%v) and the record's %+v: want both cleared", err, e.Record)
			}
		})
	}
}

// At a terminal, with no kit-config, joining picks from the person's
// private repositories, listed in one call, or takes one named; a public
// one is never listed.
func TestSetupJoinsOneOfTheirRepositories(t *testing.T) {
	for _, tt := range []struct {
		name    string
		chooses []int
		texts   []string
		repo    string
	}{
		{"picked", []int{1, 1}, nil, "someone/notes"},
		{"named", []int{1, 2}, []string{"someone/dots"}, "someone/dots"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := newMac(t, map[string]bool{"someone/dots": true, "someone/notes": true, "someone/site": false})
			ask := &asker{chooses: tt.chooses, texts: tt.texts}
			m.ask = ask
			m.holds(t, tt.repo, true, joinedConfig)
			_, report := m.runSetup(t, Answers{}, ConfigStep)
			if got := said(report, ConfigStep); got[0] != "kit-config ok "+tt.repo+" · joined · has laptop and studio" {
				t.Errorf("stood %q", got[0])
			}
			if want := "kit-config: which of your private repositories?: someone/dots, someone/notes, Another"; len(ask.asked) < 2 || ask.asked[1] != want {
				t.Errorf("asked %q, want %q", ask.asked, want)
			}
		})
	}
}

// A clone cut short, git holding no branch while GitHub has some, is no
// empty repository: it's cloned again, once, and said so should that be
// cut short too, nothing recorded of what it holds.
func TestSetupClonesAgainAClonesCutShort(t *testing.T) {
	for _, tt := range []struct {
		name  string
		twice bool
		says  string
	}{
		{"cut short once", false, "kit-config ok someone/kit-config · joined · has laptop and studio"},
		{"cut short twice", true, "kit-config failed someone/kit-config's clone, cloned again, hasn't the branches GitHub has: run kit setup again"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := newMac(t, nil)
			m.holds(t, "someone/kit-config", true, joinedConfig)
			script := m.run.On("gh", m.cloneArgs("someone/kit-config")...).Does(m.cutShort(t, "someone/kit-config")).Then()
			if tt.twice {
				script.Does(m.cutShort(t, "someone/kit-config"))
			} else {
				script.Does(m.cloning["someone/kit-config"])
			}
			_, report := m.runSetup(t, Answers{Join: "someone/kit-config"}, ConfigStep)
			if got := said(report, ConfigStep)[0]; got != tt.says {
				t.Errorf("stood %q, want %q", got, tt.says)
			}
			if rec := m.ended(t).Record; tt.twice && (rec.Cloned || rec.Empty) {
				t.Errorf("cut short twice, recorded %+v", rec)
			}
		})
	}
}

// Setup signs in as GitHub's CLI is signed in, whatever sign-in kit kept:
// the repository it creates and the one it records are on the one account.
func TestSetupSignsInAsGitHubsCLI(t *testing.T) {
	m := newMac(t, nil)
	m.run.On("security", "find-generic-password", "-s", "kit-github", "-w").Prints("gho_stale\n")
	m.creates(t, "someone/kit-config")
	_, report := m.runSetup(t, Answers{Create: "kit-config", Mac: "laptop"}, ConfigStep)
	if got := said(report, GitHubStep, ConfigStep); !slices.Equal(got, []string{"github ok someone · GitHub's CLI's sign-in", "kit-config ok someone/kit-config · new, private"}) {
		t.Errorf("stood %q", got)
	}
	if !slices.Contains(m.run.Calls(), "gh repo create someone/kit-config --private") {
		t.Errorf("ran %q: want the repository created as owner/name", m.run.Calls())
	}
}

// Without a terminal, setup asks nothing: a question its flag doesn't
// answer stops it there, naming the flag, and what's done is kept, so a run
// again, with the flag, carries on, creating nothing twice. A Mac can't be
// named as kit machine add can't name one.
func TestSetupWithoutATerminalStopsAtAQuestionItsFlagDoesntAnswer(t *testing.T) {
	m := newMac(t, nil)
	s, report := m.runSetup(t, Answers{}, MacStep)
	createOrJoin := "--create <name> makes a new config repository (kit-config, unless you've one), or --join owner/name joins one of yours"
	if got := said(report, ConfigStep); got[0] != "kit-config failed no terminal to ask at: "+createOrJoin {
		t.Errorf("without --create or --join, %s", got[0])
	}
	if !slices.Equal(s.Wanted(), []string{createOrJoin}) {
		t.Errorf("wanted %q", s.Wanted())
	}

	m.creates(t, "someone/kit-config")
	s, report = m.runSetup(t, Answers{Create: "kit-config"}, MacStep)
	if got := said(report, MacStep); got[0] != "mac failed no terminal to ask at: --mac <name> names this Mac (macbook-pro, say)" {
		t.Errorf("without --mac, %s", got[0])
	}
	if !slices.Equal(s.Wanted(), []string{"--mac <name> names this Mac (macbook-pro, say)"}) {
		t.Errorf("wanted %q", s.Wanted())
	}

	_, report = m.runSetup(t, Answers{Mac: "add"}, MacStep)
	if got := said(report, MacStep); got[0] != "mac failed a Mac can't be called add: that's kit machine add" {
		t.Errorf("--mac add, %s", got[0])
	}

	_, report = m.runSetup(t, Answers{Mac: "laptop"}, MacStep)
	if got := said(report, ConfigStep, MacStep); !slices.Equal(got, []string{"kit-config ok someone/kit-config · new, private", "mac ok laptop"}) {
		t.Errorf("a run again stood %q", got)
	}
	if e := m.ended(t); e.Creates != 1 {
		t.Errorf("created kit-config %d times, want once", e.Creates)
	}
}

// A setup stopped anywhere, by a step's flag wanting, or cut short in one,
// a run again carries on where it stopped and ends as a setup that never
// stopped does: each step's check looks at what's there, the clone and
// git's settings in it, GitHub and the Mac. Setup's done only once its
// commit's pushed; till then a run again pushes it, as to a new,
// empty, repository when kit-config started on this Mac.
func TestSetupStoppedAnywhereEndsAsOneNeverStopped(t *testing.T) {
	code, none, yes, no := []string{"~/Code"}, []string{}, true, false
	repo := "someone/kit-config"
	for _, flow := range []struct {
		name   string
		given  Answers
		github func(t *testing.T, m *mac)
	}{
		{"created", Answers{Create: "kit-config", Mac: "laptop", Restore: &code, Prefs: &yes, Link: &none},
			func(t *testing.T, m *mac) { m.creates(t, repo) }},
		{"joined", Answers{Join: repo, Mac: "mini", From: "laptop", Restore: &code, Prefs: &no, Link: &none, Apply: &no},
			func(t *testing.T, m *mac) { m.holds(t, repo, true, joinedConfig) }},
	} {
		for _, stop := range []struct {
			name string
			only []string
			cut  func(t *testing.T, m *mac)
			// creating is whether the stop comes only creating kit-config.
			creating bool
		}{
			{"after signing in", []string{GitHubStep}, nil, false},
			{"after kit-config", []string{ConfigStep}, nil, false},
			{"after naming this Mac", []string{MacStep}, nil, false},
			{"after the snapshot", []string{SnapshotStep}, nil, false},
			{"after the questions", []string{DataStep, PrefsStep, FilesStep, InstallStep}, nil, false},
			{"by a push that failed", nil, func(_ *testing.T, m *mac) { m.pushes = []check.State{check.Attention} }, false},
			{"by a commit that failed", nil, func(_ *testing.T, m *mac) { m.pushes = []check.State{check.Failed} }, false},
			{"cloning", nil, func(_ *testing.T, m *mac) {
				m.run.On("gh", m.cloneArgs(repo)...).Exits(128).PrintsToStderr("fatal: the remote end hung up unexpectedly\n").Then().Does(m.cloning[repo])
			}, false},
			{"cloning, killed before the fetch", nil, func(t *testing.T, m *mac) {
				m.run.On("gh", m.cloneArgs(repo)...).Does(m.cutShort(t, repo)).Exits(137).Then().Does(m.cloning[repo])
			}, false},
			{"cloned, git not set", nil, func(_ *testing.T, m *mac) {
				m.run.On("git", m.inClone("config", helper, boot.GitCredentials)...).Exits(255).Then().Does(m.git.put(helper, boot.GitCredentials))
			}, false},
			{"created and looked at, git not on main", nil, func(_ *testing.T, m *mac) {
				m.run.On("git", m.inClone("config", "branch.main.merge", "refs/heads/main")...).Exits(255).Then().Does(m.git.put("branch.main.merge", "refs/heads/main"))
			}, true},
			{"created, GitHub's answer lost", nil, func(_ *testing.T, m *mac) {
				m.run.On("gh", "repo", "create", repo, "--private").Does(func() { m.repos.Add(repo, true) }).Exits(1)
			}, true},
		} {
			if stop.creating && flow.given.Create == "" {
				continue
			}
			t.Run(flow.name+", "+stop.name, func(t *testing.T) {
				unbroken := newMac(t, nil)
				flow.github(t, unbroken)
				unbroken.runSetup(t, flow.given)
				want := unbroken.ended(t)
				if !want.Record.Done {
					t.Fatalf("unbroken, setup ended %+v", want)
				}

				m := newMac(t, nil)
				flow.github(t, m)
				if stop.cut != nil {
					stop.cut(t, m)
				}
				m.runSetup(t, flow.given, stop.only...)
				if m.ended(t).Record.Done {
					t.Fatal("stopped, setup's done")
				}
				m.runSetup(t, flow.given)
				if got := m.ended(t); !reflect.DeepEqual(got, want) {
					t.Errorf("run again, ended\n%+v\nwant\n%+v", got, want)
				}
				for _, d := range m.declared {
					if d.Empty != want.Declared.Empty {
						t.Errorf("committed as to a repository empty %v, want %v", d.Empty, want.Declared.Empty)
					}
				}
			})
		}
	}
}

// Setup refuses a Mac whose config folder holds anything, a config above
// all, unless its own run part-way left it there: the folder empty, or a
// clone of the repository it chose, and kit's name for the Mac none, or
// the one it chose: its origin read as set, before an insteadOf rewrites
// it. Else setup starts afresh, forgetting a run that finished. Finder's
// file alone is empty, and goes.
func TestBegin(t *testing.T) {
	partWay := record{Repo: "someone/kit-config", Cloned: true, Mac: "laptop"}
	for _, tt := range []struct {
		name    string
		files   []string
		origin  string
		machine string
		record  record
		given   Answers
		says    string
	}{
		{"nothing there", nil, "", "", record{}, Answers{}, ""},
		{"an empty folder", []string{"."}, "", "", record{}, Answers{}, ""},
		{"Finder's file alone", []string{".DS_Store"}, "", "", record{}, Answers{}, ""},
		{"a config", []string{config.File}, "", "", record{}, Answers{}, "holds a config already, so kit looks after this Mac: kit status says how it stands"},
		{"something else", []string{"notes.txt"}, "", "", record{}, Answers{}, "is there, and isn't a config: move it aside, then run kit setup again"},
		{"setup's own, part-way", []string{config.File}, "https://github.com/someone/kit-config.git", "", partWay, Answers{}, ""},
		{"setup's own, part-way, named", []string{config.File}, "git@github.com:someone/kit-config.git", "laptop", partWay, Answers{Join: "someone/kit-config", Mac: "laptop"}, ""},
		{"setup's own, part-way, not cloned yet", nil, "", "", record{Repo: "someone/kit-config"}, Answers{}, ""},
		{"setup's own, done", []string{config.File}, "https://github.com/someone/kit-config.git", "laptop", record{Repo: "someone/kit-config", Mac: "laptop", Done: true}, Answers{}, "holds a config already"},
		{"part-way, another repository's clone", []string{config.File}, "https://github.com/someone/dots.git", "", partWay, Answers{}, "holds a config already"},
		{"part-way, the Mac kit looks after another", []string{config.File}, "https://github.com/someone/kit-config.git", "studio", partWay, Answers{}, "holds a config already"},
		{"part-way, joining another", nil, "", "", partWay, Answers{Join: "someone/dots"}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := newMac(t, nil)
			for _, f := range tt.files {
				if err := os.MkdirAll(m.dirs.Config, 0o700); err != nil {
					t.Fatal(err)
				}
				if f != "." {
					m.write(t, f, "")
				}
			}
			if tt.origin != "" {
				m.git.put("origin", tt.origin)()
			}
			if tt.machine != "" {
				if err := config.WriteMachine(m.dirs.State, tt.machine); err != nil {
					t.Fatal(err)
				}
			}
			if err := kitstate.Update(m.dirs.State, recordName, func(r *record) { *r = tt.record }); err != nil {
				t.Fatal(err)
			}
			err := m.setup(tt.given).Begin(t.Context())
			switch {
			case tt.says == "" && err != nil, tt.says != "" && (err == nil || !strings.Contains(err.Error(), tt.says)):
				t.Errorf("Begin = %v, want %q", err, tt.says)
			}
			if _, statErr := os.Stat(filepath.Join(m.dirs.Config, ".DS_Store")); err == nil && statErr == nil {
				t.Error("Finder's file was left, for git to refuse to clone into the folder")
			}
			rec, _ := kitstate.Load[record](m.dirs.State, recordName)
			if kept := rec.Repo != ""; err == nil && kept != tt.record.partWay() {
				t.Errorf("the record is %+v: want it kept only part-way", rec)
			}
		})
	}
}

// The config folder is as setup starts on when it's not there, or holds
// nothing but Finder's file; a folder named as Finder's file is something.
func TestEmpty(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "kit")
	for _, tt := range []struct {
		name  string
		make  func()
		empty bool
	}{
		{"not there", func() {}, true},
		{"Finder's file", func() {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, ".DS_Store"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"a folder named as Finder's file", func() {
			if err := os.Remove(filepath.Join(dir, ".DS_Store")); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(dir, ".DS_Store"), 0o700); err != nil {
				t.Fatal(err)
			}
		}, false},
	} {
		tt.make()
		if empty, err := Empty(dir); err != nil || empty != tt.empty {
			t.Errorf("%s: Empty = %v, %v; want %v", tt.name, empty, err, tt.empty)
		}
	}
}

// Run again with a flag naming kit-config or this Mac otherwise than the
// run part-way chose: till the commit's edits are made, another repository
// starts setup afresh, setup's own clone deleted, and the name it gave the
// Mac with it; --create names one on the account signed in to. Once
// they're made, it's refused, naming the one chosen, the clone kept. This
// Mac's name, and the Mac it's a copy of, are refused, as they're written
// in kit-config and kit's state.
func TestSetupRunAgainNamingAnother(t *testing.T) {
	first := Answers{Join: "someone/kit-config", Mac: "studio"}
	for _, tt := range []struct {
		name       string
		first      Answers
		declared   bool
		again      Answers
		want       []string
		repo, mac  string
		machineSet string
	}{
		{"joining another", first, false, Answers{Join: "someone/dots", Mac: "laptop"},
			[]string{"kit-config ok someone/dots · joined · has laptop and studio", "mac ok laptop"}, "someone/dots", "laptop", "laptop"},
		{"creating one", first, false, Answers{Create: "kit-config-2", Mac: "laptop"},
			[]string{"kit-config ok someone/kit-config-2 · new, private", "mac ok laptop"}, "someone/kit-config-2", "laptop", "laptop"},
		{"creating the one joined", first, false, Answers{Create: "kit-config"},
			[]string{"kit-config ok someone/kit-config · joined · has laptop and studio", "mac ok studio"}, "someone/kit-config", "studio", "studio"},
		{"joining another, its edits made", first, true, Answers{Join: "someone/dots"},
			[]string{"kit-config failed kit setup, part-way, has someone/kit-config as kit-config, its edits made there, files moved into it and linked back: run kit setup again without --create or --join, or with --join someone/kit-config", "mac deferred needs kit-config"},
			"someone/kit-config", "studio", "studio"},
		{"naming another Mac", first, false, Answers{Mac: "laptop"},
			[]string{"kit-config ok someone/kit-config · joined · has laptop and studio", "mac failed kit setup, part-way, named this Mac studio: run kit setup again without --mac, or with --mac studio"},
			"someone/kit-config", "studio", "studio"},
		{"a copy of another Mac", Answers{Join: "someone/kit-config", Mac: "mini", From: "laptop"}, false, Answers{From: "studio"},
			[]string{"kit-config ok someone/kit-config · joined · has laptop, mini and studio", "mac failed kit setup, part-way, named this Mac mini, a copy of laptop: run kit setup again without --from, or with --from laptop"},
			"someone/kit-config", "mini", "mini"},
		{"a copy of a Mac, named as one of kit-config's", first, false, Answers{From: "laptop"},
			[]string{"kit-config ok someone/kit-config · joined · has laptop and studio", "mac failed kit setup, part-way, named this Mac studio, a copy of none of your Macs: run kit setup again without --from"},
			"someone/kit-config", "studio", "studio"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := newMac(t, nil)
			m.holds(t, "someone/kit-config", true, joinedConfig)
			m.holds(t, "someone/dots", true, joinedConfig)
			m.creates(t, "someone/kit-config-2")
			m.runSetup(t, tt.first, MacStep)
			if tt.declared {
				if err := kitstate.Update(m.dirs.State, recordName, func(r *record) { r.Declared = true }); err != nil {
					t.Fatal(err)
				}
			}
			_, report := m.runSetup(t, tt.again, MacStep)
			if got := said(report, ConfigStep, MacStep); !slices.Equal(got, tt.want) {
				t.Errorf("run again, stood\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
			}
			e := m.ended(t)
			if e.Record.Repo != tt.repo || e.Record.Mac != tt.mac || e.Machine != tt.machineSet || e.Git["origin"] != "https://github.com/"+tt.repo+".git" {
				t.Errorf("run again, ended %+v; want %s's clone, the Mac named %s", e, tt.repo, tt.mac)
			}
		})
	}
}

// The name suggested for a Mac is its model's, in a Mac's name's form,
// numbered when kit-config has a Mac by that name.
func TestSuggest(t *testing.T) {
	taken := func(names ...string) func(string) bool {
		return func(name string) bool { return slices.Contains(names, name) }
	}
	for _, tt := range []struct {
		model string
		taken func(string) bool
		want  string
	}{
		{"MacBook Pro", taken(), "macbook-pro"},
		{"Mac mini", taken(), "mac-mini"},
		{"iMac", taken(), "imac"},
		{"Mac Studio (2025)", taken(), "mac-studio-2025"},
		{"", taken(), "mac"},
		{"Mac mini", taken("mac-mini", "mac-mini-2"), "mac-mini-3"},
	} {
		if got := Suggest(tt.model, tt.taken); got != tt.want {
			t.Errorf("Suggest(%q) = %q, want %q", tt.model, got, tt.want)
		}
	}
}

// The Mac's model is what macOS names it; "Mac" when it can't be told.
func TestModel(t *testing.T) {
	run := runnertest.New(t)
	run.On("system_profiler", "SPHardwareDataType", "-json").Prints(`{"SPHardwareDataType" : [{"_name" : "hardware_overview", "machine_model" : "Mac15,3", "machine_name" : "MacBook Pro"}]}`).
		Then().Fails(runner.ErrNotFound)
	for _, want := range []string{"MacBook Pro", "Mac"} {
		if got := Model(t.Context(), run); got != want {
			t.Errorf("Model = %q, want %q", got, want)
		}
	}
}

// found is a snapshot's questions' candidates: a folder ticked and one not,
// and a file to link.
var found = snapshot.Snapshot{
	Folders: []snapshot.Folder{{Path: "~/Code", Size: 48.2e9, Whole: true, Ticked: true}, {Path: "~/Movies", Size: 31e9, Whole: true}},
	Files:   []string{"~/.zshrc"},
	ByHand:  []brew.ByHand{{Name: "Kubius"}, {Name: "Sendhit"}},
}

// snapshotted is setup on laptop, joined, the snapshot taken; laptop
// declaring a formula the Mac hasn't, Homebrew not on it, and saving apps'
// settings.
func snapshotted(t *testing.T, ask Asker) (*Setup, *mac) {
	m := newMac(t, map[string]bool{"someone/kit-config": true})
	m.ask = ask
	for name, content := range joinedConfig {
		m.write(t, name, content)
	}
	m.write(t, config.DeclFile("laptop"), "[features]\nprefs\n\n"+joinedConfig[config.DeclFile("laptop")])
	if err := kitstate.Update(m.dirs.State, recordName, func(r *record) { r.Repo = "someone/kit-config" }); err != nil {
		t.Fatal(err)
	}
	m.git.put("origin", "https://github.com/someone/kit-config.git")()
	m.onGitHub("someone/kit-config", true, true)
	s := m.setup(Answers{})
	cfg, err := config.Load(m.dirs.Config, s.Kinds)
	if err != nil {
		t.Fatal(err)
	}
	snap := found
	s.found, s.mac = &snap, snapshot.Mac{Name: "laptop", Config: cfg, Run: m.run, Dirs: m.dirs, Home: m.home}
	s.hold(boottest.Token, boottest.Login)
	return s, m
}

// Each of the snapshot's questions takes its flag's answer; at a terminal,
// it's asked, its answer suggested; without one, it names its flags and
// what they choose from. Its answer's kept in the record, for a run again,
// and a flag given again replaces it. Apps' settings are asked of a Mac
// whose own record says capture's off, whatever kit-config declares.
func TestTheSnapshotsQuestions(t *testing.T) {
	yes, no := true, false
	code := []string{"~/Code"}
	for _, tt := range []struct {
		name, step string
		recorded   func(r *record)
		capturing  bool
		given      Answers
		terminal   bool
		chooses    []int
		says       string
		asked      string
	}{
		{"data, as given", DataStep, nil, false, Answers{Restore: &code}, false, nil, "ok 1 folder · 48.2 GB", ""},
		{"data, none", DataStep, nil, false, Answers{Restore: &[]string{}}, false, nil, "ok none", ""},
		{"data, asked", DataStep, nil, false, Answers{}, true, nil, "ok 1 folder · 48.2 GB",
			"restore: which folders should a new Mac bring back from your backups?: ~/Code 48.2 GB (ticked), ~/Movies 31.0 GB"},
		{"data, no terminal", DataStep, nil, false, Answers{}, false, nil,
			"failed no terminal to ask at: --restore <path> for each folder a new Mac brings back from your backups (~/Code 48.2 GB, suggested; ~/Movies 31.0 GB), or --no-restore", ""},
		{"data, recorded", DataStep, func(r *record) { r.Restore = &[]string{} }, false, Answers{}, false, nil, "ok none", ""},
		{"data, given anew", DataStep, func(r *record) { r.Restore = &[]string{} }, false, Answers{Restore: &code}, false, nil, "ok 1 folder · 48.2 GB", ""},
		{"prefs, as given", PrefsStep, nil, false, Answers{Prefs: &no}, false, nil, "ok not saved", ""},
		{"prefs, asked, though kit-config declares them saved", PrefsStep, nil, false, Answers{}, true, []int{0}, "ok every night",
			"prefs: save them every night, so a new Mac gets them back?: Save them every night, Not now"},
		{"prefs, no terminal", PrefsStep, nil, false, Answers{}, false, nil,
			"failed no terminal to ask at: --prefs saves apps' settings every night, so a new Mac gets them back (suggested), or --no-prefs", ""},
		{"prefs, capture on", PrefsStep, nil, true, Answers{}, false, nil, "ok every night already", ""},
		{"prefs, given anew", PrefsStep, func(r *record) { r.Prefs = &yes }, false, Answers{Prefs: &no}, false, nil, "ok not saved", ""},
		{"files, asked", FilesStep, nil, false, Answers{}, true, nil, "ok 1", "files: which should kit-config keep, each moved in and linked back?: ~/.zshrc (ticked)"},
		{"files, no terminal", FilesStep, nil, false, Answers{}, false, nil,
			"failed no terminal to ask at: --link <path> for each file kit-config keeps, moved in and linked back (~/.zshrc, all suggested), or --no-link", ""},
		{"files, given anew", FilesStep, func(r *record) { r.Link = &[]string{"~/.zshrc"} }, false, Answers{Link: &[]string{}}, false, nil, "ok none", ""},
		{"to install, as given", InstallStep, nil, false, Answers{Apply: &yes}, false, nil, "ok with the rest", ""},
		{"to install, asked", InstallStep, nil, false, Answers{}, true, []int{1}, "ok stopped here to look first: kit status, then kit apply",
			"to-install: kit-config declares 1 thing this Mac hasn't\njq: Install them with the rest, Stop here to look first"},
		{"to install, no terminal", InstallStep, nil, false, Answers{}, false, nil,
			"failed no terminal to ask at: --apply installs what kit-config declares 1 thing this Mac hasn't (jq) with the rest, or --no-apply stops to look first", ""},
		{"to install, given anew", InstallStep, func(r *record) { r.Apply = &no }, false, Answers{Apply: &yes}, false, nil, "ok with the rest", ""},
		{"data, given anew once the edits are made", DataStep, func(r *record) { r.Restore, r.Declared = &[]string{}, true }, false, Answers{Restore: &code}, false, nil,
			"failed kit setup, part-way, has made its edits in kit-config, your data none: run kit setup again without --restore or --no-restore", ""},
		{"files, given anew once the edits are made", FilesStep, func(r *record) { r.Link, r.Declared = &[]string{"~/.zshrc"}, true }, false, Answers{Link: &[]string{}}, false, nil,
			"failed kit setup, part-way, has made its edits in kit-config, linked files 1: run kit setup again without --link or --no-link", ""},
		{"prefs, given anew once the edits are made", PrefsStep, func(r *record) { r.Prefs, r.Declared = &yes, true }, false, Answers{Prefs: &no}, false, nil,
			"failed kit setup, part-way, has made its edits in kit-config, apps' settings every night: run kit setup again without --prefs or --no-prefs", ""},
		{"prefs, given again as made", PrefsStep, func(r *record) { r.Prefs, r.Declared = &yes, true }, false, Answers{Prefs: &yes}, false, nil, "ok every night", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var ask *asker
			if tt.terminal {
				ask = &asker{chooses: tt.chooses}
			}
			s, m := snapshotted(t, map[bool]Asker{true: ask, false: nil}[tt.terminal])
			s.Given = tt.given
			if tt.recorded != nil {
				if err := s.update(tt.recorded); err != nil {
					t.Fatal(err)
				}
			}
			if tt.capturing {
				if err := os.WriteFile(filepath.Join(m.dirs.State, "prefs.json"), []byte(`{"on": "started fresh 2026-11-01 09:00"}`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			p, err := engine.New(s.questionSteps()[tt.step])
			if err != nil {
				t.Fatal(err)
			}
			report, _ := p.Apply(t.Context(), s.Sink, engine.Options{Jobs: 1})
			res := report.Results[tt.step]
			if got := string(res.State) + " " + res.Summary + res.Reason; got != tt.says {
				t.Errorf("stood %q\nwant  %q", got, tt.says)
			}
			if tt.asked != "" && (ask == nil || !slices.Equal(ask.asked, []string{tt.asked})) {
				t.Errorf("asked %q, want %q", ask.asked, tt.asked)
			}
			s.Given = Answers{}
			again := s.questionSteps()[tt.step].Check(t.Context())
			if res.State == check.OK && again.Summary != res.Summary {
				t.Errorf("checked again, %q; want the answer kept", again.Summary)
			}
		})
	}
}

// Apps' settings not asked, as capture's on for this Mac already, setup
// declares them saved, as they are; asked, as answered.
func TestSetupKeepsAppsSettingsSavedAsTheyAre(t *testing.T) {
	no := false
	for _, tt := range []struct {
		name     string
		answered *bool
		want     bool
	}{
		{"not asked", nil, true},
		{"answered no", &no, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, m := snapshotted(t, nil)
			if err := os.WriteFile(filepath.Join(m.dirs.State, "prefs.json"), []byte(`{"on": "restored"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := s.update(func(r *record) { r.Prefs = tt.answered }); err != nil {
				t.Fatal(err)
			}
			if err := s.commitStep().Apply(t.Context(), check.Result{}); err != nil || len(m.declared) != 1 || m.declared[0].Prefs != tt.want {
				t.Errorf("declared %+v, %v: want apps' settings saved %v", m.declared, err, tt.want)
			}
		})
	}
}

// questionSteps are the snapshot's questions' steps, by name.
func (s *Setup) questionSteps() map[string]engine.Step {
	out := map[string]engine.Step{}
	for _, step := range []engine.Step{s.dataStep(), s.prefsStep(), s.filesStep(), s.installStep()} {
		out[step.Name] = step
	}
	return out
}

// Committing declares the snapshot and the answers as one (Declare), once
// GitHub says the repository's private, and ends setup's part only once
// it's pushed: a commit that failed, or a push, says a run again carries
// on, and never to delete kit-config's folder part-way. What's left is
// said: the apps installed by hand, which kit leaves to the person, and
// where secrets go.
func TestSetupCommitsTheAnswersThenSays(t *testing.T) {
	carryOn := "kit setup again carries on; never delete ~/.config/kit part-way, as files are moved into it and linked back"
	for _, tt := range []struct {
		name    string
		private bool
		pushURL string
		state   check.State
		says    string
		done    bool
	}{
		{"pushed", true, "", check.OK, "ok committed and pushed", true},
		{"not pushed", true, "", check.Attention, "attention committed, not pushed; " + carryOn, false},
		{"not committed", true, "", check.Failed, "failed couldn't commit; " + carryOn, false},
		{"public", false, "", check.OK, "failed someone/kit-config is public on GitHub, and kit-config is pushed only to a private repository: make it private, then run kit setup again", false},
		{"pushed elsewhere, public", true, "git@github.com:someone/dots.git", check.OK, "failed someone/dots is public on GitHub, and kit-config is pushed only to a private repository: make it private, then run kit setup again", false},
		{"pushed elsewhere, off GitHub", true, "https://gitlab.com/someone/kit-config.git", check.OK,
			"failed can't prove https://gitlab.com/someone/kit-config.git private: kit proves private only a repository on github.com, at https://github.com/<owner>/<name>, git@github.com:<owner>/<name> or ssh://git@github.com/<owner>/<name>: kit-config is pushed only to a repository kit can prove private", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, m := snapshotted(t, nil)
			m.onGitHub("someone/kit-config", tt.private, true)
			if tt.pushURL != "" {
				m.git.put("pushurl", tt.pushURL)()
				m.run.On("gh", "repo", "view", "https://github.com/someone/dots", "--json", "visibility", "--jq", ".visibility").Prints("PUBLIC\n")
			}
			m.pushes = []check.State{tt.state}
			yes := true
			if err := s.update(func(r *record) { r.Restore, r.Link, r.Prefs = &[]string{"~/Code"}, &[]string{}, &yes }); err != nil {
				t.Fatal(err)
			}
			p, _ := engine.New(s.commitStep())
			report, _ := p.Apply(t.Context(), s.Sink, engine.Options{Jobs: 1})
			if got := said(report, CommitStep)[0]; got != CommitStep+" "+tt.says {
				t.Errorf("stood %q, want %q", got, tt.says)
			}
			switch {
			case tt.state == check.OK && !tt.done:
				if len(m.declared) > 0 {
					t.Errorf("declared %+v, pushed where it isn't proven private", m.declared)
				}
			case len(m.declared) != 1 || !slices.Equal(m.declared[0].Restore, []string{"~/Code"}) || len(m.declared[0].Link) != 0 || !m.declared[0].Prefs || m.declared[0].Found != s.found:
				t.Errorf("declared %+v, want the snapshot and the answers recorded", m.declared)
			}
			if s.Done() != tt.done {
				t.Errorf("done %v, want %v", s.Done(), tt.done)
			}
		})
	}
	s, _ := snapshotted(t, nil)
	byHand := s.byHandStep().Check(t.Context())
	if !strings.HasPrefix(byHand.Summary, "2, which kit doesn't look after: if kit sets this Mac up again, they're yours to install, through Homebrew or the App Store") ||
		len(byHand.Items) != 2 || byHand.Items[0].Name != "Kubius" {
		t.Errorf("by hand said %+v", byHand)
	}
	if got := s.secretsStep().Check(t.Context()).Summary; got != "kit keeps secrets in a store, never in kit-config: kit secret add" {
		t.Errorf("secrets said %q", got)
	}
}

// What kit-config declares that this Mac hasn't, when a kind's check
// couldn't tell, is never taken for nothing: the question's asked, saying
// it couldn't tell, and why; without a terminal, its flags are named,
// saying so.
func TestToInstallSaysWhatItCouldntTell(t *testing.T) {
	const couldnt = "couldn't tell what kit-config declares that this Mac hasn't"
	for _, terminal := range []bool{true, false} {
		ask := &asker{chooses: []int{0}}
		s, m := snapshotted(t, map[bool]Asker{true: ask, false: nil}[terminal])
		for _, args := range [][]string{{"list", "--formula", "--full-name", "-1"}, {"leaves"}, {"leaves", "--installed-on-request"}} {
			m.run.On("brew", args...).Exits(1).PrintsToStderr("Error: something broke\n")
		}
		step := s.questionSteps()[InstallStep]
		if res := step.Check(t.Context()); res.State != check.Attention {
			t.Errorf("Homebrew's check failing, the step checks %s %q; want it to ask", res.State, res.Summary)
		}
		p, err := engine.New(step)
		if err != nil {
			t.Fatal(err)
		}
		report, _ := p.Apply(t.Context(), s.Sink, engine.Options{Jobs: 1})
		res := report.Results[InstallStep]
		switch {
		case terminal && (len(ask.asked) != 1 || !strings.HasPrefix(ask.asked[0], "to-install: "+couldnt+"\ncouldn't tell for Homebrew formulae (Error: something broke") || res.Summary != "with the rest"):
			t.Errorf("at a terminal, asked %q, stood %s %q; want it asked, saying it couldn't tell, and why", ask.asked, res.State, res.Summary)
		case !terminal && (res.State != check.Failed || !strings.Contains(res.Reason, "--apply installs what kit-config declares that this Mac hasn't with the rest, or --no-apply stops to look first; "+couldnt+", for Homebrew formulae (Error: something broke")):
			t.Errorf("without a terminal, stood %s %q; want its flags named, saying it couldn't tell", res.State, res.Reason)
		}
	}
}

// The folders the snapshot declares on the PATH count as its parts do, on
// the screen and in --json.
func TestTheSnapshotCountsThePath(t *testing.T) {
	res := counted([]snapshot.Part{
		{Kind: config.PathsKind, Label: snapshot.PathsLabel, Lines: []config.Entry{{Name: "/opt/homebrew/bin"}, {Name: "~/.local/share/mise/shims"}}},
		{Kind: "npm", Label: "npm packages", Lines: []config.Entry{{Name: "typescript"}}},
	})
	if want := "3: 2 PATH directories · 1 npm packages"; res.Summary != want || res.Counts[config.PathsKind] != 2 {
		t.Errorf("counted = %q, %v; want %q, 2 paths", res.Summary, res.Counts, want)
	}
}
