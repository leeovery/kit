package config_test

import (
	"slices"
	"strings"
	"testing"
)

func TestSearchPath(t *testing.T) {
	tests := []struct {
		name  string
		paths string
		want  []string
	}{
		{name: "no paths file", want: []string{"/usr/bin", "/bin", "/usr/sbin", "/sbin"}},
		{
			name:  "the listed directories first, ~ expanded, each once",
			paths: "# Tools\n~/.local/bin\n/opt/homebrew/bin   # Homebrew\n\n/opt/homebrew/sbin\n~\n/usr/bin\n/opt/homebrew/bin/\n",
			want:  []string{"/Users/someone/.local/bin", "/opt/homebrew/bin", "/opt/homebrew/sbin", "/Users/someone", "/usr/bin", "/bin", "/usr/sbin", "/sbin"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := map[string]string{}
			if tt.paths != "" {
				files["paths"] = tt.paths
			}
			got, err := loadRepo(t, files).SearchPath("/Users/someone")
			if err != nil {
				t.Fatalf("SearchPath() error = %v", err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("SearchPath() = %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestSearchPathRefuses(t *testing.T) {
	for _, line := range []string{"bin", "~someone/bin", "$HOME/bin"} {
		_, err := loadRepo(t, map[string]string{"paths": "/opt/homebrew/bin\n" + line + "\n"}).SearchPath("/Users/someone")
		if err == nil || !strings.Contains(err.Error(), "paths:2: \""+line+"\" isn't an absolute directory") {
			t.Errorf("SearchPath() with %q error = %v, want one naming the line", line, err)
		}
	}
}
