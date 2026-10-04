package config_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/config"
)

func TestList(t *testing.T) {
	cfg := loadRepo(t, map[string]string{
		"brew": `# Shell
jq
ripgrep   # searching code
owner/tap/tool

# Git
git
`,
		"brew.laptop": "# Development\ngo\n\tnode@24\t# for the old projects\n",
		"brew.studio": "ffmpeg\n",
	})

	got, err := cfg.List("brew", "laptop")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	want := []config.Entry{
		{Name: "jq", File: "brew", Line: 2, Group: "Shell"},
		{Name: "ripgrep", File: "brew", Line: 3, Group: "Shell", Note: "searching code"},
		{Name: "owner/tap/tool", File: "brew", Line: 4, Group: "Shell"},
		{Name: "git", File: "brew", Line: 7, Group: "Git"},
		{Name: "go", File: "brew.laptop", Line: 2, Group: "Development"},
		{Name: "node@24", File: "brew.laptop", Line: 3, Group: "Development", Note: "for the old projects"},
	}
	if got.Kind != "brew" || !slices.Equal(got.Entries, want) {
		t.Errorf("List() = %+v\nwant %+v", got, want)
	}
	if names := got.Names(); !slices.Equal(names, []string{"jq", "ripgrep", "owner/tap/tool", "git", "go", "node@24"}) {
		t.Errorf("Names() = %q", names)
	}
}

func TestListWithoutFiles(t *testing.T) {
	cfg := loadRepo(t, map[string]string{"cask.laptop": "ghostty\n"})

	got, err := cfg.List("brew", "laptop")
	if err != nil || len(got.Entries) != 0 {
		t.Errorf("List() of a kind with no files = %+v, %v, want nothing", got, err)
	}
	got, err = cfg.List("cask", "laptop")
	if err != nil || !slices.Equal(got.Names(), []string{"ghostty"}) {
		t.Errorf("List() of a kind with only a Mac's file = %+v, %v, want its entries", got, err)
	}
}

func TestListRefuses(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		mac   string
		want  string
	}{
		{name: "an unknown Mac", files: map[string]string{}, mac: "other", want: "no Mac named other in kit.toml: one of laptop, studio"},
		{name: "a file for an unknown Mac", files: map[string]string{"brew.mini": "jq\n"}, mac: "laptop", want: "brew.mini: no Mac named mini in kit.toml (one of laptop, studio): rename or remove the file"},
		{name: "a leftover file", files: map[string]string{"brew.laptop.orig": "jq\n"}, mac: "laptop", want: "brew.laptop.orig: no Mac named laptop.orig"},
		{name: "a name twice in a file", files: map[string]string{"brew": "jq\nripgrep\njq\n"}, mac: "laptop", want: "brew:3: jq is already at line 1"},
		{name: "a name in the shared file and a Mac's", files: map[string]string{"brew": "jq\n", "brew.laptop": "go\njq\n"}, mac: "laptop", want: "brew.laptop:2: jq is in brew too (line 1): a name goes in the shared file or a Mac's, not both"},
		{name: "two names on a line", files: map[string]string{"brew": "jq ripgrep\n"}, mac: "laptop", want: `brew:1: "jq ripgrep" isn't a name`},
		{name: "a # without a space before it", files: map[string]string{"brew": "jq#fast\n"}, mac: "laptop", want: `brew:1: "jq#fast" isn't a name`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadRepo(t, tt.files).List("brew", tt.mac)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("List() error = %v, want one saying %q", err, tt.want)
			}
		})
	}
}

func TestListIgnoresAnotherMacsDuplicates(t *testing.T) {
	cfg := loadRepo(t, map[string]string{"brew.laptop": "jq\n", "brew.studio": "jq\n"})
	if _, err := cfg.List("brew", "laptop"); err != nil {
		t.Errorf("List() error = %v, want none: each Mac declares its own", err)
	}
}

func TestWhere(t *testing.T) {
	cfg := loadRepo(t, map[string]string{"brew": "jq\n", "brew.laptop": "# Go\ngo   # for kit\n", "brew.studio": "go\nffmpeg\n"})
	tests := []struct {
		name string
		want []config.Entry
	}{
		{name: "go", want: []config.Entry{{Name: "go", File: "brew.laptop", Line: 2, Group: "Go", Note: "for kit"}, {Name: "go", File: "brew.studio", Line: 1}}},
		{name: "jq", want: []config.Entry{{Name: "jq", File: "brew", Line: 1}}},
		{name: "ripgrep"},
	}
	for _, tt := range tests {
		got, err := cfg.Where("brew", tt.name)
		if err != nil || !slices.Equal(got, tt.want) {
			t.Errorf("Where(%s) = %+v, %v; want %+v", tt.name, got, err, tt.want)
		}
	}
	if got := cfg.ListFiles("cask"); !slices.Equal(got, []string{"cask", "cask.laptop", "cask.studio"}) {
		t.Errorf("ListFiles() = %q", got)
	}
}
