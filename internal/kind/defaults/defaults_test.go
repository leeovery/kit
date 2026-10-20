package defaults_test

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/defaults"
	"github.com/leeovery/kit/internal/plist"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// mac is macOS settings on a made-up Mac: each domain's settings as XML
// dict bodies, by export, every watched domain empty unless given.
type mac struct {
	t     *testing.T
	fake  *runnertest.Fake
	d     *defaults.Defaults
	state string
}

func newMac(t *testing.T) *mac {
	m := &mac{t: t, fake: runnertest.New(t), state: t.TempDir()}
	m.d = defaults.New(m.fake, m.state)
	for _, domain := range defaults.Watched {
		m.set(domain, "")
	}
	return m
}

// set scripts defaults export for id (currentHost: first for one of this
// host) as a dict of body.
func (m *mac) set(id, body string) {
	args := []string{"export", strings.TrimPrefix(id, "currentHost:"), "-"}
	if strings.HasPrefix(id, "currentHost:") {
		args = append([]string{"-currentHost"}, args...)
	}
	m.fake.On("defaults", args...).Prints("<?xml version=\"1.0\"?>\n<plist version=\"1.0\">\n<dict>" + body + "</dict>\n</plist>\n")
}

// declare declares lines, each a setting's name and value.
func (m *mac) declare(pairs ...string) config.List {
	m.t.Helper()
	var list config.List
	for i := 0; i < len(pairs); i += 2 {
		list.Entries = append(list.Entries, config.Entry{Name: pairs[i], Value: pairs[i+1], Scope: "shared", Line: i + 2})
	}
	l, err := m.d.Values(list)
	if err != nil {
		m.t.Fatal(err)
	}
	return l
}

// items are a comparison's items, each its name, state, detail and action.
func (m *mac) items(list config.List) []string {
	var out []string
	for _, it := range kind.Compare(m.t.Context(), m.d, list).Items {
		out = append(out, strings.Join(slices.DeleteFunc([]string{it.Name, it.State, it.Detail, it.Action}, func(s string) bool { return s == "" }), " "))
	}
	return out
}

func TestANewMacGetsWhatsDeclared(t *testing.T) {
	m := newMac(t)
	list := m.declare("com.apple.dock:tilesize", "-int 60")
	m.set("com.apple.dock", "<key>tilesize</key><integer>48</integer>")
	if got := m.items(list); !slices.Equal(got, []string{"com.apple.dock:tilesize changed set to 48 install"}) {
		t.Errorf("items = %q; want it changed, never seen as declared, so set by applying", got)
	}
	m.fake.On("defaults", "write", "com.apple.dock", "tilesize", "-int", "60")
	m.fake.On("killall", "Dock")
	if err := m.d.Install(t.Context(), []string{"com.apple.dock:tilesize"}); err != nil {
		t.Fatal(err)
	}
	if calls := strings.Join(m.fake.Calls(), "\n"); !strings.Contains(calls, "defaults write com.apple.dock tilesize -int 60\nkillall Dock") {
		t.Errorf("ran\n%s", calls)
	}
}

func TestASettingChangedOnTheMacIsLeftToReconcile(t *testing.T) {
	m := newMac(t)
	list := m.declare("com.apple.dock:tilesize", "-int 60", "com.apple.dock:autohide", "-bool true")
	m.set("com.apple.dock", "<key>tilesize</key><integer>60</integer><key>autohide</key><integer>1</integer>")
	if got := m.items(list); len(got) != 0 {
		t.Fatalf("items = %q; want none, as declared (1 is true)", got)
	}
	m.set("com.apple.dock", "<key>tilesize</key><integer>48</integer>")
	want := []string{"com.apple.dock:autohide diverged not set: macOS's default", "com.apple.dock:tilesize diverged set to 48"}
	if got := m.items(list); !slices.Equal(got, want) {
		t.Errorf("items = %q, want %q", got, want)
	}
	if v, err := m.d.Value(t.Context(), "com.apple.dock:tilesize"); err != nil || v != "-int 48" {
		t.Errorf("Value() = %q, %v", v, err)
	}
}

