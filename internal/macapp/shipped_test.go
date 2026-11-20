package macapp_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/macapp"
	"github.com/leeovery/kit/internal/plist"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// release lays out, in root, a release's kit: kit in bin, and the kit.app
// shipped beside it, at version, in libexec, as Homebrew and install.sh put
// them. It returns the kit's path and the app's.
func release(t *testing.T, root, version string) (kit, app string) {
	t.Helper()
	kit = filepath.Join(root, "bin", "kit")
	app = filepath.Join(root, "libexec", "kit.app")
	if err := macapp.Lay(app, version, team); err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string]string{kit: "kit " + version, filepath.Join(app, "Contents", "MacOS", "kit"): "app " + version} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return kit, app
}

// signedByTeam is codesign's check that a bundle is kit.app, signed by the
// test's team's Developer ID.
const signedByTeam = `-R=identifier "com.leeovery.kit" and anchor apple generic and certificate leaf[subject.OU] = "` + team + `"`

// copying scripts fake to copy the app shipped at from into the data
// directory dir as kit does: copied aside with ditto, then its signature
// checked whole and the team's. It returns the copy's commands, in order.
func copying(t *testing.T, fake *runnertest.Fake, from, dir string) []string {
	t.Helper()
	bundle := filepath.Join(dir, ".kit.app-build", "kit.app")
	fake.On("ditto", from, bundle).Does(func() {
		if err := os.CopyFS(bundle, os.DirFS(from)); err != nil {
			t.Error(err)
		}
	})
	fake.On("codesign", "--verify", "--strict", signedByTeam, bundle)
	return []string{
		runner.Command{Name: "ditto", Args: []string{from, bundle}}.String(),
		runner.Command{Name: "codesign", Args: []string{"--verify", "--strict", signedByTeam, bundle}}.String(),
	}
}

