package restore

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// boot is when a test Mac's boot ran; earlier and later, when files were
// written before it and since. The judgement reads no file's time: they're
// there to show none counts.
var (
	boot    = time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	earlier = boot.Add(-24 * time.Hour)
	later   = boot.Add(time.Hour)
)

// zshHistory is a line restoring a file.
var zshHistory = Line{Path: "~/.zsh_history", Pos: "laptop/declarations:5"}

// firstRun is a test Mac's restore's first run, listing its paths.
func firstRun(paths ...string) Record {
	return Record{First: Run{Time: boot.Add(time.Hour), Paths: paths}}
}

// A path is judged by its record first: done, here, or under way, whatever
// it holds. Else, with no record, by what it holds, kit's aside: nothing,
// to restore; anything, on a Mac the boot set up, to set aside, then
// restore, however old, as a shell's history a Terminal wrote before the
// boot; on a Mac in use, here.
func TestJudge(t *testing.T) {
	kits := map[string]time.Time{
		"CLAUDE.md": earlier, "skills/kit/SKILL.md": earlier, "skills/review/SKILL.md": later,
		"settings.json": later, "projects/site/memory/notes.md": earlier, "teams/a/inbox/m.json": later,
		"statsig/x": earlier, "cache/a": earlier, "plugins/one/x": earlier, "logs/a/b.log": earlier,
		".DS_Store": earlier, "projects/.DS_Store": later,
	}
	with := func(extra map[string]time.Time) map[string]time.Time {
		files := map[string]time.Time{}
		for _, m := range []map[string]time.Time{kits, extra} {
			for rel, when := range m {
				files["~/.claude/"+rel] = when
			}
		}
		return files
	}
	for _, tt := range []struct {
		name     string
		line     Line
		files    map[string]time.Time
		recorded State
		setUp    bool
		want     Verdict
	}{
		{"recorded done, holding files", claude, with(map[string]time.Time{"history.jsonl": earlier}), Done, true, VerdictDone},
		{"recorded here, since emptied", claude, nil, Here, true, VerdictHere},
		{"copying", claude, with(map[string]time.Time{"history.jsonl": earlier}), Copying, true, VerdictResume},
		{"checking", claude, with(map[string]time.Time{"history.jsonl": earlier}), Checking, true, VerdictResume},
		{"absent", claude, nil, "", true, VerdictRestore},
		{"folders alone", claude, map[string]time.Time{}, "", true, VerdictRestore},
		{"nothing but kit's, old and new", claude, with(nil), "", true, VerdictRestore},
		{"kit's, and files written since the boot", claude, with(map[string]time.Time{"history.jsonl": later, "projects/site/a.jsonl": later}), "", true, VerdictSetAside},
		{"kit's, and files older than the boot", claude, with(map[string]time.Time{"history.jsonl": later, "projects/site/a.jsonl": earlier}), "", true, VerdictSetAside},
		{"a Mac in use", claude, with(map[string]time.Time{"history.jsonl": later}), "", false, VerdictHere},
		{"a Mac in use, nothing but kit's", claude, with(nil), "", false, VerdictRestore},
		{"a file written since the boot", zshHistory, map[string]time.Time{"~/.zsh_history": later}, "", true, VerdictSetAside},
		{"a file older than the boot, a shell's history from a Terminal before it", zshHistory, map[string]time.Time{"~/.zsh_history": earlier}, "", true, VerdictSetAside},
		{"a file on a Mac in use", zshHistory, map[string]time.Time{"~/.zsh_history": later}, "", false, VerdictHere},
		{"an absent file", zshHistory, nil, "", true, VerdictRestore},
		{"a file, recorded done", zshHistory, map[string]time.Time{"~/.zsh_history": later}, Done, true, VerdictDone},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			if tt.files != nil {
				if err := os.MkdirAll(filepath.Join(home, ".claude", "projects", "empty"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for path, when := range tt.files {
				write(t, filepath.Join(home, path[2:]), when)
			}
			f, err := NewFilter(tt.line, owned)
			if err != nil {
				t.Fatal(err)
			}
			rec := firstRun(claude.Path, zshHistory.Path)
			if tt.recorded != "" {
				rec.Change(tt.line.Path, func(p *Path) { p.State = tt.recorded })
			}
			got, err := Judge(home, f, rec, tt.setUp)
			want := Judgement{Path: tt.line.Path, Verdict: tt.want}
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Errorf("Judge() = %+v, %v; want %+v", got, err, want)
			}
		})
	}
}

