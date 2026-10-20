package config_test

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kinds"
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
		{Name: "jq", Scope: "shared", Section: f, Line: 9, Notes: "Shell"},
		{Name: "ripgrep", Scope: "shared", Section: f, Line: 10, Note: "searching code"},
		{Name: "owner/tap/tool", Scope: "shared", Section: f, Line: 11},
		{Name: "git", Scope: "shared", Section: f, Line: 14, Notes: "Git"},
		{Name: "go", Scope: "laptop", Section: f, Line: 3, Notes: "Development"},
		{Name: "node@24", Scope: "laptop", Section: f, Line: 4, Note: "for the old projects"},
	}
	if got.Kind != "brew" || !reflect.DeepEqual(got.Entries, want) {
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
	if !reflect.DeepEqual(got.Entries, want) {
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
		{Name: "TOKEN", Value: "GitHub/token", Scope: "laptop", Section: "secrets op://vault/Some Item", Item: "op://vault/Some Item", Line: 6, Note: "the CLI's", Notes: "Tokens"},
	}
	if !reflect.DeepEqual(got.Entries, want) {
		t.Errorf("List() = %+v\nwant %+v", got.Entries, want)
	}
}

// An item's secrets section may go by a name, --as after its item, which
// its lines carry; a name stands for its item in the shared file and each
// Mac's, and Places lists each section with how many it holds.
func TestSecretsSectionsGoByNames(t *testing.T) {
	cfg := loadRepo(t, map[string]string{
		"shared/declarations": "[secrets op://vault/Shared Item --as tokens]\nA a/b\nB a/c\n",
		"laptop/declarations": "[secrets]\nKEY op://vault/Other/key\n\n[secrets op://vault/Shared Item --as tokens]\nC a/d\n",
	})
	got, err := cfg.List("secret", "laptop")
	if err != nil {
		t.Fatal(err)
	}
	if e := got.Entries[0]; e.Item != "op://vault/Shared Item" || e.As != "tokens" || e.Section != "secrets op://vault/Shared Item --as tokens" {
		t.Errorf("List() = %+v, want its section's item and name", e)
	}
	places, err := cfg.Places("secret", "laptop")
	want := []config.Place{
		{Item: "op://vault/Shared Item", As: "tokens", Scope: "shared", Line: 1, Count: 2},
		{Scope: "laptop", Line: 1, Count: 1},
		{Item: "op://vault/Shared Item", As: "tokens", Scope: "laptop", Line: 4, Count: 1},
	}
	if err != nil || !reflect.DeepEqual(places, want) {
		t.Errorf("Places() = %+v, %v\nwant %+v", places, err, want)
	}
}

// A secrets section's name is its item's alone: another item's section
// going by it, in the same file or the shared one, is refused; so is a name
// with no item, or other than --as after an item.
func TestSecretsSectionsNamesRefused(t *testing.T) {
	for name, c := range map[string]struct {
		files map[string]string
		want  string
	}{
		"two items by one name in a file": {map[string]string{"laptop/declarations": "[secrets op://vault/A --as tokens]\nX a/b\n\n[secrets op://vault/B --as tokens]\nY c/d\n"},
			"laptop/declarations:4: [secrets op://vault/B --as tokens]: --as tokens names [secrets op://vault/A --as tokens] at line 1 too: a name stands for one item"},
		"two items by one name, shared and a Mac's": {map[string]string{"shared/declarations": "[secrets op://vault/A --as tokens]\nX a/b\n", "laptop/declarations": "[secrets op://vault/B --as tokens]\nY c/d\n"},
			"laptop/declarations:1: [secrets op://vault/B --as tokens]: --as tokens is op://vault/A in shared/declarations (line 1): a name stands for one item"},
		"a name without an item": {map[string]string{"laptop/declarations": "[secrets --as tokens]\nX op://vault/A/b\n"},
			"laptop/declarations:1: [secrets --as tokens]: --as names an item's section, after its item, as in [secrets <item> --as <name>]"},
		"an option other than --as": {map[string]string{"laptop/declarations": "[secrets op://vault/A --after brew]\nX a/b\n"},
			`laptop/declarations:1: [secrets op://vault/A --after brew]: "--after brew" isn't what follows an item: --as and the name its section goes by, alone`},
		"a name that isn't one": {map[string]string{"laptop/declarations": "[secrets op://vault/A --as -x]\nX a/b\n"},
			"--as -x: a name is letters, digits, dots, hyphens and underscores"},
	} {
		cfg := loadRepo(t, c.files)
		if _, err := cfg.List("secret", "laptop"); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: List() error = %v, want one saying %q", name, err, c.want)
		}
	}
	// Each Mac reads the shared file and its own: the same name in two
	// Macs' files stands for each Mac's item.
	cfg := loadRepo(t, map[string]string{"laptop/declarations": "[secrets op://vault/A --as tokens]\nX a/b\n", "studio/declarations": "[secrets op://vault/B --as tokens]\nX a/b\n"})
	if _, err := cfg.List("secret", "laptop"); err != nil {
		t.Errorf("List() error = %v, want none", err)
	}
}

