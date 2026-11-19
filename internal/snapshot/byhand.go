package snapshot

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/apps"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/brew"
	"github.com/leeovery/kit/internal/plist"
	"github.com/leeovery/kit/internal/runner"
)

// byHand are the apps in the Mac's app folders installed by hand, by name:
// none of Apple's, nor the App Store's (its receipt in the app), nor one
// Chrome made for a website (chromeApp), nor one a cask put in place, as
// its app, in its suite's folder, or through its package (pkgutil's
// receipts).
func (m Mac) byHand(ctx context.Context, deps *kind.Deps) ([]string, error) {
	var casked brew.Casked
	var packaged map[string]bool
	if runner.Has(m.Run, "brew") {
		var err error
		if casked, err = brew.Of(deps).Casked(ctx); err != nil {
			return nil, fmt.Errorf("find the casks' apps: %w", err)
		}
		if packaged, err = installedBy(ctx, m.Run, casked.Receipts); err != nil {
			return nil, fmt.Errorf("find the casks' packages' apps: %w", err)
		}
	}
	var out []string
	for _, app := range apps.Installed(m.AppDirs) {
		name := filepath.Base(app)
		inSuite := slices.Contains(m.AppDirs, filepath.Dir(filepath.Dir(app))) && slices.Contains(casked.Suites, filepath.Base(filepath.Dir(app)))
		id := apps.BundleID(app)
		if strings.HasPrefix(id, "com.apple.") || strings.HasPrefix(id, chromeApp) || fromAppStore(app) || slices.Contains(casked.Apps, name) || inSuite || packaged[app] {
			continue
		}
		out = append(out, strings.TrimSuffix(name, ".app"))
	}
	return out, nil
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
// matching an id
// whole, as a regular expression, as Homebrew matches one, or as itself
// when it isn't one.
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
