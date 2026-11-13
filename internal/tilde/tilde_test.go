package tilde_test

import (
	"testing"

	"github.com/leeovery/kit/internal/tilde"
)

func TestExpand(t *testing.T) {
	for path, want := range map[string]string{
		"~/Code/site":   "/Users/someone/Code/site",
		"~/Code//site/": "/Users/someone/Code/site",
		"~/":            "/Users/someone",
		"~":             "~",
		"~someone/Code": "~someone/Code",
		"/opt/homebrew": "/opt/homebrew",
		"Code/site":     "Code/site",
	} {
		if got := tilde.Expand("/Users/someone", path); got != want {
			t.Errorf("Expand(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestShown(t *testing.T) {
	for _, c := range []struct{ home, path, want string }{
		{"/Users/someone", "/Users/someone/Code/site", "~/Code/site"},
		{"/Users/someone", "/Users/someone", "/Users/someone"},
		{"/srv/home", "/srv/homeless/Code", "/srv/homeless/Code"},
		{"/Users/someone", "/opt/homebrew", "/opt/homebrew"},
		// Without a home, nothing's in it.
		{"", "/opt/homebrew", "/opt/homebrew"},
	} {
		if got := tilde.Shown(c.home, c.path); got != c.want {
			t.Errorf("Shown(%q, %q) = %q, want %q", c.home, c.path, got, c.want)
		}
	}
}
