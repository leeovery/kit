package brew

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/apps"
	"github.com/leeovery/kit/internal/plist"
	"github.com/leeovery/kit/internal/runner"
)

// ByHand is an app installed by hand: where it is; its name, without
// .app; its bundle id; and the cask that installs it, by its token, ""
// when none does, or which couldn't be told.
type ByHand struct {
	Path, Name, ID, Cask string
}

// HandApps are the apps installed by hand, and why which casks install
// them couldn't be told, when it couldn't: they're there all the same.
type HandApps struct {
	Apps      []ByHand
	Unmatched error
}

// AppsByHand are the apps in the folders apps are installed in (AppDirs)
// installed by hand: none of Apple's, nor the App Store's (its receipt in
// the app), nor one Chrome made for a website (chromeApp), nor one a cask
// put in place, as its app, in its suite's folder, or through its package
// (pkgutil's receipts); each with the cask that installs it, if one does,
// as Homebrew's catalogue has it (match). Without Homebrew, every app but
// those is by hand, and none has a cask.
func (h *Homebrew) AppsByHand(ctx context.Context) (HandApps, error) {
	brewed := runner.Has(h.run, "brew")
	var casked Casked
	var packaged map[string]bool
	if brewed {
		var err error
		if casked, err = h.Casked(ctx); err != nil {
			return HandApps{}, fmt.Errorf("find the casks' apps: %w", err)
		}
		if packaged, err = installedBy(ctx, h.run, casked.Receipts); err != nil {
			return HandApps{}, fmt.Errorf("find the casks' packages' apps: %w", err)
		}
	}
	var found HandApps
	for _, app := range apps.Installed(h.AppDirs) {
		id := apps.BundleID(app)
		if !h.byHand(app, id, casked, packaged) {
			continue
		}
		found.Apps = append(found.Apps, ByHand{Path: app, Name: strings.TrimSuffix(filepath.Base(app), ".app"), ID: id})
	}
	if !brewed || len(found.Apps) == 0 {
		return found, nil
	}
	cat, err := h.catalogue(ctx)
	if err != nil {
		found.Unmatched = err
		return found, nil
	}
	for i, app := range found.Apps {
		if at, err := filepath.Rel(h.AppDirs[0], app.Path); err == nil && filepath.IsLocal(at) {
			found.Apps[i].Cask, _ = cat.match(at, app.ID)
		}
	}
	return found, nil
}

// byHand reports whether the app at app, whose bundle id is id, was
// installed by hand: not Apple's, nor the App Store's, nor Chrome's for a
// website, nor put in place by a cask, as casked and packaged, the apps
// the casks' packages installed, say.
func (h *Homebrew) byHand(app, id string, casked Casked, packaged map[string]bool) bool {
	inSuite := slices.Contains(h.AppDirs, filepath.Dir(filepath.Dir(app))) && slices.Contains(casked.Suites, filepath.Base(filepath.Dir(app)))
	switch {
	case strings.HasPrefix(id, "com.apple."), strings.HasPrefix(id, chromeApp), fromAppStore(app):
		return false
	case slices.Contains(casked.Apps, filepath.Base(app)), inSuite, packaged[app]:
		return false
	}
	return true
}

// chromeApp starts the bundle id of each app Chrome makes for a website,
// as com.google.Chrome.app.<the website's id>: Chrome's, not the person's.
const chromeApp = "com.google.Chrome.app."

// fromAppStore reports whether the app at app carries the App Store's
// receipt.
func fromAppStore(app string) bool {
	_, err := os.Stat(filepath.Join(app, "Contents", "_MASReceipt", "receipt"))
	return err == nil
}

// installedBy are the apps, by path, the packages whose ids match patterns
// installed, as pkgutil's receipts list them, the place a package installs
// in among them, as one can install into an app's own folder: each pattern
// matching an id whole, as a regular expression, as Homebrew matches one,
// or as itself when it isn't one.
func installedBy(ctx context.Context, run runner.Runner, patterns []string) (map[string]bool, error) {
	if len(patterns) == 0 {
		return nil, nil
	}
	ids, err := pkgutil(ctx, run, "--pkgs")
	if err != nil {
		return nil, err
	}
	found := map[string]bool{}
	for _, id := range ids {
		if !slices.ContainsFunc(patterns, func(p string) bool { return matches(p, id) }) {
			continue
		}
		root, err := installedAt(ctx, run, id)
		if err != nil {
			return nil, err
		}
		files, err := pkgutil(ctx, run, "--files", id)
		if err != nil {
			return nil, err
		}
		for _, f := range append(files, "") {
			if p := filepath.Join(root, f); filepath.Ext(p) == ".app" {
				found[p] = true
			}
		}
	}
	return found, nil
}

// matches reports whether pattern, as Homebrew reads a cask's pkgutil,
// matches id.
func matches(pattern, id string) bool {
	re, err := regexp.Compile("^(?:" + pattern + ")$")
	if err != nil {
		return pattern == id
	}
	return re.MatchString(id)
}

// installedAt is where the package id installed its files, as its receipt
// says: its volume, then its location there.
func installedAt(ctx context.Context, run runner.Runner, id string) (string, error) {
	res, err := run.Run(ctx, runner.Command{Name: "pkgutil", Args: []string{"--pkg-info-plist", id}})
	if err != nil {
		return "", err
	}
	v, err := plist.Decode(res.Stdout)
	if err != nil {
		return "", fmt.Errorf("read %s's receipt: %w", id, err)
	}
	info, _ := v.(map[string]any)
	volume, _ := info["volume"].(string)
	location, _ := info["install-location"].(string)
	return filepath.Join("/", volume, location), nil
}

// pkgutil runs pkgutil with args, and returns what it printed, a line
// each.
func pkgutil(ctx context.Context, run runner.Runner, args ...string) ([]string, error) {
	res, err := run.Run(ctx, runner.Command{Name: "pkgutil", Args: args})
	if err != nil {
		return nil, err
	}
	var lines []string
	for line := range strings.Lines(string(res.Stdout)) {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines, nil
}
