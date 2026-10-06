package config_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/config"
)

const sharedFile = `# Every Mac's declarations.

[paths]
~/.local/bin
/opt/homebrew/bin   # Homebrew

[homebrew formulae]
# Shell
jq
ripgrep   # searching code
owner/tap/tool

# Git
git

[homebrew casks]
ghostty
`

const laptopFile = `[homebrew formulae]
# Development
go
	node@24	# for the old projects

[claude mcp]
docs --transport http https://docs.example.com/mcp   # the docs server
design --transport http https://design.example.com/mcp --header "Authorization: Bearer ${DESIGN_KEY}"

[claude mcp ~/Code/site]
mail --env MAIL_KEY=${MAIL_KEY} -- npx -y mail-mcp
`

func TestList(t *testing.T) {
	cfg := loadRepo(t, map[string]string{"shared/declarations": sharedFile, "laptop/declarations": laptopFile, "studio/declarations": "[homebrew formulae]\nffmpeg\n"})
	got, err := cfg.List("brew", "laptop")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	f := "homebrew formulae"
	want := []config.Entry{
		{Name: "jq", Scope: "shared", Section: f, Line: 9, Group: "Shell"},
		{Name: "ripgrep", Scope: "shared", Section: f, Line: 10, Group: "Shell", Note: "searching code"},
		{Name: "owner/tap/tool", Scope: "shared", Section: f, Line: 11, Group: "Shell"},
		{Name: "git", Scope: "shared", Section: f, Line: 14, Group: "Git"},
		{Name: "go", Scope: "laptop", Section: f, Line: 3, Group: "Development"},
		{Name: "node@24", Scope: "laptop", Section: f, Line: 4, Group: "Development", Note: "for the old projects"},
	}
	if got.Kind != "brew" || !slices.Equal(got.Entries, want) {
		t.Errorf("List() = %+v\nwant %+v", got.Entries, want)
	}
	if names := got.Names(); !slices.Equal(names, []string{"jq", "ripgrep", "owner/tap/tool", "git", "go", "node@24"}) {
		t.Errorf("Names() = %q", names)
	}
}

// A command's line is its name, its options and a note; a project folder's
// section's names carry the folder.
func TestListCommands(t *testing.T) {
	cfg := loadRepo(t, map[string]string{"laptop/declarations": laptopFile})
	got, err := cfg.List("claude-mcp", "laptop")
	if err != nil {
		t.Fatal(err)
	}
	want := []config.Entry{
		{Name: "docs", Value: "--transport http https://docs.example.com/mcp", Scope: "laptop", Section: "claude mcp", Line: 7, Note: "the docs server"},
		{Name: "design", Value: `--transport http https://design.example.com/mcp --header "Authorization: Bearer ${DESIGN_KEY}"`, Scope: "laptop", Section: "claude mcp", Line: 8},
		{Name: "~/Code/site:mail", Value: "--env MAIL_KEY=${MAIL_KEY} -- npx -y mail-mcp", Scope: "laptop", Section: "claude mcp ~/Code/site", Folder: "~/Code/site", Line: 11},
	}
	if !slices.Equal(got.Entries, want) {
		t.Errorf("List() = %+v\nwant %+v", got.Entries, want)
	}
}

// A secrets section's header may name the 1Password item its lines'
// references are short for; the plain section's are in full.
func TestListSecretsByItem(t *testing.T) {
	cfg := loadRepo(t, map[string]string{"laptop/declarations": "[secrets]\nKEY op://vault/Other/key\n\n[secrets  op://vault/Some Item/]\n# Tokens\nTOKEN GitHub/token   # the CLI's\n"})
	got, err := cfg.List("secret", "laptop")
	if err != nil {
		t.Fatal(err)
	}
	want := []config.Entry{
		{Name: "KEY", Value: "op://vault/Other/key", Scope: "laptop", Section: "secrets", Line: 2},
		{Name: "TOKEN", Value: "GitHub/token", Scope: "laptop", Section: "secrets op://vault/Some Item", Item: "op://vault/Some Item", Line: 6, Group: "Tokens", Note: "the CLI's"},
	}
	if !slices.Equal(got.Entries, want) {
		t.Errorf("List() = %+v\nwant %+v", got.Entries, want)
	}
}