func TestAWatchedSettingChangedShowsUp(t *testing.T) {
	m := newMac(t)
	list := m.declare()
	m.set("NSGlobalDomain", "<key>AppleInterfaceStyle</key><string>Dark</string><key>NSWindow Frame Main</key><string>1 2 3 4</string>")
	if got := m.items(list); len(got) != 0 {
		t.Fatalf("first look = %q; want the values taken, nothing changed", got)
	}
	m.set("NSGlobalDomain", "<key>NSWindow Frame Main</key><string>5 6 7 8</string><key>AppleShowAllExtensions</key><true/>")
	want := []string{"NSGlobalDomain:AppleInterfaceStyle extra", "NSGlobalDomain:AppleShowAllExtensions extra"}
	if got := m.items(list); !slices.Equal(got, want) {
		t.Errorf("items = %q, want %q (a window's frame is macOS's own)", got, want)
	}
	if v, err := m.d.Value(t.Context(), "NSGlobalDomain:AppleShowAllExtensions"); err != nil || v != "-bool true" {
		t.Errorf("Value() = %q, %v", v, err)
	}
	m.fake.On("defaults", "write", "NSGlobalDomain", "AppleInterfaceStyle", "<string>Dark</string>")
	m.fake.On("defaults", "delete", "NSGlobalDomain", "AppleShowAllExtensions")
	for _, name := range []string{"NSGlobalDomain:AppleInterfaceStyle", "NSGlobalDomain:AppleShowAllExtensions"} {
		if err := m.d.Revert(t.Context(), name); err != nil {
			t.Errorf("Revert(%s) = %v", name, err)
		}
	}
}

func TestADictsEntries(t *testing.T) {
	m := newMac(t)
	list := m.declare("com.apple.symbolichotkeys:AppleSymbolicHotKeys:60", `-dict-add "<dict><key>enabled</key><false/></dict>"`)
	hotkeys := func(sixty, sixtyOne string) string {
		return "<key>AppleSymbolicHotKeys</key><dict><key>60</key><dict><key>enabled</key>" + sixty + "</dict><key>61</key><dict><key>enabled</key>" + sixtyOne + "</dict></dict>"
	}
	m.set("com.apple.symbolichotkeys", hotkeys("<true/>", "<true/>"))
	if got := m.items(list); !slices.Equal(got, []string{"com.apple.symbolichotkeys:AppleSymbolicHotKeys:60 changed set to <dict><key>enabled</key><true/></dict> install"}) {
		t.Fatalf("items = %q", got)
	}
	m.set("com.apple.symbolichotkeys", hotkeys("<false/>", "<false/>"))
	if got := m.items(list); !slices.Equal(got, []string{"com.apple.symbolichotkeys:AppleSymbolicHotKeys:61 extra"}) {
		t.Errorf("items = %q; want the entry changed alone", got)
	}
	if v, err := m.d.Value(t.Context(), "com.apple.symbolichotkeys:AppleSymbolicHotKeys:61"); err != nil || v != `-dict-add "<dict><key>enabled</key><false/></dict>"` {
		t.Errorf("Value() = %q, %v", v, err)
	}
	m.fake.On("defaults", "write", "com.apple.symbolichotkeys", "AppleSymbolicHotKeys", "-dict-add", "60", "<dict><key>enabled</key><false/></dict>")
	m.fake.On("/System/Library/PrivateFrameworks/SystemAdministration.framework/Resources/activateSettings", "-u")
	if err := m.d.Install(t.Context(), []string{"com.apple.symbolichotkeys:AppleSymbolicHotKeys:60"}); err != nil {
		t.Errorf("Install() = %v", err)
	}
}

