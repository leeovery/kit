package cli_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/runner"
)

// The cask privateWorld adds, of a tap other than Homebrew's own, what brew
// info says of it, and where it downloads from.
const (
	privateCask     = "owner/tools/app"
	privateDownload = "https://api.github.com/repos/owner/app/releases/assets/1"
	privateInfo     = `{"formulae": [], "casks": [{"token": "app", "full_token": "owner/tools/app", "old_tokens": [], "artifacts": [{"app": ["App.app"]}], "url": "` + privateDownload + `"}]}`
	tokenName       = "HOMEBREW_GITHUB_API_TOKEN"
)

// askingWithoutAToken is the request for privateCask's download, its first
// byte alone, with no token, ~/.curlrc unread.
var askingWithoutAToken = []string{"-q", "-sS", "-o", "/dev/null", "-w", "%{http_code} %{url_effective}", "-L", "-r", "0-0", "--max-time", "10", privateDownload}

// privateWorld is laptopWorld with Homebrew's GitHub token declared for the
// Mac and synced, the shell kit started from holding another value of it;
// privateCask's tap added, and its download answering, asked for without a
// token, with the HTTP status code.
func privateWorld(t *testing.T, code string) *world {
	t.Helper()
	w := laptopWorld(t)
	w.writeSection(t, "laptop", itemSection, tokenName+"   GitHub/homebrew-token\n")
	w.write(t, ".secrets.zsh", "# Written by kit.\nexport "+tokenName+"='plain-token-value'\n")
	w.env[tokenName] = "inherited-value"
	w.environ = []string{tokenName + "=inherited-value"}
	f := w.fake
	f.On("brew", "tap").Prints("owner/tools\n")
	f.On("brew", "info", "--json=v2", "--cask", privateCask).Prints(privateInfo)
	f.On("curl", askingWithoutAToken...).Prints(code + " " + privateDownload)
	f.On("brew", "update", "--quiet")
	f.On("brew", "install", "--cask", privateCask)
	return w
}

// tokensOf are the values of Homebrew's GitHub token the command that ran
// as command was given, every time it ran.
func tokensOf(t *testing.T, w *world, command string) []string {
	t.Helper()
	var values []string
	ran := false
	for _, c := range w.fake.Commands() {
		if c.String() != command {
			continue
		}
		ran = true
		for _, kv := range c.Env {
			if v, ok := strings.CutPrefix(kv, tokenName+"="); ok {
				values = append(values, v)
			}
		}
	}
	if !ran {
		t.Fatalf("%s didn't run: ran %q", command, w.fake.Calls())
	}
	return values
}

// A tap's cask whose download is private goes in its kind's section held
// for Homebrew's GitHub token, made for it, saying so; and its install has
// the token set from kit's own secrets, never the shell's, its value kept
// from the screen and the log. Asking after the download takes no token.
func TestAddingAPrivateDownloadHoldsItForTheToken(t *testing.T) {
	w := privateWorld(t, "404")
	w.expectSync(t, []string{"laptop/declarations"}, "kit cask add owner/tools/app (laptop)")

	out, errOut, code := w.run(t, "cask", "add", privateCask)
	if want := "owner/tools/app ok installed; declared in laptop; held for HOMEBREW_GITHUB_API_TOKEN: its download is private\n"; !strings.Contains(out, want) || code != 0 {
		t.Errorf("kit cask add printed\n%s%s exit %d\nwant it to hold %q", out, errOut, code, want)
	}
	if got := w.readSection(t, "laptop", "homebrew casks --secret "+tokenName); got != privateCask+"\n" {
		t.Errorf("laptop's held casks = %q, want %s", got, privateCask)
	}
	if got := w.readSection(t, "laptop", "homebrew casks"); got != "" {
		t.Errorf("laptop's casks = %q, want nothing", got)
	}
	if got := tokensOf(t, w, "brew install --cask "+privateCask); !slices.Equal(got, []string{"plain-token-value"}) {
		t.Errorf("the install had the token set to %d values, want kit's own alone", len(got))
	}
	if got := tokensOf(t, w, runner.Command{Name: "curl", Args: askingWithoutAToken}.String()); got != nil {
		t.Error("the download was asked after with the token set")
	}
	noSecretKept(t, w, out+errOut)
}

