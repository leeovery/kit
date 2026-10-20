package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/config"
)

func readFile(t *testing.T, cfg *config.Config, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(cfg.Dir, name))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

func TestDeclare(t *testing.T) {
	tests := []struct {
		name, before string
		entry        config.Entry
		kind         string
		after        string
	}{
		{
			name:   "a package, in its sorted place by its name, before another's notes",
			before: "[homebrew formulae]\nbat\nowner/tap/tool\n# a note on zoxide\nzoxide\n",
			kind:   "brew", entry: config.Entry{Name: "jq", Note: "for scripts"},
			after: "[homebrew formulae]\nbat\njq   # for scripts\nowner/tap/tool\n# a note on zoxide\nzoxide\n",
		},
		{
			name:   "an old file's headings, notes on the entries under them, the section sorted",
			before: "[homebrew formulae]\n# Shell\nripgrep\nbat\n\n# Git\ngit\n\n[homebrew casks]\nghostty\n",
			kind:   "brew", entry: config.Entry{Name: "jq"},
			after: "[homebrew formulae]\nbat\n# Git\ngit\njq\n# Shell\nripgrep\n\n[homebrew casks]\nghostty\n",
		},
		{
			name:   "the comments opening a section kept at its top",
			before: "[backup exclusions]\n# Why these\n# are excluded\n\n~/b\n# why a\n~/a\n",
			kind:   "backup-exclusion", entry: config.Entry{Name: "~/c"},
			after: "[backup exclusions]\n# Why these\n# are excluded\n\n# why a\n~/a\n~/b\n~/c\n",
		},
		{
			name:   "a setting, by its key",
			before: "[git config]\nuser.name Someone\ncore.editor micro\n",
			kind:   "git-config", entry: config.Entry{Name: "alias.co", Value: "checkout"},
			after: "[git config]\nalias.co checkout\ncore.editor micro\nuser.name Someone\n",
		},
		{
			name:   "numbers by their value",
			before: "[secrets op://vault/A]\nTOKEN_10 x/10\nTOKEN_9 x/9\n",
			kind:   "secret", entry: config.Entry{Name: "TOKEN_100", Value: "x/100", Item: "op://vault/A"},
			after: "[secrets op://vault/A]\nTOKEN_9 x/9\nTOKEN_10 x/10\nTOKEN_100 x/100\n",
		},
		{
			name:   "a section made in its place among the file's",
			before: "[paths]\n/opt/homebrew/bin\n\n[macos login items]\ncom.example.app\n",
			kind:   "npm", entry: config.Entry{Name: "intelephense"},
			after: "[paths]\n/opt/homebrew/bin\n\n[npm packages]\nintelephense\n\n[macos login items]\ncom.example.app\n",
		},
		{
			name:   "a section made at the end",
			before: "[homebrew casks]\nghostty\n",
			kind:   "login-item", entry: config.Entry{Name: "com.example.app", Note: "Example"},
			after: "[homebrew casks]\nghostty\n\n[macos login items]\ncom.example.app   # Example\n",
		},
		{
			name:   "a command, in its sorted place",
			before: "[claude mcp]\nalpha --transport http https://a.example.com\nzeta -- zeta-mcp\n",
			kind:   "claude-mcp", entry: config.Entry{Name: "mail", Value: "--env K=${K} -- npx mail-mcp", Note: "mail"},
			after: "[claude mcp]\nalpha --transport http https://a.example.com\nmail --env K=${K} -- npx mail-mcp   # mail\nzeta -- zeta-mcp\n",
		},
		{
			name:   "a project folder's section, after its kind's own",
			before: "[claude mcp]\nalpha -- a\n\n[claude mcp ~/Code/zz]\nb -- b\n",
			kind:   "claude-mcp", entry: config.Entry{Name: "~/Code/site:mail", Value: "-- mail-mcp"},
			after: "[claude mcp]\nalpha -- a\n\n[claude mcp ~/Code/site]\nmail -- mail-mcp\n\n[claude mcp ~/Code/zz]\nb -- b\n",
		},
		{
			name:   "a secret, in its item's section",
			before: "[secrets]\nKEY op://vault/i/f\n\n[secrets op://vault/A]\nB_TOKEN x/y\n",
			kind:   "secret", entry: config.Entry{Name: "A_TOKEN", Value: "x/z", Item: "op://vault/A"},
			after: "[secrets]\nKEY op://vault/i/f\n\n[secrets op://vault/A]\nA_TOKEN x/z\nB_TOKEN x/y\n",
		},
		{
			name:   "a secret's item's section made, after the plain one",
			before: "[secrets]\nKEY op://vault/i/f\n",
			kind:   "secret", entry: config.Entry{Name: "A_TOKEN", Value: "x/z", Item: "op://vault/A/"},
			after: "[secrets]\nKEY op://vault/i/f\n\n[secrets op://vault/A]\nA_TOKEN x/z\n",
		},
		{
			name:   "a secret's item's section made, going by its name",
			before: "[secrets]\nKEY op://vault/i/f\n",
			kind:   "secret", entry: config.Entry{Name: "A_TOKEN", Value: "x/z", Item: "op://vault/A", As: "tokens"},
			after: "[secrets]\nKEY op://vault/i/f\n\n[secrets op://vault/A --as tokens]\nA_TOKEN x/z\n",
		},
		{
			name:   "a secret in its item's section, whatever name it goes by",
			before: "[secrets op://vault/A --as tokens]\nB_TOKEN x/y\n",
			kind:   "secret", entry: config.Entry{Name: "A_TOKEN", Value: "x/z", Item: "op://vault/A", As: "other"},
			after: "[secrets op://vault/A --as tokens]\nA_TOKEN x/z\nB_TOKEN x/y\n",
		},
		{
			name:   "a backup tool, with its options, in its place by name",
			before: "[features]\nprefs\n\n[backups]\ntime-machine\n\n[paths]\n/opt/homebrew/bin\n",
			kind:   config.BackupsKind, entry: config.Entry{Name: "arq", Value: "--after cask:arq", Note: "offsite"},
			after: "[features]\nprefs\n\n[backups]\narq --after cask:arq   # offsite\ntime-machine\n\n[paths]\n/opt/homebrew/bin\n",
		},
		{
			name:   "a backup tool's section made after features, the tool with no options",
			before: "[features]\nprefs\n\n[paths]\n/opt/homebrew/bin\n",
			kind:   config.BackupsKind, entry: config.Entry{Name: "time-machine"},
			after: "[features]\nprefs\n\n[backups]\ntime-machine\n\n[paths]\n/opt/homebrew/bin\n",
		},
		{
			name:   "a file made",
			before: "",
			kind:   "mas", entry: config.Entry{Name: "xcode@497799835"},
			after: "[app store apps]\nxcode@497799835\n",
		},
		{
			name:   "a package in the plain section, the one holding lines back kept as written",
			before: "[homebrew formulae]\njq\n\n[homebrew formulae  --secret TOKEN]   # private releases\n# Their releases are private.\nowner/private/fetcher\n",
			kind:   "brew", entry: config.Entry{Name: "bat"},
			after: "[homebrew formulae]\nbat\njq\n\n[homebrew formulae  --secret TOKEN]   # private releases\n# Their releases are private.\nowner/private/fetcher\n",
		},
		{
			name:   "a package in the section holding lines back for the same, sorted",
			before: "[homebrew formulae]\njq\n\n[homebrew formulae --secret TOKEN --after secret]\nowner/private/fetcher\n",
			kind:   "brew", entry: config.Entry{Name: "owner/private/archiver", Hold: "--secret TOKEN --after secret"},
			after: "[homebrew formulae]\njq\n\n[homebrew formulae --secret TOKEN --after secret]\nowner/private/archiver\nowner/private/fetcher\n",
		},
		{
			name:   "a step by hand with its steps, in its place by name, another's steps kept with it",
			before: "[manual]\nalpha \"Do alpha\"\n  its step\n# why zeta\nzeta \"Do zeta\"\n  its step\n",
			kind:   config.ManualKind, entry: config.Entry{Name: "mid", Value: `"Do mid" --group "First things"`, Steps: []string{"one", " two "}},
			after: "[manual]\nalpha \"Do alpha\"\n  its step\nmid \"Do mid\" --group \"First things\"\n  one\n  two\n# why zeta\nzeta \"Do zeta\"\n  its step\n",
		},
		{
			name:   "a step by hand after the last one's steps",
			before: "[manual]\nalpha \"Do alpha\"\n  its step\n\n[checks]\ndisk -- df -h\n",
			kind:   config.ManualKind, entry: config.Entry{Name: "beta", Value: `"Do beta"`},
			after: "[manual]\nalpha \"Do alpha\"\n  its step\nbeta \"Do beta\"\n\n[checks]\ndisk -- df -h\n",
		},
		{
			name:   "a section holding lines back made, after its kind's own",
			before: "[homebrew formulae]\njq\n\n[homebrew casks]\nghostty\n",
			kind:   "brew", entry: config.Entry{Name: "owner/private/fetcher", Hold: "--secret TOKEN"},
			after: "[homebrew formulae]\njq\n\n[homebrew formulae --secret TOKEN]\nowner/private/fetcher\n\n[homebrew casks]\nghostty\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := map[string]string{}
			if tt.before != "" {
				files["laptop/declarations"] = tt.before
			}
			cfg := loadRepo(t, files)
			if err := cfg.Declare(tt.kind, "laptop", tt.entry); err != nil {
				t.Fatal(err)
			}
			if got := readFile(t, cfg, "laptop/declarations"); got != tt.after {
				t.Errorf("laptop =\n%s\nwant\n%s", got, tt.after)
			}
		})
	}
}

