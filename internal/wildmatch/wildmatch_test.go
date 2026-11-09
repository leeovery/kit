package wildmatch_test

import (
	"testing"

	"github.com/leeovery/kit/internal/wildmatch"
)

// Patterns match as rsync's wildmatch.c has them: * never past a /, **
// across them, though never as no folder between two /s; ? one character
// but /; classes with ranges, ! or ^ against them, ] first as itself, and
// named classes; \ the next character as itself; a class never a /, and one
// left open nothing.
func TestMatch(t *testing.T) {
	for _, tt := range []struct {
		pattern, text string
		want          bool
	}{
		{"*", "abc", true},
		{"*", "", true},
		{"*", "a/b", false},
		{"**", "a/b/c", true},
		{"a*", "abc", true},
		{"a*", "a/b", false},
		{"*.log", "x.log", true},
		{"*.log", "a/x.log", false},
		{"*/memory", "x/memory", true},
		{"*/memory", "x/y/memory", false},
		{"**/memory", "x/y/memory", true},
		{"**/memory", "memory", false},
		{"a/**/b", "a/x/y/b", true},
		{"a/**/b", "a/b", false},
		{"a/**", "a/b/c", true},
		{"a/**", "a", false},
		{"a*b*c", "axxbyyc", true},
		{"a*b*c", "axxbyy", false},
		{"?", "a", true},
		{"?", "/", false},
		{"?", "ab", false},
		{"[a-c]x", "bx", true},
		{"[a-c]x", "dx", false},
		{"[!a-c]x", "dx", true},
		{"[!a-c]x", "bx", false},
		{"[^a-c]x", "dx", true},
		{"[]]", "]", true},
		{"[!]]", "a", true},
		{"[a-]", "-", true},
		{"[[:digit:]]*", "7up", true},
		{"[[:digit:]]*", "up", false},
		{"[[:upper:][:digit:]]", "Q", true},
		{"[[:nothing:]]", "a", false},
		{"[[:x]", "[", true},
		{`\*`, "*", true},
		{`\*`, "a", false},
		{`a\[1]`, "a[1]", true},
		{`[\]]`, "]", true},
		{"[/]", "/", false},
		{"[!a]", "/", false},
		{"[a", "a", false},
		{"*20[0-9][0-9].[0-9]", "Tool2026.2", true},
		{"*20[0-9][0-9].[0-9]", "Tool2026.12", false},
		{"abc", "abcd", false},
		{"abcd", "abc", false},
	} {
		if got := wildmatch.Match(tt.pattern, tt.text); got != tt.want {
			t.Errorf("Match(%q, %q) = %v; want %v", tt.pattern, tt.text, got, tt.want)
		}
	}
}
