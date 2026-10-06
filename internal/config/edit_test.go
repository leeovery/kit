package config_test

import (
	"os"
	"path/filepath"
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
		kind, group  string
		after        string
	}{
		{
			name:   "into a group, in its sorted place",
			before: "[homebrew formulae]\n# Shell\nbat\nripgrep\n\n# Git\ngit\n",
			kind:   "brew", entry: config.Entry{Name: "jq", Note: "for scripts"}, group: "Shell",
			after: "[homebrew formulae]\n# Shell\nbat\njq   # for scripts\nripgrep\n\n# Git\ngit\n",
		},
		{
			name:   "sorted by the last part of the name, before another's notes",
			before: "[homebrew formulae]\n# Shell\nbat\n# a note on zoxide\nzoxide\n",
			kind:   "brew", entry: config.Entry{Name: "owner/tap/tool"}, group: "Shell",
			after: "[homebrew formulae]\n# Shell\nbat\nowner/tap/tool\n# a note on zoxide\nzoxide\n",
		},
		{
			name:   "into To be sorted, made at the section's bottom",
			before: "[homebrew formulae]\n# Shell\nbat\n\n[homebrew casks]\nghostty\n",
			kind:   "brew", entry: config.Entry{Name: "jq"},
			after: "[homebrew formulae]\n# Shell\nbat\n\n# To be sorted\njq\n\n[homebrew casks]\nghostty\n",
		},
		{
			name:   "a new group before To be sorted",
			before: "[homebrew formulae]\nbat\n\n# To be sorted\nzz\n",
			kind:   "brew", entry: config.Entry{Name: "go"}, group: "Go",
			after: "[homebrew formulae]\nbat\n\n# Go\ngo\n\n# To be sorted\nzz\n",
		},
		{
			name:   "a section made in its place among the file's",
			before: "[paths]\n/opt/homebrew/bin\n\n[macos login items]\ncom.example.app\n",
			kind:   "npm", entry: config.Entry{Name: "intelephense"}, group: "Language servers",
			after: "[paths]\n/opt/homebrew/bin\n\n[npm packages]\n# Language servers\nintelephense\n\n[macos login items]\ncom.example.app\n",
		},
		{
			name:   "a section made at the end",
			before: "[homebrew casks]\nghostty\n",
			kind:   "login", entry: config.Entry{Name: "com.example.app", Note: "Example"},
			after: "[homebrew casks]\nghostty\n\n[macos login items]\n# To be sorted\ncom.example.app   # Example\n",
		},
		{
			name:   "a command, in its sorted place, no groups",
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
			after: "[secrets]\nKEY op://vault/i/f\n\n[secrets op://vault/A]\nB_TOKEN x/y\n\n# To be sorted\nA_TOKEN x/z\n",
		},
		{
			name:   "a secret's item's section made, after the plain one",
			before: "[secrets]\nKEY op://vault/i/f\n",
			kind:   "secret", entry: config.Entry{Name: "A_TOKEN", Value: "x/z", Item: "op://vault/A/"},
			after: "[secrets]\nKEY op://vault/i/f\n\n[secrets op://vault/A]\n# To be sorted\nA_TOKEN x/z\n",
		},
		{
			name:   "a file made",
			before: "",
			kind:   "app", entry: config.Entry{Name: "xcode@497799835"},
			after: "[app store apps]\n# To be sorted\nxcode@497799835\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := map[string]string{}
			if tt.before != "" {
				files["laptop/declarations"] = tt.before
			}
			cfg := loadRepo(t, files)
			if err := cfg.Declare(tt.kind, "laptop", tt.entry, tt.group); err != nil {
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
			name:   "with its notes",
			before: "[homebrew formulae]\n# Shell\nbat\n# why jq\njq\nripgrep\n",
			kind:   "brew", entry: "jq",
			after: "[homebrew formulae]\n# Shell\nbat\nripgrep\n",
		},
		{
			name:   "a group left empty loses its heading",
			before: "[homebrew formulae]\n# Shell\nbat\n\n# Go\ngo\n\n# Git\ngit\n",
			kind:   "brew", entry: "go",
			after: "[homebrew formulae]\n# Shell\nbat\n\n# Git\ngit\n",
		},
		{
			name:   "a section left empty goes",
			before: "[homebrew formulae]\nbat\n\n[npm packages]\n# Language servers\nintelephense\n\n[macos login items]\ncom.example.app\n",
			kind:   "npm", entry: "intelephense",
			after: "[homebrew formulae]\nbat\n\n[macos login items]\ncom.example.app\n",
		},
		{
			name:   "a secret, from whichever item's section has it",
			before: "[secrets]\nKEY op://vault/i/f\n\n[secrets op://vault/A]\nTOKEN a/b\n",
			kind:   "secret", entry: "TOKEN",
			after: "[secrets]\nKEY op://vault/i/f\n",
		},
		{
			name:   "the last section left empty goes, and the blank before it",
			before: "[homebrew formulae]\nbat\n\n[claude mcp ~/Code/site]\nmail -- mail-mcp\n",
			kind:   "claude-mcp", entry: "~/Code/site:mail",
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

func TestReplaceKeepsTheNotesAbove(t *testing.T) {
	cfg := loadRepo(t, map[string]string{"laptop/declarations": "[claude mcp]\n# the tablet\ntablet -- zsh -c tablet-mcp\n"})
	if err := cfg.Replace("claude-mcp", "laptop", config.Entry{Name: "tablet", Value: "--off -- zsh -c tablet-mcp", Note: "over USB"}); err != nil {
		t.Fatal(err)
	}
	if got, want := readFile(t, cfg, "laptop/declarations"), "[claude mcp]\n# the tablet\ntablet --off -- zsh -c tablet-mcp   # over USB\n"; got != want {
		t.Errorf("laptop =\n%s\nwant\n%s", got, want)
	}
}

func TestGroups(t *testing.T) {
	cfg := loadRepo(t, map[string]string{"laptop/declarations": "[homebrew formulae]\n# Shell\nbat\n\n# Go\ngo\n\n[homebrew casks]\n# Browsers\nfirefox\n"})
	got, err := cfg.Groups("brew", "laptop")
	if err != nil || strings.Join(got, ",") != "Shell,Go" {
		t.Errorf("Groups() = %q, %v", got, err)
	}
	if !config.Grouped("brew") || config.Grouped("claude-mcp") {
		t.Error("Grouped(): want name lists grouped, commands not")
	}
}

func TestEditingRefuses(t *testing.T) {
	cfg := loadRepo(t, map[string]string{"laptop/declarations": "[homebrew formulae]\njq\n", "studio/declarations": "[homebrew taps]\nx\n"})
	for name, err := range map[string]error{
		"a name declared already":   cfg.Declare("brew", "laptop", config.Entry{Name: "jq"}, ""),
		"a name that isn't one":     cfg.Declare("brew", "laptop", config.Entry{Name: "two words"}, ""),
		"a file that doesn't read":  cfg.Declare("brew", "studio", config.Entry{Name: "jq"}, ""),
		"a file for no Mac":         cfg.Declare("brew", "mini", config.Entry{Name: "jq"}, ""),
		"a name that isn't there":   cfg.Undeclare("brew", "laptop", "ripgrep"),
		"a command without options": cfg.Declare("claude-mcp", "laptop", config.Entry{Name: "docs"}, ""),
		"a path with a colon":       cfg.Declare(config.PathsKind, "laptop", config.Entry{Name: "/opt/x:/opt/y"}, ""),
		"a path the shell expands":  cfg.Declare(config.PathsKind, "laptop", config.Entry{Name: "$HOME/bin"}, ""),
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
		"in another item's section": cfg.Declare("secret", "laptop", config.Entry{Name: "TOKEN", Value: "c/d", Item: "op://vault/B"}, ""),
		"in the plain section":      cfg.Declare("secret", "laptop", config.Entry{Name: "TOKEN", Value: "op://vault/B/c"}, ""),
		"for what isn't an item":    cfg.Declare("secret", "laptop", config.Entry{Name: "OTHER", Value: "c/d", Item: "op://vault/item/field"}, ""),
	} {
		if err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if got := readFile(t, cfg, "laptop/declarations"); got != before {
		t.Errorf("laptop = %q, want it untouched", got)
	}
	if items, err := cfg.Items("secret", "laptop"); err != nil || strings.Join(items, ",") != "op://vault/A" {
		t.Errorf("Items() = %q, %v", items, err)
	}
}

func TestDeclareAPathGoesLast(t *testing.T) {
	cfg := loadRepo(t, map[string]string{"laptop/declarations": "[paths]\n# Homebrew\n/opt/homebrew/bin\n\n[homebrew formulae]\njq\n"})
	if err := cfg.Declare(config.PathsKind, "laptop", config.Entry{Name: "/a/b", Note: "a tool"}, ""); err != nil {
		t.Fatal(err)
	}
	if got, want := readFile(t, cfg, "laptop/declarations"), "[paths]\n# Homebrew\n/opt/homebrew/bin\n/a/b   # a tool\n\n[homebrew formulae]\njq\n"; got != want {
		t.Errorf("laptop = %q, want %q", got, want)
	}
}