// A tap's cask whose download answers without a token is declared plainly,
// and installed without it.
func TestAddingAnOpenDownloadDeclaresItPlainly(t *testing.T) {
	w := privateWorld(t, "200")
	w.expectSync(t, []string{"laptop/declarations"}, "kit cask add owner/tools/app (laptop)")

	out, errOut, code := w.run(t, "cask", "add", privateCask)
	if want := "owner/tools/app ok installed; declared in laptop\n"; !strings.Contains(out, want) || code != 0 {
		t.Errorf("kit cask add printed\n%s%s exit %d\nwant it to hold %q", out, errOut, code, want)
	}
	if got := w.readSection(t, "laptop", "homebrew casks"); got != privateCask+"\n" {
		t.Errorf("laptop's casks = %q, want %s", got, privateCask)
	}
	if got := tokensOf(t, w, "brew install --cask "+privateCask); got != nil {
		t.Error("the install had the token set")
	}
}

// Where the token isn't declared for every Mac the line would reach, the
// add is refused, saying to declare it first, before anything's installed.
func TestAddingAPrivateDownloadWithoutTheTokenDeclared(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "for this Mac",
			want: "owner/tools/app failed couldn't declare: its download is private, so it needs HOMEBREW_GITHUB_API_TOKEN, which no [secrets] section declares: declare it first (kit secret add HOMEBREW_GITHUB_API_TOKEN)\n",
		},
		{
			name: "for every Mac, declared for this one alone",
			args: []string{"--shared"},
			want: "owner/tools/app failed couldn't declare: its download is private, so it needs HOMEBREW_GITHUB_API_TOKEN, which no [secrets] section declares for studio: declare it for every Mac first (kit secret add HOMEBREW_GITHUB_API_TOKEN --shared)\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := privateWorld(t, "404")
			if tt.args == nil {
				w.writeSection(t, "laptop", itemSection, "OTHER   GitHub/other\n")
			}
			before := w.read(t, ".config/kit/laptop/declarations") + w.read(t, ".config/kit/shared/declarations")

			out, errOut, code := w.run(t, slices.Concat([]string{"cask", "add", privateCask}, tt.args)...)
			if !strings.Contains(out, tt.want) || code != 1 {
				t.Errorf("kit cask add printed\n%s%s exit %d\nwant it to hold %q", out, errOut, code, tt.want)
			}
			for _, c := range w.fake.Calls() {
				if strings.HasPrefix(c, "brew install") {
					t.Errorf("ran %s, want nothing installed", c)
				}
			}
			if after := w.read(t, ".config/kit/laptop/declarations") + w.read(t, ".config/kit/shared/declarations"); after != before {
				t.Errorf("the declarations changed:\n%s", after)
			}
		})
	}
}

// A download that can't be checked, as without the network, doesn't stop
// the add: it's declared plainly, saying the check couldn't be made.
func TestAddingWhenTheDownloadCantBeChecked(t *testing.T) {
	w := privateWorld(t, "000")
	w.fake.On("curl", askingWithoutAToken...).Prints("000 " + privateDownload).Exits(6).PrintsToStderr("curl: (6) Could not resolve host: api.github.com\n")
	w.expectSync(t, []string{"laptop/declarations"}, "kit cask add owner/tools/app (laptop)")

	out, errOut, code := w.run(t, "cask", "add", privateCask)
	if want := "owner/tools/app ok installed; declared in laptop; couldn't check whether it needs a secret: its download didn't answer: curl: (6) Could not resolve host: api.github.com\n"; !strings.Contains(out, want) || code != 0 {
		t.Errorf("kit cask add printed\n%s%s exit %d\nwant it to hold %q", out, errOut, code, want)
	}
	if got := w.readSection(t, "laptop", "homebrew casks"); got != privateCask+"\n" {
		t.Errorf("laptop's casks = %q, want %s", got, privateCask)
	}
}