// relay lays the app shipped at app out again, as another release's build:
// at version, built from the files whose fingerprint is builtFrom, signed
// by the team named.
func relay(t *testing.T, app, version, builtFrom, team string) {
	t.Helper()
	if err := macapp.Lay(app, version, team); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"Resources/built-from": builtFrom + "\n", "MacOS/kit": "app " + version} {
		if err := os.WriteFile(filepath.Join(app, "Contents", name), []byte(data), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// same reports whether the bundles at a and b hold the same files.
func same(t *testing.T, a, b string) bool {
	t.Helper()
	read := func(root string) map[string]string {
		got := map[string]string{}
		err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, err := os.ReadFile(p)
			rel, _ := filepath.Rel(root, p)
			got[rel] = string(data)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	return reflect.DeepEqual(read(a), read(b))
}

// A release's kit.app is found in the libexec folder beside the kit's own,
// through the links to the kit, as Homebrew's: none beside a kit without
// one, or with one missing its executable.
func TestTheShippedAppIsFound(t *testing.T) {
	root := t.TempDir()
	kit, app := release(t, filepath.Join(root, "Cellar", "kit", "1.0.0"), "1.0.0")
	link := filepath.Join(root, "bin", "kit")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(kit, link); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(app)
	if err != nil {
		t.Fatal(err)
	}
	for name, self := range map[string]string{"the kit": kit, "a link to it": link} {
		if got := macapp.Shipped(self); got != want {
			t.Errorf("Shipped(%s) = %q, want %q", name, got, want)
		}
	}

	hand := filepath.Join(root, "scratch", "kit")
	if got := macapp.Shipped(hand); got != "" {
		t.Errorf("Shipped() of a kit with no app beside it = %q, want none", got)
	}
	if err := os.Remove(filepath.Join(app, "Contents", "MacOS", "kit")); err != nil {
		t.Fatal(err)
	}
	if got := macapp.Shipped(kit); got != "" {
		t.Errorf("Shipped() with no executable in the app = %q, want none", got)
	}

	kit, app = release(t, filepath.Join(root, "unsigned"), "1.0.0")
	if err := os.Remove(filepath.Join(app, "Contents", "Resources", "team")); err != nil {
		t.Fatal(err)
	}
	if got := macapp.Shipped(kit); got != "" {
		t.Errorf("Shipped() of an app naming no team = %q, want none", got)
	}
}

// A release's bundle is laid out as the Mac builds its own, all but the
// executable, its Info.plist carrying the release's version, numbers and
// dots alone, and the team it's signed by; without a version, the
// Info.plist is kit's own.
func TestLay(t *testing.T) {
	bundle := filepath.Join(t.TempDir(), "kit.app")
	if err := macapp.Lay(bundle, "1.2.3-rc.1", team); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(bundle, "Contents", "Info.plist"))
	if err != nil {
		t.Fatal(err)
	}
	v, err := plist.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	info := v.(map[string]any)
	for key, want := range map[string]any{"CFBundleVersion": "1.2.3", "CFBundleShortVersionString": "1.2.3", "CFBundleIdentifier": macapp.BundleID, "LSUIElement": true} {
		if info[key] != want {
			t.Errorf("Info.plist's %s = %v, want %v", key, info[key], want)
		}
	}
	for path, want := range map[string]string{"built-from": macapp.Fingerprint() + "\n", "team": team + "\n"} {
		if got, err := os.ReadFile(filepath.Join(bundle, "Contents", "Resources", path)); err != nil || string(got) != want {
			t.Errorf("Resources/%s = %q (%v), want %q", path, got, err, want)
		}
	}
	if fi, err := os.Stat(filepath.Join(bundle, "Contents", "MacOS")); err != nil || !fi.IsDir() {
		t.Errorf("no folder for the executable (%v)", err)
	}

	own := filepath.Join(t.TempDir(), "kit.app")
	if err := macapp.Lay(own, "", ""); err != nil {
		t.Fatal(err)
	}
	embedded, err := os.ReadFile("Info.plist")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(own, "Contents", "Info.plist")); err != nil || !bytes.Equal(got, embedded) {
		t.Errorf("Info.plist without a version isn't the one kit embeds (%v)", err)
	}
	if _, err := os.Stat(filepath.Join(own, "Contents", "Resources", "team")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a build with no team says one (%v)", err)
	}
}

// With a release's kit.app beside kit, that's the app: copied aside with
// ditto, its signature checked, then put in place, nothing compiled or
// signed on the Mac, Swift's compiler not even looked for.
func TestTheShippedAppIsCopied(t *testing.T) {
	_, shipped := release(t, t.TempDir(), "1.0.0")
	dir := filepath.Join(t.TempDir(), "kit")
	fake := runnertest.New(t)
	want := copying(t, fake, shipped, dir)
	app := macapp.New(fake, dir, shipped, terminal, clock)

	if !app.Ready(t.Context()) {
		t.Fatalf("Ready = false, ran %q", fake.Calls())
	}
	if got := fake.Calls(); !reflect.DeepEqual(got, want) {
		t.Errorf("ran %q, want %q", got, want)
	}
	if !same(t, app.Path(), shipped) || !app.Current() {
		t.Errorf("the app in place isn't the one shipped (Current %v)", app.Current())
	}
	if got := macapp.Identity(dir); got != "team:"+team {
		t.Errorf("Identity() = %q, want the team that signed it", got)
	}
	if _, err := os.Stat(filepath.Join(dir, ".kit.app-build")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the folder it was copied to is still there (%v)", err)
	}

	fake = runnertest.New(t)
	if !macapp.New(fake, dir, shipped, terminal, clock).Ready(t.Context()) || len(fake.Calls()) > 0 {
		t.Errorf("ran %q, want nothing: the app shipped is in place", fake.Calls())
	}
}

// The app in place is copied again whenever it isn't a copy of the build
// shipped: one built from other files, signed by another team, built on
// the Mac, or with its executable gone. Until it is, it says so.
func TestTheShippedAppReplacesAnother(t *testing.T) {
	for name, before := range map[string]func(t *testing.T, app string){
		"built from other files": func(t *testing.T, app string) {
			relay(t, app, "0.9.0", "an older kit's files", team)
		},
		"signed by another team": func(t *testing.T, app string) {
			relay(t, app, "1.0.0", macapp.Fingerprint(), "ZYXWV98765")
		},
		"built on the Mac": func(t *testing.T, app string) {
			dir := filepath.Dir(app)
			if err := os.RemoveAll(app); err != nil {
				t.Fatal(err)
			}
			if !macapp.New(runFor(t, dir), dir, "", terminal, clock).Ready(t.Context()) {
				t.Fatal("the app wasn't built")
			}
		},
		"its executable gone": func(t *testing.T, app string) {
			if err := os.Remove(filepath.Join(app, "Contents", "MacOS", "kit")); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "kit")
			_, shipped := release(t, t.TempDir(), "1.0.0")
			if err := os.CopyFS(filepath.Join(dir, "kit.app"), os.DirFS(shipped)); err != nil {
				t.Fatal(err)
			}
			before(t, filepath.Join(dir, "kit.app"))
			fake := runnertest.New(t)
			want := copying(t, fake, shipped, dir)
			app := macapp.New(fake, dir, shipped, terminal, clock)
			if app.Current() || app.Stale() != "kit.app not the one this kit ships" {
				t.Errorf("Current = %v, Stale = %q; want the app not the one this kit ships", app.Current(), app.Stale())
			}
			if !app.Ready(t.Context()) || !reflect.DeepEqual(fake.Calls(), want) {
				t.Errorf("ran %q, want it copied: %q", fake.Calls(), want)
			}
			if !same(t, app.Path(), shipped) {
				t.Error("the app in place isn't the one shipped")
			}
		})
	}
}

