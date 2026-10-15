package cli_test

import (
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/runner"
)

const zoomInfo = `{"formulae": [], "casks": [{"token": "zoom", "full_token": "zoom", "old_tokens": [], "artifacts": [{"pkg": ["zoomusInstallerFull.pkg"]}]}]}`

// missingWorld is laptopWorld declaring ripgrep, which isn't installed until
// kit installs it.
func missingWorld(t *testing.T) *world {
	t.Helper()
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "homebrew formulae", "go\nripgrep\n")
	f := w.fake
	f.On("brew", "list", "--formula", "--full-name", "-1").Prints("go\njq\noniguruma\nowner/tap/tool\nnode@20\nffmpeg\n").
		Then().Prints("go\njq\noniguruma\nowner/tap/tool\nnode@20\nffmpeg\nripgrep\n")
	f.On("brew", "leaves").Prints("go\njq\nowner/tap/tool\nnode@20\nffmpeg\n").
		Then().Prints("go\njq\nowner/tap/tool\nnode@20\nffmpeg\nripgrep\n")
	f.On("brew", "leaves", "--installed-on-request").Prints("go\njq\nowner/tap/tool\nffmpeg\n").
		Then().Prints("go\njq\nowner/tap/tool\nffmpeg\nripgrep\n")
	f.On("brew", "info", "--json=v2", "--formula", "ripgrep").Prints(`{"formulae": [{"name": "ripgrep", "full_name": "ripgrep", "aliases": ["rg"], "oldnames": []}], "casks": []}`)
	return w
}

func TestApplyInstallsWhatsMissing(t *testing.T) {
	w := missingWorld(t)
	w.fake.On("brew", "update", "--quiet")
	w.fake.On("brew", "install", "--formula", "ripgrep")

	out, errOut, code := w.run(t, "apply")
	for _, want := range []string{"kit apply · laptop\n", "brew installed ripgrep\n", "brew attention 4 declared, all installed\n", "brew extra ffmpeg\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("kit apply printed\n%s%s\nwant it to hold %q", out, errOut, want)
		}
	}
	if code != 1 {
		t.Errorf("exit %d, want 1: ffmpeg still needs reconciling", code)
	}
	calls := w.fake.Calls()
	for _, unwanted := range []string{"uninstall"} {
		for _, c := range calls {
			if strings.Contains(c, unwanted) {
				t.Errorf("ran %q: applying never removes", c)
			}
		}
	}
	if i, j := slices.Index(calls, "brew update --quiet"), slices.Index(calls, "brew install --formula ripgrep"); i < 0 || j < i {
		t.Errorf("ran %q, want brew update, then the install", calls)
	}
}

func TestApplyPlanChangesNothing(t *testing.T) {
	w := missingWorld(t)
	out, errOut, code := w.run(t, "apply", "--plan")
	if !strings.Contains(out, "kit apply --plan · laptop\n") || !strings.Contains(out, "brew missing:new ripgrep (to install)\n") || code != 1 {
		t.Errorf("kit apply --plan printed %q exit %d (%q); want ripgrep to install, exit 1", out, code, errOut)
	}
	for _, c := range w.fake.Calls() {
		if strings.HasPrefix(c, "brew install ") || strings.HasPrefix(c, "brew update") || strings.HasPrefix(c, "sudo") {
			t.Errorf("--plan ran %q, want nothing changed or asked", c)
		}
	}
}

// Requirement 4: questions up front, then unattended. Without a terminal,
// apply never prompts: a cask needing an administrator's password waits,
// saying why.
func TestApplyWithoutATerminalNeverPrompts(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "homebrew casks", "firefox\nzoom\n")
	w.fake.On("brew", "info", "--json=v2", "--cask", "zoom").Prints(zoomInfo)
	w.fake.On("sudo", "-n", "true").Exits(1).PrintsToStderr("sudo: a password is required")

	out, errOut, code := w.run(t, "apply")
	if want := "cask missing:new zoom (needs an administrator's password: run kit apply at a terminal)\n"; !strings.Contains(out, want) || code != 1 {
		t.Errorf("kit apply printed\n%s exit %d (%s); want zoom waiting, exit 1", out, code, errOut)
	}
	for _, cmd := range w.fake.Commands() {
		if cmd.Interactive || slices.Equal(cmd.Args, []string{"-v"}) || slices.Contains(cmd.Args, "--cask") && cmd.Args[0] == "install" {
			t.Errorf("ran %s, want nothing asked and nothing installed for zoom", cmd)
		}
		if len(cmd.Env) > 0 {
			t.Errorf("ran %s with %q; want nothing that asks kit, without a terminal", cmd, cmd.Env)
		}
	}
}

