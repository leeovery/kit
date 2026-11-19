package boot

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
	kitstate "github.com/leeovery/kit/internal/state"
)

// gitHub is GitHub as a test has it: a code to sign in with, the answers to
// polling it, in turn, then the sign-in; a config repository, and others,
// each empty when it has no archive.
type gitHub struct {
	mu      sync.Mutex
	codes   int
	answers []string
	token   string
	public  bool
	repo    string
	archive []byte
	others  map[string]repo
}

// repo is one of the others GitHub has: public or not, and its archive,
// none when it's empty.
type repo struct {
	public  bool
	archive []byte
}

// find is the repository GitHub has called name, and whether it has it.
func (g *gitHub) find(name string) (repo, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if name == g.repo {
		return repo{public: g.public, archive: g.archive}, true
	}
	r, ok := g.others[name]
	return r, ok
}

func (g *gitHub) serve(t *testing.T) GitHub {
	mux := http.NewServeMux()
	mux.HandleFunc("HEAD /{$}", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("POST /login/device/code", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.codes++
		g.mu.Unlock()
		if r.FormValue("client_id") != ClientID || r.FormValue("scope") != scopes {
			t.Errorf("asked for a code with %v", r.Form)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"device_code": "device", "user_code": "WDJB-MJHT", "verification_uri": "https://github.com/login/device", "expires_in": 900, "interval": 5})
	})
	mux.HandleFunc("POST /login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		if r.FormValue("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" || r.FormValue("device_code") != "device" {
			t.Errorf("polled with %v", r.Form)
		}
		answer := "token"
		if len(g.answers) > 0 {
			answer, g.answers = g.answers[0], g.answers[1:]
		}
		if answer == "token" {
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": g.token})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"error": answer})
	})
	signedIn := func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer "+g.token }
	mux.HandleFunc("GET /user", func(w http.ResponseWriter, r *http.Request) {
		if !signedIn(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"login": "someone", "id": 1234, "name": "Some One"})
	})
	mux.HandleFunc("GET /user/repos", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		list := []map[string]string{}
		for _, name := range slices.Sorted(maps.Keys(g.others)) {
			if !g.others[name].public {
				list = append(list, map[string]string{"full_name": name})
			}
		}
		_ = json.NewEncoder(w).Encode(list)
	})
	found := func(w http.ResponseWriter, r *http.Request) (repo, bool) {
		rp, ok := g.find(r.PathValue("owner") + "/" + r.PathValue("name"))
		if !ok || !signedIn(r) {
			w.WriteHeader(http.StatusNotFound)
			return repo{}, false
		}
		return rp, true
	}
	mux.HandleFunc("GET /repos/{owner}/{name}", func(w http.ResponseWriter, r *http.Request) {
		if rp, ok := found(w, r); ok {
			_ = json.NewEncoder(w).Encode(map[string]bool{"private": !rp.public})
		}
	})
	mux.HandleFunc("GET /repos/{owner}/{name}/branches", func(w http.ResponseWriter, r *http.Request) {
		if rp, ok := found(w, r); ok {
			branches := []map[string]string{}
			if rp.archive != nil {
				branches = append(branches, map[string]string{"name": "main"})
			}
			_ = json.NewEncoder(w).Encode(branches)
		}
	})
	mux.HandleFunc("GET /repos/{owner}/{name}/tarball", func(w http.ResponseWriter, r *http.Request) {
		if rp, ok := found(w, r); ok {
			_, _ = w.Write(rp.archive)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return GitHub{Client: srv.Client(), Site: srv.URL, API: srv.URL, ClientID: ClientID, Wait: func(context.Context, time.Duration) error { return nil }}
}

// archive is a config repository as GitHub's archive of it: in a folder of
// GitHub's naming, with a link that mustn't be followed and a path that
// would escape.
func archive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	add := func(h *tar.Header, body string) {
		h.Size = int64(len(body))
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte(body))
	}
	add(&tar.Header{Name: "someone-kit-config-abc123/", Typeflag: tar.TypeDir, Mode: 0o755}, "")
	for name, body := range files {
		add(&tar.Header{Name: "someone-kit-config-abc123/" + name, Typeflag: tar.TypeReg, Mode: 0o644}, body)
	}
	add(&tar.Header{Name: "someone-kit-config-abc123/link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}, "")
	add(&tar.Header{Name: "someone-kit-config-abc123/../../escape", Typeflag: tar.TypeReg, Mode: 0o644}, "out")
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const kitToml = "format = 1\nprimary = \"laptop\"\n[macs.laptop]\ndescription = \"MacBook, the primary\"\n[macs.studio]\n"

// asker answers the boot's questions as a test scripts them, noting each:
// the keys picked, in turn, then none, as a person who's stopped pressing;
// the passwords typed, in turn.
type asker struct {
	mu      sync.Mutex
	asked   []string
	choose  int
	chooses []int
	name    string
	names   []string
	err     error
	picks   []string
	secrets []string
}

func (a *asker) note(s string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.asked = append(a.asked, s)
}

func (a *asker) Choose(_ context.Context, step, question string, answers []Answer) (int, error) {
	labels := make([]string, len(answers))
	for i, an := range answers {
		labels[i] = an.Label
		if an.Does != "" {
			labels[i] += " (" + an.Does + ")"
		}
	}
	a.note(step + ": " + question + " " + strings.Join(labels, ", "))
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.chooses) > 0 {
		i := a.chooses[0]
		a.chooses = a.chooses[1:]
		return i, a.err
	}
	return a.choose, a.err
}

func (a *asker) Text(_ context.Context, step, question, suggested string) (string, error) {
	if suggested != "" {
		question += " [" + suggested + "]"
	}
	a.note(step + ": " + question)
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.names) > 0 {
		name := a.names[0]
		a.names = a.names[1:]
		return name, a.err
	}
	return a.name, a.err
}

func (a *asker) Pick(ctx context.Context, step string, keys []Key) (string, error) {
	a.mu.Lock()
	var key string
	if len(a.picks) > 0 {
		key, a.picks = a.picks[0], a.picks[1:]
	}
	a.mu.Unlock()
	if key != "" {
		return key, nil
	}
	<-ctx.Done()
	return "", ctx.Err()
}

func (a *asker) Secret(_ context.Context, step, question string) (string, error) {
	a.note(step + ": " + question)
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.secrets) == 0 {
		return "", errors.New("no password typed")
	}
	typed := a.secrets[0]
	a.secrets = a.secrets[1:]
	return typed, nil
}