// Only a path the restore's first run listed is ever set aside, and only
// till its copy starts: a line added after the first run, holding anything
// that isn't kit's, however new, is here, as on a Mac in use; one holding
// nothing but kit's, or absent, is the caller's to restore or not. A path
// whose holding folder of Arq's was made since the first run, nothing
// copied, is judged again on each run; one whose copy a run before began
// resumes, what's in it the copy's, never set aside. With no first run
// recorded, nothing's set aside.
func TestJudgeSetsAsideOnlyWhatTheFirstRunListed(t *testing.T) {
	code := Line{Path: "~/Code", Pos: "laptop/declarations:4"}
	tool := Line{Path: "~/.local/state/newtool", Pos: "laptop/declarations:9"}
	month := boot.Add(30 * 24 * time.Hour)
	holding := func(p *Path) { p.Holding = 1 }
	begun := func(p *Path) { p.Holding, p.Started = 1, boot.Add(2*time.Hour) }
	for _, tt := range []struct {
		name   string
		line   Line
		files  map[string]time.Time
		first  bool
		record func(*Path)
		want   Verdict
	}{
		{"a line added a month after the boot, its tool's fresh data", tool, map[string]time.Time{"~/.local/state/newtool/state.db": month}, true, nil, VerdictHere},
		{"a line added since, nothing but Finder's", tool, map[string]time.Time{"~/.local/state/newtool/.DS_Store": month}, true, nil, VerdictRestore},
		{"a line added since, absent", tool, nil, true, nil, VerdictRestore},
		{"its holding folder made, holding files", code, map[string]time.Time{"~/Code/site/notes.md": earlier, "~/Code/tool/main.go": month}, true, holding, VerdictSetAside},
		{"its holding folder made, nothing there", code, nil, true, holding, VerdictRestore},
		{"its copy begun by a run before", code, map[string]time.Time{"~/Code/site/notes.md": earlier}, true, begun, VerdictResume},
		{"no first run recorded, holding files", code, map[string]time.Time{"~/Code/site/notes.md": later}, false, nil, VerdictHere},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			for path, when := range tt.files {
				write(t, filepath.Join(home, path[2:]), when)
			}
			var rec Record
			if tt.first {
				rec = firstRun(code.Path)
			}
			if tt.record != nil {
				rec.Change(tt.line.Path, tt.record)
			}
			f, err := NewFilter(tt.line, owned)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Judge(home, f, rec, true)
			if want := (Judgement{Path: tt.line.Path, Verdict: tt.want}); err != nil || !reflect.DeepEqual(got, want) {
				t.Errorf("Judge() = %+v, %v; want %+v", got, err, want)
			}
		})
	}
}

// Set-up's window closes for good once the restore first settles: a path
// the first run listed, holding anything that isn't kit's, is here then,
// never set aside, as on a Mac in use; one holding nothing but kit's, or
// absent, is the caller's to restore or not. Till the restore settles, the
// window stays open, however long after the boot.
func TestJudgeOnceTheRestoreHasSettled(t *testing.T) {
	tool := Line{Path: "~/.local/share/tool", Pos: "laptop/declarations:8"}
	for _, tt := range []struct {
		name    string
		files   map[string]time.Time
		settled bool
		want    Verdict
	}{
		{"a tool's month-old data, listed after the restore settled", map[string]time.Time{"ledger.db": boot.Add(48 * time.Hour), "logs/today.log": boot.Add(30 * 24 * time.Hour)}, true, VerdictHere},
		{"the same, the restore not settled yet", map[string]time.Time{"ledger.db": boot.Add(48 * time.Hour), "logs/today.log": boot.Add(30 * 24 * time.Hour)}, false, VerdictSetAside},
		{"nothing but Finder's, after the restore settled", map[string]time.Time{".DS_Store": boot.Add(time.Hour)}, true, VerdictRestore},
		{"absent, after the restore settled", nil, true, VerdictRestore},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home, stateDir := t.TempDir(), t.TempDir()
			for rel, when := range tt.files {
				write(t, filepath.Join(home, ".local/share/tool", rel), when)
			}
			if err := RecordFirstRun(stateDir, boot.Add(time.Hour), []string{tool.Path}); err != nil {
				t.Fatal(err)
			}
			if tt.settled {
				for _, at := range []time.Time{boot.Add(24 * time.Hour), boot.Add(25 * time.Hour)} {
					if err := Settle(stateDir, at); err != nil {
						t.Fatal(err)
					}
				}
			}
			rec, err := LoadRecord(stateDir)
			if err != nil {
				t.Fatal(err)
			}
			if want := map[bool]time.Time{true: boot.Add(24 * time.Hour)}[tt.settled]; !rec.Settled.Equal(want) {
				t.Errorf("settled at %v; want %v, the first", rec.Settled, want)
			}
			f, err := NewFilter(tool, owned)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Judge(home, f, rec, true)
			if want := (Judgement{Path: tool.Path, Verdict: tt.want}); err != nil || !reflect.DeepEqual(got, want) {
				t.Errorf("Judge() = %+v, %v; want %+v", got, err, want)
			}
		})
	}
}

