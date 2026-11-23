package brew_test

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/kind/brew"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// matching is an app installed by hand, and the cask that installs it, ""
// for none: where it is, in the casks' folder, in a folder in it, or
// "other", another folder apps are in; its name, bundle id, the version it
// shows and its build.
type matching struct {
	name, in, app, id, version, build, want string
}

// byWhere: an app installed by hand has the cask that installs it, as
// Homebrew's catalogue has it: of the casks putting an app where it is, in
// the folder casks put their apps in, the one that states its bundle id,
// in any case; one without @ over those with, the app's version aside
// (byVersion). A cask puts its app where its target says, or, installing
// through a package with no app of its own, where its uninstall deletes
// one. An app anywhere else is no cask's: installing one would put a
// second copy in place, whatever id it quits. But a package that says
// nowhere is the one an app at the folder's top is, when it alone quits
// the app's id. None states a fork's id under the cask's app's name; a
// cask that states no id, is disabled, or moves into place what kit can't
// vet is never the one; nor is any when more than one would do.
var byWhere = []matching{
	{"by its name", "", "Google Chrome.app", "com.google.Chrome", "158.0.8127.37", "", "google-chrome"},
	{"its id in another case", "", "Google Chrome.app", "COM.GOOGLE.CHROME", "158.0.8127.37", "", "google-chrome"},
	{"in another folder apps are in", "other", "Google Chrome.app", "com.google.Chrome", "158.0.8127.37", "", ""},
	{"in a folder in the casks' folder", "Utilities", "Google Chrome.app", "com.google.Chrome", "158.0.8127.37", "", ""},
	{"a fork, under its name", "", "Spaceman.app", "dev.fork.Spaceman", "1.0", "", ""},
	{"the cask's own, under its name", "", "Spaceman.app", "dev.original.Spaceman", "1.0", "", "spaceman"},
	{"its package's, as its uninstall deletes it, by the id it signals", "", "zoom.us.app", "us.zoom.xos", "7.2.5", "", "zoom"},
	{"a package's that says nowhere, by the id it quits", "", "Monitor.app", "com.example.monitor", "2.7.0", "", "monitor"},
	{"a package's that says nowhere, in a folder in the casks' folder", "Utilities", "Monitor.app", "com.example.monitor", "2.7.0", "", ""},
	{"a package's that says nowhere, in another folder apps are in", "other", "Monitor.app", "com.example.monitor", "2.7.0", "", ""},
	{"by an id two packages that say nowhere quit", "", "Relay.app", "com.example.relay", "4.1", "", ""},
	{"the plain token over one with @", "", "1Password.app", "com.1password.1password", "8.12.46", "", "1password"},
	{"a cask stating no id", "", "Quiet Tool.app", "com.example.quiet", "2.1", "", ""},
	{"by its target, the id its saved state is named for", "", "Browser.app", "com.example.browser", "140.0", "", "browser"},
	{"under the name its target renames, by the id it quits", "", "Chromium.app", "com.example.browser", "140.0", "", ""},
	{"under another name, by the id a cask quits", "", "Editor.app", "com.example.editor", "3.2", "", ""},
	{"from a folder of the download, by an id it signals of several", "", "Signals.app", "com.example.signals.agent", "1.4", "", "signals"},
	{"a cask moving a suite too", "", "Office.app", "com.example.office", "", "", ""},
	{"a cask putting its app elsewhere, by the id it quits", "", "Sidekick.app", "com.example.sidekick", "5.0", "", ""},
	{"a disabled cask", "", "Old Tool.app", "com.example.oldtool", "0.9", "", ""},
	{"no bundle id", "", "Google Chrome.app", "", "158.0.8127.37", "", ""},
	{"no cask", "", "Kubius.app", "com.example.kubius", "", "", ""},
}