func (a *asker) Wait(ctx context.Context, step, says, todo string, keys []Key) (string, error) {
	a.note(step + ": " + says + ": " + todo)
	return a.Pick(ctx, step, keys)
}

// sink keeps the events the boot emits.
type sink struct {
	mu     sync.Mutex
	events []event.Event
}

func (s *sink) Emit(e event.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
}

func newBoot(t *testing.T, run runner.Runner, gh GitHub, ask Asker) (*Boot, *sink) {
	home := t.TempDir()
	s := &sink{}
	return &Boot{
		Run: run, Sink: s, Ask: ask, GitHub: gh, Now: time.Now,
		State: filepath.Join(home, "state"), Data: filepath.Join(home, "data"), Config: filepath.Join(home, "config"),
		Getenv: func(string) string { return "" },
		Home:   filepath.Join(home, "home"), Applications: filepath.Join(home, "Applications"), SudoLocal: filepath.Join(home, "sudo_local"), Homebrew: config.HomebrewPaths,
		Self: "/Users/someone/.local/bin/kit",
	}, s
}

// A Mac with no sign-in signs in on the phone: a code shown, GitHub asked
// till it's entered, a little less often when it says to slow down; the
// sign-in kept in the keychain, through the tool's input, never its command
// line; then the config repository read, private, its Macs listed.
func TestGitHubSignsInAndReads(t *testing.T) {
	gh := &gitHub{answers: []string{"authorization_pending", "slow_down", "token"}, token: "gho_secret", repo: "someone/kit-config",
		archive: archive(t, map[string]string{config.File: kitToml, "laptop/declarations": "", "studio/declarations": ""})}
	api := gh.serve(t)
	var waits []time.Duration
	api.Wait = func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }
	run := runnertest.New(t)
	run.On("security", "find-generic-password", "-s", keychainItem, "-w").Exits(44)
	run.On("security", "-i")
	b, s := newBoot(t, run, api, &asker{picks: []string{"enter"}})
	step := b.gitHubStep()

	if res := step.Check(context.Background()); res.State != check.Attention || res.Summary != "not signed in" {
		t.Fatalf("check before = %+v", res)
	}
	if err := step.Apply(context.Background(), check.Result{}); err != nil {
		t.Fatalf("apply = %v", err)
	}
	if !slices.Equal(waits, []time.Duration{5 * time.Second, 5 * time.Second, 10 * time.Second}) {
		t.Errorf("waited %v between asking; want 5s, 5s, then 10s once told to slow down", waits)
	}
	cmds := run.Commands()
	kept := cmds[len(cmds)-1]
	if kept.Name != "security" || strings.Contains(kept.String(), "gho_secret") || !strings.Contains(kept.Input, "-w gho_secret") {
		t.Errorf("kept the sign-in with %q, input %q; want it on the input alone", kept.String(), kept.Input)
	}
	var code *event.DeviceCode
	for _, e := range s.events {
		if c, ok := e.(event.DeviceCode); ok {
			code = &c
		}
	}
	if code == nil || code.Code != "WDJB-MJHT" || code.URI != "https://github.com/login/device" {
		t.Errorf("showed %+v; want the code and where to enter it", code)
	}
	if _, err := os.Stat(filepath.Join(b.archive(), config.File)); err != nil {
		t.Errorf("the config repository wasn't read: %v", err)
	}
	for _, out := range []string{"link", "../escape", "../../escape"} {
		if _, err := os.Lstat(filepath.Join(b.archive(), out)); err == nil {
			t.Errorf("unpacked %s, which it should have left out", out)
		}
	}
	if st, _ := kitstate.Load[state](b.State, stateName); st.Repo != "someone/kit-config" {
		t.Errorf("state = %+v", st)
	}

	if res := step.Check(context.Background()); res.State != check.OK || res.Summary != "someone · kit-config has laptop and studio" {
		t.Errorf("check after = %+v", res)
	}
}

