package cli_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var kitToml = filepath.Join(".config", "kit", "kit.toml")

// withOwn is kit.toml, two Macs, with settings of its own after primary,
// and a comment of its own that every edit leaves as it is.
func withOwn(settings string) string {
	return strings.Replace(twoMacs, "primary = \"laptop\"\n", "# Hears of every Mac.\nprimary = \"laptop\"\n"+settings, 1)
}

// kit terminal and kit nightly at set kit.toml's own settings in place, the
// rest of it as it was, and commit kit.toml alone, and push; a setting as
// it is already is left, and nothing's committed.
func TestOwnSettingsSet(t *testing.T) {
	tests := []struct {
		name     string
		settings string
		args     []string
		want     string
		commit   string
		after    string
	}{
		{"a terminal named", "", []string{"terminal", "ghostty"},
			"terminal ok set to ghostty\n", "kit terminal ghostty (laptop)", "terminal = \"ghostty\"\n"},
		{"the terminal taken out", "terminal = \"ghostty\" # the boot's\n", []string{"terminal", "--none"},
			"terminal ok taken out of kit.toml\n", "kit terminal --none (laptop)", ""},
		{"the terminal named already", "terminal = \"ghostty\"\n", []string{"terminal", "ghostty"},
			"terminal ok ghostty already\n", "", "terminal = \"ghostty\"\n"},
		{"no terminal already", "", []string{"terminal", "--none"},
			"terminal ok none already\n", "", ""},
		{"the nightly run's time set", "", []string{"nightly", "at", "4:30"},
			"nightly_at ok set to 04:30\n", "kit nightly at 04:30 (laptop)", "nightly_at = \"04:30\"\n"},
		{"the nightly run's time moved, its comment kept", "nightly_at = \"03:00\"  # after the backups\n", []string{"nightly", "at", "01:15"},
			"nightly_at ok set to 01:15\n", "kit nightly at 01:15 (laptop)", "nightly_at = \"01:15\"  # after the backups\n"},
		{"the nightly run's time set already", "nightly_at = \"04:30\"\n", []string{"nightly", "at", "04:30"},
			"nightly_at ok 04:30 already\n", "", "nightly_at = \"04:30\"\n"},
		{"the default, set in kit.toml", "", []string{"nightly", "at", "03:00"},
			"nightly_at ok set to 03:00\n", "kit nightly at 03:00 (laptop)", "nightly_at = \"03:00\"\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := laptopWorld(t)
			w.write(t, kitToml, withOwn(tt.settings))
			synced := "kit-config ok nothing changed\n"
			if tt.commit != "" {
				w.expectSync(t, []string{"kit.toml"}, tt.commit)
				synced = "kit-config ok committed and pushed kit.toml\n"
			}
			out, errOut, code := w.run(t, tt.args...)
			if code != 0 || !strings.Contains(out, tt.want+synced) {
				t.Errorf("kit %v printed\n%s%s exit %d; want\n%s%s", tt.args, out, errOut, code, tt.want, synced)
			}
			if got, want := w.read(t, kitToml), withOwn(tt.after); got != want {
				t.Errorf("kit.toml =\n%s\nwant\n%s", got, want)
			}
			if tt.commit == "" {
				for _, cmd := range w.fake.Commands() {
					if slices.Contains(cmd.Args, "commit") {
						t.Errorf("ran %s, want nothing committed", cmd)
					}
				}
			}
		})
	}
}