// byVersion: of the casks putting an app where it is, a prerelease's is
// its channel's, with a channel at its place: the one at its major version
// nearest its version, never the cask of releases; none, with none at its
// major version, or two as near it. Else the one without @ is the one when
// it updates itself and the app's major version is its or later, as an app
// that updates itself runs ahead of the catalogue: Homebrew adopts it as
// it is. An app of an earlier major version is the line's at that major
// version, never a channel's, and none's with no such line, as the one
// without @ would declare a major version the person may not own. Else the
// one whose version shares the most of the app's numbers it starts with
// is: the app's own version most of all, its build after a comma aside, as
// an older line's, an LTS or a beta; a tie going to the one without @. And
// only if installing it would take the app over: the app is its version,
// or an older copy of the same major version, which kit replaces, as a
// package's installer runs over it whether it updates itself or not.
// Never one whose install would refuse it.
var byVersion = []matching{
	{"an older major version, its line's", "", "Charles.app", "com.xk72.Charles", "4.6.8", "", "charles@4"},
	{"an older copy of an older line", "", "Charles.app", "com.xk72.Charles", "4.5.0", "", "charles@4"},
	{"an older major version, of no line", "", "Charles.app", "com.xk72.Charles", "3.11.6", "", ""},
	{"an older copy of the same major version", "", "Charles.app", "com.xk72.Charles", "5.1.0", "", "charles"},
	{"an older line's, as 4.2 is 4.2.0", "", "Godot.app", "org.godotengine.godot", "3.6.3.0", "", "godot@3"},
	{"the current version", "", "Godot.app", "org.godotengine.godot", "4.7.2", "", "godot"},
	{"an LTS", "", "Blender.app", "org.blenderfoundation.blender", "5.2.4", "", "blender@lts"},
	{"an older copy of an LTS, its major the current one's", "", "Blender.app", "org.blenderfoundation.blender", "5.2.3", "", "blender@lts"},
	{"an older LTS", "", "Blender.app", "org.blenderfoundation.blender", "4.5.6", "", ""},
	{"a beta", "", "KeePassXC.app", "org.keepassxc.keepassxc", "2.8.0-beta1", "", "keepassxc@beta"},
	{"a version, its build aside", "", "KeePassXC.app", "org.keepassxc.keepassxc", "2.8.0", "", "keepassxc"},
	{"as near the beta as the cask without @", "", "KeePassXC.app", "org.keepassxc.keepassxc", "2.7.10", "", "keepassxc"},
	{"newer than the cask's", "", "Example Editor.app", "com.example.editor", "3.3", "", ""},
	{"a prerelease", "", "Example Editor.app", "com.example.editor", "3.2-rc1", "", ""},
	{"no version", "", "Example Editor.app", "com.example.editor", "", "", ""},
	{"a cask of the latest", "", "Nightly.app", "com.example.nightly", "1.0", "", ""},
	{"a cask that updates itself, a newer copy", "", "Spaceman.app", "dev.original.Spaceman", "1.1", "", "spaceman"},
	{"a cask that updates itself, a later major version", "", "Google Chrome.app", "com.google.Chrome", "159.0.8200.12", "", "google-chrome"},
	{"a cask that updates itself, at the beta's version", "", "Dropbox.app", "com.getdropbox.dropbox", "281.3.5397", "", "dropbox"},
	{"a prerelease, the channel at its version", "", "1Password.app", "com.1password.1password", "8.12.48-24.BETA", "", "1password@beta"},
	{"a prerelease, the channel a week behind it", "", "1Password.app", "com.1password.1password", "8.12.50-10.BETA", "", "1password@beta"},
	{"a prerelease, the channel at its version, though the cask without @ updates itself", "", "Thaw.app", "com.example.thaw", "3.0.0-beta.7", "", "thaw@beta"},
	{"a prerelease, the channel nearest it, of a later major version than the cask without @", "", "Thaw.app", "com.example.thaw", "3.0.0-beta.8", "", "thaw@beta"},
	{"a prerelease, the channel at its major version", "", "Firefox.app", "org.mozilla.firefox", "161.0b4", "", "firefox@beta"},
	{"a prerelease, no channel at its major version", "", "Firefox.app", "org.mozilla.firefox", "162.0a1", "", ""},
	{"a prerelease, no channel at its place", "", "BBEdit.app", "com.barebones.bbedit", "16.1b2", "", "bbedit"},
	{"a prerelease, two channels as near it", "", "Lamp.app", "com.example.lamp", "3.0b2", "", ""},
	{"a prerelease, the one of two channels at its version", "", "Lamp.app", "com.example.lamp", "3.0b4", "", "lamp@beta"},
	{"a release, beside two channels", "", "Lamp.app", "com.example.lamp", "2.0", "", "lamp"},
	{"a prerelease marked as a tip, no channel at its major version", "", "Ghostty.app", "com.mitchellh.ghostty", "1.5.0-tip", "", ""},
	{"its own version, beside a channel", "", "Thaw.app", "com.example.thaw", "2.1.0", "", "thaw"},
	{"a cask that updates itself, a version kit can't read, though a line of no version shares its place", "", "Spaceman.app", "dev.original.Spaceman", "Build 7", "", ""},
	{"a cask that updates itself, its own major version beside a line", "", "BBEdit.app", "com.barebones.bbedit", "16.1", "", "bbedit"},
	{"a cask that updates itself, an earlier major version, its line's", "", "BBEdit.app", "com.barebones.bbedit", "14.6.2", "", "bbedit@14"},
	{"a cask that updates itself, an earlier major version of no line", "", "BBEdit.app", "com.barebones.bbedit", "15.0.3", "", ""},
	{"a cask that updates itself, an ESR", "", "Firefox.app", "org.mozilla.firefox", "153.7.0", "", "firefox@esr"},
	{"a cask that updates itself, its own major version beside an ESR and a beta", "", "Firefox.app", "org.mozilla.firefox", "160.0", "", "firefox"},
	{"a cask that updates itself, ahead of the beta", "", "Firefox.app", "org.mozilla.firefox", "162.0", "", "firefox"},
	{"a cask that updates itself, an earlier major version, never a channel's", "", "Little Snitch.app", "at.obdev.littlesnitch", "5.9.1", "", "little-snitch@5"},
	{"a cask that updates itself, an earlier major version, with only a channel's", "", "1Password.app", "com.1password.1password", "7.9", "", ""},
	{"a cask that updates itself, an earlier major version of no line at all", "", "Google Chrome.app", "com.google.Chrome", "120.0", "", ""},
	{"a channel's own app, of an earlier major version than its", "", "Google Chrome Beta.app", "com.google.Chrome.beta", "158.0.7258.5", "", ""},
	{"a channel's own app, of its major version", "", "Google Chrome Beta.app", "com.google.Chrome.beta", "159.0.8100.2", "", "google-chrome@beta"},
	{"a package that updates itself, an older copy", "", "Monitor.app", "com.example.monitor", "2.7.0", "", "monitor"},
	{"a package that updates itself, its version, as a prefix of it", "", "Monitor.app", "com.example.monitor", "2.8.1", "", "monitor"},
	{"a package that updates itself, its version", "", "Monitor.app", "com.example.monitor", "2.8.1.316", "", "monitor"},
	{"a package that updates itself, a newer copy", "", "Monitor.app", "com.example.monitor", "2.9", "", ""},
	{"a package that updates itself, an earlier major version", "", "Monitor.app", "com.example.monitor", "1.9", "", ""},
	{"a package beside its own app, updating itself, a newer copy", "", "Wireshark.app", "org.wireshark.Wireshark", "4.7.0", "", ""},
	{"a package beside its own app, updating itself, an older copy", "", "Wireshark.app", "org.wireshark.Wireshark", "4.6.2", "", "wireshark-app"},
}

