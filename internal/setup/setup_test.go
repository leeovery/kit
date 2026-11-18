package setup

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
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

// newSetup is setup on a MacBook Pro of its own, kit 0.1.2, signed in to
// GitHub through GitHub's CLI as someone, who sees repos; asking with ask,
// nil for no terminal.
func newSetup(t *testing.T, repos map[string]bool, ask Asker) (*Setup, *runnertest.Fake) {
	t.Helper()
	dirs, err := config.Locate(t.TempDir(), func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	run := runnertest.New(t)
	run.On("security", "find-generic-password", "-s", "kit-github", "-w").Exits(44)
	run.On("gh", "auth", "token").Prints(boottest.Token + "\n")
	run.On("gh", "auth", "status", "--hostname", "github.com")
	run.On("security", "delete-generic-password", "-s", "kit-github")
	s := &Setup{
		Run: run, Sink: sink{}, GitHub: boottest.Serve(t, repos), Now: time.Now, Dirs: dirs, Kinds: kinds.Sections(),
		Getenv: func(string) string { return "" }, Version: "0.1.2", Model: "MacBook Pro",
	}
	if ask != nil {
		s.Ask = ask
	}
	return s, run
}

// clones scripts GitHub's CLI cloning repo into the config folder, the
// clone holding files, by name, and git set to sign in through it.
func clones(t *testing.T, s *Setup, run *runnertest.Fake, repo string, files map[string]string) {
	dir := s.Dirs.Config
	run.On("gh", "repo", "clone", repo, dir, "--", "--quiet", "--single-branch").Does(func() {
		cloned := map[string]string{".git/HEAD": "ref: refs/heads/main\n"}
		maps.Copy(cloned, files)
		for name, content := range cloned {
			path := filepath.Join(dir, name)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Error(err)
			}
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Error(err)
			}
		}
	})
	run.On("git", "-C", dir, "config", "credential.https://github.com.helper", "!gh auth git-credential")
}