// Setting one is a run as any other is: plain lines with --plain, at a
// terminal too, and one document with --json.
func TestOwnSettingSetPlainAndJSON(t *testing.T) {
	tests := []struct {
		flag string
		want []string
	}{
		{"--plain", []string{"terminal ok set to ghostty\n", "kit-config ok committed and pushed kit.toml\n"}},
		{"--json", []string{`{"schema":1,"kit":"0.1.0","machine":"laptop","attention":false,"steps":[` +
			`{"id":"terminal","title":"terminal","state":"ok","summary":"set to ghostty"},` +
			`{"id":"kit-config","title":"kit-config","state":"ok","summary":"committed and pushed kit.toml"}]}`}},
	}
	for _, tt := range tests {
		w := laptopWorld(t)
		w.terminal = true
		w.expectSync(t, []string{"kit.toml"}, "kit terminal ghostty (laptop)")
		out, errOut, code := w.run(t, "terminal", "ghostty", tt.flag)
		if tt.flag == "--json" {
			out = compact(t, out)
		}
		for _, want := range tt.want {
			if code != 0 || !strings.Contains(out, want) {
				t.Errorf("kit terminal ghostty %s printed\n%s%s exit %d; want %s", tt.flag, out, errOut, code, want)
			}
		}
		want := strings.Replace(twoMacs, "primary = \"laptop\"\n", "primary = \"laptop\"\nterminal = \"ghostty\"\n", 1)
		if got := w.read(t, kitToml); got != want {
			t.Errorf("kit terminal ghostty %s: kit.toml =\n%s", tt.flag, got)
		}
	}
}

// Without a value, each shows the setting: the terminal kit.toml names, or
// none, and the boot runs on in Terminal; when the nightly run falls due,
// saying when it's the default. --json gives each as a document.
func TestOwnSettingsShown(t *testing.T) {
	tests := []struct {
		settings string
		args     []string
		want     string
	}{
		{"terminal = \"ghostty\"\n", []string{"terminal"}, "a new Mac's boot hands over to ghostty\n"},
		{"", []string{"terminal"}, "kit.toml names no terminal: a new Mac's boot runs on in Terminal\n"},
		{"nightly_at = \"04:30\"\n", []string{"nightly", "at"}, "the nightly run falls due at 04:30\n"},
		{"", []string{"nightly", "at"}, "the nightly run falls due at 03:00, the default: kit.toml sets no nightly_at\n"},
		{"terminal = \"ghostty\"\n", []string{"terminal", "--json"}, `{"schema":1,"terminal":"ghostty"}`},
		{"", []string{"terminal", "--json"}, `{"schema":1,"terminal":""}`},
		{"nightly_at = \"4:30\"\n", []string{"nightly", "at", "--json"}, `{"schema":1,"nightly_at":"04:30","default":false}`},
		{"", []string{"nightly", "at", "--json"}, `{"schema":1,"nightly_at":"03:00","default":true}`},
	}
	for _, tt := range tests {
		w := laptopWorld(t)
		w.write(t, kitToml, withOwn(tt.settings))
		out, errOut, code := w.run(t, tt.args...)
		if slices.Contains(tt.args, "--json") {
			out = compact(t, out)
		}
		if out != tt.want || errOut != "" || code != 0 {
			t.Errorf("kit %v with %q printed %q, %q, exit %d; want %q", tt.args, tt.settings, out, errOut, code, tt.want)
		}
		if len(w.fake.Commands()) > 0 {
			t.Errorf("kit %v ran %v, want nothing run", tt.args, w.fake.Commands())
		}
	}
}

// A terminal kit doesn't know, a time that isn't one, or a terminal and
// --none at once, is refused, kit.toml left as it was.
func TestOwnSettingsRefused(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"terminal", "other"}, "kit: kit doesn't know the terminal other: one of ghostty\n"},
		{[]string{"nightly", "at", "25:00"}, "kit: \"25:00\" isn't a time of day, as in 03:00\n"},
		{[]string{"nightly", "at", "3am"}, "kit: \"3am\" isn't a time of day, as in 03:00\n"},
		{[]string{"terminal", "ghostty", "--none"}, "kit: name a terminal, or give --none, not both\n"},
		{[]string{"terminal", ""}, "kit: name a terminal, or give --none\n"},
	}
	for _, tt := range tests {
		w := laptopWorld(t)
		out, errOut, code := w.run(t, tt.args...)
		if out != "" || !strings.HasPrefix(errOut, tt.want) || code != 2 {
			t.Errorf("kit %v printed %q, %q, exit %d; want %q, exit 2", tt.args, out, errOut, code, tt.want)
		}
		if got := w.read(t, kitToml); got != twoMacs {
			t.Errorf("kit %v left kit.toml\n%s", tt.args, got)
		}
		for _, cmd := range w.fake.Commands() {
			if slices.Contains(cmd.Args, "commit") {
				t.Errorf("kit %v ran %s, want nothing committed", tt.args, cmd)
			}
		}
	}
}