func TestUndeclare(t *testing.T) {
	tests := []struct {
		name, before, kind, entry, after string
	}{
		{
			name:   "from whichever of its kind's sections holds it, which goes once empty",
			before: "[homebrew formulae]\njq\n\n[homebrew formulae --secret TOKEN]\n# private\nowner/private/fetcher\n\n[homebrew casks]\nghostty\n",
			kind:   "brew", entry: "owner/private/fetcher",
			after: "[homebrew formulae]\njq\n\n[homebrew casks]\nghostty\n",
		},
		{
			name:   "with its notes",
			before: "[homebrew formulae]\nbat\n# why jq\njq\nripgrep\n",
			kind:   "brew", entry: "jq",
			after: "[homebrew formulae]\nbat\nripgrep\n",
		},
		{
			name:   "a formula sharing its name with a step and a step by hand, which stay",
			before: "[homebrew formulae]\nbat\nmkcert\n\n[steps]\nmkcert \"its authority, trusted\"\n\n[manual]\nmkcert \"Reissue certificates\"\n",
			kind:   "brew", entry: "mkcert",
			after: "[homebrew formulae]\nbat\n\n[steps]\nmkcert \"its authority, trusted\"\n\n[manual]\nmkcert \"Reissue certificates\"\n",
		},
		{
			name:   "an old file's heading, with the entry under it, the rest sorted",
			before: "[homebrew formulae]\n# Shell\nripgrep\nbat\n\n# Go\ngo\n\n# Git\ngit\n",
			kind:   "brew", entry: "go",
			after: "[homebrew formulae]\nbat\n# Git\ngit\n# Shell\nripgrep\n",
		},
		{
			name:   "the comments opening a section kept",
			before: "[backup exclusions]\n# Why these\n\n~/a\n~/b\n",
			kind:   "backup-exclusion", entry: "~/a",
			after: "[backup exclusions]\n# Why these\n\n~/b\n",
		},
		{
			name:   "a kind's last section left empty stays, so the kind's still looked after",
			before: "[homebrew formulae]\nbat\n\n[npm packages]\n# for the PHP files\nintelephense\n\n[macos login items]\ncom.example.app\n",
			kind:   "npm", entry: "intelephense",
			after: "[homebrew formulae]\nbat\n\n[npm packages]\n\n[macos login items]\ncom.example.app\n",
		},
		{
			name:   "a kind's last section left empty, a project folder's, stays as it was",
			before: "[homebrew formulae]\nbat\n\n[claude mcp ~/Code/site]   # the site's\nmail -- mail-mcp\n",
			kind:   "claude-mcp", entry: "~/Code/site:mail",
			after: "[homebrew formulae]\nbat\n\n[claude mcp ~/Code/site]   # the site's\n",
		},
		{
			name:   "a kind's last section left empty, an item's, stays as it was",
			before: "[secrets op://vault/item]   # the item\nTOKEN a/b\n\n[checks]\ndisk -- df -h\n",
			kind:   "secret", entry: "TOKEN",
			after: "[secrets op://vault/item]   # the item\n\n[checks]\ndisk -- df -h\n",
		},
		{
			name:   "a secret, from whichever item's section has it",
			before: "[secrets]\nKEY op://vault/i/f\n\n[secrets op://vault/A]\nTOKEN a/b\n",
			kind:   "secret", entry: "TOKEN",
			after: "[secrets]\nKEY op://vault/i/f\n",
		},
		{
			name:   "a path, with the comment directly above it",
			before: "[paths]\n# the PATH\n\n# Homebrew\n/opt/homebrew/bin\n\n# mine\n~/bin\n",
			kind:   config.PathsKind, entry: "/opt/homebrew/bin",
			after: "[paths]\n# the PATH\n\n# mine\n~/bin\n",
		},
		{
			name:   "a step by hand, with its notes and steps, another's kept",
			before: "[manual]\n# why alpha\nalpha \"Do alpha\"\n  first\n\tsecond\nbeta \"Do beta\"\n  third\n",
			kind:   config.ManualKind, entry: "alpha",
			after: "[manual]\nbeta \"Do beta\"\n  third\n",
		},
		{
			name:   "the last step by hand, with its steps",
			before: "[manual]\nalpha \"Do alpha\"\n  first\nbeta \"Do beta\"\n  second\n\n[checks]\ndisk -- df -h\n",
			kind:   config.ManualKind, entry: "beta",
			after: "[manual]\nalpha \"Do alpha\"\n  first\n\n[checks]\ndisk -- df -h\n",
		},
		{
			name:   "the last section left empty goes, and the blank before it",
			before: "[homebrew formulae]\nbat\n\n[checks]\ndisk -- df -h\n",
			kind:   config.ChecksKind, entry: "disk",
			after: "[homebrew formulae]\nbat\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := loadRepo(t, map[string]string{"laptop/declarations": tt.before})
			if err := cfg.Undeclare(tt.kind, "laptop", tt.entry); err != nil {
				t.Fatal(err)
			}
			if got := readFile(t, cfg, "laptop/declarations"); got != tt.after {
				t.Errorf("laptop =\n%s\nwant\n%s", got, tt.after)
			}
		})
	}
}

