package cli

import "testing"

// What removing an app tells of what it leaves names what there is: its
// files, as its kind's zap list finds them, and its settings, only where
// kit prefs saves them.
func TestLeftoverWords(t *testing.T) {
	for _, tt := range []struct {
		name                          string
		lo                            leftover
		title, removes, removed, kept string
	}{
		{"a cask's files and settings", leftover{app: "Example App", files: true, domains: []string{"com.example.app"}, saving: true},
			"Example App's settings and files", "its files from this Mac, and its saved settings from kit-prefs", "uninstalled with its files and settings", "its files kept: --zap removes them"},
		{"a cask's files, kit prefs off", leftover{app: "Example App", files: true, domains: []string{"com.example.app"}},
			"Example App's settings and files", "its files from this Mac", "uninstalled with its files", "its files kept: --zap removes them"},
		{"an App Store app's saved settings", leftover{app: "Notes Example", domains: []string{"com.example.notes"}, saving: true},
			"Notes Example's settings", "its saved settings from kit-prefs", "uninstalled with its saved settings", "its saved settings kept: --zap removes them"},
		{"nothing known", leftover{app: "Example App"}, "Example App's settings", "its saved settings from kit-prefs", "uninstalled", "its saved settings kept: --zap removes them"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := [4]string{tt.lo.title(), tt.lo.removes(), tt.lo.removed(), tt.lo.kept()}; got != [4]string{tt.title, tt.removes, tt.removed, tt.kept} {
				t.Errorf("words = %q\nwant %q", got, [4]string{tt.title, tt.removes, tt.removed, tt.kept})
			}
		})
	}
}
