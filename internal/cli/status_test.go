package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/status"
)

// laptopWorld is a world whose Mac is laptop, declaring formulae and casks,
// with Homebrew installed as the fake says.
func laptopWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t, map[string]string{
		"kit.toml":    twoMacs,
		"brew":        "# Shell\njq\nowner/tap/tool\n",
		"brew.laptop": "go\n",
		"brew.studio": "ffmpeg\n",
		"cask":        "ghostty\n",
		"paths":       "~/.local/bin\n/opt/homebrew/bin\n",
	})
	w.write(t, filepath.Join(".local", "state", "kit", "machine"), "laptop\n")
	// The drift was first seen two days ago, so it needs attention.
	w.write(t, filepath.Join(".local", "state", "kit", "drift.json"),
		`{"first_seen": {"brew:ffmpeg": "2025-12-31T00:00:00Z", "brew:node@20": "2025-12-31T00:00:00Z", "cask:firefox": "2025-12-31T00:00:00Z"}}`)
	w.env["USER"] = "someone"
	w.env["KIT_UNRELATED"] = "never passed on"
	f := w.fake
	f.On("brew", "--prefix").Prints("/opt/homebrew\n")
	f.On("brew", "list", "--formula", "--full-name", "-1").Prints("go\njq\noniguruma\nowner/tap/tool\nnode@20\nffmpeg\n")
	f.On("brew", "leaves").Prints("go\njq\nowner/tap/tool\nnode@20\nffmpeg\n")
	f.On("brew", "leaves", "--installed-on-request").Prints("go\njq\nowner/tap/tool\nffmpeg\n")
	f.On("brew", "list", "--cask", "--full-name", "-1").Prints("ghostty\nfirefox\n")
	f.On("git", "-C", filepath.Join(w.home, ".config", "kit"), "remote", "get-url", "origin").Prints("git@github.com:someone/kit-config.git\n")
	f.On("gh", "repo", "view", "someone/kit-config", "--json", "visibility", "--jq", ".visibility").Prints("PRIVATE\n")
	return w
}

func TestStatus(t *testing.T) {
	w := laptopWorld(t)
	out, errOut, status := w.run(t, "status")
	want := `kit status · laptop
homebrew ok /opt/homebrew
brew attention 3 declared, all installed
brew extra ffmpeg
brew unused-dependency node@20
cask attention 1 declared, all installed
cask extra firefox
config-private ok private on GitHub (someone/kit-config)
2 need attention
`
	if out != want || errOut != "" || status != 1 {
		t.Errorf("kit status printed\n%s%q, exit %d\nwant\n%s(exit 1)", out, errOut, status, want)
	}
}

func TestStatusJSON(t *testing.T) {
	out, _, code := laptopWorld(t).run(t, "status", "--json")
	var doc status.Document
	if err := json.Unmarshal([]byte(out), &doc); err != nil || code != 1 {
		t.Fatalf("kit status --json printed %q, exit %d: %v", out, code, err)
	}
	if doc.Schema != 1 || doc.Kit != "0.1.0" || doc.Machine != "laptop" || !doc.Attention || len(doc.Steps) != 4 {
		t.Fatalf("document = %+v", doc)
	}
	brew := doc.Steps[1]
	if brew.ID != "brew" || brew.Counts["declared"] != 3 || brew.Counts["extra"] != 1 || brew.Items[1].ID != "brew:node@20" || brew.Items[1].State != "unused-dependency" {
		t.Errorf("brew = %+v", brew)
	}
}

func TestStatusWithNothingToAttendTo(t *testing.T) {
	w := laptopWorld(t)
	w.write(t, filepath.Join(".config", "kit", "brew.laptop"), "go\nffmpeg\nnode@20\n")
	w.write(t, filepath.Join(".config", "kit", "brew.studio"), "")
	w.write(t, filepath.Join(".config", "kit", "cask.laptop"), "firefox\n")
	out, errOut, status := w.run(t, "status")
	if !strings.HasSuffix(out, "Nothing needs attention\n") || errOut != "" || status != 0 {
		t.Errorf("kit status printed\n%s%q, exit %d; want nothing needing attention, exit 0", out, errOut, status)
	}
}

