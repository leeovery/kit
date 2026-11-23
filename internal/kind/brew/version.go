package brew

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// replaces reports whether kit replaces an app of version have with its
// cask's, of version: only the same version, of another build, or an older
// copy of the same major version, as both say in dotted numbers. Another
// major version may be kept on purpose (an older line, an LTS), a newer
// copy or a prerelease is ahead of the cask, and latest says nothing, so
// --force, which deletes the app, and a package's installer, which runs
// over it, wait for none of them.
func replaces(have, version string) bool {
	mine, theirs := dotted(have), dotted(version)
	return sameVersion(have, version) || mine != nil && theirs != nil && mine[0] == theirs[0] && compare(mine, theirs) < 0
}

// notReplacing is why the app at at, of version have, isn't replaced with
// its cask's, of version: nil when it is (replaces).
func notReplacing(at, have, version string) error {
	if replaces(have, version) {
		return nil
	}
	said := fmt.Sprintf("%s and the cask's is %s: kit replaces only an older copy of the same major version", versionOf(at, have), version)
	if why := whyNot(have, version); why != "" {
		said += ", and " + why
	}
	return errors.New(said)
}

// notAdopting is why the app at at, of version have, isn't adopted by its
// cask, of version, which updates itself: it's a copy of an earlier major
// version, or one either version can't tell; line, unless "", is the cask
// whose app it is.
func notAdopting(at, have, version, line string) error {
	said := fmt.Sprintf("%s and the cask's is %s: kit takes over only a copy of the cask's major version or later", versionOf(at, have), version)
	switch {
	case line != "":
		said += ", and " + line + " is this one's line"
	case major(have) < 0 || major(version) < 0:
		said += ", and can't tell whether this one is"
	}
	return errors.New(said)
}

// notChannel is why the app at at, a prerelease of version have, isn't
// adopted by its cask, of version, as a channel shares its place: channel,
// unless "", is the one whose app it is.
func notChannel(at, have, version, channel string) error {
	said := fmt.Sprintf("%s and the cask's is %s: kit takes over a prerelease only by its channel's cask", versionOf(at, have), version)
	if channel != "" {
		return errors.New(said + ", and " + channel + " is this one's")
	}
	return errors.New(said + ", and none here is at its major version")
}

// versionOf says the app at at is of version have.
func versionOf(at, have string) string {
	if have == "" {
		return at + " says no version"
	}
	return at + " is " + have
}

// whyNot is what kit can say of an app of version have, beside its cask's,
// of version, once kit won't replace it (replaces): "" when it's an older
// major version, as the versions show.
func whyNot(have, version string) string {
	mine, theirs := dotted(have), dotted(version)
	switch {
	case prerelease.MatchString(have):
		return "this one is a prerelease"
	case prerelease.MatchString(version):
		return "the cask's is a prerelease"
	case version == "latest":
		return "latest says no version"
	case mine == nil || theirs == nil:
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

// nearness is how much of version, a cask's, an app's version have shares:
// the most when it's the same version (sameVersion), else how many of the
// numbers each starts with are the same, in order, as 5.2 of 5.2.3 and
// 5.2.4.
func nearness(have, version string) int {
	if sameVersion(have, version) {
		return math.MaxInt
	}
	mine, theirs := leading(have), leading(version)
	n := 0
	for n < len(mine) && n < len(theirs) && mine[n] == theirs[n] {
		n++
	}
	return n
}

// leadingNumbers are the dotted numbers a version starts with, as 2.8.0 of
// 2.8.0-beta1.
var leadingNumbers = regexp.MustCompile(`^\d+(?:\.\d+)*`)

// leading is the numbers version starts with, as [2 8 0] for 2.8.0-beta1:
// none when it starts otherwise.
func leading(version string) []int {
	return dotted(leadingNumbers.FindString(version))
}

// major is version's major version, the number it starts with before a
// dot, as 153 of 153.7.0esr: -1 when it starts otherwise, and for a number
// alone, as a build's, 4215, which says no major version.
func major(version string) int {
	if numbers := leading(version); len(numbers) > 1 {
		return numbers[0]
	}
	return -1
}

// atOrAbove reports whether have, an app's version, is version, a cask's,
// or later, by the numbers each starts with: false when either starts with
// none.
func atOrAbove(have, version string) bool {
	mine, theirs := leading(have), leading(version)
	return mine != nil && theirs != nil && compare(mine, theirs) >= 0
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