// A boot started again, signed in from the keychain, fetches kit-config
// again rather than read the copy an earlier boot left, as it may have
// changed since: a Mac added there is among this boot's. The boot handed
// over to reads the copy the boot before it fetched, and once kit-config's
// cloned, the clone is what's read.
func TestGitHubFetchesAgainOnEachBoot(t *testing.T) {
	files := map[string]string{config.File: kitToml, "laptop/declarations": "", "studio/declarations": ""}
	gh := &gitHub{token: "gho_secret", repo: "someone/kit-config", archive: archive(t, files)}
	api := gh.serve(t)
	run := runnertest.New(t)
	run.On("security", "find-generic-password", "-s", keychainItem, "-w").Prints("gho_secret\n")
	first, _ := newBoot(t, run, api, &asker{})
	step := first.gitHubStep()
	_ = step.Check(context.Background())
	if err := step.Apply(context.Background(), check.Result{}); err != nil {
		t.Fatalf("the first boot's apply = %v", err)
	}

	files[config.File], files["spare/declarations"] = kitToml+"[macs.spare]\n", ""
	gh.archive = archive(t, files)
	again, _ := newBoot(t, run, api, &asker{})
	again.State, again.Data, again.Config = first.State, first.Data, first.Config
	step = again.gitHubStep()
	if res := step.Check(context.Background()); res.State != check.Attention || res.Summary != "someone · kit-config not read yet" {
		t.Fatalf("a boot started again, before = %+v; want kit-config read again", res)
	}
	if err := step.Apply(context.Background(), check.Result{}); err != nil {
		t.Fatalf("a boot started again, apply = %v", err)
	}
	if res := step.Check(context.Background()); res.State != check.OK || res.Summary != "someone · kit-config has laptop, spare and studio" {
		t.Errorf("a boot started again, after = %+v; want kit-config as it is now", res)
	}

	handedOver, _ := newBoot(t, run, api, &asker{})
	handedOver.State, handedOver.Data, handedOver.Config, handedOver.HandedOver = first.State, first.Data, first.Config, true
	if res := handedOver.gitHubStep().Check(context.Background()); res.State != check.OK {
		t.Errorf("the boot handed over to = %+v; want the copy just fetched read", res)
	}

	cloned, _ := newBoot(t, run, api, &asker{})
	cloned.State, cloned.Data = first.State, first.Data
	for name, body := range map[string]string{".git/HEAD": "", config.File: kitToml, "laptop/declarations": "", "studio/declarations": ""} {
		path := filepath.Join(cloned.Config, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if res := cloned.gitHubStep().Check(context.Background()); res.State != check.OK || res.Summary != "someone · kit-config has laptop and studio" {
		t.Errorf("a boot with kit-config cloned = %+v; want the clone read", res)
	}
}

// A code that expires before it's entered gives way to a new one; a
// sign-in declined on GitHub stops the boot, saying so.
func TestGitHubCodeExpiresOrIsDeclined(t *testing.T) {
	gh := &gitHub{answers: []string{"expired_token", "token"}, token: "gho_secret", repo: "someone/kit-config", archive: archive(t, map[string]string{config.File: kitToml})}
	b, _ := newBoot(t, nil, gh.serve(t), &asker{picks: []string{"enter", "", "enter"}})
	b.Run = runnertest.New(t)
	if _, err := b.account().SignIn(context.Background(), GitHubStep); err != nil || gh.codes != 2 {
		t.Errorf("signIn = %v after %d codes; want a sign-in from the second", err, gh.codes)
	}
	gh.answers = []string{"access_denied"}
	b.Ask = &asker{picks: []string{"enter"}}
	if _, err := b.account().SignIn(context.Background(), GitHubStep); !errors.Is(err, errDenied) {
		t.Errorf("signIn = %v; want it declined", err)
	}
}

// slowPick is an asker whose waiting pick takes a moment to end once it's
// called off, and notes when it has.
type slowPick struct {
	asker
	ended atomic.Bool
}

func (a *slowPick) Pick(ctx context.Context, step string, keys []Key) (string, error) {
	if key, err := a.asker.Pick(ctx, step, keys); err == nil {
		return key, nil
	}
	time.Sleep(20 * time.Millisecond)
	a.ended.Store(true)
	return "", ctx.Err()
}

// Signed in, signing in has stopped asking how: the way it waited on while
// GitHub did is done with before it returns, as what runs next asks its
// own questions.
func TestGitHubSignInIsDoneAskingOnceSignedIn(t *testing.T) {
	gh := &gitHub{answers: []string{"token"}, token: "gho_secret"}
	ask := &slowPick{picks: []string{"enter"}}
	b, _ := newBoot(t, runnertest.New(t), gh.serve(t), ask)
	if _, err := b.account().SignIn(context.Background(), GitHubStep); err != nil {
		t.Fatal(err)
	}
	if !ask.ended.Load() {
		t.Error("signIn returned still asking how to sign in")
	}
}

// Signing in here, in the browser: kit opens GitHub's page there, the code
// shown to enter.
func TestGitHubSignInHere(t *testing.T) {
	gh := &gitHub{answers: []string{"authorization_pending", "token"}, token: "gho_secret"}
	run := runnertest.New(t)
	opened := make(chan struct{})
	run.On("open", "https://github.com/login/device").Does(func() { close(opened) })
	b, _ := newBoot(t, run, gh.serve(t), &asker{picks: []string{"s"}})
	api := b.GitHub
	api.Wait = func(ctx context.Context, _ time.Duration) error {
		select {
		case <-opened:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	b.GitHub = api
	if _, err := b.account().SignIn(context.Background(), GitHubStep); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(run.Calls(), "open https://github.com/login/device") {
		t.Errorf("ran %v; want GitHub's page opened", run.Calls())
	}
}

// A config repository that's public is refused: the data layer is always
// private. One --config names that isn't there, or holds no kit.toml, says
// how to name another. With none named and the person's kit-config not
// there, the boot asks which, failing as its asker does without a
// terminal.
func TestGitHubRefusesAPublicConfig(t *testing.T) {
	noTerminal := errors.New("no terminal to ask at")
	for _, tt := range []struct {
		name, repo, config string
		public             bool
		files              map[string]string
		ask                *asker
		want               string
		asked              []string
	}{
		{name: "public", repo: "someone/kit-config", public: true, ask: &asker{}, want: "public on GitHub"},
		{name: "missing, named", repo: "someone/elsewhere", config: "someone/nowhere", ask: &asker{},
			want: "someone/nowhere isn't on GitHub, or this sign-in can't see it: name yours with --config owner/name"},
		{name: "no kit.toml, named", repo: "someone/notes", config: "someone/notes", files: map[string]string{"README.md": "notes\n"}, ask: &asker{},
			want: "someone/notes holds no kit.toml, so it's no config repository: name yours with --config owner/name"},
		{name: "kit-config missing, named", repo: "someone/elsewhere", config: "someone/kit-config", ask: &asker{},
			want: "someone/kit-config isn't on GitHub, or this sign-in can't see it: name yours with --config owner/name"},
		{name: "missing, no terminal", repo: "someone/elsewhere", ask: &asker{err: noTerminal}, want: noTerminal.Error(),
			asked: []string{noConfig}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			files := tt.files
			if files == nil {
				files = map[string]string{config.File: kitToml}
			}
			gh := &gitHub{token: "gho_secret", public: tt.public, repo: tt.repo, archive: archive(t, files)}
			run := runnertest.New(t)
			run.On("security", "find-generic-password", "-s", keychainItem, "-w").Prints("gho_secret")
			b, _ := newBoot(t, run, gh.serve(t), tt.ask)
			b.Repo = tt.config
			step := b.gitHubStep()
			_ = step.Check(context.Background())
			if err := step.Apply(context.Background(), check.Result{}); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("apply = %v; want it to say %q", err, tt.want)
			}
			if _, err := os.Stat(b.archive()); err == nil {
				t.Error("read the config repository anyway")
			}
			if !slices.Equal(tt.ask.asked, tt.asked) {
				t.Errorf("asked %q; want %q", tt.ask.asked, tt.asked)
			}
		})
	}
}

// The answers to which repository is kit-config, and to the terminal a new
// one's Mac uses, as the boot's asker notes them.
const (
	create   = "Create kit-config (a new private repository on someone's account)"
	join     = "Join one of yours (pick from your private repositories)"
	another  = "Another name (create it under a name of your own)"
	noConfig = "github: someone has no repository named kit-config " + create + ", " + join + ", " + another
	which    = "github: which of your private repositories? "
	pasted   = "Another (named as owner/name)"
	terminal = "github: kit-config starts on this Mac: which terminal will it use? Ghostty (installed, then kit carries on there), Terminal (macOS's own: kit carries on here)"
)

// refused is the question asked again, saying why the repository named
// last was refused.
func refused(why string) string {
	return "github: " + why + " " + create + ", " + join + ", " + another
}

// With no kit-config of theirs and none named, the person chooses: one of
// their private repositories, listed, never looked inside; one named as
// owner/name; or one created, kit-config suggested. One that's empty, or
// to be created, starts kit-config on this Mac, the terminal it uses
// asked, nothing read. One refused, not there, public, holding files when
// it was to be created, or no kit.toml, isn't read, and the question's
// asked again, saying why.
func TestGitHubWithNoKitConfig(t *testing.T) {
	notes := repo{archive: archive(t, map[string]string{"README.md": "notes\n"})}
	configs := repo{archive: archive(t, map[string]string{config.File: kitToml, "laptop/declarations": "", "studio/declarations": ""})}
	for _, tt := range []struct {
		name    string
		others  map[string]repo
		ask     *asker
		asked   []string
		repo    string
		fresh   *fresh
		summary string
	}{
		{name: "created", ask: &asker{}, asked: []string{noConfig, terminal},
			repo: "someone/kit-config", fresh: &fresh{Create: true, Terminal: "ghostty"}, summary: "someone · someone/kit-config, new, private"},
		{name: "created, named", ask: &asker{chooses: []int{2, 1}, name: "my-config"}, asked: []string{noConfig, "github: its name, a new private repository on someone's account [kit-config]", terminal},
			repo: "someone/my-config", fresh: &fresh{Create: true}, summary: "someone · someone/my-config, new, private"},
		{name: "picked", others: map[string]repo{"someone/configs": configs, "someone/notes": notes}, ask: &asker{chooses: []int{1, 0}},
			asked: []string{noConfig, which + "someone/configs, someone/notes, " + pasted},
			repo:  "someone/configs", summary: "someone · kit-config has laptop and studio"},
		{name: "picked, empty", others: map[string]repo{"someone/empty": {}}, ask: &asker{chooses: []int{1, 0, 0}},
			asked: []string{noConfig, which + "someone/empty, " + pasted, terminal},
			repo:  "someone/empty", fresh: &fresh{Terminal: "ghostty"}, summary: "someone · someone/empty, private, empty"},
		{name: "picked, no kit.toml", others: map[string]repo{"someone/notes": notes}, ask: &asker{chooses: []int{1, 0, 0, 1}},
			asked: []string{noConfig, which + "someone/notes, " + pasted, refused("someone/notes holds no kit.toml, so it's no config repository"), terminal},
			repo:  "someone/kit-config", fresh: &fresh{Create: true}, summary: "someone · someone/kit-config, new, private"},
		{name: "pasted, public", others: map[string]repo{"other/public": {public: true, archive: configs.archive}}, ask: &asker{chooses: []int{1, 0, 0, 0}, name: "other/public"},
			asked: []string{noConfig, which + pasted, "github: its name, as owner/name", refused("other/public is public on GitHub, and a config repository is always private"), terminal},
			repo:  "someone/kit-config", fresh: &fresh{Create: true, Terminal: "ghostty"}, summary: "someone · someone/kit-config, new, private"},
		{name: "pasted, not there", ask: &asker{chooses: []int{1, 0, 0, 0}, names: []string{"other/nowhere"}},
			asked: []string{noConfig, which + pasted, "github: its name, as owner/name", refused("other/nowhere isn't on GitHub, or this sign-in can't see it"), terminal},
			repo:  "someone/kit-config", fresh: &fresh{Create: true, Terminal: "ghostty"}, summary: "someone · someone/kit-config, new, private"},
		{name: "their own, no kit.toml", others: map[string]repo{"someone/kit-config": notes}, ask: &asker{chooses: []int{1, 0}, name: "configs"},
			asked: []string{"github: someone/kit-config holds no kit.toml, so it's no config repository Join another of yours (pick from your private repositories), Create a new one (create it under a name of your own)",
				"github: its name, a new private repository on someone's account [kit-config]", terminal},
			repo: "someone/configs", fresh: &fresh{Create: true, Terminal: "ghostty"}, summary: "someone · someone/configs, new, private"},
		{name: "created, there with files", others: map[string]repo{"someone/notes": notes}, ask: &asker{chooses: []int{2, 0, 0}, names: []string{"notes"}},
			asked: []string{noConfig, "github: its name, a new private repository on someone's account [kit-config]", refused("someone/notes is on GitHub already, holding files"), terminal},
			repo:  "someone/kit-config", fresh: &fresh{Create: true, Terminal: "ghostty"}, summary: "someone · someone/kit-config, new, private"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			gh := &gitHub{token: "gho_secret", repo: "someone/elsewhere", others: tt.others}
			run := runnertest.New(t)
			run.On("security", "find-generic-password", "-s", keychainItem, "-w").Prints("gho_secret")
			b, _ := newBoot(t, run, gh.serve(t), tt.ask)
			step := b.gitHubStep()
			_ = step.Check(context.Background())
			if err := step.Apply(context.Background(), check.Result{}); err != nil {
				t.Fatalf("apply = %v", err)
			}
			if !slices.Equal(tt.ask.asked, tt.asked) {
				t.Errorf("asked\n%s\nwant\n%s", strings.Join(tt.ask.asked, "\n"), strings.Join(tt.asked, "\n"))
			}
			st, _ := kitstate.Load[state](b.State, stateName)
			if st.Repo != tt.repo || !reflect.DeepEqual(st.Fresh, tt.fresh) {
				t.Errorf("recorded %s, fresh %+v; want %s, fresh %+v", st.Repo, st.Fresh, tt.repo, tt.fresh)
			}
			if _, err := os.Stat(b.archive()); (err == nil) != (tt.fresh == nil) {
				t.Errorf("the boot's copy there: %v; want it only for a config read", err == nil)
			}
			if res := step.Check(context.Background()); res.State != check.OK || res.Summary != tt.summary {
				t.Errorf("check after = %+v; want %q", res, tt.summary)
			}
		})
	}
}

// A boot started again, kit-config chosen to start on this Mac, asks
// nothing again: the repository not created yet, or created and still
// empty, starts here as before; once it holds kit-config's first files,
// pushed by the boot before, it's read.
func TestGitHubWithNoKitConfigStartedAgain(t *testing.T) {
	gh := &gitHub{token: "gho_secret", repo: "someone/elsewhere", others: map[string]repo{}}
	api := gh.serve(t)
	run := runnertest.New(t)
	run.On("security", "find-generic-password", "-s", keychainItem, "-w").Prints("gho_secret")
	first, _ := newBoot(t, run, api, &asker{})
	step := first.gitHubStep()
	_ = step.Check(context.Background())
	if err := step.Apply(context.Background(), check.Result{}); err != nil {
		t.Fatalf("the first boot's apply = %v", err)
	}
	for _, tt := range []struct {
		name    string
		there   *repo
		summary string
	}{
		{"not created yet", nil, "someone · someone/kit-config, new, private"},
		{"created, empty", &repo{}, "someone · someone/kit-config, new, private"},
		{"pushed", &repo{archive: archive(t, map[string]string{config.File: kitToml})}, "someone · kit-config has laptop and studio"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if tt.there != nil {
				gh.mu.Lock()
				gh.others["someone/kit-config"] = *tt.there
				gh.mu.Unlock()
			}
			ask := &asker{err: errors.New("asked again")}
			again, _ := newBoot(t, run, api, ask)
			again.State, again.Data, again.Config = first.State, first.Data, first.Config
			step := again.gitHubStep()
			if res := step.Check(context.Background()); res.State != check.Attention {
				t.Fatalf("before = %+v; want kit-config chosen again", res)
			}
			if err := step.Apply(context.Background(), check.Result{}); err != nil || len(ask.asked) > 0 {
				t.Fatalf("apply = %v, asking %q; want nothing asked", err, ask.asked)
			}
			if res := step.Check(context.Background()); res.State != check.OK || res.Summary != tt.summary {
				t.Errorf("after = %+v; want %q", res, tt.summary)
			}
		})
	}
}

