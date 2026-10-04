package testguard

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestConfigPlaces(t *testing.T) {
	home := t.TempDir()
	tests := []struct {
		name string
		home string
		env  map[string]string
		want []string
	}{
		{name: "the home alone", home: home, want: []string{"~/.config/kit"}},
		{
			name: "KIT_CONFIG and XDG_CONFIG_HOME too",
			home: home,
			env:  map[string]string{"KIT_CONFIG": "/elsewhere/kit-config", "XDG_CONFIG_HOME": "/elsewhere/config"},
			want: []string{"~/.config/kit", "/elsewhere/kit-config", "/elsewhere/config/kit"},
		},
		{
			name: "relative ones name nothing testguard can know",
			home: home,
			env:  map[string]string{"KIT_CONFIG": "kit-config", "XDG_CONFIG_HOME": "config"},
			want: []string{"~/.config/kit"},
		},
		{name: "no home", env: map[string]string{"KIT_CONFIG": "/elsewhere/kit-config"}, want: []string{"/elsewhere/kit-config"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, p := range configPlaces(tt.home, getenvOf(tt.env)) {
				got = append(got, p.shown)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("configPlaces() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStatePlaces(t *testing.T) {
	home := t.TempDir()
	got := shownOf(statePlaces(home, getenvOf(map[string]string{"XDG_STATE_HOME": "/elsewhere/state"})))
	if want := []string{"~/.local/state/kit", "/elsewhere/state/kit"}; !slices.Equal(got, want) {
		t.Errorf("statePlaces() = %q, want %q", got, want)
	}
	got = shownOf(statePlaces(home, getenvOf(map[string]string{"XDG_STATE_HOME": "state"})))
	if want := []string{"~/.local/state/kit"}; !slices.Equal(got, want) {
		t.Errorf("statePlaces() with a relative XDG_STATE_HOME = %q, want %q", got, want)
	}
}

func TestWatchRealSeesWhatTestsChange(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(home, configDir)
	state := filepath.Join(home, stateDir)
	writeFileIn(t, filepath.Join(config, "kit.toml"), "format = 1\n")
	writeFileIn(t, filepath.Join(config, ".git", "HEAD"), "ref: refs/heads/main\n")
	writeFileIn(t, filepath.Join(config, "brew"), "jq\n")
	writeFileIn(t, filepath.Join(state, machineFile), "laptop\n")

	ws := watchReal(home, getenvOf(nil))
	if got := ws.changes(); got != nil {
		t.Fatalf("before anything changed, changes() = %q, want none", got)
	}

	later := time.Now().Add(time.Hour)
	writeFileIn(t, filepath.Join(config, "kit.toml"), "format = 2\n")
	touch(t, filepath.Join(config, "kit.toml"), later)
	writeFileIn(t, filepath.Join(config, "cask"), "ghostty\n")
	if err := os.Remove(filepath.Join(config, "brew")); err != nil {
		t.Fatal(err)
	}
	writeFileIn(t, filepath.Join(config, ".git", "HEAD"), "ref: refs/heads/other\n")
	writeFileIn(t, filepath.Join(config, ".git", "FETCH_HEAD"), "abc\n")
	writeFileIn(t, filepath.Join(state, machineFile), "studio\n")
	touch(t, filepath.Join(state, machineFile), later)
	writeFileIn(t, filepath.Join(state, "drift.json"), "{}\n")
	writeFileIn(t, filepath.Join(home, logsDir, "run.jsonl"), "{}\n")

	want := []string{
		"the real ~/.config/kit/brew was removed",
		"the real ~/.config/kit/cask was created",
		"the real ~/.config/kit/kit.toml was modified",
		"the real ~/.local/state/kit/machine was modified",
		"the real ~/Library/Logs/kit appeared",
	}
	if got := ws.changes(); !slices.Equal(got, want) {
		t.Errorf("changes() = %q, want %q", got, want)
	}
}

func TestWatchRealLetsALiveKitWriteItsStateAndLogs(t *testing.T) {
	home := t.TempDir()
	writeFileIn(t, filepath.Join(home, stateDir, machineFile), "laptop\n")
	writeFileIn(t, filepath.Join(home, logsDir, "old.jsonl"), "{}\n")

	ws := watchReal(home, getenvOf(nil))
	writeFileIn(t, filepath.Join(home, stateDir, "drift.json"), "{}\n")
	writeFileIn(t, filepath.Join(home, logsDir, "new.jsonl"), "{}\n")

	if got := ws.changes(); got != nil {
		t.Errorf("changes() = %q, want none: a live kit writes its state and logs", got)
	}
}

func TestStateAppearingIsATests(t *testing.T) {
	home := t.TempDir()
	ws := watchReal(home, getenvOf(nil))
	writeFileIn(t, filepath.Join(home, stateDir, machineFile), "laptop\n")

	if got, want := ws.changes(), []string{"the real ~/.local/state/kit appeared"}; !slices.Equal(got, want) {
		t.Errorf("changes() = %q, want %q", got, want)
	}
}

func TestOutsideGit(t *testing.T) {
	tests := map[string]bool{
		"~/.config/kit":                true,
		"~/.config/kit/brew":           true,
		"~/.config/kit/.gitignore":     true,
		"~/.config/kit/.git":           false,
		"~/.config/kit/.git/HEAD":      false,
		"/elsewhere/kit/.git/refs/x":   false,
		"/elsewhere/kit/docs/.git-tip": true,
	}
	for name, want := range tests {
		if got := outsideGit(name); got != want {
			t.Errorf("outsideGit(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestDiff(t *testing.T) {
	before := snapshot{"a": {size: 1}, "b": {size: 1}, "c": {size: 1}}
	after := snapshot{"a": {size: 1}, "c": {size: 2}, "d": {size: 1}}
	want := []string{"the real b was removed", "the real c was modified", "the real d was created"}
	if got := diff(before, after); !slices.Equal(got, want) {
		t.Errorf("diff() = %q, want %q", got, want)
	}
}

func getenvOf(env map[string]string) func(string) string {
	return func(name string) string { return env[name] }
}

func shownOf(places []place) []string {
	var shown []string
	for _, p := range places {
		shown = append(shown, p.shown)
	}
	return shown
}

func writeFileIn(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// touch sets path's modification time to when, so a rewrite of the same size
// within the clock's resolution still shows.
func touch(t *testing.T, path string, when time.Time) {
	t.Helper()
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}
