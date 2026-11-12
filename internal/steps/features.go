package steps

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
)

// FeaturePrefs is the feature of apps' settings, saved and brought back,
// switched on for a Mac in its [features] section.
const FeaturePrefs = "prefs"

// Features are the features kit knows.
var Features = []string{FeatureFileSharing, FeaturePrefs, FeatureRemoteLogin, FeatureTouchIDSudo}

// fullDiskAccessProbes are folders macOS shows only to an app with Full Disk
// Access, relative to the home.
var fullDiskAccessProbes = []string{"Library/Safari", "Library/Mail", "Library/Messages"}

// FullDiskAccessName names the step checking the terminal's Full Disk
// Access.
const FullDiskAccessName = "full-disk-access"

// FullDiskAccess checks the terminal kit runs in can read what only Full
// Disk Access shows, as a capture or a restore of apps' settings run there
// needs. Not in the hourly launch's runs: kit.app's grant is AppAccess's.
func FullDiskAccess(home string) engine.Step {
	return engine.Step{
		Name: FullDiskAccessName, Title: "Full Disk Access", Area: AreaBackups,
		Check: func(context.Context) check.Result {
			switch granted, found := ProbeFullDiskAccess(home); {
			case granted:
				return check.Result{State: check.OK, Summary: "granted"}
			case !found:
				return check.Result{State: check.OK, Summary: "nothing protected to read"}
			}
			return problem(FullDiskAccessName, "not granted", [3]string{"denied", "the terminal kit runs in can't read what only Full Disk Access shows, so a capture or restore of apps' settings here fails", "System Settings › Privacy & Security › Full Disk Access: add the terminal kit runs in"})
		},
	}
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