// byHardVersion: an app's version is the one it shows, or its build's when
// only that starts with a number, as Sublime Text shows Build 4215. A
// build's says no major version: at or above the cask's build, as it's
// updated itself, it's the cask's; below, or beside a cask's dotted
// version, which it can't be told against, none's. One that still can't be
// told gets no cask that updates itself, as which major version it is
// can't be told. A cask that updates itself whose own version can't be
// told (latest) is the app's only when no line shares its place.
var byHardVersion = []matching{
	{"shown as a build, its build the cask's", "", "Sublime Text.app", "com.sublimetext.4", "Build 4215", "4215", "sublime-text"},
	{"shown as a build, a later build than the cask's", "", "Sublime Text.app", "com.sublimetext.4", "Build 4300", "4300", "sublime-text"},
	{"shown as a build, an earlier build than the cask's", "", "Sublime Text.app", "com.sublimetext.4", "Build 4200", "4200", ""},
	{"shown as a build, of an earlier major version", "", "Sublime Text.app", "com.sublimetext.3", "Build 3211", "3211", ""},
	{"shown as a build, beside a cask of dotted numbers", "", "Ghostty.app", "com.mitchellh.ghostty", "9999", "", ""},
	{"dotted numbers, beside a cask of a build", "", "Sublime Text.app", "com.sublimetext.4", "4300.1", "", ""},
	{"shown as a build, with none", "", "Sublime Text.app", "com.sublimetext.4", "Build 4215", "", ""},
	{"no version", "", "BBEdit.app", "com.barebones.bbedit", "", "", ""},
	{"a cask of the latest, with no line", "", "Stream.app", "com.example.stream", "1.0", "", "stream"},
	{"a cask of the latest, no version either, with no line", "", "Stream.app", "com.example.stream", "", "", "stream"},
	{"a cask of the latest, with a line", "", "Board.app", "com.example.board", "2.4", "", ""},
}

func TestTheCaskThatInstallsAnAppByHand(t *testing.T) { matchesAll(t, byWhere) }

func TestTheCaskOfTheAppsVersion(t *testing.T) { matchesAll(t, byVersion) }

func TestTheCaskOfAnAppWhoseVersionIsHardToTell(t *testing.T) { matchesAll(t, byHardVersion) }

// matchesAll has each of cases found installed by hand, with the cask it
// wants.
func matchesAll(t *testing.T, cases []matching) {
	t.Helper()
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			fake := runnertest.New(t)
			apps, other := t.TempDir(), t.TempDir()
			path := tt.installIn(t, apps, other)
			noCasks(t, fake)
			catalogued(t, fake)
			found, err := withApps(fake, apps, other).AppsByHand(t.Context())
			want := []brew.ByHand{{Path: path, Name: strings.TrimSuffix(tt.app, ".app"), ID: tt.id, Cask: tt.want}}
			if err != nil || found.Unmatched != nil || !slices.Equal(found.Apps, want) {
				t.Errorf("AppsByHand() = %+v, %v; want %+v", found, err, want)
			}
		})
	}
}

