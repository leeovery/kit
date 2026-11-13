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
// to restore; anything, on a Mac the boot set up, all of it to set aside,
// then restore, however old, as a shell's history a Terminal wrote before
// the boot; on a Mac in use, here.
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
		new      []string
	}{
		{"recorded done, holding files", claude, with(map[string]time.Time{"history.jsonl": earlier}), Done, true, VerdictDone, nil},
		{"recorded here, since emptied", claude, nil, Here, true, VerdictHere, nil},
		{"copying", claude, with(map[string]time.Time{"history.jsonl": earlier}), Copying, true, VerdictResume, nil},
		{"checking", claude, with(map[string]time.Time{"history.jsonl": earlier}), Checking, true, VerdictResume, nil},
		{"absent", claude, nil, "", true, VerdictRestore, nil},
		{"folders alone", claude, map[string]time.Time{}, "", true, VerdictRestore, nil},
		{"nothing but kit's, old and new", claude, with(nil), "", true, VerdictRestore, nil},
		{"kit's, and files written since the boot", claude, with(map[string]time.Time{"history.jsonl": later, "projects/site/a.jsonl": later}), "", true, VerdictSetAside, []string{"~/.claude/history.jsonl", "~/.claude/projects/site/a.jsonl"}},
		{"kit's, and files older than the boot", claude, with(map[string]time.Time{"history.jsonl": later, "projects/site/a.jsonl": earlier}), "", true, VerdictSetAside, []string{"~/.claude/history.jsonl", "~/.claude/projects/site/a.jsonl"}},
		{"a Mac in use", claude, with(map[string]time.Time{"history.jsonl": later}), "", false, VerdictHere, nil},
		{"a Mac in use, nothing but kit's", claude, with(nil), "", false, VerdictRestore, nil},
		{"a file written since the boot", zshHistory, map[string]time.Time{"~/.zsh_history": later}, "", true, VerdictSetAside, []string{"~/.zsh_history"}},
		{"a file older than the boot, a shell's history from a Terminal before it", zshHistory, map[string]time.Time{"~/.zsh_history": earlier}, "", true, VerdictSetAside, []string{"~/.zsh_history"}},
		{"a file on a Mac in use", zshHistory, map[string]time.Time{"~/.zsh_history": later}, "", false, VerdictHere, nil},
		{"an absent file", zshHistory, nil, "", true, VerdictRestore, nil},
		{"a file, recorded done", zshHistory, map[string]time.Time{"~/.zsh_history": later}, Done, true, VerdictDone, nil},
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
			want := Judgement{Path: tt.line.Path, Verdict: tt.want, New: tt.new}
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
		new    []string
	}{
		{"a line added a month after the boot, its tool's fresh data", tool, map[string]time.Time{"~/.local/state/newtool/state.db": month}, true, nil, VerdictHere, nil},
		{"a line added since, nothing but Finder's", tool, map[string]time.Time{"~/.local/state/newtool/.DS_Store": month}, true, nil, VerdictRestore, nil},
		{"a line added since, absent", tool, nil, true, nil, VerdictRestore, nil},
		{"its holding folder made, holding files", code, map[string]time.Time{"~/Code/site/notes.md": earlier, "~/Code/tool/main.go": month}, true, holding, VerdictSetAside, []string{"~/Code/site/notes.md", "~/Code/tool/main.go"}},
		{"its holding folder made, nothing there", code, nil, true, holding, VerdictRestore, nil},
		{"its copy begun by a run before", code, map[string]time.Time{"~/Code/site/notes.md": earlier}, true, begun, VerdictResume, nil},
		{"no first run recorded, holding files", code, map[string]time.Time{"~/Code/site/notes.md": later}, false, nil, VerdictHere, nil},
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
			if want := (Judgement{Path: tt.line.Path, Verdict: tt.want, New: tt.new}); err != nil || !reflect.DeepEqual(got, want) {
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
		new     []string
	}{
		{"a tool's month-old data, listed after the restore settled", map[string]time.Time{"ledger.db": boot.Add(48 * time.Hour), "logs/today.log": boot.Add(30 * 24 * time.Hour)}, true, VerdictHere, nil},
		{"the same, the restore not settled yet", map[string]time.Time{"ledger.db": boot.Add(48 * time.Hour), "logs/today.log": boot.Add(30 * 24 * time.Hour)}, false, VerdictSetAside, []string{"~/.local/share/tool/ledger.db", "~/.local/share/tool/logs/today.log"}},
		{"nothing but Finder's, after the restore settled", map[string]time.Time{".DS_Store": boot.Add(time.Hour)}, true, VerdictRestore, nil},
		{"absent, after the restore settled", nil, true, VerdictRestore, nil},
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
			if want := (Judgement{Path: tool.Path, Verdict: tt.want, New: tt.new}); err != nil || !reflect.DeepEqual(got, want) {
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

// Setting aside moves what's in a path into kit's state, however old, each
// file keeping its path from the home folder, kit's own left in place; the
// record says what went where. A file gone since it was judged is passed over; then the
// path holds nothing but kit's, so it's restored.
func TestSetAside(t *testing.T) {
	home, stateDir := t.TempDir(), t.TempDir()
	for path, when := range map[string]time.Time{".claude/CLAUDE.md": earlier, ".claude/history.jsonl": earlier, ".claude/todos/a.json": later} {
		write(t, filepath.Join(home, path), when)
	}
	f, err := NewFilter(claude, owned)
	if err != nil {
		t.Fatal(err)
	}
	j, err := Judge(home, f, firstRun(claude.Path), true)
	if err != nil || j.Verdict != VerdictSetAside {
		t.Fatalf("Judge() = %+v, %v", j, err)
	}
	j.New = append(j.New, "~/.claude/gone")
	now := boot.Add(2 * time.Hour)
	if err := SetAside(stateDir, home, j, now); err != nil {
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
	if err != nil || !reflect.DeepEqual(rec.Paths["~/.claude"].SetAside, want) {
		t.Errorf("record = %+v, %v; want set aside %+v", rec, err, want)
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
	j := Judgement{Path: "~/.zsh_history", Verdict: VerdictSetAside, New: []string{"~/.zsh_history"}}
	written := []string{"set-up's first", "set-up's second"}
	for i, at := range []time.Time{now, now.Add(500 * time.Millisecond)} {
		if err := os.WriteFile(filepath.Join(home, ".zsh_history"), []byte(written[i]), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := SetAside(stateDir, home, j, at); err != nil {
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

// Setting aside that fails partway records what it moved, and says what
// failed; one that moved nothing records nothing.
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
	j := Judgement{Path: "~/.claude", Verdict: VerdictSetAside, New: []string{"~/.claude/a", "~/.claude/sub/b"}}
	if err := SetAside(stateDir, home, j, now); err == nil {
		t.Fatal("SetAside() = nil error, with a file that can't be moved")
	}
	rec, err := LoadRecord(stateDir)
	want := []Aside{{Time: now, Dir: dir, Paths: []string{"~/.claude/a"}}}
	if err != nil || !reflect.DeepEqual(rec.Paths["~/.claude"].SetAside, want) {
		t.Errorf("record = %+v, %v; want set aside %+v", rec, err, want)
	}

	stateDir = t.TempDir()
	if err := SetAside(stateDir, home, Judgement{Path: "~/.claude", New: []string{"~/.claude/gone"}}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "restore.json")); !os.IsNotExist(err) {
		t.Errorf("a set-aside that moved nothing wrote a record: %v", err)
	}
}