func TestThisHostsAndTheSystemsSettings(t *testing.T) {
	m := newMac(t)
	list := m.declare(
		"currentHost:NSGlobalDomain:com.apple.mouse.tapBehavior", "-int 1",
		"/Library/Preferences/com.apple.loginwindow:autoLoginUser", "-string someone",
	)
	m.set("currentHost:NSGlobalDomain", "")
	m.set("/Library/Preferences/com.apple.loginwindow", "")
	if got := m.items(list); len(got) != 2 {
		t.Fatalf("items = %q", got)
	}
	names := []string{"currentHost:NSGlobalDomain:com.apple.mouse.tapBehavior", "/Library/Preferences/com.apple.loginwindow:autoLoginUser"}
	if admin, _ := m.d.NeedsAdmin(t.Context(), names); !slices.Equal(admin, names[1:]) {
		t.Errorf("NeedsAdmin() = %q", admin)
	}
	m.fake.On("defaults", "-currentHost", "write", "NSGlobalDomain", "com.apple.mouse.tapBehavior", "-int", "1")
	m.fake.On("sudo", "-n", "defaults", "write", "/Library/Preferences/com.apple.loginwindow", "autoLoginUser", "-string", "someone")
	if err := m.d.Install(t.Context(), names); err != nil {
		t.Errorf("Install() = %v", err)
	}
}

func TestDeclaredValuesRead(t *testing.T) {
	d := defaults.New(runnertest.New(t), t.TempDir())
	for _, value := range []string{"-bool maybe", "-int sixty", `-dict-add "<integer>sixty</integer>"`, "<foo/>", "'<dict><key>a</key><bar/></dict>'", "<date>2026-01-01T00:00:00.5Z</date>"} {
		list := config.List{Entries: []config.Entry{{Name: "com.apple.dock:x:y", Value: value, Scope: "shared", Line: 2}}}
		if !strings.HasPrefix(value, "-dict-add") {
			list.Entries[0].Name = "com.apple.dock:x"
		}
		if _, err := d.Values(list); err == nil || !strings.Contains(err.Error(), "shared/declarations:2:") {
			t.Errorf("Values(%s) error = %v", value, err)
		}
	}
}

// defaults write takes a date only to the second, in UTC, ending Z: one
// with an offset, +00:00 too, or finer than the second, it can't parse, as
// macOS's own defaults showed.
func TestADateDefaultsWriteTakes(t *testing.T) {
	d := defaults.New(runnertest.New(t), t.TempDir())
	for value, want := range map[string]string{
		"<date>2026-01-01T00:00:00Z</date>":      "",
		"<date>2026-01-01T00:00:00+01:00</date>": "its date, 2026-01-01T00:00:00+01:00, has an offset, which defaults write doesn't take: the same time is 2025-12-31T23:00:00Z",
		"<date>2026-01-01T00:00:00+00:00</date>": "its date, 2026-01-01T00:00:00+00:00, has an offset",
		"<date>2026-01-01T00:00:00-05:00</date>": "its date, 2026-01-01T00:00:00-05:00, has an offset",
		"<date>2026-01-01T00:00:00.5Z</date>":    "its date, 2026-01-01T00:00:00.5Z, is finer than the second",
	} {
		_, err := d.Values(config.List{Entries: []config.Entry{{Name: "com.apple.dock:x", Value: value, Scope: "shared", Line: 2}}})
		if want == "" && err != nil || want != "" && (err == nil || !strings.Contains(err.Error(), want)) {
			t.Errorf("Values(%s) error = %v, want %q", value, err, want)
		}
	}
}

func TestNoiseKitTookBeforeKnowingItIsForgotten(t *testing.T) {
	m := newMac(t)
	list := m.declare()
	// kit's record from before the noise list held the heartbeat's key.
	record := `{"domains": {"com.apple.controlcenter": true}, "baseline": {"com.apple.controlcenter:LastHeartbeatDateString.daily": "<string>yesterday</string>"}}`
	if err := os.WriteFile(filepath.Join(m.state, "settings.json"), []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}
	m.set("com.apple.controlcenter", "<key>NSStatusItem Visible WiFi</key><true/>")
	if got := m.items(list); !slices.Equal(got, []string{"com.apple.controlcenter:NSStatusItem Visible WiFi extra"}) {
		t.Errorf("items = %q; want the noise forgotten, the new setting found", got)
	}
}

