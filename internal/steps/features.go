package steps

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// FeaturePrefs is the feature of apps' settings, saved and brought back,
// switched on for a Mac in its [features] section.
const FeaturePrefs = "prefs"

// Features are the features kit knows.
var Features = []string{FeatureFileSharing, FeaturePrefs, FeatureRemoteLogin, FeatureScreenSharing, FeatureTouchIDSudo}

// fullDiskAccessProbes are folders macOS shows only to an app with Full Disk
// Access, relative to the home.
var fullDiskAccessProbes = []string{"Library/Safari", "Library/Mail", "Library/Messages"}

// HasFullDiskAccess reports whether what kit runs in reads what only Full
// Disk Access shows, in home, as far as can be told: so, with none of it
// there to read.
func HasFullDiskAccess(home string) bool {
	granted, found := ProbeFullDiskAccess(home)
	return granted || !found
}

// ProbeFullDiskAccess reports whether what only Full Disk Access shows, in
// home, reads, and whether any of it was found there to read.
func ProbeFullDiskAccess(home string) (granted, found bool) {
	for _, probe := range fullDiskAccessProbes {
		_, err := os.ReadDir(filepath.Join(home, probe))
		switch {
		case err == nil:
			return true, true
		case errors.Is(err, fs.ErrPermission):
			found = true
		}
	}
	return false, found
}