// This Mac: one of the config's, chosen, each with its description; or a
// new one, named, then started fresh or from one of the config's Macs, kept
// as new, with where it starts from, till kit-config has it; or the one
// --mac names, with nothing asked. A new Mac started from another, or one
// kit-config has, clean-installed under its own name, records that the boot
// set it up; a new Mac started fresh doesn't.
func TestThisMac(t *testing.T) {
	const (
		which   = "mac: which of your Macs is this? laptop (MacBook, the primary), studio, new (a new machine ID, started fresh or from one of your Macs)"
		named   = "mac: its name, as kit-config will know it"
		badName = "mac: that can't name a Mac: lower case letters, digits and dashes"
		start   = "mac: where does it start from? fresh (only what every Mac shares), laptop (a copy of laptop's setup and settings to start from), studio (a copy of studio's setup and settings to start from)"
	)
	for _, tt := range []struct {
		name         string
		mac          string
		ask          *asker
		want         string
		asked        []string
		newMac, from string
		setUp        bool
	}{
		{"chosen", "", &asker{choose: 1}, "studio", []string{which}, "", "", true},
		{"new, fresh", "", &asker{chooses: []int{2, 0}, name: "mini"}, "mini", []string{which, named, start}, "mini", "", false},
		{"new, from studio", "", &asker{chooses: []int{2, 2}, name: "mini"}, "mini", []string{which, named, start}, "mini", "studio", true},
		{"new, a name that can't be one, asked again", "", &asker{chooses: []int{2, 0}, names: []string{"No Good"}, name: "mini"}, "mini", []string{which, named, badName, start}, "mini", "", false},
		{"named", "laptop", &asker{}, "laptop", nil, "", "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b, _ := newBoot(t, runnertest.New(t), GitHub{}, tt.ask)
			b.Mac = tt.mac
			if err := os.MkdirAll(b.archive(), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(b.archive(), config.File), []byte(kitToml), 0o600); err != nil {
				t.Fatal(err)
			}
			step := b.macStep()
			if res := step.Check(context.Background()); res.State != check.Attention {
				t.Fatalf("check before = %+v", res)
			}
			if err := step.Apply(context.Background(), check.Result{}); err != nil {
				t.Fatalf("apply = %v", err)
			}
			if got, _ := config.ReadMachine(b.State); got != tt.want {
				t.Errorf("named %q; want %q", got, tt.want)
			}
			if !slices.Equal(tt.ask.asked, tt.asked) {
				t.Errorf("asked %q; want %q", tt.ask.asked, tt.asked)
			}
			if st, _ := kitstate.Load[state](b.State, stateName); st.NewMac != tt.newMac || st.From != tt.from {
				t.Errorf("new Mac %q from %q; want %q from %q", st.NewMac, st.From, tt.newMac, tt.from)
			}
			if setUp, err := SetUp(b.State); err != nil || setUp != tt.setUp {
				t.Errorf("SetUp() = %v, %v; want %v", setUp, err, tt.setUp)
			}
			if res := step.Check(context.Background()); res.State != check.OK || res.Summary != tt.want {
				t.Errorf("check after = %+v", res)
			}
		})
	}
	for _, bad := range []string{"No Good", config.Shared} {
		b, _ := newBoot(t, runnertest.New(t), GitHub{}, &asker{})
		b.Mac = bad
		_ = os.MkdirAll(b.archive(), 0o700)
		_ = os.WriteFile(filepath.Join(b.archive(), config.File), []byte(kitToml), 0o600)
		if err := b.macStep().Apply(context.Background(), check.Result{}); err == nil || !strings.Contains(err.Error(), "can't name a Mac") {
			t.Errorf("--mac %s: %v; want it refused", bad, err)
		}
	}
}

