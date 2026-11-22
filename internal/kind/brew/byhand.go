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
// those is by hand, and none has a cask. With no app but those, Homebrew
// is asked nothing; pkgutil only while an app is left that a cask's
// package might have installed.
func (h *Homebrew) AppsByHand(ctx context.Context) (HandApps, error) {
	return h.appsByHand(ctx, false)
}

// appsByHand is AppsByHand; with casksOnly, only the apps a cask installs,
// the rest dropped before pkgutil's asked about any: none, when the
// catalogue can't be read.
func (h *Homebrew) appsByHand(ctx context.Context, casksOnly bool) (HandApps, error) {
	var found HandApps
	for _, app := range apps.Installed(h.AppDirs) {
		if id := apps.BundleID(app); !others(app, id) {
			found.Apps = append(found.Apps, ByHand{Path: app, Name: strings.TrimSuffix(filepath.Base(app), ".app"), ID: id})
		}
	}
	if len(found.Apps) == 0 || !runner.Has(h.run, "brew") {
		return found, nil
	}
	casked, err := h.Casked(ctx)
	if err != nil {
		return HandApps{}, fmt.Errorf("find the casks' apps: %w", err)
	}
	found.Apps = slices.DeleteFunc(found.Apps, func(app ByHand) bool { return h.putBy(app.Path, casked) })
	if len(found.Apps) == 0 {
		return found, nil
	}
	if found.Unmatched = h.matched(ctx, found.Apps); casksOnly {
		found.Apps = slices.DeleteFunc(found.Apps, func(app ByHand) bool { return app.Cask == "" })
	}
	if len(found.Apps) == 0 || len(casked.Receipts) == 0 {
		return found, nil
	}
	packaged, err := installedBy(ctx, h.run, casked.Receipts)
	if err != nil {
		return HandApps{}, fmt.Errorf("find the casks' packages' apps: %w", err)
	}
	found.Apps = slices.DeleteFunc(found.Apps, func(app ByHand) bool { return packaged[app.Path] })
	return found, nil
}

// matched gives each of hand, the apps installed by hand, the cask that
// installs it, as Homebrew's catalogue has it, or says why it can't.
func (h *Homebrew) matched(ctx context.Context, hand []ByHand) error {
	cat, err := h.catalogue(ctx)
	if err != nil {
		return err
	}
	for i, app := range hand {
		if at, err := filepath.Rel(h.AppDirs[0], app.Path); err == nil && filepath.IsLocal(at) {
			hand[i].Cask, _ = cat.match(at, app.ID)
		}
	}
	return nil
}

// InstalledByHand finds the apps installed by hand that a cask installs
// (AppsByHand), by the cask's token, each with the app's name, as in
// Example.app: none, and why, when which casks install them couldn't be
// told.
func (c casks) InstalledByHand(ctx context.Context) (map[string]string, error) {
	hand, err := c.appsByHand(ctx, true)
	if err == nil {
		err = hand.Unmatched
	}
	if err != nil {
		return nil, err
	}
	found := map[string]string{}
	for _, app := range hand.Apps {
		if _, had := found[app.Cask]; !had {
			found[app.Cask] = filepath.Base(app.Path)
		}
	}
	return found, nil
}

// others reports whether the app at app, whose bundle id is id, is another's
// than the person's: Apple's, the App Store's, or Chrome's for a website.
func others(app, id string) bool {
	return strings.HasPrefix(id, "com.apple.") || strings.HasPrefix(id, chromeApp) || fromAppStore(app)
}

// putBy reports whether a cask put the app at app in place, as its app or
// in its suite's folder, as casked says.
func (h *Homebrew) putBy(app string, casked Casked) bool {
	inSuite := slices.Contains(h.AppDirs, filepath.Dir(filepath.Dir(app))) && slices.Contains(casked.Suites, filepath.Base(filepath.Dir(app)))
	return inSuite || slices.Contains(casked.Apps, filepath.Base(app))
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