// A path its source hadn't has set-up's window shut to it, the restore
// settled or not: written there since, as by a tool run weeks later, it's
// here, never set aside; absent, or holding nothing but kit's, it's the
// caller's to restore or not.
func TestJudgeAPathItsSourceHadnt(t *testing.T) {
	tool := Line{Path: "~/.local/share/tool", Pos: "laptop/declarations:8"}
	for _, tt := range []struct {
		name  string
		files map[string]time.Time
		want  Verdict
	}{
		{"written since", map[string]time.Time{"ledger.db": later.Add(30 * 24 * time.Hour)}, VerdictHere},
		{"nothing but Finder's", map[string]time.Time{".DS_Store": later}, VerdictRestore},
		{"absent", nil, VerdictRestore},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			for rel, when := range tt.files {
				write(t, filepath.Join(home, ".local/share/tool", rel), when)
			}
			rec := firstRun(tool.Path)
			rec.Change(tool.Path, func(p *Path) {
				p.State, p.From = NotThere, Origin{Kind: FromTimeMachine, Detail: "com.apple.TimeMachine.2026-11-19-030000.backup"}
			})
			f, err := NewFilter(tool, owned)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Judge(home, f, rec, true)
			if want := (Judgement{Path: tool.Path, Verdict: tt.want}); err != nil || !reflect.DeepEqual(got, want) {
				t.Errorf("Judge() = %+v, %v; want %+v", got, err, want)
			}
		})
	}
}

// A path that can't be read can't be judged: the judgement fails, saying
// which.
func TestJudgeFailsOnWhatItCantRead(t *testing.T) {
	home := t.TempDir()
	locked := filepath.Join(home, ".claude", "locked")
	write(t, filepath.Join(locked, "a"), later)
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	f, err := NewFilter(claude, owned)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Judge(home, f, Record{}, true); err == nil || !strings.HasPrefix(err.Error(), "read ~/.claude: ") || !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Judge() = %v; want it failing to read ~/.claude", err)
	}
}

// Judging looks no further than the first file that isn't kit's, as the
// check judges every path each run: a folder beyond it that can't be read
// is never read, but setting aside, which moves them all, reads it.
func TestJudgeLooksNoFurtherThanTheFirstFile(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, ".claude", "a"), later)
	locked := filepath.Join(home, ".claude", "b")
	write(t, filepath.Join(locked, "c"), later)
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	f, err := NewFilter(claude, owned)
	if err != nil {
		t.Fatal(err)
	}
	if j, err := Judge(home, f, firstRun(claude.Path), true); err != nil || j.Verdict != VerdictSetAside {
		t.Errorf("Judge() = %+v, %v; want it set aside, nothing past ~/.claude/a read", j, err)
	}
	if _, err := SetAside(t.TempDir(), home, f, later); err == nil || !errors.Is(err, fs.ErrPermission) {
		t.Errorf("SetAside() = %v; want it failing to read ~/.claude/b", err)
	}
}

