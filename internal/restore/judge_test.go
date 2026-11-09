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

// setUp is when a test Mac's set-up began; earlier and later, when files were
// written before it and since.
var (
	setUp   = time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	earlier = setUp.Add(-24 * time.Hour)
	later   = setUp.Add(time.Hour)
)

// zshHistory is a line restoring a file.
var zshHistory = Line{Path: "~/.zsh_history", Pos: "laptop/declarations:5"}

// firstRun is a test Mac's restore's first run, listing its paths.
func firstRun(paths ...string) Record {
	return Record{First: Run{Time: setUp.Add(time.Hour), Paths: paths}}
}

// A path is judged by its record first: done, here, or under way, whatever
// it holds. Else, with no record or held, by what it holds, kit's aside:
// nothing, to restore; only files set-up wrote, from the moment it began, to
// set aside, then restore; anything older, here. A Mac with no record of its
// set-up's start is in use: whatever it holds is here.
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
		began    time.Time
		want     Verdict
		new      []string
	}{
		{"recorded done, holding older files", claude, with(map[string]time.Time{"history.jsonl": earlier}), Done, setUp, VerdictDone, nil},
		{"recorded here, since emptied", claude, nil, Here, setUp, VerdictHere, nil},
		{"copying", claude, with(map[string]time.Time{"history.jsonl": earlier}), Copying, setUp, VerdictResume, nil},
		{"checking", claude, with(map[string]time.Time{"history.jsonl": earlier}), Checking, setUp, VerdictResume, nil},
		{"held, nothing there", claude, nil, Held, setUp, VerdictRestore, nil},
		{"held, holding files set-up wrote", claude, with(map[string]time.Time{"history.jsonl": later}), Held, setUp, VerdictSetAside, []string{"~/.claude/history.jsonl"}},
		{"held, holding an older file", claude, with(map[string]time.Time{"history.jsonl": earlier}), Held, setUp, VerdictHere, nil},
		{"absent", claude, nil, "", setUp, VerdictRestore, nil},
		{"folders alone", claude, map[string]time.Time{}, "", setUp, VerdictRestore, nil},
		{"nothing but kit's, old and new", claude, with(nil), "", setUp, VerdictRestore, nil},
		{"kit's, and files set-up wrote", claude, with(map[string]time.Time{"history.jsonl": later, "projects/site/a.jsonl": later}), "", setUp, VerdictSetAside, []string{"~/.claude/history.jsonl", "~/.claude/projects/site/a.jsonl"}},
		{"kit's, a file set-up wrote and an older one", claude, with(map[string]time.Time{"history.jsonl": later, "projects/site/a.jsonl": earlier}), "", setUp, VerdictHere, nil},
		{"a Mac in use", claude, with(map[string]time.Time{"history.jsonl": later}), "", time.Time{}, VerdictHere, nil},
		{"a Mac in use, nothing but kit's", claude, with(nil), "", time.Time{}, VerdictRestore, nil},
		{"a file set-up wrote", zshHistory, map[string]time.Time{"~/.zsh_history": later}, "", setUp, VerdictSetAside, []string{"~/.zsh_history"}},
		{"an older file", zshHistory, map[string]time.Time{"~/.zsh_history": earlier}, "", setUp, VerdictHere, nil},
		{"an absent file", zshHistory, nil, "", setUp, VerdictRestore, nil},
		{"a file written as set-up began", zshHistory, map[string]time.Time{"~/.zsh_history": setUp}, "", setUp, VerdictSetAside, []string{"~/.zsh_history"}},
		{"a file written just before set-up began", zshHistory, map[string]time.Time{"~/.zsh_history": setUp.Add(-time.Nanosecond)}, "", setUp, VerdictHere, nil},
		{"a file, recorded done", zshHistory, map[string]time.Time{"~/.zsh_history": later}, Done, setUp, VerdictDone, nil},
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
			got, err := Judge(home, f, rec, tt.began)
			want := Judgement{Path: tt.line.Path, Verdict: tt.want, New: tt.new}
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Errorf("Judge() = %+v, %v; want %+v", got, err, want)
			}
		})
	}
}

// Only a path the restore's first run listed is ever set aside, and only
// till its copy starts: a line added after the first run, its files all
// newer than set-up's start, is here, as on a Mac in use; one holding
// nothing but kit's, or absent, is the caller's to restore or not. A path
// held since the first run, untouched, is judged again on each run; one
// whose copy started, then held, resumes. With no first run recorded,
// nothing's set aside.
func TestJudgeSetsAsideOnlyWhatTheFirstRunListed(t *testing.T) {
	code := Line{Path: "~/Code", Pos: "laptop/declarations:4"}
	tool := Line{Path: "~/.local/state/newtool", Pos: "laptop/declarations:9"}
	month := setUp.Add(30 * 24 * time.Hour)
	held := func(p *Path) { p.State = Held }
	heldOnceCopying := func(p *Path) { p.State, p.Started = Held, setUp.Add(2*time.Hour) }
	for _, tt := range []struct {
		name   string
		line   Line
		files  map[string]time.Time
		first  bool
		record func(*Path)
		want   Verdict
		new    []string
	}{
		{"a line added a month after set-up began, its tool's fresh data", tool, map[string]time.Time{"~/.local/state/newtool/state.db": month}, true, nil, VerdictHere, nil},
		{"a line added since, nothing but Finder's", tool, map[string]time.Time{"~/.local/state/newtool/.DS_Store": month}, true, nil, VerdictRestore, nil},
		{"a line added since, absent", tool, nil, true, nil, VerdictRestore, nil},
		{"held since the first run, holding set-up's files", code, map[string]time.Time{"~/Code/site/notes.md": later, "~/Code/tool/main.go": month}, true, held, VerdictSetAside, []string{"~/Code/site/notes.md", "~/Code/tool/main.go"}},
		{"held since the first run, holding an older file", code, map[string]time.Time{"~/Code/site/notes.md": earlier}, true, held, VerdictHere, nil},
		{"held since the first run, nothing there", code, nil, true, held, VerdictRestore, nil},
		{"held once its copy started", code, map[string]time.Time{"~/Code/site/notes.md": later}, true, heldOnceCopying, VerdictResume, nil},
		{"no first run recorded, holding set-up's files", code, map[string]time.Time{"~/Code/site/notes.md": later}, false, nil, VerdictHere, nil},
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
			got, err := Judge(home, f, rec, setUp)
			if want := (Judgement{Path: tt.line.Path, Verdict: tt.want, New: tt.new}); err != nil || !reflect.DeepEqual(got, want) {
				t.Errorf("Judge() = %+v, %v; want %+v", got, err, want)
			}
		})
	}
}

