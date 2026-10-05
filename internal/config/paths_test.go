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

func TestSearchPathRefusesARelativeDirectory(t *testing.T) {
	cfg := loadRepo(t, map[string]string{"shared/declarations": "[paths]\nbin\n"})
	if _, err := cfg.SearchPath("/home/someone", "laptop"); err == nil || !strings.Contains(err.Error(), `shared/declarations:2: "bin" isn't an absolute directory`) {
		t.Errorf("SearchPath() error = %v", err)
	}
}