// A Mac added to kit-config ahead, started from another (kit machine add
// --from), records that the boot set it up, however the boot's told its
// name: chosen, typed as a new one, or given; as does one kit-config has
// with no from, clean-installed under its own name. Not on a Mac kit had
// named before this boot; one recorded already stays so.
func TestSetUpForAMacAddedAhead(t *testing.T) {
	const ahead = "format = 1\nprimary = \"laptop\"\n[macs.laptop]\n[macs.studio]\nfrom = \"laptop\"\n"
	for _, tt := range []struct {
		name   string
		mac    string
		ask    *asker
		named  string
		before bool
		want   bool
	}{
		{"chosen", "", &asker{choose: 1}, "", false, true},
		{"typed as a new one", "", &asker{chooses: []int{2}, name: "studio"}, "", false, true},
		{"given", "studio", &asker{}, "", false, true},
		{"given, on a Mac kit named before", "studio", &asker{}, "old", false, false},
		{"given, on a Mac kit named before, recorded set up by an earlier boot", "studio", &asker{}, "old", true, true},
		{"a Mac kit-config has with no from, chosen: clean-installed", "", &asker{choose: 0}, "", false, true},
		{"a Mac kit-config has with no from, on a Mac kit named before", "laptop", &asker{}, "old", false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b, _ := newBoot(t, runnertest.New(t), GitHub{}, tt.ask)
			b.Mac = tt.mac
			writeFiles(t, b.archive(), map[string]string{config.File: ahead})
			if tt.named != "" {
				if err := config.WriteMachine(b.State, tt.named); err != nil {
					t.Fatal(err)
				}
			}
			if err := kitstate.Update(b.State, stateName, func(st *state) { st.SetUp = tt.before }); err != nil {
				t.Fatal(err)
			}
			if err := b.macStep().Apply(context.Background(), check.Result{}); err != nil {
				t.Fatalf("apply = %v", err)
			}
			if setUp, err := SetUp(b.State); err != nil || setUp != tt.want {
				t.Errorf("SetUp() = %v, %v; want %v", setUp, err, tt.want)
			}
			if st, _ := kitstate.Load[state](b.State, stateName); st.NewMac != "" || st.From != "" {
				t.Errorf("new Mac %q from %q; want none: kit-config has it", st.NewMac, st.From)
			}
		})
	}
}

