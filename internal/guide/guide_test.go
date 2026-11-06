package guide

import (
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// Every file built in is the guide's, its end's or a topic's, and each of
// those is there, with something in it: none is left out, none forgotten.
func TestEveryFileIsReadAndThere(t *testing.T) {
	want := append([]string{"checking", "main"}, TopicNames()...)
	slices.Sort(want)
	entries, err := files.ReadDir("text")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, strings.TrimSuffix(e.Name(), ".md"))
	}
	if !slices.Equal(got, want) {
		t.Errorf("files built in = %q, want %q", got, want)
	}
	for _, name := range want {
		if strings.TrimSpace(read(name)) == "" {
			t.Errorf("%s.md is empty", name)
		}
	}
}

// The guide reads in one go: about 300 lines at most, the depth in its
// topics.
func TestTheGuideReadsInOneGo(t *testing.T) {
	if n := strings.Count(Main(), "\n"); n > 300 {
		t.Errorf("the guide is %d lines, want 300 at most: move the depth into a topic", n)
	}
}

// kit agent prints the guide as it's written, so every line of it stays
// within 80 columns.
func TestEveryLineFitsInEightyColumns(t *testing.T) {
	for _, name := range append([]string{"main", "checking"}, TopicNames()...) {
		for i, line := range strings.Split(read(name), "\n") {
			if n := utf8.RuneCountInString(line); n > 80 {
				t.Errorf("%s.md:%d is %d columns, want 80 at most", name, i+1, n)
			}
		}
	}
}