func TestApplyAtATerminalAsksForThePasswordUpFront(t *testing.T) {
	w := laptopWorld(t)
	w.terminal = true
	w.writeSection(t, "laptop", "homebrew casks", "firefox\nzoom\n")
	w.fake.On("brew", "list", "--cask", "--full-name", "-1").Prints("ghostty\nfirefox\n").
		Then().Prints("ghostty\nfirefox\n").
		Then().Prints("ghostty\nfirefox\nzoom\n")
	w.fake.On("brew", "info", "--json=v2", "--cask", "zoom").Prints(zoomInfo)
	sudo := w.expectPassword()
	w.fake.On("brew", "update", "--quiet")
	w.fake.On("brew", "install", "--cask", "zoom")

	w.choose = func(string, []string) (int, error) { return 0, nil }

	if _, errOut, code := w.run(t, "apply"); code != 1 {
		t.Errorf("kit apply exit %d (%s), want 1: ffmpeg still needs reconciling", code, errOut)
	}
	// What applying leaves differing from the config, it offers to settle.
	if !slices.Equal(w.asked, []string{"Reconcile now?"}) || len(w.became) != 1 || !slices.Equal(w.became[0], []string{"reconcile"}) {
		t.Errorf("kit asked %q and became %q; want Reconcile now?, answered yes, kit reconcile", w.asked, w.became)
	}
	calls := w.fake.Calls()
	asked, install := slices.Index(calls, askedFor), slices.Index(calls, "brew install --cask zoom")
	if asked < 0 || install < asked {
		t.Fatalf("ran %q, want the password asked for before anything's installed", calls)
	}
	// Asked in kit's own field, a row naming what needs it, and given to
	// sudo on its input, never on a command line.
	if !slices.Equal(w.typedFor, []string{"zoom  needs an administrator's password"}) || !slices.Equal(sudo.Answered(), []string{"hunter2"}) {
		t.Errorf("asked for %q, sudo given %q", w.typedFor, sudo.Answered())
	}
	if strings.Contains(strings.Join(calls, "\n"), "hunter2") {
		t.Errorf("the password was on a command line: %q", calls)
	}
	// What it installs can ask kit for the password (sudo -A, through kit's
	// helper), and Homebrew keeps the one given (its check of sudo, which
	// would have sudo forget it, is done).
	for _, cmd := range w.fake.Commands() {
		if cmd.String() != "brew install --cask zoom" {
			continue
		}
		askpass := slices.IndexFunc(cmd.Env, func(kv string) bool {
			return strings.HasPrefix(kv, "SUDO_ASKPASS=") && strings.HasSuffix(kv, "/kit-askpass")
		})
		if askpass < 0 || !slices.Contains(cmd.Env, "HOMEBREW_SUDO_CHECKED=1") {
			t.Errorf("brew install --cask zoom ran with %q; want kit's helper and Homebrew's check done", cmd.Env)
		}
	}
	for _, c := range calls[:asked] {
		if strings.HasPrefix(c, "brew install") || strings.HasPrefix(c, "brew update") {
			t.Errorf("ran %q before asking for the password, want nothing applied first", c)
		}
	}
}

