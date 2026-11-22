package cli_test

import (
	"bytes"
	"flag"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/backup/arq"
	"github.com/leeovery/kit/internal/backup/timemachine"
	"github.com/leeovery/kit/internal/boot/boottest"
	"github.com/leeovery/kit/internal/kind/defaults"
	"github.com/leeovery/kit/internal/render/rendertest"
)

var update = flag.Bool("update", false, "rewrite the golden files with what kit setup's screen draws")

// tty is a terminal kit runs at in a test: what kit draws on it, and the
// keys typed at it.
type tty struct {
	mu   sync.Mutex
	out  bytes.Buffer
	keys *io.PipeWriter
}

func (s *tty) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.out.Write(p)
}

func (s *tty) raw() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.out.String()
}

// atTerminal has w's kit run at a terminal width by height, in true colour,
// drawing on the tty it returns.
func atTerminal(t *testing.T, w *world, width, height int) *tty {
	in, keys := io.Pipe()
	t.Cleanup(func() { _ = keys.Close() })
	s := &tty{keys: keys}
	w.terminal, w.size, w.input, w.output = true, [2]int{width, height}, in, s
	w.environ = []string{"TERM=xterm-256color", "COLORTERM=truecolor", "CLICOLOR_FORCE=1"}
	return s
}

// keyCodes are what a terminal sends for the keys a test presses by name.
var keyCodes = map[string]string{"enter": "\r", "esc": "\x1b", "up": "\x1b[A", "down": "\x1b[B", "space": " ", "backspace": "\x7f"}

// press types keys, by name or as text.
func (s *tty) press(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		code, named := keyCodes[k]
		if !named {
			code = k
		}
		if _, err := io.WriteString(s.keys, code); err != nil {
			t.Fatalf("type %q: %v", k, err)
		}
		// One key at a time, as a person types them, so none is taken for
		// part of another's sequence.
		time.Sleep(20 * time.Millisecond)
	}
}

// showing waits till the screen shows text, then is the screen as it
// stands, each cell styled, its running marks as they start.
func (s *tty) showing(t *testing.T, text string) string {
	t.Helper()
	return s.when(t, text, func(screen string) bool { return strings.Contains(screen, text) })
}

