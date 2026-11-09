package restore

import (
	"os"
	"path/filepath"
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
// under its own name, as Arq puts a folder in the one it's given; empty,
// missing, or with Finder's files alone, it holds nothing; anything else
// in it is an error saying where it should be.
func TestHolds(t *testing.T) {
	for _, tt := range []struct {
		name, path string
		files      []string
		held       bool
		err        string
	}{
		{"a folder restored", "~/Code", []string{"Code/app/main.go"}, true, ""},
		{"a file restored", "~/.zsh_history", []string{".zsh_history"}, true, ""},
		{"empty", "~/Code", nil, false, ""},
		{"Finder's files", "~/Code", []string{".DS_Store"}, false, ""},
		{"its contents alone", "~/Code", []string{"app/main.go", "site/index.php"}, false, "HOLD holds app, site, not Code: restore ~/Code itself into it, as Arq puts a folder in the one it's given"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			hold := t.TempDir()
			for _, f := range tt.files {
				write(t, filepath.Join(hold, f), time.Now())
			}
			held, err := Holds(hold, tt.path)
			if held != tt.held || tt.err == "" && err != nil || tt.err != "" && (err == nil || err.Error() != filepathed(tt.err, hold)) {
				t.Errorf("Holds() = %v, %v; want %v, %q", held, err, tt.held, tt.err)
			}
		})
	}
	if held, err := Holds(filepath.Join(t.TempDir(), "gone"), "~/Code"); held || err != nil {
		t.Errorf("for a holding folder that isn't there, Holds() = %v, %v; want nothing held", held, err)
	}
}

// Arq's restore opens as a source of its holding folders: each path where
// Arq put it in its folder; recorded as Arq's, in kit's state; never
// changing as it's copied; each path's holding folder going once it's
// restored. What's at each is what's there: a folder, a file, nothing for
// a holding folder still empty, and an error for one holding something
// else.
func TestArqOpens(t *testing.T) {
	state := t.TempDir()
	holds, err := Hold(state, []string{"~/Code", "~/.zsh_history", "~/Documents", "~/Music"})
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(holds["~/Code"], "Code", "main.go"), time.Now())
	write(t, filepath.Join(holds["~/.zsh_history"], ".zsh_history"), time.Now())
	write(t, filepath.Join(holds["~/Music"], "song.mp3"), time.Now())
	src, err := Arq{StateDir: state, Holds: holds}.Open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if src.Live || !src.Frees || src.Origin != (Origin{Kind: FromArq, Detail: ArqFolder(state)}) || src.Host != "" {
		t.Errorf("opened %+v; want Arq's, from kit's state, freeing its room, not live", src)
	}
	if got := src.Source("~/Code"); got != (Source{Path: filepath.Join(state, "restore", "arq", "1", "Code")}) {
		t.Errorf("~/Code is at %+v", got)
	}
	for path, want := range map[string]Kind{"~/Code": Folder, "~/.zsh_history": File, "~/Documents": Missing} {
		if got, err := src.Stat(path); err != nil || got.Kind != want {
			t.Errorf("Stat(%s) = %+v, %v; want %v", path, got, err, want)
		}
	}
	if _, err := src.Stat("~/Music"); err == nil {
		t.Error("Stat(~/Music), its holding folder holding a song alone, found no error")
	}
	if err := src.Close(t.Context()); err != nil {
		t.Errorf("Close() = %v", err)
	}
}

// filepathed is s, HOLD in it standing for hold.
func filepathed(s, hold string) string {
	return filepath.Clean(hold) + s[len("HOLD"):]
}