func TestListWithoutFiles(t *testing.T) {
	got, err := loadRepo(t, map[string]string{}).List("brew", "laptop")
	if err != nil || len(got.Entries) != 0 {
		t.Errorf("List() = %+v, %v; want nothing declared", got, err)
	}
}

func TestListRefuses(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{name: "an unknown Mac", files: map[string]string{}, want: "no Mac named other"},
		{name: "a line before any section", files: map[string]string{"shared/declarations": "jq\n"}, want: `shared/declarations:1: "jq" is before any section`},
		{name: "a section kit doesn't know", files: map[string]string{"shared/declarations": "[homebrew taps]\nowner/tap\n"}, want: "shared/declarations:1: kit doesn't know the section [homebrew taps]"},
		{name: "a section twice", files: map[string]string{"shared/declarations": "[homebrew casks]\na\n\n[homebrew casks]\nb\n"}, want: "shared/declarations:4: [homebrew casks] is at line 1 too"},
		{name: "a name twice", files: map[string]string{"shared/declarations": "[homebrew formulae]\njq\nripgrep\njq\n"}, want: "shared/declarations:4: jq is already at line 2"},
		{name: "two names on a line", files: map[string]string{"shared/declarations": "[homebrew formulae]\njq ripgrep\n"}, want: `shared/declarations:2: "jq ripgrep" isn't a name`},
		{name: "a # without a space before it", files: map[string]string{"shared/declarations": "[homebrew formulae]\njq#fast\n"}, want: `"jq#fast" isn't a name`},
		{name: "a constraint alone", files: map[string]string{"shared/declarations": "[homebrew formulae]\n^4.0\n"}, want: `"^4.0" isn't a name`},
		{name: "a command with nothing after its name", files: map[string]string{"laptop/declarations": "[claude mcp]\ndocs\n"}, want: "laptop/declarations:2: docs has nothing after its name"},
		{name: "a quote left open", files: map[string]string{"laptop/declarations": "[claude mcp]\ndocs --header \"X: y\n"}, want: "laptop/declarations:2: a quote isn't closed"},
		{name: "a secret in two items' sections", files: map[string]string{"laptop/declarations": "[secrets op://vault/A]\nX a/b\n\n[secrets op://vault/B]\nX c/d\n"}, want: "laptop/declarations:5: X is in [secrets op://vault/A] at line 2 too"},
		{name: "a secrets section for what isn't an item", files: map[string]string{"laptop/declarations": "[secrets op://vault/item/field]\nX a/b\n"}, want: `laptop/declarations:1: [secrets op://vault/item/field]: "op://vault/item/field" isn't a 1Password item's reference`},
		{name: "in the shared file and a Mac's", files: map[string]string{"shared/declarations": "[homebrew formulae]\njq\n", "laptop/declarations": "[homebrew formulae]\ngo\njq\n"}, want: "laptop/declarations:3: jq is in shared/declarations too (line 2)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mac := "laptop"
			if tt.name == "an unknown Mac" {
				mac = "other"
			}
			cfg := loadRepo(t, tt.files)
			_, err := cfg.List("brew", mac)
			if tt.name == "a command with nothing after its name" || tt.name == "a quote left open" {
				_, err = cfg.List("claude-mcp", mac)
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("List() error = %v, want one saying %q", err, tt.want)
			}
		})
	}
}

