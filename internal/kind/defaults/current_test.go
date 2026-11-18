package defaults_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind/defaults"
)

// snapshotMac is a made-up Mac whose watched domains, and their currentHost
// copies, are empty unless given.
func snapshotMac(t *testing.T) *mac {
	m := newMac(t)
	for _, domain := range defaults.Watched {
		m.set("currentHost:"+domain, "")
	}
	return m
}

// names are entries' names.
func names(entries []config.Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Name
	}
	return out
}

// placeholders are a value of each plist type, standing in for what the
// measured Mac had.
var placeholders = map[string]string{
	"bool": "<true/>", "integer": "<integer>1</integer>", "real": "<real>0.5</real>", "string": "<string>x</string>",
	"array": "<array/>", "dict": "<dict/>", "date": "<date>2026-01-01T00:00:00Z</date>", "data": "<data></data>",
}

// Every setting a Mac on macOS 26 had in the watched domains, judged a key
// at a time: a snapshot declares the person's choices, and leaves out
// kit's noise and macOS's own state.
func TestASnapshotKeepsChoicesAndLeavesMacOSsOwn(t *testing.T) {
	data, err := os.ReadFile("testdata/macos-26.tsv")
	if err != nil {
		t.Fatal(err)
	}
	bodies := map[string]string{}
	var kept []string
	for line := range strings.Lines(string(data)) {
		if strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(strings.TrimSuffix(line, "\n"), "\t")
		verdict, typ, domain, key := fields[0], fields[1], fields[2], fields[3]
		bodies[domain] += "<key>" + key + "</key>" + placeholders[typ]
		if verdict == "kept" {
			kept = append(kept, domain+":"+key)
		}
	}
	m := snapshotMac(t)
	for domain, body := range bodies {
		m.set(domain, body)
	}
	m.declare()
	got, err := m.d.Current(t.Context(), "26.7.1")
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(kept)
	if gotNames := names(got); !slices.Equal(gotNames, kept) {
		for _, name := range gotNames {
			if !slices.Contains(kept, name) {
				t.Errorf("declared %s, which is macOS's own", name)
			}
		}
		for _, name := range kept {
			if !slices.Contains(gotNames, name) {
				t.Errorf("left out %s, the person's choice", name)
			}
		}
	}
}

// Each setting a snapshot declares reads as adopting it declares it, a
// dict's entries declared one by one going by its entries; what's declared
// already, and a date as a value, are left out.
func TestASnapshotDeclaresAsAdoptingDoes(t *testing.T) {
	m := snapshotMac(t)
	m.set("com.apple.dock", "<key>tilesize</key><integer>60</integer><key>autohide</key><true/><key>mineffect</key><string>scale effect</string>")
	m.set("currentHost:NSGlobalDomain", "<key>com.apple.mouse.tapBehavior</key><integer>1</integer>")
	m.set("NSGlobalDomain", "<key>AppleSetUpWhen</key><date>2026-01-01T00:00:00Z</date>")
	m.set("com.apple.symbolichotkeys", "<key>AppleSymbolicHotKeys</key><dict><key>28</key><dict><key>enabled</key><false/></dict><key>29</key><dict><key>enabled</key><true/></dict></dict>")
	m.declare("com.apple.dock:autohide", "-bool true", "com.apple.symbolichotkeys:AppleSymbolicHotKeys:28", `-dict-add "<dict><key>enabled</key><false/></dict>"`)
	got, err := m.d.Current(t.Context(), "26.0")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		`com.apple.dock:mineffect -string "scale effect"`,
		"com.apple.dock:tilesize -int 60",
		`com.apple.symbolichotkeys:AppleSymbolicHotKeys:29 -dict-add "<dict><key>enabled</key><true/></dict>"`,
		"currentHost:NSGlobalDomain:com.apple.mouse.tapBehavior -int 1",
	}
	var lines []string
	for _, e := range got {
		lines = append(lines, e.Name+" "+e.Value)
	}
	if !slices.Equal(lines, want) {
		t.Errorf("Current() =\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	for _, e := range got {
		if adopted, err := m.d.Value(t.Context(), e.Name); err != nil || adopted != e.Value {
			t.Errorf("%s: Value() = %q, %v; the snapshot wrote %q", e.Name, adopted, err, e.Value)
		}
	}
}

