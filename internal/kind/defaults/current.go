package defaults

import (
	"context"
	"fmt"
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/config"
)

// never are the settings in the watched domains that macOS keeps for
// itself, beyond noise, so a snapshot of the Mac never declares them:
// window places, upgrade and migration markers, versions of its records,
// opaque caches, iCloud's state, and the state of Finder's and the input
// menu's views. Each list is path.Match patterns by domain, "*" for any,
// kept by the major version of macOS it was judged on (neverFor).
var never = map[int]map[string][]string{
	26: {
		"*": {
			"version", "*.version", "*Version", "*Location", "*UpgradedTo*", "*UpgradeLevel",
			"*TransitionComplete", "*DidMigrate", "*Migration", "HasDisplayed*", "*Cache", "*History",
		},
		"NSGlobalDomain": {
			"AKLastLocale", "CFKAL", "CFZKL", "NSBXY", "NSTAB", "NavPanelFileListModeFor*", "com.apple.finder.SyncExtensions",
		},
		"com.apple.AppleMultitouchTrackpad":                  {"UserPreferences"},
		"com.apple.driver.AppleBluetoothMultitouch.trackpad": {"UserPreferences"},
		"com.apple.HIToolbox":                                {"AppleCurrentKeyboardLayoutInputSourceID", "AppleSelectedInputSources"},
		// The menu bar's items' offsets, which move by themselves, each display
		// its own.
		"com.apple.controlcenter": {"ControlCenterDisplayableChronoControlsProviderConfiguration", "IRServiceToken", "NSStatusItem Preferred Position *"},
		"com.apple.finder": {
			"FXDetached*ProviderID", "FXICloud*", "FXInfoPanesExpanded", "FXLastSearchScope", "MeetingRoomViewSetting",
			"PreferencesWindow.LastSelection", "RecentsArrangeGroupViewBy", "SGTRecentFileSearches", "Sidebar*",
			"SmartSharedSearchViewSettings", "TagsCloudSerialNumber",
		},
	},
}

// neverFor is the never list for the macOS whose version sw_vers gives, as
// in 26.1: the list of the newest version at or below its major version, so
// a new macOS carries the last list forward; below every one, the oldest.
func neverFor(version string) (map[string][]string, error) {
	major, err := strconv.Atoi(strings.SplitN(version, ".", 2)[0])
	if err != nil {
		return nil, fmt.Errorf("macOS's version %q doesn't read", version)
	}
	if len(never) == 0 {
		return nil, nil
	}
	versions := slices.Sorted(maps.Keys(never))
	at := versions[0]
	for _, v := range versions {
		if v <= major {
			at = v
		}
	}
	return never[at], nil
}

// isNever reports whether key, in domain, is one of list's.
func isNever(list map[string][]string, domain, key string) bool {
	for _, pattern := range slices.Concat(list["*"], list[domain]) {
		if ok, _ := path.Match(pattern, key); ok {
			return true
		}
	}
	return false
}

// Current are the settings set now in the watched domains, and in their
// currentHost copies, as a snapshot of the Mac declares them, each named,
// and its value written, as adopting it declares it (Value), in name order.
// Left out are those declared (Values), noise, and macOS's own state for
// the macOS whose version sw_vers gives (neverFor), a value holding a date
// among it. A key some of whose entries are declared goes by its entries,
// as kit watches it.
func (d *Defaults) Current(ctx context.Context, version string) ([]config.Entry, error) {
	list, err := neverFor(version)
	if err != nil {
		return nil, err
	}
	var out []config.Entry
	for _, currentHost := range []bool{false, true} {
		for _, domain := range Watched {
			m, err := d.export(ctx, currentHost, domain)
			if err != nil {
				return nil, err
			}
			maps.DeleteFunc(m, func(key string, _ any) bool { return isNever(list, domain, key) })
			for s, v := range d.settingsOf(domainID(currentHost, domain), m) {
				// A setting's name takes its last colon as its entry's start,
				// so an entry whose key holds one can't be named.
				if strings.Contains(s.Entry, ":") {
					continue
				}
				if !d.isDeclared(s.Name()) && !holdsDate(v) {
					out = append(out, config.Entry{Name: s.Name(), Value: valueWords(v, s.Entry != "")})
				}
			}
		}
	}
	slices.SortFunc(out, func(a, b config.Entry) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// holdsDate reports whether v is a date, or holds one, in a dict or an
// array, however deep: a time something happened, never a choice.
func holdsDate(v any) bool {
	switch x := v.(type) {
	case time.Time:
		return true
	case map[string]any:
		return slices.ContainsFunc(slices.Collect(maps.Values(x)), holdsDate)
	case []any:
		return slices.ContainsFunc(x, holdsDate)
	}
	return false
}