// runTo runs setup's steps up to step, and what it needs: how each stood.
func runTo(t *testing.T, s *Setup, step string) engine.Report {
	t.Helper()
	p, err := engine.New(s.Steps()...)
	if err != nil {
		t.Fatal(err)
	}
	report, err := p.Apply(t.Context(), s.Sink, engine.Options{Command: "setup", Jobs: 1, Only: []string{step}})
	if err != nil {
		t.Fatal(err)
	}
	return report
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

// At a terminal, a person with no kit-config is offered one, created
// private on their account, and this Mac named as its model suggests: the
// config's first files say this kit is the oldest that reads it, this Mac
// is the primary, and, run in Ghostty, it's the terminal a new Mac's boot
// hands over to.
func TestSetupCreatesAConfig(t *testing.T) {
	ask := &asker{chooses: []int{0}}
	s, run := newSetup(t, map[string]bool{"someone/notes": true}, ask)
	s.Getenv = func(name string) string { return map[string]string{"TERM_PROGRAM": "ghostty"}[name] }
	run.On("gh", "repo", "create", "kit-config", "--private").Prints("https://github.com/someone/kit-config\n")
	clones(t, s, run, "someone/kit-config", map[string]string{})
	for _, args := range [][]string{
		{"symbolic-ref", "HEAD", "refs/heads/main"},
		{"config", "remote.origin.fetch", "+refs/heads/main:refs/remotes/origin/main"},
		{"config", "branch.main.remote", "origin"},
		{"config", "branch.main.merge", "refs/heads/main"},
	} {
		run.On("git", append([]string{"-C", s.Dirs.Config}, args...)...)
	}

	report := runTo(t, s, MacStep)
	want := []string{
		"github ok someone · GitHub's CLI's sign-in",
		"kit-config ok someone/kit-config · created, private",
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
	toml, _ := os.ReadFile(filepath.Join(s.Dirs.Config, config.File))
	if want := "format = 1\nminimum_kit = \"0.1.2\"\nprimary = \"macbook-pro\"\nterminal = \"ghostty\"\n\n[macs.macbook-pro]\ndescription = \"MacBook Pro\"\n"; string(toml) != want {
		t.Errorf("kit.toml =\n%s\nwant\n%s", toml, want)
	}
	if _, err := os.Stat(filepath.Join(s.Dirs.Config, config.DeclFile("macbook-pro"))); err != nil {
		t.Errorf("the Mac's declarations file: %v", err)
	}
	if name, _ := config.ReadMachine(s.Dirs.State); name != "macbook-pro" {
		t.Errorf("this Mac is named %q, want macbook-pro", name)
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
// is refused.
func TestSetupJoinsAConfig(t *testing.T) {
	for _, tt := range []struct {
		name     string
		private  bool
		given    Answers
		want     []string
		macTable string
		copied   string
	}{
		{"a Mac it knows", true, Answers{Join: "someone/kit-config", Mac: "studio"},
			[]string{"kit-config ok someone/kit-config · joined · has laptop and studio", "mac ok studio"}, "", ""},
		{"a new Mac, a copy of another", true, Answers{Join: "someone/kit-config", Mac: "mini", From: "laptop"},
			[]string{"kit-config ok someone/kit-config · joined · has laptop and studio", "mac ok mini · new"},
			"[macs.mini]\ndescription = \"MacBook Pro\"\nfrom = \"laptop\"\n", "[homebrew formulae]\njq\n"},
		{"a public repository", false, Answers{Join: "someone/kit-config", Mac: "studio"},
			[]string{"kit-config failed someone/kit-config is public on GitHub, and a config repository is always private: make it private, then run kit setup again", "mac deferred needs kit-config"}, "", ""},
		{"one it can't see", true, Answers{Join: "other/kit-config", Mac: "studio"},
			[]string{"kit-config failed other/kit-config isn't on GitHub, or this sign-in can't see it", "mac deferred needs kit-config"}, "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, run := newSetup(t, map[string]bool{"someone/kit-config": tt.private}, nil)
			s.Given = tt.given
			clones(t, s, run, "someone/kit-config", joinedConfig)
			report := runTo(t, s, MacStep)
			if got := said(report, ConfigStep, MacStep); !slices.Equal(got, tt.want) {
				t.Errorf("steps stood\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
			}
			toml, _ := os.ReadFile(filepath.Join(s.Dirs.Config, config.File))
			if tt.macTable != "" && !strings.HasSuffix(string(toml), tt.macTable) {
				t.Errorf("kit.toml =\n%s\nwant it to end %q", toml, tt.macTable)
			}
			if tt.macTable == "" && len(toml) > 0 && string(toml) != joinedConfig[config.File] {
				t.Errorf("kit.toml =\n%s\nwant it as it was", toml)
			}
			if tt.copied != "" {
				if got, _ := os.ReadFile(filepath.Join(s.Dirs.Config, config.DeclFile(tt.given.Mac))); string(got) != tt.copied {
					t.Errorf("%s's declarations = %q, want the copy %q", tt.given.Mac, got, tt.copied)
				}
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
			ask := &asker{chooses: tt.chooses, texts: tt.texts}
			s, run := newSetup(t, map[string]bool{"someone/dots": true, "someone/notes": true, "someone/site": false}, ask)
			clones(t, s, run, tt.repo, map[string]string{})
			report := runTo(t, s, ConfigStep)
			if got := said(report, ConfigStep); got[0] != "kit-config ok "+tt.repo+" · joined" {
				t.Errorf("stood %q", got[0])
			}
			if want := "kit-config: which of your private repositories?: someone/dots, someone/notes, Another"; len(ask.asked) < 2 || ask.asked[1] != want {
				t.Errorf("asked %q, want %q", ask.asked, want)
			}
		})
	}
}

// Without a terminal, setup asks nothing: a question its flag doesn't
// answer stops it there, naming the flag, and what's done is kept, so a run
// again, with the flag, carries on, creating nothing twice.
func TestSetupWithoutATerminalStopsAtAQuestionItsFlagDoesntAnswer(t *testing.T) {
	s, run := newSetup(t, map[string]bool{}, nil)
	report := runTo(t, s, MacStep)
	if got := said(report, ConfigStep); !strings.Contains(got[0], "kit-config failed no terminal to ask at: --create <name> makes a new config repository (kit-config, unless you've one), or --join owner/name joins one of yours") {
		t.Errorf("without --create or --join, %s", got[0])
	}

	s.Given.Create = "kit-config"
	run.On("gh", "repo", "create", "kit-config", "--private")
	clones(t, s, run, "someone/kit-config", map[string]string{})
	run.OnMatch("git", func(args []string) bool {
		return slices.Contains(args, "symbolic-ref") || slices.Contains(args, "config")
	})
	report = runTo(t, s, MacStep)
	if got := said(report, MacStep); got[0] != "mac failed no terminal to ask at: --mac <name> names this Mac (macbook-pro, say)" {
		t.Errorf("without --mac, %s", got[0])
	}
	if want := []string{"--create <name> makes a new config repository (kit-config, unless you've one), or --join owner/name joins one of yours", "--mac <name> names this Mac (macbook-pro, say)"}; !slices.Equal(s.Wanted(), want) {
		t.Errorf("wanted %q, want %q", s.Wanted(), want)
	}

	s.Given.Mac = "laptop"
	if got := said(runTo(t, s, MacStep), ConfigStep, MacStep); !slices.Equal(got, []string{"kit-config ok someone/kit-config · created, private", "mac ok laptop"}) {
		t.Errorf("a run again stood %q", got)
	}
	created := 0
	for _, call := range run.Calls() {
		if strings.HasPrefix(call, "gh repo create") {
			created++
		}
	}
	if created != 1 {
		t.Errorf("created kit-config %d times, want once", created)
	}
}

// Setup refuses a Mac whose config folder holds anything, a config above
// all, unless its own run part-way left it there; else it starts afresh,
// forgetting a run that finished.
func TestBegin(t *testing.T) {
	for _, tt := range []struct {
		name   string
		files  []string
		record record
		says   string
	}{
		{"nothing there", nil, record{}, ""},
		{"an empty folder", []string{"."}, record{}, ""},
		{"a config", []string{config.File}, record{}, "holds a config already, so kit looks after this Mac: kit status says how it stands"},
		{"something else", []string{"notes.txt"}, record{}, "is there, and isn't a config: move it aside, then run kit setup again"},
		{"setup's own, part-way", []string{config.File}, record{Repo: "someone/kit-config", Mac: "laptop"}, ""},
		{"setup's own, done", []string{config.File}, record{Repo: "someone/kit-config", Done: true}, "holds a config already"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dirs, _ := config.Locate(t.TempDir(), func(string) string { return "" })
			for _, f := range tt.files {
				if err := os.MkdirAll(dirs.Config, 0o700); err != nil {
					t.Fatal(err)
				}
				if f != "." {
					_ = os.WriteFile(filepath.Join(dirs.Config, f), nil, 0o600)
				}
			}
			if err := kitstate.Update(dirs.State, recordName, func(r *record) { *r = tt.record }); err != nil {
				t.Fatal(err)
			}
			err := Begin(dirs)
			switch {
			case tt.says == "" && err != nil, tt.says != "" && (err == nil || !strings.Contains(err.Error(), tt.says)):
				t.Errorf("Begin = %v, want %q", err, tt.says)
			}
			rec, _ := kitstate.Load[record](dirs.State, recordName)
			if kept := rec.Repo != ""; err == nil && kept != tt.record.partWay() {
				t.Errorf("the record is %+v: want it kept only part-way", rec)
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
	ByHand:  []string{"Kubius", "Sendhit"},
}

// snapshotted is setup on laptop, joined, the snapshot taken; shared
// declaring a formula the Mac hasn't, Homebrew not on it.
func snapshotted(t *testing.T, ask Asker) (*Setup, *runnertest.Fake) {
	s, run := newSetup(t, nil, ask)
	for name, content := range joinedConfig {
		path := filepath.Join(s.Dirs.Config, name)
		_ = os.MkdirAll(filepath.Dir(path), 0o700)
		_ = os.WriteFile(path, []byte(content), 0o600)
	}
	cfg, err := config.Load(s.Dirs.Config, s.Kinds)
	if err != nil {
		t.Fatal(err)
	}
	snap := found
	s.found, s.mac = &snap, snapshot.Mac{Name: "laptop", Config: cfg, Run: run, Dirs: s.Dirs}
	for _, args := range [][]string{{"list", "--formula", "--full-name", "-1"}, {"leaves"}, {"leaves", "--installed-on-request"}} {
		run.On("brew", args...).Fails(runner.ErrNotFound)
	}
	return s, run
}

// Each of the snapshot's questions takes its flag's answer; at a terminal,
// it's asked, its answer suggested; without one, it names its flags and
// what they choose from. Its answer's kept in the record, for a run again.
func TestTheSnapshotsQuestions(t *testing.T) {
	yes, no := true, false
	code := []string{"~/Code"}
	for _, tt := range []struct {
		name, step string
		given      Answers
		terminal   bool
		chooses    []int
		says       string
		asked      string
	}{
		{"data, as given", DataStep, Answers{Restore: &code}, false, nil, "ok 1 folder · 48.2 GB", ""},
		{"data, none", DataStep, Answers{Restore: &[]string{}}, false, nil, "ok none", ""},
		{"data, asked", DataStep, Answers{}, true, nil, "ok 1 folder · 48.2 GB",
			"restore: which folders should a new Mac bring back from your backups?: ~/Code 48.2 GB (ticked), ~/Movies 31.0 GB"},
		{"data, no terminal", DataStep, Answers{}, false, nil,
			"failed no terminal to ask at: --restore <path> for each folder a new Mac brings back from your backups (~/Code 48.2 GB, suggested; ~/Movies 31.0 GB), or --no-restore", ""},
		{"prefs, as given", PrefsStep, Answers{Prefs: &no}, false, nil, "ok not saved", ""},
		{"prefs, asked", PrefsStep, Answers{}, true, []int{0}, "ok every night",
			"prefs: save them every night, so a new Mac gets them back?: Save them every night, Not now"},
		{"prefs, no terminal", PrefsStep, Answers{}, false, nil,
			"failed no terminal to ask at: --prefs saves apps' settings every night, so a new Mac gets them back (suggested), or --no-prefs", ""},
		{"files, asked", FilesStep, Answers{}, true, nil, "ok 1", "files: which should kit-config keep, each moved in and linked back?: ~/.zshrc (ticked)"},
		{"files, no terminal", FilesStep, Answers{}, false, nil,
			"failed no terminal to ask at: --link <path> for each file kit-config keeps, moved in and linked back (~/.zshrc, all suggested), or --no-link", ""},
		{"to install, as given", InstallStep, Answers{Apply: &yes}, false, nil, "ok with the rest", ""},
		{"to install, asked", InstallStep, Answers{}, true, []int{1}, "ok stopped here to look first: kit status, then kit apply",
			"to-install: kit-config declares 1 thing this Mac hasn't: jq: Install them with the rest, Stop here to look first"},
		{"to install, no terminal", InstallStep, Answers{}, false, nil,
			"failed no terminal to ask at: --apply installs what kit-config declares 1 thing this Mac hasn't (jq) with the rest, or --no-apply stops to look first", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var ask *asker
			if tt.terminal {
				ask = &asker{chooses: tt.chooses}
			}
			s, _ := snapshotted(t, map[bool]Asker{true: ask, false: nil}[tt.terminal])
			s.Given = tt.given
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
			again := s.questionSteps()[tt.step].Check(t.Context())
			if res.State == check.OK && again.Summary != res.Summary {
				t.Errorf("checked again, %q; want the answer kept", again.Summary)
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

// Committing declares the snapshot and the answers as one (Declare), and
// ends setup's part once it's committed, a push that failed included; one
// that failed leaves setup to carry on. What's left is said: the apps
// installed by hand, which kit leaves to the person, and where secrets go.
func TestSetupCommitsTheAnswersThenSays(t *testing.T) {
	for _, state := range []check.State{check.OK, check.Attention, check.Failed} {
		t.Run(string(state), func(t *testing.T) {
			s, _ := snapshotted(t, nil)
			yes := true
			if err := s.update(func(r *record) { r.Restore, r.Link, r.Prefs = &[]string{"~/Code"}, &[]string{}, &yes }); err != nil {
				t.Fatal(err)
			}
			var got Declaring
			s.Declare = func(_ context.Context, d Declaring) check.Result {
				got = d
				return check.Result{State: state, Summary: "committed"}
			}
			step := s.commitStep()
			_ = step.Apply(t.Context(), check.Result{})
			if !slices.Equal(got.Restore, []string{"~/Code"}) || len(got.Link) != 0 || !got.Prefs || got.Found != s.found {
				t.Errorf("declared %+v, want the snapshot and the answers recorded", got)
			}
			if res := step.Check(t.Context()); res.State != state || s.Done() != (state != check.Failed) {
				t.Errorf("checked %+v, done %v", res, s.Done())
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
