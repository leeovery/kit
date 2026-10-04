package config_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const shellList = `# Shell
bat
jq   # the skills need it
zoxide

# Git
gh
# delta pages every diff
git-delta
`

func TestDeclare(t *testing.T) {
	tests := []struct {
		name               string
		before             string
		entry, group, note string
		want               string
	}{
		{
			name:   "into a group, in its sorted place",
			before: shellList, entry: "fzf", group: "Shell",
			want: "# Shell\nbat\nfzf\njq   # the skills need it\nzoxide\n\n# Git\ngh\n# delta pages every diff\ngit-delta\n",
		},
		{
			name:   "at a group's end, sorted after everything in it",
			before: shellList, entry: "zsh", group: "Shell",
			want: "# Shell\nbat\njq   # the skills need it\nzoxide\nzsh\n\n# Git\ngh\n# delta pages every diff\ngit-delta\n",
		},
		{
			name:   "a tap's, sorted by its last part, above the next entry's notes",
			before: shellList, entry: "owner/tap/git-crypt", group: "Git", note: "for the encrypted repos",
			want: "# Shell\nbat\njq   # the skills need it\nzoxide\n\n# Git\ngh\nowner/tap/git-crypt   # for the encrypted repos\n# delta pages every diff\ngit-delta\n",
		},
		{
			name:   "into To be sorted, made at the bottom",
			before: shellList, entry: "hello",
			want: shellList + "\n# To be sorted\nhello\n",
		},
		{
			name:   "at the end of To be sorted, unsorted",
			before: shellList + "\n# To be sorted\nwget\n", entry: "aria2",
			want: shellList + "\n# To be sorted\nwget\naria2\n",
		},
		{
			name:   "into a new group, above To be sorted",
			before: shellList + "\n# To be sorted\nwget\n", entry: "go", group: "Go",
			want: shellList + "\n# Go\ngo\n\n# To be sorted\nwget\n",
		},
		{
			name:   "into a new group, at the bottom",
			before: shellList, entry: "go", group: "Go",
			want: shellList + "\n# Go\ngo\n",
		},
		{
			name:   "into a file that isn't there yet",
			before: "", entry: "plex-media-server", group: "Media server",
			want: "# Media server\nplex-media-server\n",
		},
		{
			name:   "into a group with no entries yet",
			before: "# Shell\n\n# Git\ngh\n", entry: "bat", group: "Shell",
			want: "# Shell\nbat\n\n# Git\ngh\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := map[string]string{}
			if tt.before != "" {
				files["brew.laptop"] = tt.before
			}
			cfg := loadRepo(t, files)
			if err := cfg.Declare("brew.laptop", tt.entry, tt.group, tt.note); err != nil {
				t.Fatalf("Declare() error = %v", err)
			}
			if got := readRepoFile(t, cfg.Dir, "brew.laptop"); got != tt.want {
				t.Errorf("brew.laptop =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

func TestUndeclare(t *testing.T) {
	tests := []struct {
		name   string
		before string
		entry  string
		want   string
	}{
		{
			name:   "an entry",
			before: shellList, entry: "bat",
			want: "# Shell\njq   # the skills need it\nzoxide\n\n# Git\ngh\n# delta pages every diff\ngit-delta\n",
		},
		{
			name:   "with its notes",
			before: shellList, entry: "git-delta",
			want: "# Shell\nbat\njq   # the skills need it\nzoxide\n\n# Git\ngh\n",
		},
		{
			name:   "the last in its group, which goes",
			before: "# Shell\nbat\n\n# Go\ngo\n\n# Git\ngh\n", entry: "go",
			want: "# Shell\nbat\n\n# Git\ngh\n",
		},
		{
			name:   "the last in the last group",
			before: shellList + "\n# To be sorted\nhello\n", entry: "hello",
			want: shellList,
		},
		{
			name:   "the last in the first group",
			before: "# Go\ngo\n\n# Git\ngh\n", entry: "go",
			want: "# Git\ngh\n",
		},
		{
			name:   "the last in the file",
			before: "# To be sorted\nhello\n", entry: "hello",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := loadRepo(t, map[string]string{"brew.laptop": tt.before})
			if err := cfg.Undeclare("brew.laptop", tt.entry); err != nil {
				t.Fatalf("Undeclare() error = %v", err)
			}
			if got := readRepoFile(t, cfg.Dir, "brew.laptop"); got != tt.want {
				t.Errorf("brew.laptop =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

func TestDeclareThenUndeclareLeavesTheFileAsItWas(t *testing.T) {
	odd := "# Shell\n\tbat\njq      # spaced  oddly\n\n\n# Git   \ngh\n"
	cfg := loadRepo(t, map[string]string{"brew.laptop": odd})
	for _, step := range []func() error{
		func() error { return cfg.Declare("brew.laptop", "fzf", "Shell", "") },
		func() error { return cfg.Undeclare("brew.laptop", "fzf") },
		func() error { return cfg.Declare("brew.laptop", "hello", "", "a test") },
		func() error { return cfg.Undeclare("brew.laptop", "hello") },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	if got := readRepoFile(t, cfg.Dir, "brew.laptop"); got != odd {
		t.Errorf("brew.laptop =\n%q\nwant it as it was\n%q", got, odd)
	}
}

func TestDeclareAndUndeclareRefuse(t *testing.T) {
	cfg := loadRepo(t, map[string]string{"brew.laptop": shellList, "cask": "jq ripgrep\n"})
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "a name already there", err: cfg.Declare("brew.laptop", "jq", "", ""), want: "jq is in brew.laptop already"},
		{name: "a name that isn't one", err: cfg.Declare("brew.laptop", "two words", "", ""), want: `"two words" isn't a name`},
		{name: "a Mac the config doesn't know", err: cfg.Declare("brew.mini", "jq", "", ""), want: "brew.mini: no Mac named mini in kit.toml"},
		{name: "a path", err: cfg.Declare("../brew", "jq", "", ""), want: `"../brew" isn't a list file`},
		{name: "a file that doesn't read as a list", err: cfg.Declare("cask", "ghostty", "", ""), want: "cask needs fixing before kit edits it"},
		{name: "a name that isn't there", err: cfg.Undeclare("brew.laptop", "ripgrep"), want: "ripgrep isn't in brew.laptop"},
	}
	for _, tt := range tests {
		if tt.err == nil || !strings.Contains(tt.err.Error(), tt.want) {
			t.Errorf("%s: error = %v, want one saying %q", tt.name, tt.err, tt.want)
		}
	}
	if got := readRepoFile(t, cfg.Dir, "brew.laptop"); got != shellList {
		t.Errorf("brew.laptop changed to\n%s\nwant it left as it was", got)
	}
}

func TestGroups(t *testing.T) {
	cfg := loadRepo(t, map[string]string{"brew.laptop": shellList + "\n# To be sorted\nhello\n"})
	got, err := cfg.Groups("brew.laptop")
	if want := []string{"Shell", "Git", "To be sorted"}; err != nil || !slices.Equal(got, want) {
		t.Errorf("Groups() = %q, %v; want %q", got, err, want)
	}
	if got, err := cfg.Groups("brew.studio"); err != nil || got != nil {
		t.Errorf("Groups() of no file = %q, %v; want none", got, err)
	}
}

func TestDeclareKeepsTheFilesMode(t *testing.T) {
	cfg := loadRepo(t, map[string]string{"brew.laptop": shellList})
	path := filepath.Join(cfg.Dir, "brew.laptop")
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Declare("brew.laptop", "fzf", "Shell", ""); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o640 {
		t.Errorf("mode after Declare: %v, %v; want 0640 kept", info, err)
	}
}

func readRepoFile(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}
