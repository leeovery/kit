package config_test

import (
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/config"
)

const settings = `[macos settings]
# Dock
com.apple.dock tilesize -int 60   # icons
-currentHost NSGlobalDomain com.apple.mouse.tapBehavior -int 1
com.apple.controlcenter "NSStatusItem Visible WiFi" -bool true
com.apple.symbolichotkeys AppleSymbolicHotKeys -dict-add 60 '<dict><key>enabled</key><false/></dict>'
com.apple.dock persistent-others '<array/>'
`

func TestSettingsRead(t *testing.T) {
	cfg := loadRepo(t, map[string]string{"shared/declarations": settings})
	list, err := cfg.List("defaults", "laptop")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range list.Entries {
		got = append(got, e.Name+" | "+e.Value+" | "+e.Note)
	}
	want := []string{
		"com.apple.dock:tilesize | -int 60 | icons",
		"currentHost:NSGlobalDomain:com.apple.mouse.tapBehavior | -int 1 | ",
		"com.apple.controlcenter:NSStatusItem Visible WiFi | -bool true | ",
		`com.apple.symbolichotkeys:AppleSymbolicHotKeys:60 | -dict-add "<dict><key>enabled</key><false/></dict>" | `,
		`com.apple.dock:persistent-others | "<array/>" | `,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("entries =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestSettingsRefuseWhatDoesntRead(t *testing.T) {
	for _, line := range []string{
		"com.apple.dock tilesize",
		"com.apple.dock tilesize -int",
		"com.apple.dock tilesize -int 1 2",
		"com.apple.dock tilesize 60",
		"com.apple.symbolichotkeys AppleSymbolicHotKeys -dict-add 60",
	} {
		cfg := loadRepo(t, map[string]string{"shared/declarations": "[macos settings]\n" + line + "\n"})
		if _, err := cfg.List("defaults", "laptop"); err == nil || !strings.Contains(err.Error(), "shared/declarations:2:") {
			t.Errorf("%q: error = %v", line, err)
		}
	}
}

func TestDeclareASetting(t *testing.T) {
	cfg := loadRepo(t, map[string]string{"laptop/declarations": "[macos settings]\n# Dock\ncom.apple.dock tilesize -int 60\n"})
	for _, e := range []config.Entry{
		{Name: "com.apple.dock:autohide", Value: "-bool true"},
		{Name: "currentHost:NSGlobalDomain:com.apple.mouse.tapBehavior", Value: "-int 1", Note: "tap to click"},
		{Name: "com.apple.symbolichotkeys:AppleSymbolicHotKeys:61", Value: `-dict-add "<dict><key>enabled</key><true/></dict>"`},
		{Name: "com.apple.controlcenter:NSStatusItem Visible WiFi", Value: "-bool true"},
	} {
		if err := cfg.Declare("defaults", "laptop", e, "Dock"); err != nil {
			t.Fatalf("Declare(%s) = %v", e.Name, err)
		}
	}
	got := readFile(t, cfg, "laptop/declarations")
	for _, want := range []string{
		"com.apple.dock autohide -bool true\n",
		"-currentHost NSGlobalDomain com.apple.mouse.tapBehavior -int 1   # tap to click\n",
		`com.apple.symbolichotkeys AppleSymbolicHotKeys -dict-add 61 "<dict><key>enabled</key><true/></dict>"` + "\n",
		`com.apple.controlcenter "NSStatusItem Visible WiFi" -bool true` + "\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("laptop =\n%s\nwant a line %q", got, want)
		}
	}
	if _, err := cfg.List("defaults", "laptop"); err != nil {
		t.Errorf("the file doesn't read back: %v", err)
	}
}