// A Mac's last section of a kind, left empty, goes while the shared file
// has the kind's: the kind's still looked after on the Mac.
func TestUndeclareLeavesTheSharedSection(t *testing.T) {
	cfg := loadRepo(t, map[string]string{
		"shared/declarations": "[npm packages]\n",
		"laptop/declarations": "[homebrew formulae]\nbat\n\n[npm packages]\nintelephense\n",
	})
	if err := cfg.Undeclare("npm", "laptop", "intelephense"); err != nil {
		t.Fatal(err)
	}
	if got, want := readFile(t, cfg, "laptop/declarations"), "[homebrew formulae]\nbat\n"; got != want {
		t.Errorf("laptop =\n%s\nwant\n%s", got, want)
	}
	if list, err := cfg.List("npm", "laptop"); err != nil || !list.Declared {
		t.Errorf("List(npm, laptop) = %+v, %v; want it declared still", list, err)
	}
}

// The shared file's last section of a kind, left empty, stays: every Mac's
// kind's still looked after.
func TestUndeclareKeepsTheSharedFilesLastSection(t *testing.T) {
	cfg := loadRepo(t, map[string]string{
		"shared/declarations": "[npm packages]\ntypescript\n",
		"laptop/declarations": "[npm packages]\nintelephense\n",
	})
	if err := cfg.Undeclare("npm", config.Shared, "typescript"); err != nil {
		t.Fatal(err)
	}
	if got, want := readFile(t, cfg, "shared/declarations"), "[npm packages]\n"; got != want {
		t.Errorf("shared =\n%s\nwant\n%s", got, want)
	}
}

// AddSection adds a kind's section, empty, in its place, unless the file has
// one of the kind's.
func TestAddSection(t *testing.T) {
	cfg := loadRepo(t, map[string]string{"laptop/declarations": "[paths]\n~/bin\n\n[homebrew formulae --after mise]\nbat\n"})
	for _, tt := range []struct {
		kind  string
		added bool
		want  string
	}{
		{"npm", true, "[paths]\n~/bin\n\n[homebrew formulae --after mise]\nbat\n\n[npm packages]\n"},
		{"npm", false, "[paths]\n~/bin\n\n[homebrew formulae --after mise]\nbat\n\n[npm packages]\n"},
		{"brew", false, "[paths]\n~/bin\n\n[homebrew formulae --after mise]\nbat\n\n[npm packages]\n"},
		{"git-config", true, "[paths]\n~/bin\n\n[git config]\n\n[homebrew formulae --after mise]\nbat\n\n[npm packages]\n"},
	} {
		added, err := cfg.AddSection(tt.kind, "laptop")
		if got := readFile(t, cfg, "laptop/declarations"); err != nil || added != tt.added || got != tt.want {
			t.Errorf("AddSection(%s) = %v, %v; laptop =\n%s\nwant %v,\n%s", tt.kind, added, err, got, tt.added, tt.want)
		}
	}
}

func TestReplaceKeepsTheNotesAbove(t *testing.T) {
	cfg := loadRepo(t, map[string]string{"laptop/declarations": "[claude mcp]\n# the tablet\ntablet -- zsh -c tablet-mcp\n"})
	if err := cfg.Replace("claude-mcp", "laptop", config.Entry{Name: "tablet", Value: "--off -- zsh -c tablet-mcp", Note: "over USB"}); err != nil {
		t.Fatal(err)
	}
	if got, want := readFile(t, cfg, "laptop/declarations"), "[claude mcp]\n# the tablet\ntablet --off -- zsh -c tablet-mcp   # over USB\n"; got != want {
		t.Errorf("laptop =\n%s\nwant\n%s", got, want)
	}
}

func TestReplaceKeepsTheSteps(t *testing.T) {
	cfg := loadRepo(t, map[string]string{"laptop/declarations": "[manual]\nalpha \"Do alpha\"\n  first\n  second\nbeta \"Do beta\"\n"})
	if err := cfg.Replace(config.ManualKind, "laptop", config.Entry{Name: "alpha", Value: `"Do it now" --test true`}); err != nil {
		t.Fatal(err)
	}
	if got, want := readFile(t, cfg, "laptop/declarations"), "[manual]\nalpha \"Do it now\" --test true\n  first\n  second\nbeta \"Do beta\"\n"; got != want {
		t.Errorf("laptop =\n%s\nwant\n%s", got, want)
	}
}

// A sorted section is written in kit's form whichever way it's touched,
// keeping every entry and note, and a section in that form is left as it is.
func TestSortedSectionsKeepTheirForm(t *testing.T) {
	before := "[macos settings]\n# Settings\n\n# the dock\ncom.apple.dock tilesize -int 60\n\n# Keyboard\ncom.apple.symbolichotkeys AppleSymbolicHotKeys -dict-add 184 \"<dict/>\"   # screenshots\ncom.apple.symbolichotkeys AppleSymbolicHotKeys -dict-add 28 \"<dict/>\"\n-currentHost com.apple.dock autohide -bool true\n# trailing\n\n[homebrew casks]\nghostty\n"
	sorted := "[macos settings]\n# Settings\n\n-currentHost com.apple.dock autohide -bool true\n# the dock\ncom.apple.dock tilesize -int 60\ncom.apple.symbolichotkeys AppleSymbolicHotKeys -dict-add 28 \"<dict/>\"\n# Keyboard\ncom.apple.symbolichotkeys AppleSymbolicHotKeys -dict-add 184 \"<dict/>\"   # screenshots\n# trailing\n\n[homebrew casks]\nghostty\n"
	cfg := loadRepo(t, map[string]string{"laptop/declarations": before})
	names := func() []string {
		t.Helper()
		list, err := cfg.List("defaults", "laptop")
		if err != nil {
			t.Fatal(err)
		}
		return slices.Sorted(slices.Values(list.Names()))
	}
	was := names()
	if err := cfg.Replace("defaults", "laptop", config.Entry{Name: "com.apple.dock:tilesize", Value: "-int 60"}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, cfg, "laptop/declarations"); got != sorted {
		t.Fatalf("laptop =\n%s\nwant\n%s", got, sorted)
	}
	if now := names(); !slices.Equal(now, was) {
		t.Errorf("names = %q, want %q", now, was)
	}
	if err := cfg.Declare("defaults", "laptop", config.Entry{Name: "com.apple.finder:ShowPathbar", Value: "-bool true"}); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Undeclare("defaults", "laptop", "com.apple.finder:ShowPathbar"); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, cfg, "laptop/declarations"); got != sorted {
		t.Errorf("after adding and removing a setting, laptop =\n%s\nwant it unchanged:\n%s", got, sorted)
	}
}