// What follows a header's name is an item when the kind's section says it
// is: the config knows no store's references of its own.
func TestItemsAreTheKindsToRecognise(t *testing.T) {
	sections := []config.Section{{Kind: "token", Header: "tokens", Form: config.Secrets, Items: func(ref string) error {
		if strings.HasPrefix(ref, "vault:") {
			return nil
		}
		return errors.New("not a vault's item, as in vault:item")
	}}}
	dir := writeRepo(t, map[string]string{config.File: twoMacs, "laptop/declarations": "[tokens vault:keys --as keys]\nKEY key\n"})
	cfg, err := config.Load(dir, sections)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := cfg.List("token", "laptop"); err != nil || got.Entries[0].Item != "vault:keys" || got.Entries[0].As != "keys" {
		t.Errorf("List() = %+v, %v", got, err)
	}
	dir = writeRepo(t, map[string]string{config.File: twoMacs, "laptop/declarations": "[tokens op://vault/item]\nKEY key\n"})
	cfg, err = config.Load(dir, sections)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.List("token", "laptop"); err == nil || err.Error() != "laptop/declarations:1: [tokens op://vault/item]: not a vault's item, as in vault:item" {
		t.Errorf("List() error = %v", err)
	}
}

// A features section can say what its features wait for, as a list's
// section does, but never a secret they need.
func TestFeaturesWait(t *testing.T) {
	cfg := loadRepo(t, map[string]string{"laptop/declarations": "[features]\nprefs\n\n[features --after cask:app]\n# Its check reads the app.\narq\n"})
	got, err := cfg.List(config.FeaturesKind, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	want := []config.Entry{
		{Name: "prefs", Scope: "laptop", Section: "features", Line: 2},
		{Name: "arq", Scope: "laptop", Section: "features --after cask:app", Hold: "--after cask:app", Line: 6, Notes: "Its check reads the app."},
	}
	if !reflect.DeepEqual(got.Entries, want) || !slices.Equal(got.Entries[1].After(), []string{"cask:app"}) {
		t.Errorf("List() = %+v\nwant %+v, arq after cask:app", got.Entries, want)
	}
	cfg = loadRepo(t, map[string]string{"laptop/declarations": "[features --secret TOKEN]\narq\n"})
	if _, err := cfg.List(config.FeaturesKind, "laptop"); err == nil || !strings.Contains(err.Error(), `laptop/declarations:1: [features --secret TOKEN]: "--secret" isn't one of [features]'s options: --after`) {
		t.Errorf("List() error = %v, want --secret refused", err)
	}
}

// A backups section names the tools a Mac uses, a line each: a tool's
// name, then any options, words as a shell splits them, and a note; never
// options after its header.
func TestBackupsSection(t *testing.T) {
	cfg := loadRepo(t, map[string]string{
		"shared/declarations": "[backups]\ntime-machine\n",
		"laptop/declarations": "[backups]\n# Offsite.\narq --after cask:arq --before \"my step\"   # the second copy\n",
	})
	got, err := cfg.List(config.BackupsKind, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	want := []config.Entry{
		{Name: "time-machine", Scope: "shared", Section: "backups", Line: 2},
		{Name: "arq", Value: `--after cask:arq --before "my step"`, Scope: "laptop", Section: "backups", Line: 3, Note: "the second copy", Notes: "Offsite."},
	}
	if !reflect.DeepEqual(got.Entries, want) || !got.Declared {
		t.Errorf("List() = %+v\nwant %+v", got, want)
	}
	for _, tt := range []struct{ file, wrong string }{
		{"[backups]\ntime/machine\n", `laptop/declarations:2: "time/machine" isn't a name`},
		{"[backups]\narq --after \"cask:arq\n", "laptop/declarations:2: "},
		{"[backups --after cask:arq]\narq\n", "laptop/declarations:1: [backups --after cask:arq]: only a list's sections, and [features], take options"},
		{"[backups]\narq\n\n[backups]\ntime-machine\n", "laptop/declarations:4: [backups] is at line 1 too"},
	} {
		cfg := loadRepo(t, map[string]string{"laptop/declarations": tt.file})
		if _, err := cfg.List(config.BackupsKind, "laptop"); err == nil || !strings.Contains(err.Error(), tt.wrong) {
			t.Errorf("%q: List() error = %v, want %q", tt.file, err, tt.wrong)
		}
	}
}

// A list's section can hold its lines back, what for after its header:
// its entries are the list's like any other, saying what they wait for and
// need, as kit writes it, however the header has it.
func TestListHeldLines(t *testing.T) {
	cfg := loadRepo(t, map[string]string{"laptop/declarations": "[homebrew formulae]\njq\n\n[homebrew formulae --secret TOKEN  --after cask:app --secret TOKEN]   # private\n# Their releases are private.\nowner/private/fetcher   # a note\n"})
	got, err := cfg.List("brew", "laptop")
	if err != nil {
		t.Fatal(err)
	}
	hold := "--after cask:app --secret TOKEN"
	want := []config.Entry{
		{Name: "jq", Scope: "laptop", Section: "homebrew formulae", Line: 2},
		{Name: "owner/private/fetcher", Scope: "laptop", Section: "homebrew formulae " + hold, Hold: hold, Line: 6, Note: "a note", Notes: "Their releases are private."},
	}
	if !reflect.DeepEqual(got.Entries, want) {
		t.Fatalf("List() = %+v\nwant %+v", got.Entries, want)
	}
	held := got.Entries[1]
	if !slices.Equal(held.After(), []string{"cask:app"}) || !slices.Equal(held.Secrets(), []string{"TOKEN"}) || got.Entries[0].After() != nil || got.Entries[0].Secrets() != nil {
		t.Errorf("After() = %q, Secrets() = %q; want what its section holds its lines back for, and nothing for the plain section's", held.After(), held.Secrets())
	}
}

func TestListWithoutFiles(t *testing.T) {
	got, err := loadRepo(t, map[string]string{}).List("brew", "laptop")
	if err != nil || len(got.Entries) != 0 || got.Declared {
		t.Errorf("List() = %+v, %v; want nothing declared", got, err)
	}
}

// A kind is declared for a Mac while the shared file or the Mac's has a
// section of it, even an empty one; not while neither has.
func TestListDeclaredBySection(t *testing.T) {
	cfg := loadRepo(t, map[string]string{
		"shared/declarations": "[homebrew casks]\n\n[claude mcp ~/Code/site]\nmail -- mail-mcp\n",
		"laptop/declarations": "[npm packages]\n# none yet\n",
	})
	for _, tt := range []struct {
		kind, mac string
		want      bool
	}{
		{"cask", "laptop", true}, {"cask", "studio", true},
		{"claude-mcp", "studio", true},
		{"npm", "laptop", true}, {"npm", "studio", false},
		{"brew", "laptop", false},
	} {
		if got, err := cfg.List(tt.kind, tt.mac); err != nil || got.Declared != tt.want {
			t.Errorf("List(%s, %s) = %+v, %v; want declared %v", tt.kind, tt.mac, got, err, tt.want)
		}
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
		{name: "a secrets section for what isn't an item", files: map[string]string{"laptop/declarations": "[secrets op://vault/item/field]\nX a/b\n"}, want: `laptop/declarations:1: [secrets op://vault/item/field]: "op://vault/item/field" isn't an item's reference kit knows: 1Password's, as in op://vault/item`},
		{name: "a list section's option kit doesn't know", files: map[string]string{"shared/declarations": "[homebrew formulae --needs secret]\njq\n"}, want: `shared/declarations:1: [homebrew formulae --needs secret]: "--needs" isn't one of [homebrew formulae]'s options: --after and --secret`},
		{name: "a list section's option without its value", files: map[string]string{"shared/declarations": "[homebrew formulae --after cask:app --secret]\njq\n"}, want: "shared/declarations:1: [homebrew formulae --after cask:app --secret]: --secret needs a secret's name after it"},
		{name: "a list section waiting for half a thing", files: map[string]string{"shared/declarations": "[homebrew formulae --after cask:]\njq\n"}, want: "--after cask:: --after names a step, or one of its things as in cask:firefox"},
		{name: "a list section needing what no secret fills", files: map[string]string{"shared/declarations": "[homebrew formulae --secret 9LIVES]\njq\n"}, want: "--secret 9LIVES: a secret is named by what it fills"},
		{name: "options on a section that isn't a list's", files: map[string]string{"shared/declarations": "[git config --after brew]\nuser.name Someone\n"}, want: "shared/declarations:1: [git config --after brew]: only a list's sections, and [features], take options"},
		{name: "two sections holding lines back for the same", files: map[string]string{"shared/declarations": "[homebrew formulae --secret A --after b]\njq\n\n[homebrew formulae --after b --secret A]\ngo\n"}, want: "shared/declarations:4: [homebrew formulae --after b --secret A] is at line 1 too"},
		{name: "a name in a list's section and one holding lines back", files: map[string]string{"shared/declarations": "[homebrew formulae]\njq\n\n[homebrew formulae --secret A]\njq\n"}, want: "shared/declarations:5: jq is in [homebrew formulae] at line 2 too"},
		{name: "a list's line indented", files: map[string]string{"shared/declarations": "[homebrew formulae]\njq\n  ripgrep\n"}, want: `shared/declarations:3: "ripgrep" is indented, but only a [manual] line takes steps, indented under it`},
		{name: "a step with no line above it", files: map[string]string{"shared/declarations": "[manual]\n  Scan the code\n"}, want: `shared/declarations:2: "Scan the code" is indented, but there's no line directly above it for it to be a step of`},
		{name: "a step after a blank line", files: map[string]string{"shared/declarations": "[manual]\nsign-in \"Sign in\"\n\n\tScan the code\n"}, want: `shared/declarations:4: "Scan the code" is indented, but there's no line directly above`},
		{name: "a step after a comment", files: map[string]string{"shared/declarations": "[manual]\nsign-in \"Sign in\"\n# the code\n  Scan the code\n"}, want: `shared/declarations:4: "Scan the code" is indented, but there's no line directly above`},
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

// A step by hand's steps are the lines indented directly under it, in
// order, tabs or spaces; a comment indented among them is a comment.
func TestListSteps(t *testing.T) {
	cfg := loadRepo(t, map[string]string{"laptop/declarations": "[manual]\n" +
		"sign-in \"Sign in to the app\" --group \"First things\"\n" +
		"  Scan the code the app shows   # with the phone\n" +
		"\tTurn on its command line\n" +
		"  If it doesn't ask, in its settings:\n" +
		"# the next\n" +
		"tool \"Install the tool\" --test test -d /Applications/Tool.app\n"})
	got, err := cfg.List(config.ManualKind, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 2 {
		t.Fatalf("List() = %+v, want two lines", got.Entries)
	}
	if steps := got.Entries[0].Steps; !slices.Equal(steps, []string{"Scan the code the app shows   # with the phone", "Turn on its command line", "If it doesn't ask, in its settings:"}) {
		t.Errorf("sign-in's steps = %q", steps)
	}
	if steps := got.Entries[1].Steps; steps != nil {
		t.Errorf("tool's steps = %q, want none", steps)
	}
}

// An entry's notes are the comments directly above it, as one paragraph:
// each without its # and the space after it, joined with single spaces. A
// comment block a blank line away isn't its notes, nor is one above the
// entry before it.
func TestListNotes(t *testing.T) {
	cfg := loadRepo(t, map[string]string{"laptop/declarations": "[manual]\n" +
		"# Opening the section.\n" +
		"\n" +
		"backups \"Turn backups on\"\n" +
		"#   Indented a little,\n" +
		"  #then not spaced.\n" +
		"#\n" +
		"# Last. \n" +
		"sign-in \"Sign in to the app\"\n" +
		"  Scan the code\n" +
		"# Apart from it.\n" +
		"\n" +
		"tool \"Install the tool\"\n"})
	got, err := cfg.List(config.ManualKind, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	var notes []string
	for _, e := range got.Entries {
		notes = append(notes, e.Name+": "+e.Notes)
	}
	want := []string{"backups: ", "sign-in:   Indented a little, then not spaced. Last.", "tool: "}
	if !slices.Equal(notes, want) {
		t.Errorf("notes = %q, want %q", notes, want)
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
	want := []config.Entry{{Name: "ffmpeg", Scope: "studio", Section: "homebrew formulae", Line: 3, Note: "for video", Notes: "Media"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Where(ffmpeg) = %+v, %v; want %+v", got, err, want)
	}
	if got, err := cfg.Where("claude-mcp", "~/Code/site:mail"); err != nil || len(got) != 1 || got[0].Folder != "~/Code/site" {
		t.Errorf("Where(~/Code/site:mail) = %+v, %v", got, err)
	}
}

func TestLoadRefusesFilesForUnknownMacs(t *testing.T) {
	dir := writeRepo(t, map[string]string{config.File: twoMacs, "mini/declarations": "# The mini\n[homebrew casks]\nplex-media-server\n", "personal-patterns": "[Ss]omeone\n", "README.md": "[homebrew casks]\n", "notes/todo": "[homebrew casks]\n"})
	if _, err := config.Load(dir, kinds.Sections()); err == nil || !strings.Contains(err.Error(), "mini/declarations declares for a Mac kit.toml doesn't name") {
		t.Errorf("Load() error = %v, want mini refused", err)
	}
}

func TestAMacCantBeCalledShared(t *testing.T) {
	dir := writeRepo(t, map[string]string{config.File: "format = 1\nprimary = \"shared\"\n\n[macs.shared]\n"})
	if _, err := config.Load(dir, kinds.Sections()); err == nil || !strings.Contains(err.Error(), "a Mac can't be called shared") {
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