// when waits till the screen, its text alone, is as shows says, then is
// the screen as it stands, styled, its running marks as they start.
func (s *tty) when(t *testing.T, what string, shows func(screen string) bool) string {
	t.Helper()
	for range 1000 {
		raw := s.raw()
		if shows(turned(rendertest.Screen(raw))) {
			return turned(rendertest.Styled(raw))
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the screen never showed %s:\n%s", what, rendertest.Screen(s.raw()))
	return ""
}

// turned is a screen with its running marks as they start, whichever way
// they've turned since.
func turned(screen string) string {
	return strings.NewReplacer("◓", "◐", "◑", "◐", "◒", "◐").Replace(screen)
}

// matches checks screen against testdata/<name>.golden, cell for cell: its
// glyphs, their colours, and which are bold. -update writes it.
func matches(t *testing.T, name, screen string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(screen), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s (run with -update to write it): %v", path, err)
	}
	if screen != string(want) {
		t.Errorf("drew\n%s\nwant %s's\n%s", ansi.Strip(screen), path, ansi.Strip(string(want)))
	}
}

// running runs kit with args in w, in the background: what it ends with,
// once it has.
func running(t *testing.T, w *world, args ...string) <-chan int {
	ended := make(chan int, 1)
	go func() {
		_, _, code := w.run(t, args...)
		ended <- code
	}()
	return ended
}

// signedOut is a Mac in use whose GitHub's CLI isn't signed in: setup signs
// in on the phone, then GitHub's CLI has it, as handing over does. Homebrew
// is here, two formulae and a cask installed; so are a folder or two of
// the person's, and their shell's and tools' files; Touch ID for sudo and
// File Sharing are on, and Time Machine and Arq back it up.
func signedOut(t *testing.T, repos map[string]bool) *world {
	w := inUse(t, repos)
	f := w.fake
	f.On("gh", "auth", "token").Exits(1)
	f.On("security", "-i")
	f.On("brew", "list", "--formula", "--full-name", "-1").Prints("jq\nripgrep\n")
	f.On("brew", "leaves").Prints("jq\nripgrep\n")
	f.On("brew", "leaves", "--installed-on-request").Prints("jq\nripgrep\n")
	f.On("brew", "list", "--cask", "--full-name", "-1").Prints("ghostty\n")
	w.caskroom(t, nil)
	for _, path := range []string{"Documents/notes.txt", "Pictures/photo.jpg", "Downloads/archive.zip", ".zshrc", ".tmux.conf", ".config/ghostty/config"} {
		w.write(t, path, "something\n")
	}
	w.write(t, filepath.Join("etc", "sudo_local"), "auth       sufficient     pam_tid.so\n")
	f.On("launchctl", "print-disabled", "system").Prints("disabled services = {\n\t\"com.openssh.sshd\" => disabled\n\t\"com.apple.smbd\" => enabled\n}\n")
	f.On("defaults", "export", timemachine.Prefs, "-").Prints("<plist><dict><key>AutoBackup</key><true/><key>Destinations</key><array><dict/></array></dict></plist>")
	f.On("tmutil", "status").Prints("Running = 0;\n")
	f.On("tmutil", "destinationinfo").Prints("Mount Point   : /Volumes/Backups\n")
	f.On(arq.Arqc, "stats").Prints(`{"backupPlans": [{"name": "Offsite", "lastBackedUp": "2026-01-02T02:00:00Z", "schedule": {"type": "daily"}}]}`)
	return w
}

// settingUp is kit setup's screen's Mac in use: signed out, kit-config to
// be created, two apps installed by hand, Tally one a cask installs; kit
// apply, carrying on, finding it all as setup left it.
func settingUp(t *testing.T) *world {
	w := signedOut(t, map[string]bool{})
	w.creates(t)
	healthyMac(w)
	for _, name := range []string{"Sketchpad", "Tally"} {
		w.write(t, filepath.Join("Applications", name+".app", "Contents", "Info.plist"), `<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.example.`+strings.ToLower(name)+`</string></dict></plist>`)
	}
	dir := filepath.Join(w.home, ".config", "kit")
	w.fake.On("git", "-C", dir, "check-ignore", "--verbose", "--stdin", "-z").Exits(1)
	w.fake.On("brew", "--prefix").Prints("/opt/homebrew\n")
	w.homebrewCache(t, `{"protected":"e30","signature":"c2ln"}
{"casks": {"tally": {"auto_updates": true, "raw_artifacts": [[":uninstall", {":quit": "com.example.tally"}], [":app", ["Tally.app"]]]}}}`)
	w.fake.On("sudo", "-S", "-p", "[kit: sudo wants the password]", "-v")
	w.fake.On("git", "-C", dir, "--literal-pathspecs", "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", "laptop/home").Prints("laptop/home/.zshrc\x00")
	return w
}

// laptopCommit is setup's commit of the Mac named laptop, ~/.zshrc linked.
var laptopCommit = commit{
	message: "kit setup (laptop): this Mac as it is", paths: []string{"kit.toml", "shared", "laptop"},
	files:  []string{"kit.toml", "laptop/declarations", "laptop/home/.zshrc", "shared/declarations"},
	status: "?? kit.toml\x00?? laptop/declarations\x00?? laptop/home/.zshrc\x00?? shared/declarations\x00",
}

// readingSlowly has the snapshot's read of macOS's settings wait till read,
// which the test's end calls too.
func (w *world) readingSlowly(t *testing.T) (read func()) {
	reading := make(chan struct{})
	read = sync.OnceFunc(func() { close(reading) })
	t.Cleanup(read)
	w.fake.On("defaults", "export", defaults.Watched[0], "-").Prints("<plist version=\"1.0\"><dict/></plist>").Does(func() { <-reading })
	return read
}

// stoppedAs checks what kit setup, stopped, leaves on screen: rows done as
// they were done, row says, and nothing said to fail, nor skipped, nor
// what's said only once setup's done.
func stoppedAs(t *testing.T, screen *tty, ended <-chan int, says string) {
	t.Helper()
	code := <-ended
	left := rendertest.Screen(screen.raw())
	if code == 0 || !strings.Contains(left, says) {
		t.Errorf("stopped, kit setup exited %d, leaving\n%s\nwant it to hold %q", code, left, says)
	}
	for _, never := range []string{"✗", "the run was stopped", "skipped", "INSTALLED BY HAND", "kit keeps secrets", "esc stop"} {
		if strings.Contains(left, never) {
			t.Errorf("stopped, kit setup left\n%s\nwant no %q", left, never)
		}
	}
}

// Esc at any of kit setup's questions stops it there: that question's row
// says it stopped, the rows done before it as they were, and nothing more.
func TestSetupScreenStoppedAtAQuestion(t *testing.T) {
	questions := []struct{ asks, row string }{
		{"how will you sign in?", "– GitHub  stopped"},
		{"has no repository named kit-config", "– kit-config  stopped"},
		{"what will kit-config call it?", "– This Mac  stopped"},
		{"which folders should a new Mac bring back", "– Your data  stopped"},
		{"save them every night", "– Apps' settings  stopped"},
		{"which should kit-config keep", "– Linked files  stopped"},
		{"which apps should Homebrew take over", "– To Homebrew  stopped"},
	}
	for at, q := range questions {
		t.Run(q.row, func(t *testing.T) {
			w := settingUp(t)
			screen := atTerminal(t, w, 80, 48)
			ended := running(t, w, "setup")
			for _, before := range questions[:at] {
				screen.showing(t, before.asks)
				screen.press(t, "enter")
				if before.asks == "how will you sign in?" {
					screen.showing(t, boottest.Code)
					w.repos.Entered()
				}
			}
			screen.showing(t, q.asks)
			screen.press(t, "esc")
			stoppedAs(t, screen, ended, q.row)
			if at > 1 && !strings.Contains(rendertest.Screen(screen.raw()), "● kit-config  someone/kit-config · new, private") {
				t.Errorf("stopped, kit setup left\n%s\nwant kit-config's row as it was done", rendertest.Screen(screen.raw()))
			}
		})
	}
}

// Esc while the snapshot looks stops setup: no part said to fail, and the
// questions after it say they stopped.
func TestSetupScreenStoppedWhileFinding(t *testing.T) {
	w := settingUp(t)
	read := w.readingSlowly(t)
	screen := atTerminal(t, w, 80, 48)
	ended := running(t, w, "setup", "--create", "kit-config", "--mac", "laptop")
	screen.showing(t, "how will you sign in?")
	screen.press(t, "enter")
	screen.showing(t, boottest.Code)
	w.repos.Entered()
	screen.showing(t, "◐ macOS settings")
	screen.press(t, "esc")
	read()
	stoppedAs(t, screen, ended, "– Linked files  stopped\n  – To Homebrew  stopped\n  – To install  stopped")
}

// Esc while setup commits and pushes stops it: kit-config's row as it was
// done, never pushed, nor skipped.
func TestSetupScreenStoppedWhileCommitting(t *testing.T) {
	w := settingUp(t)
	w.expectCommit(t, laptopCommit)
	w.fake.On("git", "-C", filepath.Join(w.home, ".config", "kit"), "push", "--quiet", "origin", "HEAD:refs/heads/main").Hangs()
	screen := atTerminal(t, w, 80, 48)
	ended := running(t, w, "setup", "--create", "kit-config", "--mac", "laptop", "--no-restore", "--no-prefs", "--link", "~/.zshrc", "--no-adopt")
	screen.showing(t, "how will you sign in?")
	screen.press(t, "enter")
	screen.showing(t, boottest.Code)
	w.repos.Entered()
	screen.showing(t, "committing and pushing")
	screen.press(t, "esc")
	stoppedAs(t, screen, ended, "● kit-config  someone/kit-config · new, private\n")
}

// At a terminal, kit setup draws its screen as round 13's frames have it,
// from a Mac in use, its GitHub's CLI signed out, kit-config made anew:
// signing in, the way chosen; the code, with the phone, the whole window;
// the config; this Mac's name, typed; the snapshot part-way, a row a part;
// your data, ticked; apps' settings; the files to link; the apps Homebrew
// can take over, Tally left by hand, so kit apply never asks of it.
func TestSetupScreen(t *testing.T) {
	w := settingUp(t)
	w.expectCommit(t, laptopCommit)
	read := w.readingSlowly(t)
	screen := atTerminal(t, w, 80, 48)
	ended := running(t, w, "setup")

	matches(t, "setup-signing-in", screen.showing(t, "how will you sign in?"))
	screen.press(t, "enter")
	matches(t, "setup-code", screen.showing(t, boottest.Code))
	w.repos.Entered()

	matches(t, "setup-config", screen.showing(t, "has no repository named kit-config"))
	screen.press(t, "enter")

	screen.showing(t, "what will kit-config call it?")
	for range "macbook-pro" {
		screen.press(t, "backspace")
	}
	screen.press(t, "laptop")
	matches(t, "setup-naming", screen.showing(t, "❯ laptop"))
	screen.press(t, "enter")

	matches(t, "setup-snapshot", screen.when(t, "the snapshot, macOS's settings alone being read", func(s string) bool {
		return strings.Contains(s, "◐ macOS settings") && strings.Count(s, "◐") == 1 && strings.Count(s, "○") == 6
	}))
	read()

	screen.showing(t, "which folders should a new Mac bring back")
	screen.press(t, "down", "down")
	matches(t, "setup-your-data", screen.showing(t, "❯ □  ~/Downloads"))
	screen.press(t, "enter")

	matches(t, "setup-apps-settings", screen.showing(t, "save them every night"))
	screen.press(t, "enter")

	matches(t, "setup-linked-files", screen.showing(t, "which should kit-config keep"))
	screen.press(t, "down", "space", "down", "space", "enter")

	screen.showing(t, "which apps should Homebrew take over")
	screen.press(t, "space")
	matches(t, "setup-to-homebrew", screen.showing(t, "❯ □  Tally"))
	screen.press(t, "enter")

	done := screen.when(t, "setup done, kit apply carrying on", func(s string) bool {
		return strings.Contains(s, "kit apply carries on now") && !strings.Contains(s, "esc stop")
	})
	lines := strings.Split(done, "\n")
	last := slices.IndexFunc(lines, func(l string) bool { return strings.Contains(l, "carries on now") })
	matches(t, "setup-done", strings.Join(lines[:last+1], "\n"))
	if code := <-ended; code != 0 {
		t.Errorf("kit setup exited %d, want 0", code)
	}
	if left := w.read(t, driftFile); !strings.Contains(left, `"left_by_hand": {`+"\n"+`    "cask:tally": "2026-01-02T03:04:05Z"`) {
		t.Errorf("drift.json = %s, want Tally left by hand", left)
	}
}

// Joining kit-config at a terminal, kit setup asks which of its Macs this
// is, as round 13's frames have it; a new one, it's named as its model
// suggests, starting fresh; then, once the snapshot's taken, whether what
// kit-config declares that this Mac hasn't installs with the rest, or setup
// stops to look first, which it does, kit.toml knowing the new Mac.
func TestSetupScreenJoining(t *testing.T) {
	w := inUse(t, map[string]bool{"someone/kit-config": true})
	f := w.fake
	f.On("brew", "list", "--formula", "--full-name", "-1").Prints("jq\n")
	f.On("brew", "leaves").Prints("jq\n")
	f.On("brew", "leaves", "--installed-on-request").Prints("jq\n")
	f.On("brew", "list", "--cask", "--full-name", "-1")
	w.caskroom(t, nil)
	f.On("brew", "info", "--json=v2", "--formula", "fd", "lazygit", "ripgrep").Prints(`{"formulae": [{"name": "fd", "full_name": "fd"}, {"name": "lazygit", "full_name": "lazygit"}, {"name": "ripgrep", "full_name": "ripgrep"}], "casks": []}`)
	w.clones(t, "someone/kit-config", map[string]string{
		"kit.toml": twoMacs, "laptop/declarations": "", "studio/declarations": "",
		"shared/declarations": "[homebrew formulae]\nfd\nlazygit\nripgrep\n",
	})
	w.expectCommit(t, commit{
		message: "kit setup (macbook-pro): this Mac as it is", paths: []string{"kit.toml", "macbook-pro"},
		files: []string{"kit.toml", "macbook-pro/declarations"}, status: " M kit.toml\x00?? macbook-pro/declarations\x00",
	})
	screen := atTerminal(t, w, 80, 48)
	ended := running(t, w, "setup")

	screen.showing(t, "someone has kit-config")
	screen.press(t, "enter")
	screen.showing(t, "which of your Macs is this?")
	screen.press(t, "down", "down")
	matches(t, "setup-joining", screen.showing(t, "❯ A new one"))
	screen.press(t, "enter")
	screen.showing(t, "what will kit-config call it?")
	screen.press(t, "enter")
	screen.showing(t, "where does it start from?")
	screen.press(t, "enter")
	screen.showing(t, "which folders should a new Mac bring back")
	screen.press(t, "enter")
	screen.showing(t, "save them every night")
	screen.press(t, "down", "enter")

	matches(t, "setup-joining-shared", screen.showing(t, "this Mac hasn't"))
	screen.press(t, "down", "enter")
	stopped := screen.showing(t, "stopped here to look first")
	if code := <-ended; code != 0 {
		t.Errorf("kit setup exited %d, want 0", code)
	}
	if strings.Contains(stopped, "carries on now") {
		t.Errorf("stopped to look first, kit setup drew\n%s\nwant kit apply not carrying on", ansi.Strip(stopped))
	}
	if toml := w.read(t, filepath.Join(".config", "kit", "kit.toml")); !strings.HasSuffix(toml, "[macs.macbook-pro]\ndescription = \"MacBook Pro\"\n") {
		t.Errorf("kit.toml =\n%s\nwant it to know macbook-pro", toml)
	}
}

// Its output at a terminal, but its input not, as when it's redirected,
// kit setup has no keys to read: it draws no screen, and asks nothing,
// each question its flags don't answer naming them, as without a terminal.
func TestSetupWithoutKeys(t *testing.T) {
	w := inUse(t, map[string]bool{})
	w.creates(t)
	w.terminal, w.noKeys = true, true
	out, errOut, code := w.run(t, "setup", "--create", "kit-config", "--mac", "laptop")
	if code != 2 || !strings.Contains(errOut, "no terminal to ask at: answer with --restore <path>") || !strings.Contains(out, "mac ok laptop") || strings.Contains(out, "▀█▀") {
		t.Errorf("without keys, kit setup printed\n%s%s(exit %d)\nwant plain lines, the flags named, exit 2", out, errOut, code)
	}
}