// A later release's build of the same files, by the same team, leaves the
// copy in place alone, its older version and all: macOS grants both alike,
// and the hourly run may be inside it.
func TestALaterReleaseOfTheSameFilesIsKept(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "kit")
	_, shipped := release(t, t.TempDir(), "1.0.0")
	if err := os.CopyFS(filepath.Join(dir, "kit.app"), os.DirFS(shipped)); err != nil {
		t.Fatal(err)
	}
	relay(t, shipped, "1.1.0", macapp.Fingerprint(), team)
	fake := runnertest.New(t)
	app := macapp.New(fake, dir, shipped, terminal, clock)
	if !app.Current() || !app.Ready(t.Context()) || len(fake.Calls()) > 0 {
		t.Errorf("Current = %v, ran %q; want the copy in place kept", app.Current(), fake.Calls())
	}
	if same(t, app.Path(), shipped) {
		t.Error("the copy in place was replaced by the later release's")
	}
}

// A copy that isn't kit.app signed by the team it names, as one signed ad
// hoc, isn't put in place, nor tried again in the run: the notification
// goes through osascript, and the app says why it isn't in place.
func TestACopyNotSignedByItsTeam(t *testing.T) {
	_, shipped := release(t, t.TempDir(), "1.0.0")
	dir := filepath.Join(t.TempDir(), "kit")
	fake := runnertest.New(t)
	copied := copying(t, fake, shipped, dir)
	fake.On("codesign", "--verify", "--strict", signedByTeam, filepath.Join(dir, ".kit.app-build", "kit.app")).Exits(3)
	script := `display notification "Open it" with title "kit" subtitle "Sign in"`
	fake.On("osascript", "-e", script)
	app := macapp.New(fake, dir, shipped, terminal, clock)

	app.Notify(t.Context(), "card:sign-in", "Sign in", "Open it")
	app.Notify(t.Context(), "card:sign-in", "Sign in", "Open it")
	want := slices.Concat(copied, []string{"osascript -e '" + script + "'", "osascript -e '" + script + "'"})
	if got := fake.Calls(); !reflect.DeepEqual(got, want) {
		t.Errorf("ran %q, want %q", got, want)
	}
	if _, err := os.Stat(app.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a copy not signed by its team was put in place (%v)", err)
	}
	why := app.Unready()
	if !strings.HasPrefix(why, "kit.app isn't in place: check kit.app, copied from "+shipped+", is signed by team "+team+"'s Developer ID: ") || !strings.HasSuffix(why, "; the next kit apply tries again") {
		t.Errorf("Unready() = %q, want the check that failed, and when it's tried again", why)
	}
}

// A copy naming no team, or one that isn't a team's, is refused before its
// signature's checked, as there's no team to check it against.
func TestACopyNamingNoTeam(t *testing.T) {
	for name, named := range map[string]string{"none": "", "not a team's": `X" or anchor apple`} {
		t.Run(name, func(t *testing.T) {
			_, shipped := release(t, t.TempDir(), "1.0.0")
			if err := os.WriteFile(filepath.Join(shipped, "Contents", "Resources", "team"), []byte(named), 0o644); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(t.TempDir(), "kit")
			fake := runnertest.New(t)
			copied := copying(t, fake, shipped, dir)
			app := macapp.New(fake, dir, shipped, terminal, clock)
			if app.Ready(t.Context()) || !reflect.DeepEqual(fake.Calls(), copied[:1]) {
				t.Errorf("ran %q, want the copy alone, refused", fake.Calls())
			}
			if _, err := os.Stat(app.Path()); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("a copy naming no team was put in place (%v)", err)
			}
		})
	}
}

// A kit with no app beside it, as one built by hand, keeps a release's
// build in place when it's built from the files that kit embeds: it's
// rebuilt, losing the Developer ID's grant, only for other files.
func TestAKitBuiltByHandKeepsAReleasesBuild(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "kit")
	_, shipped := release(t, t.TempDir(), "1.0.0")
	if err := os.CopyFS(filepath.Join(dir, "kit.app"), os.DirFS(shipped)); err != nil {
		t.Fatal(err)
	}
	fake := runnertest.New(t)
	if !macapp.New(fake, dir, "", terminal, clock).Ready(t.Context()) || len(fake.Calls()) > 0 {
		t.Errorf("ran %q, want nothing: a release's build of the same files is in place", fake.Calls())
	}
}