// Set-up's window closes for good once the restore first settles: a path
// the first run listed, its files all newer than set-up's start, is here
// then, never set aside, as on a Mac in use; one holding nothing but kit's,
// or absent, is the caller's to restore or not. Till the restore settles,
// the window stays open, however long after set-up.
func TestJudgeOnceTheRestoreHasSettled(t *testing.T) {
	began := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	tool := Line{Path: "~/.local/share/tool", Pos: "laptop/declarations:8"}
	for _, tt := range []struct {
		name    string
		files   map[string]time.Time
		settled bool
		want    Verdict
		new     []string
	}{
		{"a tool's month-old data, listed after the restore settled", map[string]time.Time{"ledger.db": began.Add(48 * time.Hour), "logs/today.log": began.Add(30 * 24 * time.Hour)}, true, VerdictHere, nil},
		{"the same, the restore not settled yet", map[string]time.Time{"ledger.db": began.Add(48 * time.Hour), "logs/today.log": began.Add(30 * 24 * time.Hour)}, false, VerdictSetAside, []string{"~/.local/share/tool/ledger.db", "~/.local/share/tool/logs/today.log"}},
		{"a file written as set-up began, after the restore settled", map[string]time.Time{"ledger.db": began}, true, VerdictHere, nil},
		{"nothing but Finder's, after the restore settled", map[string]time.Time{".DS_Store": began.Add(time.Hour)}, true, VerdictRestore, nil},
		{"absent, after the restore settled", nil, true, VerdictRestore, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home, stateDir := t.TempDir(), t.TempDir()
			for rel, when := range tt.files {
				write(t, filepath.Join(home, ".local/share/tool", rel), when)
			}
			if err := RecordFirstRun(stateDir, began.Add(time.Hour), []string{tool.Path}); err != nil {
				t.Fatal(err)
			}
			if tt.settled {
				for _, at := range []time.Time{began.Add(24 * time.Hour), began.Add(25 * time.Hour)} {
					if err := Settle(stateDir, at); err != nil {
						t.Fatal(err)
					}
				}
			}
			rec, err := LoadRecord(stateDir)
			if err != nil {
				t.Fatal(err)
			}
			if want := map[bool]time.Time{true: began.Add(24 * time.Hour)}[tt.settled]; !rec.Settled.Equal(want) {
				t.Errorf("settled at %v; want %v, the first", rec.Settled, want)
			}
			f, err := NewFilter(tool, owned)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Judge(home, f, rec, began)
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
			got, err := Judge(home, f, rec, setUp)
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
	if _, err := Judge(home, f, Record{}, setUp); err == nil || !strings.HasPrefix(err.Error(), "read ~/.claude: ") || !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Judge() = %v; want it failing to read ~/.claude", err)
	}
}

// Setting aside moves what set-up wrote into kit's state, each file keeping
// its path from the home folder, kit's own left in place; the record says
// what went where. A file gone since it was judged is passed over; then the
// path holds nothing but kit's, so it's restored.
func TestSetAside(t *testing.T) {
	home, stateDir := t.TempDir(), t.TempDir()
	for path, when := range map[string]time.Time{".claude/CLAUDE.md": earlier, ".claude/history.jsonl": later, ".claude/todos/a.json": later} {
		write(t, filepath.Join(home, path), when)
	}
	f, err := NewFilter(claude, owned)
	if err != nil {
		t.Fatal(err)
	}
	j, err := Judge(home, f, firstRun(claude.Path), setUp)
	if err != nil || j.Verdict != VerdictSetAside {
		t.Fatalf("Judge() = %+v, %v", j, err)
	}
	j.New = append(j.New, "~/.claude/gone")
	now := setUp.Add(2 * time.Hour)
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
	if j, err := Judge(home, f, rec, setUp); err != nil || j.Verdict != VerdictRestore {
		t.Errorf("Judge() after = %+v, %v; want it restored", j, err)
	}
}

// Setting aside that fails partway records what it moved, and says what
// failed; one that moved nothing records nothing.
func TestSetAsideFailsPartway(t *testing.T) {
	home, stateDir := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".claude", "a"), later)
	write(t, filepath.Join(home, ".claude", "sub", "b"), later)
	now := setUp.Add(time.Hour)
	dir := filepath.Join(stateDir, "restore", "set-aside", "20261120-100000")
	write(t, filepath.Join(dir, ".claude", "sub"), now)
	j := Judgement{Path: "~/.claude", Verdict: VerdictSetAside, New: []string{"~/.claude/a", "~/.claude/sub/b"}}
	if err := SetAside(stateDir, home, j, now); err == nil {
		t.Fatal("SetAside() = nil error, with a file where a folder goes")
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