// A Mac part-way through set-up under kit before set_up, its boot having
// recorded when its set-up began instead, is set up still, and stays so as
// the boot's record changes.
func TestSetUpReadsWhenSetUpBegan(t *testing.T) {
	stateDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stateDir, stateName), []byte(`{"began": "2026-11-20T09:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if setUp, err := SetUp(stateDir); err != nil || !setUp {
		t.Fatalf("SetUp() = %v, %v; want set up", setUp, err)
	}
	if err := ForgetNewMac(stateDir); err != nil {
		t.Fatal(err)
	}
	if setUp, err := SetUp(stateDir); err != nil || !setUp {
		t.Errorf("the record changed, SetUp() = %v, %v; want set up still", setUp, err)
	}
}

// The self-test: the chip, memory, the disk, macOS, the network.
func TestSelfTest(t *testing.T) {
	gh := &gitHub{}
	run := runnertest.New(t)
	run.On("scutil", "--get", "LocalHostName").Prints("Some-MacBook\n")
	run.On("sysctl", "-n", "machdep.cpu.brand_string").Prints("Apple M1 Max\n")
	run.On("sysctl", "-n", "hw.ncpu").Prints("10\n")
	run.On("sysctl", "-n", "hw.memsize").Prints("68719476736\n")
	run.On("diskutil", "info", "/").Prints("   Device Node:   /dev/disk3s1\n   Volume Name:   Macintosh HD\n")
	run.On("sw_vers", "-productVersion").Prints("26.7.1\n")
	b, _ := newBoot(t, run, gh.serve(t), &asker{})
	got := b.SelfTest(context.Background(), func(string) (uint64, uint64, error) { return 912e9, 994e9, nil })
	var lines []string
	for _, tst := range got.Tests {
		lines = append(lines, string(tst.State)+" "+tst.Name+": "+strings.Join(tst.Says, " · "))
	}
	want := []string{
		"ok Apple M1 Max: 10 cores",
		"ok Memory: 65536 MB",
		"ok Disk: Macintosh HD · 912 GB free of 994 GB",
		"ok macOS: 26.7.1",
		"ok Network: github.com · ",
	}
	for i := range lines {
		if i < len(want) && strings.HasPrefix(lines[i], "ok Network: github.com · ") && strings.HasSuffix(lines[i], " ms") {
			lines[i] = "ok Network: github.com · "
		}
	}
	if got.Mac != "Some-MacBook" || !slices.Equal(lines, want) {
		t.Errorf("self-test of %q =\n%s\nwant\n%s", got.Mac, strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
}