// Setting aside moves all a path holds into kit's state, however old, each
// file keeping its path from the home folder, kit's own left in place; the
// record says what went where, as setting aside returns it. Then the path
// holds nothing but kit's, so it's restored.
func TestSetAside(t *testing.T) {
	home, stateDir := t.TempDir(), t.TempDir()
	for path, when := range map[string]time.Time{".claude/CLAUDE.md": earlier, ".claude/history.jsonl": earlier, ".claude/todos/a.json": later} {
		write(t, filepath.Join(home, path), when)
	}
	f, err := NewFilter(claude, owned)
	if err != nil {
		t.Fatal(err)
	}
	if j, err := Judge(home, f, firstRun(claude.Path), true); err != nil || j.Verdict != VerdictSetAside {
		t.Fatalf("Judge() = %+v, %v", j, err)
	}
	now := boot.Add(2 * time.Hour)
	aside, err := SetAside(stateDir, home, f, now)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(stateDir, "restore", "set-aside", "20261120-110000")
	for _, rel := range []string{".claude/history.jsonl", ".claude/todos/a.json"} {
		if got := read(t, filepath.Join(dir, rel)); got != filepath.Join(home, rel) {
			t.Errorf("set aside %s holds %q", rel, got)
		}
		if _, err := os.Lstat(filepath.Join(home, rel)); !os.IsNotExist(err) {
			t.Errorf("%s is still in place: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "CLAUDE.md")); err != nil {
		t.Errorf("kit's own file was moved: %v", err)
	}
	rec, err := LoadRecord(stateDir)
	want := []Aside{{Time: now, Dir: dir, Paths: []string{"~/.claude/history.jsonl", "~/.claude/todos/a.json"}}}
	if err != nil || !reflect.DeepEqual(rec.Paths["~/.claude"].SetAside, want) || !reflect.DeepEqual(aside, want[0]) {
		t.Errorf("record = %+v, %v, returned %+v; want set aside %+v", rec, err, aside, want)
	}
	if j, err := Judge(home, f, rec, true); err != nil || j.Verdict != VerdictRestore {
		t.Errorf("Judge() after = %+v, %v; want it restored", j, err)
	}
}

// Two set-asides in one second, as of two small paths copied one after the
// other, each go into a folder of their own: neither replaces what the
// other set aside.
func TestSetAsideTwiceInASecond(t *testing.T) {
	home, stateDir := t.TempDir(), t.TempDir()
	now := boot.Add(time.Hour)
	f, err := NewFilter(zshHistory, owned)
	if err != nil {
		t.Fatal(err)
	}
	written := []string{"set-up's first", "set-up's second"}
	for i, at := range []time.Time{now, now.Add(500 * time.Millisecond)} {
		if err := os.WriteFile(filepath.Join(home, ".zsh_history"), []byte(written[i]), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := SetAside(stateDir, home, f, at); err != nil {
			t.Fatal(err)
		}
	}
	rec, err := LoadRecord(stateDir)
	asides := rec.Paths["~/.zsh_history"].SetAside
	if err != nil || len(asides) != 2 || asides[0].Dir == asides[1].Dir {
		t.Fatalf("the record = %+v, %v; want two set-asides, each in its own folder", asides, err)
	}
	for i, a := range asides {
		if got := read(t, filepath.Join(a.Dir, ".zsh_history")); got != written[i] {
			t.Errorf("set-aside %d holds %q, want %q", i, got, written[i])
		}
	}
}

// Setting aside that fails partway records what it moved, and returns it,
// and says what failed; a file gone since it was found is passed over, and
// one that moved nothing records nothing and returns nothing.
func TestSetAsideFailsPartway(t *testing.T) {
	home, stateDir := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".claude", "a"), later)
	write(t, filepath.Join(home, ".claude", "sub", "b"), later)
	now := boot.Add(time.Hour)
	dir := filepath.Join(stateDir, "restore", "set-aside", "20261120-100000")
	sub := filepath.Join(home, ".claude", "sub")
	if err := os.Chmod(sub, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sub, 0o700) })
	aside, err := SetAsideFiles(stateDir, home, "~/.claude", []string{"~/.claude/a", "~/.claude/sub/b"}, now)
	if err == nil {
		t.Fatal("SetAsideFiles() = nil error, with a file that can't be moved")
	}
	rec, err := LoadRecord(stateDir)
	want := []Aside{{Time: now, Dir: dir, Paths: []string{"~/.claude/a"}}}
	if err != nil || !reflect.DeepEqual(rec.Paths["~/.claude"].SetAside, want) || !reflect.DeepEqual(aside, want[0]) {
		t.Errorf("record = %+v, %v, returned %+v; want set aside %+v", rec, err, aside, want)
	}

	stateDir = t.TempDir()
	if aside, err := SetAsideFiles(stateDir, home, "~/.claude", []string{"~/.claude/gone"}, now); err != nil || len(aside.Paths) > 0 {
		t.Fatalf("SetAsideFiles() = %+v, %v; want nothing moved", aside, err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "restore.json")); !os.IsNotExist(err) {
		t.Errorf("a set-aside that moved nothing wrote a record: %v", err)
	}
}