// A # inside quotes is part of a command, not its note.
func TestCommandsKeepAQuotedHash(t *testing.T) {
	cfg := loadRepo(t, map[string]string{"laptop/declarations": "[claude mcp]\nx --header \"X-Tag: a #b\"   # the note\n"})
	got, err := cfg.List("claude-mcp", "laptop")
	if err != nil || len(got.Entries) != 1 || got.Entries[0].Value != `--header "X-Tag: a #b"` || got.Entries[0].Note != "the note" {
		t.Errorf("List() = %+v, %v", got.Entries, err)
	}
}

func TestWhere(t *testing.T) {
	cfg := loadRepo(t, map[string]string{"shared/declarations": sharedFile, "laptop/declarations": laptopFile, "studio/declarations": "[homebrew formulae]\n# Media\nffmpeg   # for video\n"})
	got, err := cfg.Where("brew", "ffmpeg")
	want := []config.Entry{{Name: "ffmpeg", Scope: "studio", Section: "homebrew formulae", Line: 3, Group: "Media", Note: "for video"}}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("Where(ffmpeg) = %+v, %v; want %+v", got, err, want)
	}
	if got, err := cfg.Where("claude-mcp", "~/Code/site:mail"); err != nil || len(got) != 1 || got[0].Folder != "~/Code/site" {
		t.Errorf("Where(~/Code/site:mail) = %+v, %v", got, err)
	}
}

func TestLoadRefusesFilesForUnknownMacs(t *testing.T) {
	dir := writeRepo(t, map[string]string{config.File: twoMacs, "mini/declarations": "# The mini\n[homebrew casks]\nplex-media-server\n", "personal-patterns": "[Ss]omeone\n", "README.md": "[homebrew casks]\n", "notes/todo": "[homebrew casks]\n"})
	if _, err := config.Load(dir); err == nil || !strings.Contains(err.Error(), "mini/declarations declares for a Mac kit.toml doesn't name") {
		t.Errorf("Load() error = %v, want mini refused", err)
	}
}

func TestAMacCantBeCalledShared(t *testing.T) {
	dir := writeRepo(t, map[string]string{config.File: "format = 1\nprimary = \"shared\"\n\n[macs.shared]\n"})
	if _, err := config.Load(dir); err == nil || !strings.Contains(err.Error(), "a Mac can't be called shared") {
		t.Errorf("Load() error = %v", err)
	}
}

// kit prefs's lists: paths and patterns a line, spaces and all; and a
// domain's pattern then its app's bundle id.
func TestListPrefsSections(t *testing.T) {
	cfg := loadRepo(t, map[string]string{
		"shared/declarations": "[prefs files]\n# An app's settings\n~/Library/Application Support/Some App/settings.json   # its settings\n~/Library/Application Support/Tool/*20[0-9][0-9].[0-9]/options\n\n[prefs deny]\nnet.example.Chatty*\n\n[prefs apps]\ncom.example.helper com.example.app   # its helper\njetbrains.ps.* com.jetbrains.PhpStorm\n\n[prefs machine-bound]\nio.example.*\n~/.config/tool/local.json\n",
		"laptop/declarations": "[prefs deny]\norg.example.Other\n",
	})
	for kind, want := range map[string][]string{
		config.PrefsFilesKind:        {"~/Library/Application Support/Some App/settings.json", "~/Library/Application Support/Tool/*20[0-9][0-9].[0-9]/options"},
		config.PrefsDenyKind:         {"net.example.Chatty*", "org.example.Other"},
		config.PrefsAppsKind:         {"com.example.helper", "jetbrains.ps.*"},
		config.PrefsMachineBoundKind: {"io.example.*", "~/.config/tool/local.json"},
	} {
		list, err := cfg.List(kind, "laptop")
		if err != nil || !slices.Equal(list.Names(), want) {
			t.Errorf("List(%s) = %q, %v; want %q", kind, list.Names(), err, want)
		}
	}
	apps, _ := cfg.List(config.PrefsAppsKind, "laptop")
	if e := apps.Entries[0]; e.Value != "com.example.app" || e.Note != "its helper" {
		t.Errorf("[prefs apps] entry = %+v", e)
	}
}