// A step declared --sudo, applied at a terminal, has its own sudo given the
// password kit asked for: through kit's helper, which sudo runs with -A, or
// without it once sudo has no terminal, as nothing kit runs has, and
// DISPLAY is set.
func TestAStepsOwnSudoIsGivenThePassword(t *testing.T) {
	w := laptopWorld(t)
	w.terminal = true
	w.writeSection(t, "laptop", "steps", `trust "Trusts the authority" --sudo`+"\n")
	run := w.stepScript(t, "laptop", "trust")
	w.fake.On(run, "check").Exits(1).Prints("not trusted\n").
		Then().Exits(1).Prints("not trusted\n").
		Then().Prints("trusted\n")
	w.fake.On(run, "apply")
	w.expectPassword()

	if _, errOut, code := w.run(t, "apply", "trust"); code != 0 {
		t.Errorf("kit apply trust exit %d (%s), want 0", code, errOut)
	}
	applied := slices.IndexFunc(w.fake.Commands(), func(cmd runner.Command) bool { return cmd.String() == run+" apply" })
	if applied < 0 {
		t.Fatalf("ran %q, want the step applied", w.fake.Calls())
	}
	env := w.fake.Commands()[applied].Env
	askpass := slices.ContainsFunc(env, func(kv string) bool {
		return strings.HasPrefix(kv, "SUDO_ASKPASS=") && strings.HasSuffix(kv, "/kit-askpass")
	})
	if !askpass || !slices.Contains(env, "DISPLAY=") {
		t.Errorf("the step applied with %q; want kit's helper, and DISPLAY set", env)
	}
}

// keyboard is a terminal's keys as a test presses them, read while kit
// listens, the terminal in its raw mode meanwhile.
type keyboard struct {
	mu      sync.Mutex
	keys    chan ask.Key
	raw     bool
	started int
}

func (k *keyboard) listen() (<-chan ask.Key, func()) {
	k.mu.Lock()
	defer k.mu.Unlock()
	keys := make(chan ask.Key)
	k.keys, k.raw = keys, true
	k.started++
	return keys, sync.OnceFunc(func() {
		k.mu.Lock()
		defer k.mu.Unlock()
		k.raw = false
		close(keys)
	})
}

// state is whether keys are read, the terminal in its raw mode, and how
// many times they were.
func (k *keyboard) state() (raw bool, started int) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.raw, k.started
}

// press presses keys, one at a time: each taken, and what it does done,
// before the next is, as a key that does nothing follows the last.
func (k *keyboard) press(t *testing.T, keys ...ask.Key) {
	k.mu.Lock()
	ch, raw := k.keys, k.raw
	k.mu.Unlock()
	if !raw {
		t.Error("keys pressed while kit isn't reading them")
		return
	}
	for _, key := range append(keys, ask.Key{Name: "nothing"}) {
		ch <- key
	}
}

// At a terminal big enough for applying's screen, kit reads keys while
// it's up, in the terminal's raw mode, where ctrl+c is a key, not an
// interrupt: ctrl+c, or q's Stop, stops the run as the interrupt does, what
// was still to start not run, and nothing offered after. The terminal's
// left out of its raw mode.
func TestApplyStopsFromItsScreen(t *testing.T) {
	for _, tt := range []struct {
		name  string
		press []ask.Key
	}{
		{"ctrl+c", []ask.Key{{Name: "ctrl+c"}}},
		{"q, Stop", []ask.Key{{Text: "q"}, {Name: "down"}, {Name: "enter"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := missingWorld(t)
			w.writeSection(t, "laptop", "manual", `search "Search with ripgrep" --after brew --test test -f /searched`+"\n")
			w.terminal, w.size = true, [2]int{160, 64}
			kb := &keyboard{}
			w.listen = kb.listen
			w.fake.On("brew", "update", "--quiet")
			w.fake.On("brew", "install", "--formula", "ripgrep").Does(func() { kb.press(t, tt.press...) }).Exits(1)

			out, errOut, code := w.run(t, "apply")
			// The record's wordmark is the last kit draws.
			record := ansi.Strip(out[max(strings.LastIndex(out, "  █  ▄▀"), 0):])
			searched := slices.ContainsFunc(w.fake.Calls(), func(c string) bool { return strings.Contains(c, "/searched") })
			if code != 1 || searched || !strings.Contains(record, "│  stopped · ") || !strings.Contains(record, "→ kit apply carries on from here") {
				t.Errorf("kit apply, stopped from its screen, exit %d (%s), printed\n%s\nwant the run stopped, the step after the formulae not run, exit 1", code, errOut, record)
			}
			if len(w.asked) > 0 {
				t.Errorf("kit asked %q after the run was stopped; want nothing offered", w.asked)
			}
			if raw, started := kb.state(); raw || started != 1 {
				t.Errorf("kit read keys %d times, raw at the end: %v; want once, while its screen was up", started, raw)
			}
		})
	}
}
