package config_test

import (
	"slices"
	"strings"
	"testing"
)

func TestSearchPath(t *testing.T) {
	cfg := loadRepo(t, map[string]string{
		"shared/declarations": "[paths]\n# switchboard first\n~/.local/share/switchboard/bin\n/opt/homebrew/bin\n/usr/bin\n",
		"laptop/declarations": "[paths]\n~/Library/Application Support/Tool/bin   # spaces and all\n",
	})
	got, err := cfg.SearchPath("/home/someone", "laptop")
	want := []string{"/home/someone/.local/share/switchboard/bin", "/opt/homebrew/bin", "/usr/bin", "/home/someone/Library/Application Support/Tool/bin", "/bin", "/usr/sbin", "/sbin"}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("SearchPath() = %q, %v\nwant %q", got, err, want)
	}
	if got, err := cfg.SearchPath("/home/someone", "studio"); err != nil || len(got) != 6 {
		t.Errorf("SearchPath(studio) = %q, %v; want the shared paths and the system's", got, err)
	}
}

func TestTheShellsPathKeepsARelativeDirectory(t *testing.T) {
	cfg := loadRepo(t, map[string]string{"shared/declarations": "[paths]\n~/bin\nnode_modules/.bin\n/opt/homebrew/bin\n"})
	shell, err := cfg.ShellPath("/home/someone", "laptop")
	if want := []string{"/home/someone/bin", "node_modules/.bin", "/opt/homebrew/bin"}; err != nil || !slices.Equal(shell, want) {
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
