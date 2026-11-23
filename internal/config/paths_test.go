package config_test

import (
	"cmp"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/config"
)

// The Mac's own folders come first, then the shared file's, then
// Homebrew's, then, on kit's own, the system's, each once, in the place it
// first appears: a Mac declaring a folder the shared file declares too
// raises it on that Mac. A relative folder stays in the shell's PATH, in
// its place, and out of kit's.
func TestThePathsOrder(t *testing.T) {
	const (
		shared      = "[paths]\n# switchboard first\n~/.local/share/switchboard/bin\n/opt/homebrew/bin\n/usr/bin\n"
		switchboard = "/home/someone/.local/share/switchboard/bin"
		homebrew    = "/opt/homebrew/bin"
		sbin        = "/opt/homebrew/sbin"
		shims       = "/home/someone/.local/share/mise/shims"
	)
	tests := []struct {
		name, mac, laptop string
		shell, kits       []string
	}{
		{name: "a Mac with no declarations file", mac: "studio",
			shell: []string{switchboard, homebrew, "/usr/bin", sbin},
			kits:  []string{switchboard, homebrew, "/usr/bin", sbin, "/bin", "/usr/sbin", "/sbin"}},
		{name: "shared alone",
			shell: []string{switchboard, homebrew, "/usr/bin", sbin},
			kits:  []string{switchboard, homebrew, "/usr/bin", sbin, "/bin", "/usr/sbin", "/sbin"}},
		{name: "the Mac's first", laptop: "[paths]\n~/Library/Application Support/Tool/bin   # spaces and all\n",
			shell: []string{"/home/someone/Library/Application Support/Tool/bin", switchboard, homebrew, "/usr/bin", sbin},
			kits:  []string{"/home/someone/Library/Application Support/Tool/bin", switchboard, homebrew, "/usr/bin", sbin, "/bin", "/usr/sbin", "/sbin"}},
		{name: "a shared folder raised", laptop: "[paths]\n~/.local/share/mise/shims\n/opt/homebrew/bin\n",
			shell: []string{shims, homebrew, switchboard, "/usr/bin", sbin},
			kits:  []string{shims, homebrew, switchboard, "/usr/bin", sbin, "/bin", "/usr/sbin", "/sbin"}},
		{name: "a system folder raised", laptop: "[paths]\n/bin\n",
			shell: []string{"/bin", switchboard, homebrew, "/usr/bin", sbin},
			kits:  []string{"/bin", switchboard, homebrew, "/usr/bin", sbin, "/usr/sbin", "/sbin"}},
		{name: "a relative folder in its place", laptop: "[paths]\nnode_modules/.bin\n~/go/bin\n",
			shell: []string{"node_modules/.bin", "/home/someone/go/bin", switchboard, homebrew, "/usr/bin", sbin},
			kits:  []string{"/home/someone/go/bin", switchboard, homebrew, "/usr/bin", sbin, "/bin", "/usr/sbin", "/sbin"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := loadRepo(t, map[string]string{"shared/declarations": shared, "laptop/declarations": tt.laptop})
			mac := cmp.Or(tt.mac, "laptop")
			if got, err := cfg.ShellPath("/home/someone", mac); err != nil || !slices.Equal(got, tt.shell) {
				t.Errorf("ShellPath() = %q, %v\nwant %q", got, err, tt.shell)
			}
			if got, err := cfg.SearchPath("/home/someone", mac); err != nil || !slices.Equal(got, tt.kits) {
				t.Errorf("SearchPath() = %q, %v\nwant %q", got, err, tt.kits)
			}
		})
	}
}

// kit puts Homebrew's folders on the PATH, kit's and the shell's, after
// every folder declared and before the system's, with no line in [paths]
// for them: a Homebrew folder declared keeps its place, each folder once,
// so a config declaring them both reads as it did before kit added them.
func TestHomebrewsFoldersFollowTheDeclared(t *testing.T) {
	const (
		bin, sbin = "/opt/homebrew/bin", "/opt/homebrew/sbin"
		mine      = "/home/someone/bin"
		gobin     = "/home/someone/go/bin"
	)
	system := []string{"/usr/bin", "/bin", "/usr/sbin", "/sbin"}
	tests := []struct {
		name          string
		files         map[string]string
		shell, search []string
	}{
		{name: "nothing declared", files: map[string]string{},
			shell: []string{bin, sbin}, search: slices.Concat([]string{bin, sbin}, system)},
		{name: "none of Homebrew's declared", files: map[string]string{"laptop/declarations": "[paths]\n~/bin\n", "shared/declarations": "[paths]\n~/go/bin\n"},
			shell: []string{mine, gobin, bin, sbin}, search: slices.Concat([]string{mine, gobin, bin, sbin}, system)},
		{name: "both declared, as a config started before",
			files: map[string]string{"laptop/declarations": "[paths]\n~/bin\n", "shared/declarations": "[paths]\n/opt/homebrew/bin   # Homebrew\n/opt/homebrew/sbin   # Homebrew\n~/go/bin\n"},
			shell: []string{mine, bin, sbin, gobin}, search: slices.Concat([]string{mine, bin, sbin, gobin}, system)},
		{name: "one declared, in a Mac's own", files: map[string]string{"laptop/declarations": "[paths]\n/opt/homebrew/sbin\n~/bin\n"},
			shell: []string{sbin, mine, bin}, search: slices.Concat([]string{sbin, mine, bin}, system)},
		{name: "a relative folder", files: map[string]string{"laptop/declarations": "[paths]\nnode_modules/.bin\n"},
			shell: []string{"node_modules/.bin", bin, sbin}, search: slices.Concat([]string{bin, sbin}, system)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := loadRepo(t, tt.files)
			if got, err := cfg.ShellPath("/home/someone", "laptop"); err != nil || !slices.Equal(got, tt.shell) {
				t.Errorf("ShellPath() = %q, %v\nwant %q", got, err, tt.shell)
			}
			if got, err := cfg.SearchPath("/home/someone", "laptop"); err != nil || !slices.Equal(got, tt.search) {
				t.Errorf("SearchPath() = %q, %v\nwant %q", got, err, tt.search)
			}
		})
	}
}