// The token declared and not synced yet, the add's install waits for it no
// more than kit apply's would: it isn't installed, or declared, saying what
// isn't there.
func TestAddingAPrivateDownloadBeforeTheTokensSynced(t *testing.T) {
	w := privateWorld(t, "404")
	w.write(t, ".secrets.zsh", "# Written by kit.\n")

	out, errOut, code := w.run(t, "cask", "add", privateCask)
	if want := "owner/tools/app failed couldn't install: needs HOMEBREW_GITHUB_API_TOKEN, which isn't there yet\n"; !strings.Contains(out, want) || code != 1 {
		t.Errorf("kit cask add printed\n%s%s exit %d\nwant it to hold %q", out, errOut, code, want)
	}
	for _, c := range w.fake.Calls() {
		if strings.HasPrefix(c, "brew install") {
			t.Errorf("ran %s, want nothing installed", c)
		}
	}
	if got := w.readSection(t, "laptop", "homebrew casks --secret "+tokenName); got != "" {
		t.Errorf("laptop's held casks = %q, want nothing declared", got)
	}
}

// Adding what's declared already in a section held for the token installs
// it with the token set, as kit apply would.
func TestAddingWhatsDeclaredHeldInstallsItWithTheToken(t *testing.T) {
	w := privateWorld(t, "404")
	w.writeSection(t, "laptop", "homebrew casks --secret "+tokenName, privateCask+"\n")

	out, errOut, code := w.run(t, "cask", "add", privateCask)
	if want := "owner/tools/app ok installed; declared already, in laptop\n"; !strings.Contains(out, want) || code != 0 {
		t.Errorf("kit cask add printed\n%s%s exit %d\nwant it to hold %q", out, errOut, code, want)
	}
	if got := tokensOf(t, w, "brew install --cask "+privateCask); !slices.Equal(got, []string{"plain-token-value"}) {
		t.Errorf("the install had the token set to %d values, want kit's own alone", len(got))
	}
	noSecretKept(t, w, out+errOut)
}

// Adopting a tap's cask whose download is private, as kit reconcile does
// one installed and not declared, declares it held for the token, saying
// so.
func TestAdoptingAPrivateDownloadHoldsItForTheToken(t *testing.T) {
	w := privateWorld(t, "404")
	w.fake.On("brew", "list", "--cask", "--full-name", "-1").Prints("ghostty\nfirefox\n" + privateCask + "\n")
	w.write(t, driftFile, `{"first_seen": {"cask:firefox": "2025-12-31T00:00:00Z", "cask:owner/tools/app": "2025-12-31T00:00:00Z"}}`)
	w.expectSync(t, []string{"laptop/declarations"}, "kit reconcile (laptop): adopt cask:owner/tools/app")

	out, errOut, code := w.run(t, "reconcile", "cask:"+privateCask, "--adopt")
	if want := "cask:owner/tools/app ok already installed; declared in laptop; held for HOMEBREW_GITHUB_API_TOKEN: its download is private\n"; !strings.Contains(out, want) || code != 0 {
		t.Errorf("kit reconcile printed\n%s%s exit %d\nwant it to hold %q", out, errOut, code, want)
	}
	if got := w.readSection(t, "laptop", "homebrew casks --secret "+tokenName); got != privateCask+"\n" {
		t.Errorf("laptop's held casks = %q, want %s", got, privateCask)
	}
}