func TestStatusOfOneStep(t *testing.T) {
	out, _, status := laptopWorld(t).run(t, "status", "cask")
	want := "kit status · laptop\nhomebrew ok /opt/homebrew\ncask attention 1 declared, all installed\ncask extra firefox\n1 needs attention\n"
	if out != want || status != 1 {
		t.Errorf("kit status cask printed\n%s exit %d\nwant\n%s", out, status, want)
	}
	_, errOut, status := laptopWorld(t).run(t, "status", "plex")
	if !strings.Contains(errOut, "no step named plex on this Mac") || status != 2 {
		t.Errorf("kit status plex printed %q, exit %d; want it refused", errOut, status)
	}
}

func TestStatusDefersWhatNeedsHomebrew(t *testing.T) {
	w := laptopWorld(t)
	w.fake.On("brew", "--prefix").Fails(errNotFound)
	out, _, status := w.run(t, "status")
	for _, want := range []string{
		"homebrew attention not installed: brew isn't on kit's PATH\n",
		"brew deferred needs Homebrew\n",
		"cask deferred needs Homebrew\n",
		"config-private ok private on GitHub (someone/kit-config)\n",
		"1 needs attention, 2 deferred\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("kit status printed\n%s\nwant it to hold %q", out, want)
		}
	}
	if status != 1 {
		t.Errorf("exit %d, want 1", status)
	}
}

// Requirement 2: the Mac's name is settled before any config is read, and
// nothing runs without it.
func TestStatusNeedsTheMacsNameFirst(t *testing.T) {
	for _, tt := range []struct {
		machine string
		want    string
	}{
		{machine: "", want: "kit: this Mac has no name yet: run kit machine <name>, one of laptop, studio\n"},
		{machine: "mini\n", want: "kit: this Mac is named mini, which kit.toml doesn't know: run kit machine <name>, one of laptop, studio\n"},
	} {
		w := newWorld(t, map[string]string{"kit.toml": twoMacs, "brew.laptop": "go\n"})
		if tt.machine != "" {
			w.write(t, filepath.Join(".local", "state", "kit", "machine"), tt.machine)
		}
		out, errOut, status := w.run(t, "status")
		if out != "" || errOut != tt.want || status != 2 {
			t.Errorf("kit status printed %q, %q, exit %d; want %q, exit 2", out, errOut, status, tt.want)
		}
		if calls := w.fake.Calls(); len(calls) != 0 {
			t.Errorf("ran %q before the Mac had a name, want nothing", calls)
		}
		if _, err := os.Stat(filepath.Join(w.home, "Library", "Logs", "kit")); err == nil {
			t.Errorf("logged a run, want none")
		}
	}
}

// Requirement 3: kit runs programs on its own PATH, from the config's paths,
// in an environment of its own.
func TestStatusRunsProgramsOnKitsOwnPath(t *testing.T) {
	w := laptopWorld(t)
	w.env["PATH"] = "/somewhere/inherited/bin"
	w.env["SSH_AUTH_SOCK"] = "/private/tmp/agent.sock"
	if _, _, status := w.run(t, "status"); status != 1 {
		t.Fatalf("kit status exit %d, want 1", status)
	}
	wantPath := []string{filepath.Join(w.home, ".local", "bin"), "/opt/homebrew/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin"}
	if !slices.Equal(w.path, wantPath) {
		t.Errorf("kit's PATH = %q, want %q", w.path, wantPath)
	}
	wantEnv := []string{
		"HOME=" + w.home, "PATH=" + strings.Join(wantPath, ":"), "USER=someone", "SSH_AUTH_SOCK=/private/tmp/agent.sock",
		"HOMEBREW_NO_AUTO_UPDATE=1", "HOMEBREW_NO_ANALYTICS=1", "HOMEBREW_NO_ENV_HINTS=1", "HOMEBREW_NO_COLOR=1",
	}
	if !slices.Equal(w.childEnv, wantEnv) {
		t.Errorf("programs' environment = %q\nwant %q", w.childEnv, wantEnv)
	}
}