// Folders declared as setup's snapshot declares them, last in the Mac's
// [paths], come after the Mac's own and ahead of the shared file's, as
// kit's later runs have them: a program there shadows Homebrew's.
func TestPathsAddingGoLastOfTheMacs(t *testing.T) {
	cfg := loadRepo(t, map[string]string{
		"shared/declarations": "[paths]\n/opt/homebrew/bin\n",
		"laptop/declarations": "[paths]\n~/bin\n",
	})
	p, err := cfg.PathLists("/home/someone", "laptop")
	if err != nil {
		t.Fatal(err)
	}
	got := p.Adding("/home/someone/.local/share/mise/shims").Shell()
	if want := []string{"/home/someone/bin", "/home/someone/.local/share/mise/shims", "/opt/homebrew/bin", "/opt/homebrew/sbin"}; !slices.Equal(got, want) {
		t.Errorf("Adding().Shell() = %q, want %q", got, want)
	}
	if want := []string{"/home/someone/bin", "/opt/homebrew/bin", "/opt/homebrew/sbin"}; !slices.Equal(p.Shell(), want) {
		t.Errorf("Shell() = %q after Adding, want %q unchanged", p.Shell(), want)
	}
}

// kit's PATH from a shell's, as setup's snapshot makes it with the folders
// it declares (PathLists.Adding): the shell's absolute folders, in order,
// then Homebrew's and the system's, each once, so a program there shadows
// theirs. With none, it's Homebrew's and the system's, kit's base PATH.
func TestSearchPathOfAShellsPath(t *testing.T) {
	for _, tt := range []struct {
		name        string
		shell, want []string
	}{
		{name: "a shell's", shell: []string{"/opt/homebrew/bin", "node_modules/.bin", "/home/someone/.local/bin", "/usr/bin"},
			want: []string{"/opt/homebrew/bin", "/home/someone/.local/bin", "/usr/bin", "/opt/homebrew/sbin", "/bin", "/usr/sbin", "/sbin"}},
		{name: "none", want: []string{"/opt/homebrew/bin", "/opt/homebrew/sbin", "/usr/bin", "/bin", "/usr/sbin", "/sbin"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := config.SearchPathOf(tt.shell); !slices.Equal(got, tt.want) {
				t.Errorf("SearchPathOf() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTheShellsPathKeepsARelativeDirectory(t *testing.T) {
	cfg := loadRepo(t, map[string]string{"shared/declarations": "[paths]\n~/bin\nnode_modules/.bin\n/opt/homebrew/bin\n"})
	shell, err := cfg.ShellPath("/home/someone", "laptop")
	if want := []string{"/home/someone/bin", "node_modules/.bin", "/opt/homebrew/bin", "/opt/homebrew/sbin"}; err != nil || !slices.Equal(shell, want) {
		t.Errorf("ShellPath() = %q, %v; want %q", shell, err, want)
	}
	kits, err := cfg.SearchPath("/home/someone", "laptop")
	if err != nil || slices.Contains(kits, "node_modules/.bin") {
		t.Errorf("SearchPath() = %q, %v; want the relative directory left out", kits, err)
	}
}

func TestPathsRefuseWhatTheyCantHold(t *testing.T) {
	cfg := loadRepo(t, map[string]string{"shared/declarations": "[paths]\n$HOME/bin\n"})
	if _, err := cfg.SearchPath("/home/someone", "laptop"); err == nil || !strings.Contains(err.Error(), `shared/declarations:2: "$HOME/bin" can't hold a colon or a $`) {
		t.Errorf("SearchPath() error = %v", err)
	}
}

// Only kit's own PATH raises: a Mac's line of any other kind may not
// repeat the shared file's.
func TestOnlyThePathsRaise(t *testing.T) {
	cfg := loadRepo(t, map[string]string{})
	for kind, want := range map[string]bool{config.PathsKind: true, "brew": false, config.FeaturesKind: false, config.RestoreKind: false, "nosuch": false} {
		if got := cfg.Raises(kind); got != want {
			t.Errorf("Raises(%s) = %t, want %t", kind, got, want)
		}
	}
}
