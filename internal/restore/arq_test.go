package restore

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// Each path restored through Arq gets a holding folder, numbered, made in
// kit's state and recorded: one that has one keeps it, even listed in
// another order; the rest the lowest numbers no other path holds, so a
// number freed is used again.
func TestHoldNumbersEachPathsHoldingFolder(t *testing.T) {
	state := t.TempDir()
	hold := func(paths ...string) map[string]string {
		t.Helper()
		holds, err := Hold(state, paths)
		if err != nil {
			t.Fatal(err)
		}
		return holds
	}
	first := hold("~/Code", "~/.claude")
	if first["~/Code"] != Holding(state, 1) || first["~/.claude"] != Holding(state, 2) {
		t.Errorf("held %v; want ~/Code in 1, ~/.claude in 2", first)
	}
	for _, dir := range first {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Errorf("%s isn't made: %v", dir, err)
		}
	}
	if again := hold("~/Documents", "~/.claude"); again["~/.claude"] != Holding(state, 2) || again["~/Documents"] != Holding(state, 3) {
		t.Errorf("held again %v; want ~/.claude kept in 2, ~/Documents in 3", again)
	}
	if err := UpdateRecord(state, func(r *Record) { r.Change("~/Code", func(p *Path) { p.Holding = 0 }) }); err != nil {
		t.Fatal(err)
	}
	if freed := hold("~/Music"); freed["~/Music"] != Holding(state, 1) {
		t.Errorf("held %v; want ~/Music in 1, freed", freed)
	}
	rec, err := LoadRecord(state)
	if err != nil {
		t.Fatal(err)
	}
	for path, n := range map[string]int{"~/Music": 1, "~/.claude": 2, "~/Documents": 3} {
		if got := rec.Paths[path].Holding; got != n {
			t.Errorf("%s's holding folder is recorded as %d, want %d", path, got, n)
		}
	}
}

// A holding folder holds Arq's restore of its path once the path's in it
// under its own name, as Arq puts a folder in the one it's given; else it
// says what it holds instead, Finder's files aside: nothing, empty or
// missing; another's path; or a folder's contents restored without it.
func TestHolds(t *testing.T) {
	for _, tt := range []struct {
		name, path string
		files      []string
		held       bool
		instead    []string
	}{
		{"a folder restored", "~/Code", []string{"Code/app/main.go"}, true, nil},
		{"a file restored", "~/.zsh_history", []string{".zsh_history"}, true, nil},
		{"empty", "~/Code", nil, false, nil},
		{"Finder's files", "~/Code", []string{".DS_Store"}, false, nil},
		{"another path's", "~/Code", []string{".claude/history.jsonl"}, false, []string{".claude"}},
		{"its contents alone", "~/Code", []string{"app/main.go", "site/index.php"}, false, []string{"app", "site"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			hold := t.TempDir()
			for _, f := range tt.files {
				write(t, filepath.Join(hold, f), time.Now())
			}
			held, instead, err := Holds(hold, tt.path)
			if err != nil || held != tt.held || !slices.Equal(instead, tt.instead) {
				t.Errorf("Holds() = %v, %q, %v; want %v, %q", held, instead, err, tt.held, tt.instead)
			}
		})
	}
	if held, instead, err := Holds(filepath.Join(t.TempDir(), "gone"), "~/Code"); held || instead != nil || err != nil {
		t.Errorf("for a holding folder that isn't there, Holds() = %v, %q, %v; want nothing held", held, instead, err)
	}
}

// Arq's restore opens as a source of its holding folders: each path where
// Arq put it in its folder; recorded as Arq's, in kit's state, said from ~;
// never changing as it's copied; each path's holding folder going once
// it's restored. What's at each is what's there, a folder or a file; a
// holding folder still empty, or holding something else, is an error
// saying so, never a path its source hasn't, as Arq may still be restoring
// it; only a path the person says Arq's backup hasn't is nothing there.
func TestArqOpens(t *testing.T) {
	home := t.TempDir()
	state := filepath.Join(home, ".local", "state", "kit")
	holds, err := Hold(state, []string{"~/Code", "~/.zsh_history", "~/Documents", "~/Music"})
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(holds["~/Code"], "Code", "main.go"), time.Now())
	write(t, filepath.Join(holds["~/.zsh_history"], ".zsh_history"), time.Now())
	write(t, filepath.Join(holds["~/Music"], "song.mp3"), time.Now())
	src, err := Arq{StateDir: state, Home: home, Holds: holds, Lacks: []string{"~/Gone"}}.Open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if src.Live || !src.Frees || src.Origin != (Origin{Kind: FromArq, Detail: "~/.local/state/kit/restore/arq"}) || src.Host != "" {
		t.Errorf("opened %+v; want Arq's, from kit's state, from ~, freeing its room, not live", src)
	}
	if got := src.Source("~/Code"); got != (Source{Path: filepath.Join(state, "restore", "arq", "1", "Code")}) {
		t.Errorf("~/Code is at %+v", got)
	}
	for path, want := range map[string]Kind{"~/Code": Folder, "~/.zsh_history": File, "~/Gone": Missing} {
		if got, err := src.Stat(path); err != nil || got.Kind != want {
			t.Errorf("Stat(%s) = %+v, %v; want %v", path, got, err, want)
		}
	}
	for path, want := range map[string]string{
		"~/Documents": holds["~/Documents"] + " holds nothing: Arq's restore of ~/Documents isn't in it",
		"~/Music":     holds["~/Music"] + " holds song.mp3, not Music: restore ~/Music itself into it, as Arq puts a folder in the one it's given",
	} {
		if got, err := src.Stat(path); err == nil || err.Error() != want {
			t.Errorf("Stat(%s) = %+v, %v; want %q", path, got, err, want)
		}
	}
	if err := src.Close(t.Context()); err != nil {
		t.Errorf("Close() = %v", err)
	}
}