func TestStatusIsLogged(t *testing.T) {
	w := laptopWorld(t)
	if _, _, status := w.run(t, "status"); status != 1 {
		t.Fatalf("kit status exit %d, want 1", status)
	}
	out, _, status := w.run(t, "log")
	for _, want := range []string{
		"kit status · laptop · 2 Jan 2026 03:04:05",
		"! Formulae",
		"exit 0        0.0s  brew leaves --installed-on-request\n",
		"exit 0        0.0s  gh repo view someone/kit-config --json visibility --jq .visibility\n",
		"2 need attention",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("kit log printed\n%s\nwant it to hold %q", out, want)
		}
	}
	if status != 0 {
		t.Errorf("kit log exit %d, want 0", status)
	}
}

// Drift counts after a day: new drift is shown, quiet, and needs attention
// once a day has passed since it was first seen.
func TestStatusQuietensNewDrift(t *testing.T) {
	w := laptopWorld(t)
	w.write(t, filepath.Join(".local", "state", "kit", "drift.json"), "{}")

	out, _, code := w.run(t, "status")
	for _, want := range []string{"brew ok 3 declared, all installed\n", "brew extra:new ffmpeg\n", "brew unused-dependency:new node@20\n", "cask extra:new firefox\n", "Nothing needs attention\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("first kit status printed\n%s\nwant it to hold %q", out, want)
		}
	}
	if code != 0 {
		t.Errorf("first kit status exit %d, want 0", code)
	}
	if got := w.read(t, filepath.Join(".local", "state", "kit", "drift.json")); !strings.Contains(got, `"brew:ffmpeg": "2026-01-02T03:04:05Z"`) {
		t.Errorf("drift.json holds\n%s\nwant ffmpeg first seen now", got)
	}

	w.now = w.now.Add(23 * time.Hour)
	if _, _, code := w.run(t, "status"); code != 0 {
		t.Errorf("kit status a day less an hour later: exit %d, want 0", code)
	}
	w.now = w.now.Add(2 * time.Hour)
	out, _, code = w.run(t, "status")
	if !strings.Contains(out, "brew extra ffmpeg\n") || code != 1 {
		t.Errorf("kit status over a day later printed\n%s exit %d; want ffmpeg needing attention, exit 1", out, code)
	}
}

// A kind with nothing declared for the Mac, whose program isn't installed,
// has nothing to check, so its step isn't in the run.
func TestStatusLeavesOutAKindWithNothingToCheck(t *testing.T) {
	w := newWorld(t, map[string]string{"kit.toml": twoMacs, "brew.studio": "ffmpeg\n"})
	w.write(t, filepath.Join(".local", "state", "kit", "machine"), "laptop\n")
	w.fake.On("brew", "--prefix").Fails(errNotFound)
	w.fake.On("git", "-C", filepath.Join(w.home, ".config", "kit"), "remote", "get-url", "origin").Prints("git@github.com:someone/kit-config.git\n")
	w.fake.On("gh", "repo", "view", "someone/kit-config", "--json", "visibility", "--jq", ".visibility").Prints("PRIVATE\n")

	out, _, _ := w.run(t, "status")
	want := "kit status · laptop\nhomebrew attention not installed: brew isn't on kit's PATH\nconfig-private ok private on GitHub (someone/kit-config)\n1 needs attention\n"
	if out != want {
		t.Errorf("kit status printed\n%s\nwant\n%s", out, want)
	}
}