// A tap's cask known by its name alone, its tap added, is checked by its
// full name, and held as it's named.
func TestAddingAPrivateDownloadByItsShortName(t *testing.T) {
	w := privateWorld(t, "404")
	w.fake.On("brew", "info", "--json=v2", "--cask", "app").Prints(privateInfo)
	w.fake.On("brew", "install", "--cask", "app")
	w.expectSync(t, []string{"laptop/declarations"}, "kit cask add app (laptop)")

	out, errOut, code := w.run(t, "cask", "add", "app")
	if want := "app ok installed; declared in laptop; held for HOMEBREW_GITHUB_API_TOKEN: its download is private\n"; !strings.Contains(out, want) || code != 0 {
		t.Errorf("kit cask add printed\n%s%s exit %d\nwant it to hold %q", out, errOut, code, want)
	}
	if got := w.readSection(t, "laptop", "homebrew casks --secret "+tokenName); got != "app\n" {
		t.Errorf("laptop's held casks = %q, want app", got)
	}
	if got := tokensOf(t, w, "brew install --cask app"); !slices.Equal(got, []string{"plain-token-value"}) {
		t.Errorf("the install had the token set to %d values, want kit's own alone", len(got))
	}
}

// A refused add takes out the tap it added to check.
func TestARefusedAddTakesOutTheTapItAdded(t *testing.T) {
	w := privateWorld(t, "404")
	w.writeSection(t, "laptop", itemSection, "OTHER   GitHub/other\n")
	w.fake.On("brew", "tap").Prints("homebrew/cask\n")
	w.fake.On("brew", "tap", "owner/tools")
	w.fake.On("brew", "untap", "owner/tools")

	out, errOut, code := w.run(t, "cask", "add", privateCask)
	if want := "owner/tools/app failed couldn't declare: its download is private, so it needs HOMEBREW_GITHUB_API_TOKEN"; !strings.Contains(out, want) || code != 1 {
		t.Errorf("kit cask add printed\n%s%s exit %d\nwant it to hold %q", out, errOut, code, want)
	}
	calls := w.fake.Calls()
	if tapped, untapped := slices.Index(calls, "brew tap owner/tools"), slices.Index(calls, "brew untap owner/tools"); tapped < 0 || untapped < tapped {
		t.Errorf("ran %q, want the tap added, then taken out", calls)
	}
}

// Stopped while it checks, the add fails, declaring nothing: declared
// plainly, it would be a new Mac's failure again.
func TestAnAddStoppedWhileItChecks(t *testing.T) {
	w := privateWorld(t, "404")
	ctx, stop := context.WithCancel(t.Context())
	defer stop()
	w.ctx = ctx
	w.fake.On("curl", askingWithoutAToken...).Does(stop).Hangs()

	out, errOut, code := w.run(t, "cask", "add", privateCask)
	if want := "owner/tools/app failed couldn't declare: stopped checking whether it needs a secret: context canceled\n"; !strings.Contains(out, want) || code != 1 {
		t.Errorf("kit cask add printed\n%s%s exit %d\nwant it to hold %q", out, errOut, code, want)
	}
	for _, c := range w.fake.Calls() {
		if strings.HasPrefix(c, "brew install") {
			t.Errorf("ran %s, want nothing installed", c)
		}
	}
	if got := w.readSection(t, "laptop", "homebrew casks"); got != "" {
		t.Errorf("laptop's casks = %q, want nothing declared", got)
	}
}

// At a terminal, the add's row says what it's doing while it checks.
func TestAnAddSaysItsChecking(t *testing.T) {
	w := privateWorld(t, "404")
	w.terminal = true
	w.expectSync(t, []string{"laptop/declarations"}, "kit cask add owner/tools/app (laptop)")

	out, errOut, code := w.run(t, "cask", "add", privateCask)
	if out = ansi.Strip(out); !strings.Contains(out, "owner/tools/app  checking whether its download is private") || code != 0 {
		t.Errorf("kit cask add printed\n%s%s exit %d\nwant its row checking", out, errOut, code)
	}
}