// installIn puts tt's app where it is, of apps, the casks' folder, and
// other, another folder apps are in, and returns where.
func (tt matching) installIn(t *testing.T, apps, other string) string {
	t.Helper()
	dir := filepath.Join(apps, tt.in)
	if tt.in == "other" {
		dir = other
	}
	return installBuild(t, dir, tt.app, tt.id, tt.version, tt.build)
}

// Every cask offered for an app installed by hand takes the app over once
// it's installed: kit offers nothing its install refuses. Homebrew, as
// faked, fails to adopt an app of another version than the cask's, for a
// cask that neither updates itself nor installs a package, saying the app
// differs, as it does.
func TestWhatIsOfferedIsTakenOver(t *testing.T) {
	casks := catalogueOf(t)
	for _, tt := range slices.Concat(byWhere, byVersion, byHardVersion) {
		if tt.want == "" {
			continue
		}
		t.Run(tt.name, func(t *testing.T) {
			fake, apps, kind := adopting(t)
			tt.installIn(t, apps, t.TempDir())
			adopt := fake.On("brew", "install", "--cask", "--adopt", tt.want)
			if k := casks[tt.want]; !k.AutoUpdates && !k.pkg && tt.version != k.Version {
				adopt.Exits(1).PrintsToStderr("Error: " + tt.want + ": It seems the existing App is different from the one being installed.\n")
			}
			fake.On("brew", "install", "--cask", "--force", tt.want)
			fake.On("brew", "list", "--cask", "--full-name", "-1").Prints(tt.want + "\n")
			if err := kind.Install(t.Context(), []string{tt.want}); err != nil {
				t.Errorf("offered %s, whose install failed: %v", tt.want, err)
			}
		})
	}
}

// A line that updates itself, declared, takes over only an app it's
// offered: never one of a later major version, as bbedit@14 never takes
// over BBEdit 16.1, nor one whose version can't be told. Homebrew adopts
// such a cask's app unchecked.
func TestALineTakesOverOnlyWhatIsOfferedIt(t *testing.T) {
	casks := catalogueOf(t)
	for _, tt := range slices.Concat(byVersion, byHardVersion) {
		for token, k := range casks {
			if !k.updatingLine(token) || !k.puts(tt.app) {
				continue
			}
			t.Run(tt.name+", by "+token, func(t *testing.T) {
				fake, apps, kind := adopting(t)
				tt.installIn(t, apps, t.TempDir())
				fake.On("brew", "install", "--cask", "--adopt", token)
				fake.On("brew", "list", "--cask", "--full-name", "-1").Prints(token + "\n")
				switch err := kind.Install(t.Context(), []string{token}); {
				case token == tt.want && err != nil:
					t.Errorf("offered %s, whose install failed: %v", token, err)
				case token != tt.want && err == nil:
					t.Errorf("%s took over an app offered %q", token, tt.want)
				}
			})
		}
	}
}

// fixtureCask is a cask in the catalogue in testdata, as far as faking
// Homebrew's adopt needs it: its version, its build after a comma aside;
// whether it updates itself; and whether it installs a package.
type fixtureCask struct {
	Version     string  `json:"version"`
	AutoUpdates bool    `json:"auto_updates"`
	Artifacts   [][]any `json:"raw_artifacts"`
	pkg         bool
}

// updatingLine reports whether the cask, of token, is a line of versions
// (bbedit@14, firefox@esr), not a channel, that updates itself and
// installs no package.
func (k fixtureCask) updatingLine(token string) bool {
	return brew.IsLine(token) && k.AutoUpdates && !k.pkg
}

// puts reports whether the cask puts app in place.
func (k fixtureCask) puts(app string) bool {
	return slices.ContainsFunc(k.Artifacts, func(a []any) bool {
		if len(a) != 2 || a[0] != ":app" {
			return false
		}
		names, _ := a[1].([]any)
		return slices.Contains(names, any(app))
	})
}

// catalogueOf is the casks of the catalogue in testdata, each by its token.
func catalogueOf(t *testing.T) map[string]fixtureCask {
	t.Helper()
	_, body, _ := strings.Cut(fixture(t, "catalogue.jws.json.payload"), "\n")
	var p struct {
		Casks map[string]fixtureCask `json:"casks"`
	}
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatal(err)
	}
	for token, k := range p.Casks {
		k.Version, _, _ = strings.Cut(k.Version, ",")
		k.pkg = slices.ContainsFunc(k.Artifacts, func(a []any) bool { return len(a) > 0 && a[0] == ":pkg" })
		p.Casks[token] = k
	}
	return p.Casks
}
