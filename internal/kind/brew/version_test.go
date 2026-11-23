package brew_test

import (
	"math"
	"slices"
	"testing"

	"github.com/leeovery/kit/internal/kind/brew"
)

// A version's major version is the number it starts with, whatever
// follows; none when it starts otherwise.
func TestAVersionsMajorVersion(t *testing.T) {
	for version, want := range map[string]int{
		"Build 4215": -1, "1.0 (123)": 1, "5.2a": 5, "3.0b2": 3, "02.09.01.70": 2, "2024.1": 2024,
		"10.0": 10, "1.2.3,456": 1, "1.2.3:abc": 1, "153.7.0esr": 153, "latest": -1, "": -1,
	} {
		if got := brew.Major(version); got != want {
			t.Errorf("Major(%q) = %d, want %d", version, got, want)
		}
	}
}

// A version of dotted numbers alone is its numbers, leading zeros aside;
// any other is none.
func TestAVersionOfDottedNumbers(t *testing.T) {
	for version, want := range map[string][]int{
		"02.09.01.70": {2, 9, 1, 70}, "2024.1": {2024, 1}, "10.0": {10, 0}, "4215": {4215},
		"Build 4215": nil, "1.0 (123)": nil, "5.2a": nil, "3.0b2": nil, "1.2.3,456": nil,
		"1.2.3:abc": nil, "153.7.0esr": nil, "1.": nil, "1..2": nil, "": nil,
	} {
		if got := brew.Dotted(version); !slices.Equal(got, want) {
			t.Errorf("Dotted(%q) = %v, want %v", version, got, want)
		}
	}
}

// How near a cask's version is an app's: the most when it's the same, as
// written or as numbers; else how many of the numbers each starts with are
// the same, in order.
func TestHowNearAVersionIs(t *testing.T) {
	for _, tt := range []struct {
		have, version string
		want          int
	}{
		{"5.2.3", "5.2.4", 2},
		{"5.2.3", "5.3.0", 1},
		{"4.2", "4.2.0", math.MaxInt},
		{"2.8.0-beta1", "2.8.0-beta1", math.MaxInt},
		{"2.8.0", "2.8.0-beta1", 3},
		{"1.0 (123)", "1.0", 2},
		{"10.0", "9.9", 0},
		{"Build 4215", "4215", 0},
		{"", "1.0", 0},
	} {
		if got := brew.Nearness(tt.have, tt.version); got != tt.want {
			t.Errorf("Nearness(%q, %q) = %d, want %d", tt.have, tt.version, got, tt.want)
		}
	}
}

// kit replaces an app with its cask's only as the same version, as written
// or as numbers, or an older copy of the same major version, as both say in
// dotted numbers; and says why not otherwise.
func TestReplacingAVersion(t *testing.T) {
	for _, tt := range []struct {
		have, version string
		replaces      bool
		why           string
	}{
		{"3.1", "3.2", true, ""},
		{"3.2", "3.2", true, ""},
		{"4215", "4215", true, ""},
		{"16.88", "16.88.24081116", true, ""},
		{"02.09.01.70", "2.9.1.71", true, ""},
		{"2.8.0-beta1", "2.8.0-beta1", true, ""},
		{"9.9", "10.0", false, ""},
		{"10.0", "9.9", false, "this one is newer"},
		{"3.3", "3.2", false, "this one is newer"},
		{"3.0b2", "3.0", false, "this one is a prerelease"},
		{"2.7.10", "2.8.0-beta1", false, "the cask's is a prerelease"},
		{"1.0", "latest", false, "latest says no version"},
		{"Build 4215", "4215", false, "can't tell whether this one is"},
		{"1.0 (123)", "1.1", false, "can't tell whether this one is"},
		{"5.2a", "5.3", false, "can't tell whether this one is"},
		{"1.2.3:abc", "1.2.4", false, "can't tell whether this one is"},
		{"153.7.0esr", "160.0.1", false, "can't tell whether this one is"},
		{"", "1.0", false, "can't tell whether this one is"},
	} {
		if got := brew.Replaces(tt.have, tt.version); got != tt.replaces {
			t.Errorf("Replaces(%q, %q) = %v, want %v", tt.have, tt.version, got, tt.replaces)
		}
		if tt.replaces {
			continue
		}
		if got := brew.WhyNot(tt.have, tt.version); got != tt.why {
			t.Errorf("WhyNot(%q, %q) = %q, want %q", tt.have, tt.version, got, tt.why)
		}
	}
}
