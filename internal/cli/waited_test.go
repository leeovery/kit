package cli_test

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// before reports whether calls ran a before b, both run.
func before(calls []string, a, b string) bool {
	i, j := slices.Index(calls, a), slices.Index(calls, b)
	return i >= 0 && j >= 0 && i < j
}

// The things a step waits for install first, each kind's on their own, as
// one command, a tap's named by its own name; then the rest of each kind,
// once the other kind's first are done, as the two take turns, though the
// formulae are ready first.
func TestWhatsWaitedForInstallsFirst(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "homebrew formulae", "go\nmise\nripgrep\n")
	w.writeSection(t, "laptop", "homebrew casks", "owner/tap/signer\nzoom\n")
	w.writeSection(t, "laptop", "manual", strings.Join([]string{
		`sign-in "Sign in to the signer" --after cask:signer --before gh --test test -f /signed-in`,
		`tools "Set the tools up" --after brew:mise --before gh --test test -f /tools`,
	}, "\n")+"\n")
	f := w.fake
	f.On("brew", "list", "--cask", "--full-name", "-1").Prints("ghostty\nfirefox\n").Does(func() { time.Sleep(50 * time.Millisecond) })
	f.On("brew", "tap")
	f.On("brew", "tap", "owner/tap")
	f.On("brew", "info", "--json=v2", "--formula", "mise", "ripgrep").Prints(`{"formulae": [{"name": "mise", "full_name": "mise"}, {"name": "ripgrep", "full_name": "ripgrep"}], "casks": []}`)
	f.On("brew", "info", "--json=v2", "--cask", "zoom").Prints(`{"formulae": [], "casks": [{"token": "zoom", "full_token": "zoom", "artifacts": [{"app": ["zoom.us.app"]}]}]}`)
	f.On("brew", "update", "--quiet")
	for _, args := range [][]string{{"--formula", "mise"}, {"--formula", "ripgrep"}, {"--cask", "owner/tap/signer"}, {"--cask", "zoom"}} {
		f.On("brew", slices.Concat([]string{"install"}, args)...)
	}
	f.On("test", "-f", "/signed-in").Exits(1)
	f.On("test", "-f", "/tools").Exits(1)
	f.On("sudo", "-n", "true").Exits(1)

	w.run(t, "apply")
	calls := w.fake.Calls()
	mise, ripgrep, signer, zoom := "brew install --formula mise", "brew install --formula ripgrep", "brew install --cask owner/tap/signer", "brew install --cask zoom"
	if !before(calls, mise, ripgrep) || !before(calls, signer, zoom) || !before(calls, signer, ripgrep) || !before(calls, mise, zoom) {
		t.Errorf("ran %q: want mise and the signer each installed on its own, before either kind's rest", calls)
	}
}

// A line held back that a step waits for keeps to its section, and isn't
// the other kind's to wait for: the casks install, though the formulae
// check slower, then the formulae not held back, while the formulae's
// section waits for its secret, which never comes.
func TestAHeldLineWaitedForKeepsToItsSection(t *testing.T) {
	w := heldWorld(t)
	w.writeSection(t, "laptop", "homebrew casks", "zoom\n")
	w.writeSection(t, "laptop", "manual", `fetch "Fetch with the fetcher" --after brew:owner/private/fetcher --test test -f /fetched`+"\n")
	f := w.fake
	f.On("brew", "list", "--formula", "--full-name", "-1").Prints("go\njq\noniguruma\nowner/tap/tool\nnode@20\nffmpeg\n").Does(func() { time.Sleep(50 * time.Millisecond) })
	f.On("brew", "info", "--json=v2", "--cask", "zoom").Prints(`{"formulae": [], "casks": [{"token": "zoom", "full_token": "zoom", "artifacts": [{"app": ["zoom.us.app"]}]}]}`)
	f.On("brew", "install", "--cask", "zoom")
	f.On("op", "whoami").Exits(1)
	f.On("op", "account", "list", "--format", "json").Prints("[]")
	f.On("test", "-f", "/fetched").Exits(1)
	f.On("sudo", "-n", "true").Exits(1)

	w.run(t, "apply")
	calls := w.fake.Calls()
	if !before(calls, "brew install --cask zoom", "brew install --formula ripgrep") {
		t.Errorf("ran %q: want the casks installed, then the formulae not held back", calls)
	}
	if slices.Contains(calls, "brew install --formula owner/private/fetcher") {
		t.Error("the fetcher was installed, though its secret never came")
	}
}