// A value kit took before it read a string's outer spaces and a key's
// exactly, unmarked in its record, is unchanged while it reads the same
// that way, compared as XML, and is taken again as it is: once, the
// baseline then marked exact. Any other change shows, as every change does
// to a baseline marked: a number or a boolean changed, even by a little or
// for one macOS reads the same; a key beside one it's the same as once
// trimmed; a value taken exactly with its spaces, now without. A change
// that shows leaves the baseline as it was marked.
func TestAValueTakenTrimmedIsTakenAgainExactly(t *testing.T) {
	cases := []struct {
		name, was, now string
		absorbed       bool
	}{
		{"a string gaining a newline", "<string>Dark</string>", "<string>Dark\n</string>", true},
		{"a string gaining a space", "<string>omw</string>", "<string>omw </string>", true},
		{"an array's string gaining a space", "<array><string>x</string></array>", "<array><string>x </string></array>", true},
		{"a dict's key gaining a space", "<dict><key>a</key><string>x</string></dict>", "<dict><key> a</key><string>x</string></dict>", true},
		{"an integer changed", "<integer>1760000000</integer>", "<integer>1760001000</integer>", false},
		{"1 become true", "<integer>1</integer>", "<true/>", false},
		{"0 become false", "<integer>0</integer>", "<false/>", false},
		{"a real changed a little", "<real>0.5</real>", "<real>0.5000001</real>", false},
		{"a key beside one the same once trimmed", "<dict><key>a</key><string>x</string></dict>", "<dict><key>a</key><string>x</string><key> a</key><string>x</string></dict>", false},
		{"a value taken with its spaces, now without", "<string>Light&#xA;</string>", "<string>Light</string>", false},
	}
	for _, marked := range []bool{false, true} {
		for _, c := range cases {
			// A dict's keys are read in no set order: each, many times.
			for range 20 {
				m := newMac(t)
				list := m.declare()
				record, err := json.Marshal(map[string]any{"domains": map[string]bool{"NSGlobalDomain": true}, "baseline": map[string]string{"NSGlobalDomain:K": c.was}, "exact_baselines": map[string]bool{"NSGlobalDomain:K": marked}})
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(m.state, "settings.json"), record, 0o600); err != nil {
					t.Fatal(err)
				}
				m.set("NSGlobalDomain", "<key>K</key>"+c.now)
				want, baseline := []string{"NSGlobalDomain:K extra"}, c.was
				if c.absorbed && !marked {
					want = nil
					v, err := plist.Read("<plist>" + c.now + "</plist>")
					if err != nil {
						t.Fatal(err)
					}
					baseline = plist.XML(v)
				}
				if got := m.items(list); !slices.Equal(got, want) {
					t.Fatalf("%s, marked %v: items = %q, want %q", c.name, marked, got, want)
				}
				rec := m.record()
				if exact := marked || c.absorbed; rec.Baseline["NSGlobalDomain:K"] != baseline || rec.Exact["NSGlobalDomain:K"] != exact {
					t.Fatalf("%s, marked %v: record = %+v; want the baseline %q, marked exact %v", c.name, marked, rec, baseline, exact)
				}
			}
		}
	}
}

// record is kit's record of the watched settings' baselines.
func (m *mac) record() (rec struct {
	Baseline map[string]string
	Exact    map[string]bool `json:"exact_baselines"`
},
) {
	m.t.Helper()
	data, err := os.ReadFile(filepath.Join(m.state, "settings.json"))
	if err == nil {
		err = json.Unmarshal(data, &rec)
	}
	if err != nil {
		m.t.Fatal(err)
	}
	return rec
}

