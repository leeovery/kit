package boot

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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
// polling it, in turn, then the sign-in; and a config repository.
type gitHub struct {
	mu      sync.Mutex
	codes   int
	answers []string
	token   string
	public  bool
	repo    string
	archive []byte
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
		_ = json.NewEncoder(w).Encode(map[string]string{"login": "someone"})
	})
	mux.HandleFunc("GET /repos/{owner}/{name}", func(w http.ResponseWriter, r *http.Request) {
		if !signedIn(r) || r.PathValue("owner")+"/"+r.PathValue("name") != g.repo {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"private": !g.public})
	})
	mux.HandleFunc("GET /repos/{owner}/{name}/tarball", func(w http.ResponseWriter, r *http.Request) {
		if !signedIn(r) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(g.archive)
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
	name    string
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
	return a.choose, a.err
}

func (a *asker) Name(_ context.Context, step, question string) (string, error) {
	a.note(step + ": " + question)
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
		Home:   filepath.Join(home, "home"), Applications: filepath.Join(home, "Applications"), SudoLocal: filepath.Join(home, "sudo_local"),
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
	find := run.On("security", "find-generic-password", "-s", keychainItem, "-w").Exits(44)
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

	// Checked again, from the keychain, as a boot started again would be.
	find.Prints("gho_secret\n").Exits(0)
	b2, _ := newBoot(t, run, api, &asker{})
	b2.State, b2.Data, b2.Config = b.State, b.Data, b.Config
	if res := b2.gitHubStep().Check(context.Background()); res.State != check.OK || res.Summary != "someone · kit-config has laptop and studio" {
		t.Errorf("check after = %+v", res)
	}
}

// A code that expires before it's entered gives way to a new one; a
// sign-in declined on GitHub stops the boot, saying so.
func TestGitHubCodeExpiresOrIsDeclined(t *testing.T) {
	gh := &gitHub{answers: []string{"expired_token", "token"}, token: "gho_secret", repo: "someone/kit-config", archive: archive(t, map[string]string{config.File: kitToml})}
	b, _ := newBoot(t, nil, gh.serve(t), &asker{picks: []string{"enter", "", "enter"}})
	b.Run = runnertest.New(t)
	if _, err := b.signIn(context.Background()); err != nil || gh.codes != 2 {
		t.Errorf("signIn = %v after %d codes; want a sign-in from the second", err, gh.codes)
	}
	gh.answers = []string{"access_denied"}
	b.Ask = &asker{picks: []string{"enter"}}
	if _, err := b.signIn(context.Background()); !errors.Is(err, errDenied) {
		t.Errorf("signIn = %v; want it declined", err)
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
	if _, err := b.signIn(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(run.Calls(), "open https://github.com/login/device") {
		t.Errorf("ran %v; want GitHub's page opened", run.Calls())
	}
}

// A config repository that's public is refused: the data layer is always
// private. One that isn't there says how to name another.
func TestGitHubRefusesAPublicConfig(t *testing.T) {
	for _, tt := range []struct {
		name   string
		public bool
		repo   string
		want   string
	}{
		{"public", true, "someone/kit-config", "public on GitHub"},
		{"missing", false, "someone/elsewhere", "--config owner/name"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			gh := &gitHub{token: "gho_secret", public: tt.public, repo: tt.repo, archive: archive(t, map[string]string{config.File: kitToml})}
			run := runnertest.New(t)
			run.On("security", "find-generic-password", "-s", keychainItem, "-w").Prints("gho_secret")
			b, _ := newBoot(t, run, gh.serve(t), &asker{})
			step := b.gitHubStep()
			_ = step.Check(context.Background())
			if err := step.Apply(context.Background(), check.Result{}); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("apply = %v; want it to say %q", err, tt.want)
			}
			if _, err := os.Stat(b.archive()); err == nil {
				t.Error("read the config repository anyway")
			}
		})
	}
}

// This Mac: one of the config's, chosen, each with its description; or a
// new one, named, kept as new till kit-config has it; or the one --mac
// names, with nothing asked.
func TestThisMac(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mac    string
		ask    *asker
		want   string
		asked  []string
		newMac string
	}{
		{"chosen", "", &asker{choose: 1}, "studio", []string{"mac: which of your Macs is this? laptop (MacBook, the primary), studio, new (a Mac kit-config doesn't have yet)"}, ""},
		{"new", "", &asker{choose: 2, name: "mini"}, "mini", []string{"mac: which of your Macs is this? laptop (MacBook, the primary), studio, new (a Mac kit-config doesn't have yet)", "mac: its name, as kit-config will know it"}, "mini"},
		{"named", "laptop", &asker{}, "laptop", nil, ""},
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
			if st, _ := kitstate.Load[state](b.State, stateName); st.NewMac != tt.newMac {
				t.Errorf("new Mac %q; want %q", st.NewMac, tt.newMac)
			}
			if res := step.Check(context.Background()); res.State != check.OK || res.Summary != tt.want {
				t.Errorf("check after = %+v", res)
			}
		})
	}
	b, _ := newBoot(t, runnertest.New(t), GitHub{}, &asker{choose: 2, name: "No Good"})
	_ = os.MkdirAll(b.archive(), 0o700)
	_ = os.WriteFile(filepath.Join(b.archive(), config.File), []byte(kitToml), 0o600)
	if err := b.macStep().Apply(context.Background(), check.Result{}); err == nil || !strings.Contains(err.Error(), "can't name a Mac") {
		t.Errorf("a bad name: %v", err)
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