// A Mac leaves out what the list of the newest macOS at or below its own
// says: a new macOS carries the last list forward, and one older than
// every list takes the oldest.
func TestTheNeverListIsTheNewestAtOrBelowTheMacsMacOS(t *testing.T) {
	defer defaults.SetNever(map[int]map[string][]string{
		15: {"*": {"OldState"}},
		26: {"com.apple.dock": {"NewState"}},
	})()
	for _, tt := range []struct{ version, kept string }{
		{"26.1", "OldState"},
		{"27.0", "OldState"},
		{"15.7.2", "NewState"},
		{"14.6", "NewState"},
	} {
		m := snapshotMac(t)
		m.set("com.apple.dock", "<key>OldState</key><true/><key>NewState</key><true/>")
		m.declare()
		got, err := m.d.Current(t.Context(), tt.version)
		if want := []string{"com.apple.dock:" + tt.kept}; err != nil || !slices.Equal(names(got), want) {
			t.Errorf("macOS %s: Current() = %q, %v; want %q", tt.version, names(got), err, want)
		}
	}
	if _, err := snapshotMac(t).d.Current(t.Context(), "unknown"); err == nil {
		t.Error("Current(unknown) = nil error, want macOS's version refused")
	}
	defer defaults.SetNever(nil)()
	m := snapshotMac(t)
	m.set("com.apple.dock", "<key>OldState</key><true/>")
	m.declare()
	if got, err := m.d.Current(t.Context(), "26.1"); err != nil || len(got) != 1 {
		t.Errorf("with no lists, Current() = %q, %v; want the setting, none left out", names(got), err)
	}
}

// A choice noise's patterns would catch, as the clock's date, shows when
// it changes; macOS's own beside it never does.
func TestChoicesNoiseWouldCatchShowUp(t *testing.T) {
	cases := []struct{ domain, choice, own string }{
		{"com.apple.menuextra.clock", "ShowDate", "LastHeartbeatDate"},
		{"NSGlobalDomain", "NSNavPanelExpandedStateForSaveMode2", "NSNavPanelFileListModeForOpenMode2"},
	}
	for _, c := range cases {
		m := newMac(t)
		list := m.declare()
		m.items(list)
		m.set(c.domain, "<key>"+c.choice+"</key><true/><key>"+c.own+"</key><true/>")
		if got, want := m.items(list), []string{c.domain + ":" + c.choice + " extra"}; !slices.Equal(got, want) {
			t.Errorf("items = %q, want %q", got, want)
		}
	}
}

// A choice kit watched as noise before, set then, isn't shown as changed
// the first time kit sees it as a choice: its value's taken quietly, and
// it shows once it changes.
func TestAChoiceOnceNoiseIsTakenQuietly(t *testing.T) {
	m := newMac(t)
	list := m.declare()
	if err := os.WriteFile(filepath.Join(m.state, "settings.json"), []byte(`{"domains": {"com.apple.menuextra.clock": true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m.set("com.apple.menuextra.clock", "<key>ShowDate</key><integer>1</integer>")
	if got := m.items(list); len(got) != 0 {
		t.Errorf("items = %q; want none, its value taken quietly", got)
	}
	m.set("com.apple.menuextra.clock", "<key>ShowDate</key><integer>2</integer>")
	if got, want := m.items(list), []string{"com.apple.menuextra.clock:ShowDate extra"}; !slices.Equal(got, want) {
		t.Errorf("items = %q, want %q", got, want)
	}
}

// A dict going by its entries leaves out an entry whose key holds a colon,
// which no setting's name can say, as a name's last colon starts its
// entry's key: written, its line would set another key.
func TestAnEntryWhoseKeyHoldsAColonIsLeftOut(t *testing.T) {
	m := snapshotMac(t)
	m.set("com.apple.symbolichotkeys", "<key>AppleSymbolicHotKeys</key><dict><key>28</key><true/><key>29</key><true/><key>a:b</key><true/></dict>")
	m.declare("com.apple.symbolichotkeys:AppleSymbolicHotKeys:28", `-dict-add "<true/>"`)
	got, err := m.d.Current(t.Context(), "26.0")
	if want := []string{"com.apple.symbolichotkeys:AppleSymbolicHotKeys:29"}; err != nil || !slices.Equal(names(got), want) {
		t.Errorf("Current() = %q, %v; want %q", names(got), err, want)
	}
}

// A date anywhere in a value marks it macOS's own, as a date at the top
// does.
func TestADateInADictIsNeverDeclared(t *testing.T) {
	m := snapshotMac(t)
	m.set("com.apple.dock", "<key>tilesize</key><integer>60</integer><key>shown</key><dict><key>at</key><date>2026-01-01T00:00:00Z</date></dict>")
	m.declare()
	got, err := m.d.Current(t.Context(), "26.0")
	if want := []string{"com.apple.dock:tilesize"}; err != nil || !slices.Equal(names(got), want) {
		t.Errorf("Current() = %q, %v; want %q", names(got), err, want)
	}
}