// Each baseline is marked exact as kit finds it so, or takes it: one of a
// domain kit doesn't read in a pass, as one no longer declared, stays
// unmarked, so a value it took trimmed reads the same when the domain is
// read again, and is taken again then; one it takes now, of a domain it's
// never watched, is marked.
func TestEachBaselineIsMarkedExactOnItsOwn(t *testing.T) {
	m := newMac(t)
	record := `{"domains": {"NSGlobalDomain": true, "com.example.app": true}, "baseline": {"NSGlobalDomain:K": "<string>x</string>", "com.example.app:K": "<string>Dark</string>"}}`
	if err := os.WriteFile(filepath.Join(m.state, "settings.json"), []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}
	m.set("NSGlobalDomain", "<key>K</key><string>x</string>")
	m.set("com.apple.dock", "<key>tilesize</key><integer>48</integer>")
	if got := m.items(m.declare()); got != nil {
		t.Fatalf("items = %q, want none", got)
	}
	rec := m.record()
	if want := map[string]bool{"NSGlobalDomain:K": true, "com.apple.dock:tilesize": true}; !maps.Equal(rec.Exact, want) {
		t.Errorf("marked exact %v, want %v: the global domain's found the same, the Dock's taken, the app's not read", rec.Exact, want)
	}

	list := m.declare("com.example.app:Theme", "-string Dark")
	m.set("com.example.app", "<key>Theme</key><string>Dark</string><key>K</key><string>Dark </string>")
	if got := m.items(list); got != nil {
		t.Errorf("items = %q, want the app's value taken trimmed taken again", got)
	}
	if rec := m.record(); rec.Baseline["com.example.app:K"] != "<string>Dark </string>" || !rec.Exact["com.example.app:K"] {
		t.Errorf("record = %+v, want the app's value taken again, exactly, marked", rec)
	}
}

// Finder's own bookkeeping of the toolbar buttons sync apps add never shows
// as changed: only a setting a person changed does.
func TestFindersToolbarBookkeepingIsNoise(t *testing.T) {
	m := newMac(t)
	list := m.declare()
	if err := os.WriteFile(filepath.Join(m.state, "settings.json"), []byte(`{"domains": {"com.apple.finder": true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m.set("com.apple.finder", "<key>FXSyncExtensionToolbarItemsPendingRemove</key><array/><key>FXSyncExtensionToolbarItemsPendingAdd</key><array/><key>ShowPathbar</key><true/>")
	if got := m.items(list); !slices.Equal(got, []string{"com.apple.finder:ShowPathbar extra"}) {
		t.Errorf("items = %q; want Finder's bookkeeping left out, the setting found", got)
	}
}

// Control Center's microphone and camera item, which macOS shows while an
// app uses them, never shows as changed; the menu bar items a person chose
// do.
func TestTheMicrophoneAndCameraItemIsNoise(t *testing.T) {
	m := newMac(t)
	list := m.declare()
	if err := os.WriteFile(filepath.Join(m.state, "settings.json"), []byte(`{"domains": {"com.apple.controlcenter": true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m.set("com.apple.controlcenter", "<key>NSStatusItem VisibleCC AudioVideoModule</key><true/><key>NSStatusItem Visible Bluetooth</key><true/>")
	if got := m.items(list); !slices.Equal(got, []string{"com.apple.controlcenter:NSStatusItem Visible Bluetooth extra"}) {
		t.Errorf("items = %q; want the microphone's item left out, Bluetooth's found", got)
	}
}

// Status reads a defaults export as it's printed, laid out over lines, the
// same as before: a string as declared is as declared; and a string's
// spaces, which defaults write keeps, count.
func TestAnExportsStringsReadExactly(t *testing.T) {
	m := newMac(t)
	list := m.declare("com.apple.finder:FXPreferredViewStyle", "-string Nlsv", "com.apple.finder:Padded", "-string ' a '")
	m.set("com.apple.finder", "\n\t<key>FXPreferredViewStyle</key>\n\t<string>Nlsv</string>\n\t<key>Padded</key>\n\t<string> a </string>\n")
	if got := m.items(list); len(got) != 0 {
		t.Errorf("items = %q; want none, both as declared", got)
	}
	m.set("com.apple.finder", "\n\t<key>FXPreferredViewStyle</key>\n\t<string>Nlsv</string>\n\t<key>Padded</key>\n\t<string>a</string>\n")
	if got := m.items(list); len(got) != 1 || !strings.HasPrefix(got[0], "com.apple.finder:Padded") {
		t.Errorf("items = %q; want Padded alone set otherwise", got)
	}
}