func TestEditingRefuses(t *testing.T) {
	cfg := loadRepo(t, map[string]string{"laptop/declarations": "[homebrew formulae]\njq\n", "studio/declarations": "[homebrew taps]\nx\n"})
	for name, err := range map[string]error{
		"a name declared already":   cfg.Declare("brew", "laptop", config.Entry{Name: "jq"}),
		"a name that isn't one":     cfg.Declare("brew", "laptop", config.Entry{Name: "two words"}),
		"a file that doesn't read":  cfg.Declare("brew", "studio", config.Entry{Name: "jq"}),
		"a file for no Mac":         cfg.Declare("brew", "mini", config.Entry{Name: "jq"}),
		"a name that isn't there":   cfg.Undeclare("brew", "laptop", "ripgrep"),
		"a command without options": cfg.Declare("claude-mcp", "laptop", config.Entry{Name: "docs"}),
		"a path with a colon":       cfg.Declare(config.PathsKind, "laptop", config.Entry{Name: "/opt/x:/opt/y"}),
		"a path the shell expands":  cfg.Declare(config.PathsKind, "laptop", config.Entry{Name: "$HOME/bin"}),
		"steps under a package":     cfg.Declare("brew", "laptop", config.Entry{Name: "bat", Steps: []string{"Run it"}}),
		"a step that's a comment":   cfg.Declare(config.ManualKind, "laptop", config.Entry{Name: "x", Value: `"Do x"`, Steps: []string{"# not a step"}}),
		"a step that's blank":       cfg.Declare(config.ManualKind, "laptop", config.Entry{Name: "x", Value: `"Do x"`, Steps: []string{" "}}),
	} {
		if err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if got := readFile(t, cfg, "laptop/declarations"); got != "[homebrew formulae]\njq\n" {
		t.Errorf("laptop = %q, want it untouched", got)
	}
}

// A secret is declared once in a file, whichever item's section it's in.
func TestDeclareASecretOnce(t *testing.T) {
	before := "[secrets op://vault/A]\nTOKEN a/b\n"
	cfg := loadRepo(t, map[string]string{"laptop/declarations": before})
	for name, err := range map[string]error{
		"in another item's section": cfg.Declare("secret", "laptop", config.Entry{Name: "TOKEN", Value: "c/d", Item: "op://vault/B"}),
		"in the plain section":      cfg.Declare("secret", "laptop", config.Entry{Name: "TOKEN", Value: "op://vault/B/c"}),
		"for what isn't an item":    cfg.Declare("secret", "laptop", config.Entry{Name: "OTHER", Value: "c/d", Item: "op://vault/item/field"}),
	} {
		if err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if got := readFile(t, cfg, "laptop/declarations"); got != before {
		t.Errorf("laptop = %q, want it untouched", got)
	}
	if places, err := cfg.Places("secret", "laptop"); err != nil || len(places) != 1 || places[0] != (config.Place{Item: "op://vault/A", Scope: "laptop", Line: 1, Count: 1}) {
		t.Errorf("Places() = %+v, %v", places, err)
	}
}

// A section made goes by a name only when no file read with its own has
// that name for another item: the shared file's checked against each Mac's,
// a Mac's against the shared one.
func TestDeclareASecretsNameStandsForOneItem(t *testing.T) {
	files := map[string]string{
		"shared/declarations": "[secrets op://vault/S --as keys]\nS s/f\n",
		"laptop/declarations": "[homebrew formulae]\njq\n",
		"studio/declarations": "[secrets op://vault/T --as tokens]\nT t/f\n",
	}
	cfg := loadRepo(t, files)
	for name, c := range map[string]struct {
		scope, item, as, want string
	}{
		"shared, by a Mac's name": {"shared", "op://vault/A", "tokens", "the config wouldn't read with it: studio/declarations:1: [secrets op://vault/T --as tokens]: --as tokens is op://vault/A in shared/declarations (line 1): a name stands for one item"},
		"a Mac's, by shared's":    {"laptop", "op://vault/A", "keys", "the config wouldn't read with it: laptop/declarations:4: [secrets op://vault/A --as keys]: --as keys is op://vault/S in shared/declarations (line 1): a name stands for one item"},
	} {
		if err := cfg.CheckDeclare("secret", c.scope, config.Entry{Name: "A", Value: "a/f", Item: c.item, As: c.as}); err == nil || err.Error() != c.want {
			t.Errorf("%s: CheckDeclare() = %v, want %q", name, err, c.want)
		}
		if err := cfg.Declare("secret", c.scope, config.Entry{Name: "A", Value: "a/f", Item: c.item, As: c.as}); err == nil || err.Error() != c.want {
			t.Errorf("%s: Declare() = %v, want %q", name, err, c.want)
		}
	}
	for path, before := range files {
		if got := readFile(t, cfg, path); got != before {
			t.Errorf("%s = %q, want it untouched", path, got)
		}
	}
	for name, c := range map[string]struct{ scope, item, as string }{
		"the same item, by its name": {"shared", "op://vault/T", "tokens"},
		"another Mac's name":         {"laptop", "op://vault/A", "tokens"},
		"no name":                    {"shared", "op://vault/A", ""},
	} {
		if err := cfg.CheckDeclare("secret", c.scope, config.Entry{Name: "A", Value: "a/f", Item: c.item, As: c.as}); err != nil {
			t.Errorf("%s: CheckDeclare() = %v", name, err)
		}
	}
}

// An edit is judged by the config as kit would load it after: a name in the
// shared file can't go in a Mac's, nor one in a Mac's in the shared file,
// whatever the kind. A Mac whose list didn't load before isn't the edit's
// to refuse.
func TestDeclareJudgedByTheConfigAfter(t *testing.T) {
	files := map[string]string{
		"shared/declarations": "[secrets op://vault/Item]\nTOKEN Section/token\n\n[homebrew formulae]\njq\n",
		"laptop/declarations": "[secrets op://vault/Item]\nOWN Section/own\n",
		"studio/declarations": "[secrets]\nSTUDIO op://vault/S/f\n",
	}
	cfg := loadRepo(t, files)
	for name, c := range map[string]struct {
		kind, scope string
		e           config.Entry
		want        string
	}{
		"a Mac's, named in shared": {"secret", "laptop", config.Entry{Name: "TOKEN", Value: "Section/new", Item: "op://vault/Item"},
			"TOKEN is in the shared declarations already (shared/declarations), for every Mac"},
		"shared, named in a Mac's": {"secret", "shared", config.Entry{Name: "STUDIO", Value: "Section/new", Item: "op://vault/Item"},
			"the config wouldn't read with it: studio/declarations:2: STUDIO is in shared/declarations too (line 2): a thing goes in the shared declarations or a Mac's, not both"},
		"another kind's, named in shared": {"brew", "laptop", config.Entry{Name: "jq"},
			"jq is in the shared declarations already (shared/declarations), for every Mac"},
	} {
		for check, err := range map[string]error{"CheckDeclare": cfg.CheckDeclare(c.kind, c.scope, c.e), "Declare": cfg.Declare(c.kind, c.scope, c.e)} {
			if err == nil || err.Error() != c.want {
				t.Errorf("%s: %s() = %v, want %q", name, check, err, c.want)
			}
		}
	}
	for path, before := range files {
		if got := readFile(t, cfg, path); got != before {
			t.Errorf("%s = %q, want it untouched", path, got)
		}
	}

	broken := loadRepo(t, map[string]string{
		"shared/declarations": "[secrets]\nTWICE op://vault/S/f\n",
		"studio/declarations": "[secrets]\nTWICE op://vault/S/f\n",
	})
	if err := broken.Declare("secret", "shared", config.Entry{Name: "NEW", Value: "op://vault/S/g"}); err != nil {
		t.Errorf("Declare() beside a Mac's list broken already = %v", err)
	}
}

// Declaring in the shared file what Macs' files hold moves it there, judged
// as one edit: it reads with every file written, though not with the shared
// file alone.
func TestDeclareSharedMovesItOutOfTheMacs(t *testing.T) {
	cfg := loadRepo(t, map[string]string{
		"shared/declarations": "[homebrew formulae]\njq\n",
		"laptop/declarations": "[homebrew formulae]\ngo\nripgrep\n",
		"studio/declarations": "[homebrew formulae]\ngo\n",
	})
	written, _, err := cfg.DeclareShared("brew", config.Entry{Name: "go"}, []string{"laptop", "studio"})
	if err != nil || !slices.Equal(written, []string{"shared", "laptop", "studio"}) {
		t.Fatalf("DeclareShared() wrote %q, %v", written, err)
	}
	for path, want := range map[string]string{
		"shared/declarations": "[homebrew formulae]\ngo\njq\n",
		"laptop/declarations": "[homebrew formulae]\nripgrep\n",
		"studio/declarations": "",
	} {
		if got := readFile(t, cfg, path); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
	if written, _, err := cfg.DeclareShared("brew", config.Entry{Name: "ripgrep"}, nil); err == nil || len(written) > 0 {
		t.Errorf("DeclareShared() of what a Mac's file holds, not moved, wrote %q, %v", written, err)
	}
}

// A shared declare loses nothing a line it moves says, whatever the kind.
// What its command can say must be the same, by meaning (options in any
// order, a secret's reference short or in full), else it's refused, naming
// the line and what differs. What no command says is carried, the same in
// every line moved, else refused, naming them: its section's hold, a step
// by hand's steps and its --after and --before. Their note and the comments
// above them, compared by their words, the block taking the line out takes
// (a comment apart from it, a blank line between, too), and an item block's
// name are carried, one between them, else refused; a note given wins, the
// comments kept, and a note kit made up is used only when none is. A name
// in the shared file is refused as before. Each Mac's file is left without
// the line and its block. CheckDeclareShared judges as DeclareShared does,
// writing nothing.
func TestDeclareSharedKeepsWhatAMovedLineSays(t *testing.T) {
	for name, c := range map[string]struct {
		had, laptop, studio string
		kind                string
		e                   config.Entry
		want, shared, said  string
		left                map[string]string
	}{
		"a setting written otherwise": {laptop: "[macos settings]\ncom.apple.dock tilesize -integer 60\ncom.apple.dock autohide -bool YES\n", kind: "defaults", e: config.Entry{Name: "com.apple.dock:tilesize", Value: "-int 60"},
			shared: "[macos settings]\ncom.apple.dock tilesize -int 60\n", left: map[string]string{"laptop": "[macos settings]\ncom.apple.dock autohide -bool YES\n"}},
		"a yes written as true": {laptop: "[macos settings]\ncom.apple.dock autohide -bool YES\n", kind: "defaults", e: config.Entry{Name: "com.apple.dock:autohide", Value: "-bool true"},
			shared: "[macos settings]\ncom.apple.dock autohide -bool true\n"},
		"a setting set otherwise": {laptop: "[macos settings]\ncom.apple.dock tilesize -int 48\n", kind: "defaults", e: config.Entry{Name: "com.apple.dock:tilesize", Value: "-int 60"},
			want: "laptop/declarations:2 has com.apple.dock:tilesize with -int 48, where this gives -int 60: set it the same here, or take that line out first"},
		"a server in JSON, the same as its options": {laptop: "[claude mcp]\ndocs {\"type\": \"http\", \"url\": \"https://docs.example.com/mcp\"}\n", kind: "claude-mcp", e: config.Entry{Name: "docs", Value: "--transport http https://docs.example.com/mcp"},
			shared: "[claude mcp]\ndocs --transport http https://docs.example.com/mcp\n"},
		"a git setting quoted otherwise": {laptop: "[git config]\nalias.st \"status -sb\"\n", kind: "git-config", e: config.Entry{Name: "alias.st", Value: "'status -sb'"},
			shared: "[git config]\nalias.st 'status -sb'\n"},
		"an option given twice": {laptop: "[backups]\narq --after cask:arq --after cask:arq\n", kind: config.BackupsKind, e: config.Entry{Name: "arq", Value: "--after cask:arq"},
			shared: "[backups]\narq --after cask:arq\n"},
		"a group written elsewhere": {laptop: "[manual]\nsign-in \"Sign in\" --group First --after cask:app\n", kind: config.ManualKind, e: config.Entry{Name: "sign-in", Value: `"Sign in" --group First`},
			shared: "[manual]\nsign-in \"Sign in\" --after cask:app --group First\n"},
		"a block's name another item has": {laptop: "[secrets op://vault/Item --as keys]\nFOO Section/foo\n", studio: "[secrets op://vault/Studio --as keys]\nBAR Section/bar\n", kind: "secret", e: config.Entry{Name: "FOO", Value: "Section/foo", Item: "op://vault/Item"},
			shared: "[secrets op://vault/Item]\nFOO Section/foo\n", said: "its block's name, keys, not carried: studio/declarations:1 gives it to op://vault/Studio",
			left: map[string]string{"studio": "[secrets op://vault/Studio --as keys]\nBAR Section/bar\n"}},
		"a section's own comments, kept": {laptop: "[homebrew formulae]\n# my brews\n\ngo\n", kind: "brew", e: config.Entry{Name: "go"},
			shared: "[homebrew formulae]\ngo\n", left: map[string]string{"laptop": "[homebrew formulae]\n# my brews\n"}},
		"a blank line before the last, gone": {laptop: "[paths]\n~/bin\n\n~/go/bin\n", kind: config.PathsKind, e: config.Entry{Name: "~/go/bin"},
			shared: "[paths]\n~/go/bin\n", left: map[string]string{"laptop": "[paths]\n~/bin\n"}},
		"a section's hold, carried": {laptop: "[homebrew formulae --after cask:x]\ngo\n", kind: "brew", e: config.Entry{Name: "go"},
			shared: "[homebrew formulae --after cask:x]\ngo\n"},
		"a hold of two, in another order, carried": {laptop: "[homebrew formulae --secret TOKEN --after cask:x]\ngo\n", studio: "[homebrew formulae --after cask:x --secret TOKEN]\ngo\n", kind: "brew", e: config.Entry{Name: "go"},
			shared: "[homebrew formulae --after cask:x --secret TOKEN]\ngo\n"},
		"holds that differ": {laptop: "[homebrew formulae --after cask:x]\ngo\n", studio: "[homebrew formulae]\ngo\n", kind: "brew", e: config.Entry{Name: "go"},
			want: `the lines it moves have different sections, laptop/declarations:2 ("--after cask:x"), studio/declarations:2 (none): make them the same, or take those lines out first`},
		"options in another order": {laptop: "[backups]\narq --after cask:arq --before config\n", kind: config.BackupsKind, e: config.Entry{Name: "arq", Value: "--before config --after cask:arq"},
			shared: "[backups]\narq --before config --after cask:arq\n"},
		"options dropped": {laptop: "[backups]\narq --after cask:arq\n", kind: config.BackupsKind, e: config.Entry{Name: "arq"},
			want: "laptop/declarations:2 has arq with --after cask:arq, where this doesn't: give the same, or take that line out first"},
		"a step's --after dropped": {studio: "[steps]\ntool \"Install it\" --after mise\n", kind: config.StepsKind, e: config.Entry{Name: "tool", Value: `"Install it"`},
			want: "studio/declarations:2 has tool with --after mise, where this doesn't: give the same, or take that line out first"},
		"a check's other command": {studio: "[checks]\ndns -- dig example.com\n", kind: config.ChecksKind, e: config.Entry{Name: "dns", Value: "-- dig example.org"},
			want: "studio/declarations:2 has dns with -- dig example.com, where this gives -- dig example.org: give the same"},
		"a step by hand's steps and order, carried": {laptop: "[manual]\nsign-in \"Sign in\" --after cask:app --test true\n    Open the app\n    Sign in\n", kind: config.ManualKind, e: config.Entry{Name: "sign-in", Value: `"Sign in" --test true`},
			shared: "[manual]\nsign-in \"Sign in\" --after cask:app --test true\n  Open the app\n  Sign in\n"},
		"a step by hand's steps that differ": {laptop: "[manual]\nsign-in \"Sign in\"\n    Open the app\n", studio: "[manual]\nsign-in \"Sign in\"\n    Open it\n", kind: config.ManualKind, e: config.Entry{Name: "sign-in", Value: `"Sign in"`},
			want: `the lines it moves have different steps under them, laptop/declarations:2 ("Open the app"), studio/declarations:2 ("Open it"): make them the same, or take those lines out first`},
		"a step by hand's order that differs": {laptop: "[manual]\nsign-in \"Sign in\" --after cask:app\n", studio: "[manual]\nsign-in \"Sign in\"\n", kind: config.ManualKind, e: config.Entry{Name: "sign-in", Value: `"Sign in"`},
			want: `the lines it moves have different options, laptop/declarations:2 ("--after cask:app"), studio/declarations:2 (none)`},
		"a secret's reference, short for the same": {laptop: "[secrets]\nFOO op://vault/Item/Section/foo\n", kind: "secret", e: config.Entry{Name: "FOO", Value: "Section/foo", Item: "op://vault/Item"},
			shared: "[secrets op://vault/Item]\nFOO Section/foo\n"},
		"a secret's block name, carried": {laptop: "[secrets op://vault/Item --as keys]\nFOO Section/foo\n", kind: "secret", e: config.Entry{Name: "FOO", Value: "Section/foo", Item: "op://vault/Item"},
			shared: "[secrets op://vault/Item --as keys]\nFOO Section/foo\n"},
		"a secret in another item": {laptop: "[secrets op://vault/Other --as keys]\nFOO Section/foo\n", kind: "secret", e: config.Entry{Name: "FOO", Value: "Section/foo", Item: "op://vault/Item"},
			want: "laptop/declarations:2 has FOO with op://vault/Other/Section/foo, where this gives op://vault/Item/Section/foo"},
		"a file's mode dropped": {laptop: "[secrets op://vault/Item]\n~/.npmrc Section/npmrc --mode 644\n", kind: "secret", e: config.Entry{Name: "~/.npmrc", Value: "Section/npmrc", Item: "op://vault/Item"},
			want: "laptop/declarations:2 has ~/.npmrc with --mode 644, where this gives --mode 600"},
		"a note carried": {laptop: "[homebrew formulae]\ngo   # a compiler\n", studio: "[homebrew formulae]\ngo\n", kind: "brew", e: config.Entry{Name: "go", DefaultNote: "Go"},
			shared: "[homebrew formulae]\ngo   # a compiler\n"},
		"a note made up, none moved": {laptop: "[homebrew formulae]\ngo\n", kind: "brew", e: config.Entry{Name: "go", DefaultNote: "Go"},
			shared: "[homebrew formulae]\ngo   # Go\n"},
		"notes that differ": {laptop: "[homebrew formulae]\ngo   # a compiler\n", studio: "[homebrew formulae]\ngo   # for work\n", kind: "brew", e: config.Entry{Name: "go"},
			want: `the lines it moves have different notes, laptop/declarations:2 ("a compiler"), studio/declarations:2 ("for work"): say which with --note`},
		"a note given": {laptop: "[homebrew formulae]\ngo   # a compiler\n", studio: "[homebrew formulae]\ngo   # for work\n", kind: "brew", e: config.Entry{Name: "go", Note: "Go"},
			shared: "[homebrew formulae]\ngo   # Go\n"},
		"in shared already, notes that differ": {had: "[homebrew formulae]\ngo\n", laptop: "[homebrew formulae]\ngo   # a compiler\n", studio: "[homebrew formulae]\ngo   # for work\n", kind: "brew", e: config.Entry{Name: "go"},
			want: "go is in shared/declarations already"},
		"comments carried, in their place": {had: "[homebrew formulae]\n# about jq\njq\n", laptop: "[homebrew formulae]\n# the compiler\n# for work\ngo\nripgrep\n", studio: "[homebrew formulae]\ngo\n", kind: "brew", e: config.Entry{Name: "go"},
			shared: "[homebrew formulae]\n# the compiler\n# for work\ngo\n# about jq\njq\n", left: map[string]string{"laptop": "[homebrew formulae]\nripgrep\n"}},
		"a comment apart, carried": {laptop: "[homebrew formulae]\njq\n\n# about go\n\ngo\n", kind: "brew", e: config.Entry{Name: "go"},
			shared: "[homebrew formulae]\n# about go\ngo\n", left: map[string]string{"laptop": "[homebrew formulae]\njq\n"}},
		"comments apart that differ": {laptop: "[homebrew formulae]\njq\n# laptop go\n\ngo\n", studio: "[homebrew formulae]\n# studio go\ngo\n", kind: "brew", e: config.Entry{Name: "go"},
			want: `the lines it moves have different comments above them, laptop/declarations:5 ("laptop go"), studio/declarations:3 ("studio go"): make them the same, or take those lines out first`},
		"comments in a section kit doesn't sort": {studio: "[steps]\n# from its maker\ntool \"Install it\"\n", kind: config.StepsKind, e: config.Entry{Name: "tool", Value: `"Install it"`},
			shared: "[steps]\n# from its maker\ntool \"Install it\"\n"},
		"comments on a PATH's directory": {laptop: "[paths]\n~/bin\n# its tools\n~/go/bin\n", kind: config.PathsKind, e: config.Entry{Name: "~/go/bin"},
			shared: "[paths]\n# its tools\n~/go/bin\n", left: map[string]string{"laptop": "[paths]\n~/bin\n"}},
		"comments that differ": {laptop: "[homebrew formulae]\n# the compiler\ngo\n", studio: "[homebrew formulae]\n# for work\ngo\n", kind: "brew", e: config.Entry{Name: "go"},
			want: `the lines it moves have different comments above them, laptop/declarations:3 ("the compiler"), studio/declarations:3 ("for work"): make them the same, or take those lines out first`},
		"comments and a note given": {laptop: "[homebrew formulae]\n# the compiler\ngo   # a compiler\n", kind: "brew", e: config.Entry{Name: "go", Note: "Go"},
			shared: "[homebrew formulae]\n# the compiler\ngo   # Go\n"},
	} {
		files := map[string]string{"shared/declarations": c.had, "laptop/declarations": c.laptop, "studio/declarations": c.studio}
		cfg := loadRepo(t, files)
		where, err := cfg.Where(c.kind, c.e.Name)
		if err != nil {
			t.Fatalf("%s: Where() = %v", name, err)
		}
		var from []string
		for _, e := range where {
			if e.Scope != config.Shared {
				from = append(from, e.Scope)
			}
		}
		checked := cfg.CheckDeclareShared(c.kind, c.e, from)
		if c.want != "" {
			_, _, err := cfg.DeclareShared(c.kind, c.e, from)
			if checked == nil || err == nil || checked.Error() != err.Error() || !strings.Contains(err.Error(), c.want) {
				t.Errorf("%s: CheckDeclareShared() = %v, DeclareShared() = %v; want both %q, in the same words", name, checked, err, c.want)
			}
			for path, before := range files {
				if got := readFile(t, cfg, path); got != before {
					t.Errorf("%s: %s = %q, want it untouched", name, path, got)
				}
			}
			continue
		}
		if checked != nil {
			t.Errorf("%s: CheckDeclareShared() = %v", name, checked)
		}
		if _, said, err := cfg.DeclareShared(c.kind, c.e, from); err != nil || said != c.said {
			t.Errorf("%s: DeclareShared() = %q, %v; want %q", name, said, err, c.said)
		}
		if got := readFile(t, cfg, "shared/declarations"); got != c.shared {
			t.Errorf("%s: shared/declarations = %q, want %q", name, got, c.shared)
		}
		for _, mac := range []string{"laptop", "studio"} {
			if got := readFile(t, cfg, mac+"/declarations"); got != c.left[mac] {
				t.Errorf("%s: %s/declarations = %q, want %q", name, mac, got, c.left[mac])
			}
		}
	}
}

// A move that fails partway says which files it wrote, so they're
// committed: the shared file written, a Mac's not.
func TestDeclareSharedSaysWhatItWroteWhenAWriteFails(t *testing.T) {
	cfg := loadRepo(t, map[string]string{
		"shared/declarations": "[homebrew formulae]\njq\n",
		"laptop/declarations": "[homebrew formulae]\ngo\n",
	})
	laptop := filepath.Join(cfg.Dir, "laptop")
	if err := os.Chmod(laptop, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(laptop, 0o700) })
	written, _, err := cfg.DeclareShared("brew", config.Entry{Name: "go"}, []string{"laptop"})
	if err == nil || !slices.Equal(written, []string{"shared"}) {
		t.Errorf("DeclareShared() with laptop's folder read-only wrote %q, %v; want shared's alone, and why laptop's wasn't", written, err)
	}
}

// A Mac's section the move leaves empty goes, as the shared file has the
// kind's section once the same edit's written: as the shared declaration
// then the Mac's taken out one after another left it.
func TestDeclareSharedDropsASectionItEmpties(t *testing.T) {
	cfg := loadRepo(t, map[string]string{
		"shared/declarations": "[go tools]\nx\n",
		"laptop/declarations": "[homebrew formulae]\ngo\n\n[go tools]\ny\n",
	})
	if _, _, err := cfg.DeclareShared("brew", config.Entry{Name: "go"}, []string{"laptop"}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, cfg, "laptop/declarations"); got != "[go tools]\ny\n" {
		t.Errorf("laptop = %q, want its emptied section gone", got)
	}
}

// A Mac's file can't declare what the shared one does: Declare says it's in
// the shared declarations, for every Mac, whatever the kind.
func TestDeclareWhatTheSharedFileHas(t *testing.T) {
	cfg := loadRepo(t, map[string]string{"shared/declarations": "[homebrew formulae]\njq\n\n[paths]\n~/bin\n"})
	for kind, name := range map[string]string{"brew": "jq", config.PathsKind: "~/bin"} {
		want := name + " is in the shared declarations already (shared/declarations), for every Mac"
		if err := cfg.Declare(kind, "laptop", config.Entry{Name: name}); err == nil || err.Error() != want {
			t.Errorf("Declare(%s %s) = %v, want %q", kind, name, err, want)
		}
	}
}

// kit never writes a line that wouldn't read back as the kind reads it:
// Declare refuses it, as CheckDeclare says, and the file's untouched.
func TestDeclareRefusesALineTheKindWontRead(t *testing.T) {
	before := "[secrets op://vault/A]\nTOKEN a/b\n"
	cfg := loadRepo(t, map[string]string{"laptop/declarations": before})
	for _, c := range []struct {
		e    config.Entry
		want string
	}{
		{config.Entry{Name: "NEW", Value: "a/c --mode 644", Item: "op://vault/A"}, "NEW: --mode is a file's"},
		{config.Entry{Name: "~/.x", Value: "a/c --mode abc", Item: "op://vault/A"}, "~/.x: --mode abc isn't a file's permissions, as in 600"},
		{config.Entry{Name: "~/.x", Value: "a/c --mode 1000", Item: "op://vault/A"}, "~/.x: --mode 1000 isn't a file's permissions, as in 600"},
		{config.Entry{Name: "NEW", Value: "op://vault/B/c --mode 644"}, "NEW: --mode is a file's"},
		{config.Entry{Name: "NEW", Value: "op://vault/B/c", Item: "op://vault/A"}, "NEW: op://vault/B/c is in full"},
		{config.Entry{Name: "NEW", Value: "elsewhere"}, `NEW: "elsewhere" isn't a reference kit knows`},
	} {
		for name, err := range map[string]error{
			"CheckDeclare": cfg.CheckDeclare("secret", "laptop", c.e),
			"Declare":      cfg.Declare("secret", "laptop", c.e),
		} {
			if err == nil || !strings.HasPrefix(err.Error(), c.want) {
				t.Errorf("%s(%s %s) = %v, want %q", name, c.e.Name, c.e.Value, err, c.want)
			}
		}
	}
	if got := readFile(t, cfg, "laptop/declarations"); got != before {
		t.Errorf("laptop = %q, want it untouched", got)
	}
	if err := cfg.CheckDeclare("secret", "laptop", config.Entry{Name: "~/.x", Value: "a/c --mode 644", Item: "op://vault/A"}); err != nil {
		t.Errorf("CheckDeclare(a file's, --mode 644) = %v", err)
	}
}

func TestDeclareAPathGoesLast(t *testing.T) {
	cfg := loadRepo(t, map[string]string{"laptop/declarations": "[paths]\n# Homebrew\n/opt/homebrew/bin\n\n[homebrew formulae]\njq\n"})
	if err := cfg.Declare(config.PathsKind, "laptop", config.Entry{Name: "/a/b", Note: "a tool"}); err != nil {
		t.Fatal(err)
	}
	if got, want := readFile(t, cfg, "laptop/declarations"), "[paths]\n# Homebrew\n/opt/homebrew/bin\n/a/b   # a tool\n\n[homebrew formulae]\njq\n"; got != want {
		t.Errorf("laptop = %q, want %q", got, want)
	}
}

// Undeclaring a section's last line keeps the section when it still holds
// comments of its own, header and comments, which read back as a section
// declaring nothing; one without goes.
func TestUndeclareKeepsASectionsOwnComments(t *testing.T) {
	cfg := loadRepo(t, map[string]string{
		"shared/declarations": "[homebrew formulae]\njq\n\n[homebrew casks]\nfirefox\n",
		"laptop/declarations": "[homebrew formulae]\n# my brews\n\ngo\n\n[homebrew casks]\nzoom\n",
	})
	for _, c := range []struct{ kind, name string }{{"brew", "go"}, {"cask", "zoom"}} {
		if err := cfg.Undeclare(c.kind, "laptop", c.name); err != nil {
			t.Fatalf("Undeclare(%s) = %v", c.name, err)
		}
	}
	if got := readFile(t, cfg, "laptop/declarations"); got != "[homebrew formulae]\n# my brews\n" {
		t.Errorf("laptop/declarations = %q, want the section with its comment alone", got)
	}
	if list, err := cfg.List("brew", "laptop"); err != nil || !slices.Equal(list.Names(), []string{"jq"}) {
		t.Errorf("List(brew, laptop) = %v, %v; want the shared jq alone", list.Names(), err)
	}
}

// A line declared into a section kit doesn't sort that holds comments
// alone goes after them and a blank line, so they stay the section's own.
func TestDeclareAfterASectionsOwnComments(t *testing.T) {
	cfg := loadRepo(t, map[string]string{"laptop/declarations": "[paths]\n# my paths\n"})
	if err := cfg.Declare(config.PathsKind, "laptop", config.Entry{Name: "~/a"}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, cfg, "laptop/declarations"); got != "[paths]\n# my paths\n\n~/a\n" {
		t.Errorf("laptop/declarations = %q, want ~/a after the comment and a blank line", got)
	}
}

// Each kind's reading tells apart lines that write different things,
// however alike they look: a shared declare of one never moves the other.
func TestReadingsTellLinesApart(t *testing.T) {
	for name, c := range map[string]struct {
		section, line, kind string
		e                   config.Entry
	}{
		"a dict's entry, not a key":           {"macos settings", "com.apple.dock persistent -dict-add tile '<integer>5</integer>'", "defaults", config.Entry{Name: "com.apple.dock:persistent:tile", Value: "-int 5"}},
		"a key, not a dict's entry":           {"macos settings", "com.apple.dock persistent:tile -int 5", "defaults", config.Entry{Name: "com.apple.dock:persistent:tile", Value: "-dict-add '<integer>5</integer>'"}},
		"a string, not a number":              {"macos settings", "com.apple.dock tilesize -string 60", "defaults", config.Entry{Name: "com.apple.dock:tilesize", Value: "-int 60"}},
		"a server elsewhere":                  {"claude mcp", "docs --transport http https://docs.example.com/mcp", "claude-mcp", config.Entry{Name: "docs", Value: "--transport http https://other.example.com/mcp"}},
		"a server declared off":               {"claude mcp", "docs --off --transport http https://docs.example.com/mcp", "claude-mcp", config.Entry{Name: "docs", Value: "--transport http https://docs.example.com/mcp"}},
		"a git setting's other value":         {"git config", "pull.rebase true", "git-config", config.Entry{Name: "pull.rebase", Value: "false"}},
		"a power setting's other value":       {"power settings", "-c sleep 0", "power", config.Entry{Name: "charger:sleep", Value: "10"}},
		"a step without --sudo":               {"steps", `tool "Install it" --sudo`, config.StepsKind, config.Entry{Name: "tool", Value: `"Install it"`}},
		"a step needing another secret":       {"steps", `tool "Install it" --secret TOKEN`, config.StepsKind, config.Entry{Name: "tool", Value: `"Install it" --secret OTHER`}},
		"a step doing otherwise":              {"steps", `tool "Install it"`, config.StepsKind, config.Entry{Name: "tool", Value: `"Install the tool"`}},
		"a step by hand in another group":     {"manual", `sign-in "Sign in" --group First`, config.ManualKind, config.Entry{Name: "sign-in", Value: `"Sign in" --group Second`}},
		"a step by hand tested otherwise":     {"manual", `sign-in "Sign in" --test true`, config.ManualKind, config.Entry{Name: "sign-in", Value: `"Sign in" --test false`}},
		"a job running otherwise":             {"nightly", "tidy -- tidy --all", config.NightlyKind, config.Entry{Name: "tidy", Value: "-- tidy"}},
		"a backup tool placed otherwise":      {"backups", "arq --before config", config.BackupsKind, config.Entry{Name: "arq", Value: "--after config"}},
		"a secret's file of another mode":     {"secrets", "~/.npmrc op://vault/Item/npmrc --mode 644", "secret", config.Entry{Name: "~/.npmrc", Value: "op://vault/Item/npmrc --mode 640"}},
		"a secret kept elsewhere":             {"secrets", "TOKEN op://vault/Item/token", "secret", config.Entry{Name: "TOKEN", Value: "op://vault/Other/token"}},
		"a formula held for another":          {"homebrew formulae --after cask:x", "go", "brew", config.Entry{Name: "go", Hold: "--after cask:y"}},
		"a step by hand's steps, given apart": {"manual", "sign-in \"Sign in\"\n  Open the app", config.ManualKind, config.Entry{Name: "sign-in", Value: `"Sign in"`, Steps: []string{"Open it"}}},
	} {
		cfg := loadRepo(t, map[string]string{"laptop/declarations": "[" + c.section + "]\n" + c.line + "\n"})
		err := cfg.CheckDeclareShared(c.kind, c.e, []string{"laptop"})
		if _, differs := errors.AsType[config.LineDiffers](err); !differs {
			t.Errorf("%s: CheckDeclareShared() = %v, want it refused as saying otherwise", name, err)
		}
	}
}

// Options carried from the lines moved compare as a set: written in
// another order, or one given twice, they're the same.
func TestCarriedOptionsAreASet(t *testing.T) {
	cfg := loadRepo(t, map[string]string{
		"laptop/declarations": "[manual]\nsign-in \"Sign in\" --after a --after b --after a\n",
		"studio/declarations": "[manual]\nsign-in \"Sign in\" --after b --after a\n",
	})
	if _, _, err := cfg.DeclareShared(config.ManualKind, config.Entry{Name: "sign-in", Value: `"Sign in"`}, []string{"laptop", "studio"}); err != nil {
		t.Fatalf("DeclareShared() = %v", err)
	}
	if got := readFile(t, cfg, "shared/declarations"); got != "[manual]\nsign-in \"Sign in\" --after a --after b\n" {
		t.Errorf("shared/declarations = %q", got)
	}
}

// A reading given for none of the config's own sections is a mistake in
// kit: loading panics, rather than taking it for a kind with no header.
func TestAReadingForNoSectionPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("Load() didn't panic")
		}
	}()
	dir := writeRepo(t, map[string]string{config.File: twoMacs})
	_, _ = config.Load(dir, []config.Section{{Kind: "nothing", Says: func(config.Entry) ([]string, error) { return nil, nil }}})
}
