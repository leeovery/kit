package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// On a new Mac, a folder declared to keep out of Spotlight isn't there
// yet, as a restore makes it later: there's nothing to keep out, so kit
// status and kit apply find the exclusion ok, never asking Spotlight about
// it, adding it to Spotlight's list, or making the folder.
func TestSpotlightExclusionOfAFolderNotThereYet(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "spotlight exclusions", "~/Code\n")
	w.fake.On("mdfind", "-onlyin", filepath.Join(w.home, "Code"), "-count", `kMDItemDisplayName == "*"`)
	w.fake.On("sudo", "-n", "true")
	for _, args := range [][]string{{"status"}, {"apply"}} {
		out, errOut, _ := w.run(t, args...)
		if res := lastResult(t, w, "spotlight-exclusion"); res.State != "ok" {
			t.Errorf("kit %s: spotlight-exclusion %s %q; printed\n%s%s", args[0], res.State, res.Reason, out, errOut)
		}
	}
	for _, c := range w.fake.Calls() {
		if strings.Contains(c, "mdfind") || strings.Contains(c, "PlistBuddy") {
			t.Errorf("ran %q; want Spotlight neither asked nor changed", c)
		}
	}
	if _, err := os.Stat(filepath.Join(w.home, "Code")); !os.IsNotExist(err) {
		t.Errorf("~/Code after kit apply: %v; want it not made", err)
	}
}
