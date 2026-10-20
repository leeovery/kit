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

// FullDiskAccess checks kit can read what only Full Disk Access shows, as
// settings capture needs: run by the hourly launch's app, the app's grant
// (lost when it's rebuilt unsigned); run at a terminal, the terminal's.
func FullDiskAccess(home string) engine.Step {
	return engine.Step{
		Name: "full-disk-access", Title: "Full Disk Access", Area: AreaBackups,
		Check: func(context.Context) check.Result {
			denied := 0
			for _, probe := range fullDiskAccessProbes {
				_, err := os.ReadDir(filepath.Join(home, probe))
				switch {
				case err == nil:
					return check.Result{State: check.OK, Summary: "granted"}
				case errors.Is(err, fs.ErrPermission):
					denied++
				}
			}
			if denied == 0 {
				return check.Result{State: check.OK, Summary: "nothing protected to read"}
			}
			return problem("full-disk-access", "not granted", [3]string{"denied", "kit can't read what only Full Disk Access shows, so settings capture will fail", "System Settings › Privacy & Security › Full Disk Access: add the app (or terminal) kit runs in"})
		},
	}
}
