package brew

import (
	"cmp"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// replaces reports whether kit replaces an app of version have with its
// cask's, of version: only an older copy of the same major version, as
// both say in dotted numbers. Another major version may be kept on purpose
// (an older line, an LTS), a newer copy or a prerelease is ahead of the
// cask, and latest says nothing, so --force, which deletes the app, waits
// for none of them.
func replaces(have, version string) bool {
	mine, theirs := dotted(have), dotted(version)
	return mine != nil && theirs != nil && mine[0] == theirs[0] && compare(mine, theirs) < 0
}

// notReplacing is why the app at at, of version have, isn't replaced with
// its cask's, of version: nil when it is (replaces).
func notReplacing(at, have, version string) error {
	if replaces(have, version) {
		return nil
	}
	app := at + " is " + have
	if have == "" {
		app = at + " says no version"
	}
	said := fmt.Sprintf("%s and the cask's is %s: kit replaces only an older copy of the same major version", app, version)
	if why := whyNot(have, version); why != "" {
		said += ", and " + why
	}
	return errors.New(said)
}

// whyNot is what kit can say of an app of version have, beside its cask's,
// of version, once it isn't an older copy of the same major version: ""
// when it's an older major version, as the versions show.
func whyNot(have, version string) string {
	mine, theirs := dotted(have), dotted(version)
	switch {
	case prerelease.MatchString(have):
		return "this one is a prerelease"
	case prerelease.MatchString(version):
		return "the cask's is a prerelease"
	case version == "latest":
		return "latest says no version"
	case mine == nil || theirs == nil || compare(mine, theirs) == 0:
		return "can't tell whether this one is"
	case compare(mine, theirs) > 0:
		return "this one is newer"
	}
	return ""
}

// prerelease is a prerelease's version, as in 2.8.0-beta1, 1.0-rc.2 or
// 3.0b2.
var prerelease = regexp.MustCompile(`(?i)(?:^|[^a-z])(?:alpha|beta|preview|pre|rc|dev|nightly|canary|insiders?)(?:[^a-z]|$)|^\d+(?:\.\d+)*[ab]\d+$`)

// sameVersion reports whether have, an app's version, is version, a
// cask's: as written, or as dotted numbers, 4.2 being 4.2.0.
func sameVersion(have, version string) bool {
	mine, theirs := dotted(have), dotted(version)
	return have != "" && (have == version || mine != nil && theirs != nil && compare(mine, theirs) == 0)
}

// dottedNumbers is a version of numbers alone, as in 4.6.8.
var dottedNumbers = regexp.MustCompile(`^\d+(?:\.\d+)*$`)

// dotted is version's numbers, as in [4 6 8] for 4.6.8: none for any other
// version, a prerelease's or latest.
func dotted(version string) []int {
	if !dottedNumbers.MatchString(version) {
		return nil
	}
	parts := strings.Split(version, ".")
	numbers := make([]int, len(parts))
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil
		}
		numbers[i] = n
	}
	return numbers
}

// compare is -1, 0 or 1 as a is an older version than b, the same or a
// newer one, a missing number being 0.
func compare(a, b []int) int {
	for i := range max(len(a), len(b)) {
		if c := cmp.Compare(nth(a, i), nth(b, i)); c != 0 {
			return c
		}
	}
	return 0
}

// nth is numbers' i'th, 0 past its end.
func nth(numbers []int, i int) int {
	if i < len(numbers) {
		return numbers[i]
	}
	return 0
}
