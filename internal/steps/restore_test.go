package steps_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/restore"
	"github.com/leeovery/kit/internal/runner/runnertest"
	"github.com/leeovery/kit/internal/steps"
)

// setUpBegan is when a test Mac's set-up began; before and during, when
// files were written before it and since.
var (
	setUpBegan = time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	beforeIt   = setUpBegan.Add(-24 * time.Hour)
	duringIt   = setUpBegan.Add(time.Hour)
)

// snapshotX and snapshotY are two of Time Machine's backups, by name.
const (
	snapshotX = "com.apple.TimeMachine.2026-11-08-141900.backup"
	snapshotY = "com.apple.TimeMachine.2026-11-01-090000.backup"
)

// opened is a restore's source as it's asked for: its origin, the time
// picking Time Machine's backup, and whether kit apply's flags named it.
type opened struct {
	origin restore.Origin
	at     time.Time
	chosen bool
}

// fakeSource is a source to restore from, the old Mac's home folder in a
// folder of its own: live, as the old Mac in use, or a backup's; as
// opened, the origin it's recorded by, and the one gone it stands in for;
// for the old Mac, what its look says is at each listed path (look). Its
// first opens fail as fails says, in turn, and every one as err does. It
// notes each time it's asked for, opened, looked at, and closed.
type fakeSource struct {
	home             string
	origin, replaces restore.Origin
	live             bool
	look             map[string]restore.There
	fails            []error
	err              error

	mu     sync.Mutex
	asked  []opened
	tries  int
	opens  int
	looked [][]string
	closes int
}

func (f *fakeSource) For(o restore.Origin, at time.Time, chosen bool) restore.Opener {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, opened{o, at, chosen})
	return f
}

func (f *fakeSource) Open(context.Context) (*restore.Opened, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tries++
	if len(f.fails) > 0 {
		err := f.fails[0]
		f.fails = f.fails[1:]
		return nil, err
	}
	if f.err != nil {
		return nil, f.err
	}
	f.opens++
	o := &restore.Opened{Home: f.home, Live: f.live, Origin: f.origin, Replaces: f.replaces, Close: func(context.Context) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.closes++
		return nil
	}}
	if f.look != nil {
		o.Look = func(_ context.Context, paths []string) (map[string]restore.There, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.looked = append(f.looked, paths)
			return f.look, nil
		}
	}
	return o, nil
}

// ticking is a clock that moves on every so far each time it's read, from
// the hour after set-up began.
type ticking struct {
	mu    sync.Mutex
	at    time.Time
	every time.Duration
}

func (c *ticking) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := c.at
	c.at = c.at.Add(c.every)
	return t
}

// restoring is a test Mac's data restore: its home folder, kit's state, the
// old Mac's home folder in its source, rsync faked, the free space on its
// disk, and what the step says and logs as it goes.
type restoring struct {
	t                *testing.T
	home, state, old string
	fake             *runnertest.Fake
	src              *fakeSource
	clock            *ticking
	free             uint64
	r                steps.Restoring

	mu     sync.Mutex
	doing  []said
	logged []string
}

// said is what the step said it was doing, and when, by its clock.
type said struct {
	event.Doing
	at time.Time
}

// newRestoring is a Mac started from laptop restoring paths, each from ~,
// from a backup taken as snapshotX, plenty of room on its disk.
func newRestoring(t *testing.T, paths ...string) *restoring {
	t.Helper()
	c := &restoring{t: t, home: t.TempDir(), state: t.TempDir(), old: t.TempDir(), fake: runnertest.New(t),
		clock: &ticking{at: duringIt.Add(time.Hour), every: time.Second}, free: 1e12}
	c.src = &fakeSource{home: c.old, origin: restore.Origin{Kind: restore.FromTimeMachine, Detail: snapshotX}}
	var lines []restore.Line
	for i, p := range paths {
		lines = append(lines, restore.Line{Path: p, Pos: fmt.Sprintf("studio/declarations:%d", i+1)})
	}
	c.r = steps.Restoring{
		Lines: lines,
		Owned: func(context.Context) (restore.Owned, error) { return restore.Owned{}, nil },
		Home:  c.home, StateDir: c.state, Began: setUpBegan, From: "laptop",
		Sources: c.src.For, Rsync: restore.NewRsync(c.fake),
		Space: func(string) (uint64, uint64, error) { return c.free, 2e12, nil },
		Now:   c.clock.now,
	}
	return c
}

// step is the data restore's step, as the test Mac has it now.
func (c *restoring) step() engine.Step { return steps.Restore(c.r) }

// ctx is the test's context, the step's sayings and log noted.
func (c *restoring) ctx(ctx context.Context) context.Context {
	ctx = event.WithSaying(ctx, func(d event.Doing) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.doing = append(c.doing, said{d, c.clock.at})
	})
	return event.WithLogging(ctx, func(l event.Logged) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.logged = append(c.logged, l.Line)
	})
}

// apply applies the step, as kit apply does: on what its check finds.
func (c *restoring) apply(ctx context.Context) error {
	step := c.step()
	return step.Apply(c.ctx(ctx), step.Check(ctx))
}

// write writes a file at path, from ~, in dir, last written when.
func write(t *testing.T, dir, path string, when time.Time) {
	t.Helper()
	full := filepath.Join(dir, strings.TrimPrefix(path, "~/"))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(path), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(full, when, when); err != nil {
		t.Fatal(err)
	}
}

// record is the restore's record, as it stands.
func (c *restoring) record() restore.Record {
	c.t.Helper()
	rec, err := restore.LoadRecord(c.state)
	if err != nil {
		c.t.Fatal(err)
	}
	return rec
}

// change changes the record of path, as an earlier run left it.
func (c *restoring) change(path string, change func(*restore.Path)) {
	c.t.Helper()
	if err := restore.UpdateRecord(c.state, func(r *restore.Record) { r.Change(path, change) }); err != nil {
		c.t.Fatal(err)
	}
}

// rsync's runs on a path: sizing its copy, the copy, its second pass, and,
// for a file, the look at whether the one here differs.
const (
	sizing    = "sizing"
	copyingOp = "copying"
	comparing = "comparing"
	differing = "differing"
)

// opOf is which of rsync's runs args are, by the options that set each
// apart: the look alone runs without --update.
func opOf(args []string) string {
	switch {
	case !slices.Contains(args, "--update"):
		return differing
	case slices.Contains(args, "--info=name1,progress2"):
		return copyingOp
	case slices.Contains(args, "--dry-run"):
		return sizing
	case slices.Contains(args, "--checksum"):
		return comparing
	}
	return "?"
}

// on scripts rsync's run op on path, from ~, as from the source.
func (c *restoring) on(op, path string) *runnertest.Script {
	return c.onFrom(op, filepath.Join(c.old, strings.TrimPrefix(path, "~/")))
}

// onFrom scripts rsync's run op copying from, a path in full.
func (c *restoring) onFrom(op, from string) *runnertest.Script {
	return c.fake.OnMatch("rsync", func(args []string) bool {
		return opOf(args) == op && strings.TrimSuffix(args[len(args)-2], "/") == from
	})
}

// stats is rsync's --stats for files and bytes copied, of a path holding
// those alone.
func stats(files int, bytes int64) string {
	return statsOf(files, bytes, files, bytes)
}

// statsOf is rsync's --stats for files and bytes copied, of a path holding
// totalFiles and totalBytes.
func statsOf(files int, bytes int64, totalFiles int, totalBytes int64) string {
	return fmt.Sprintf("Number of files: %d (reg: %d, dir: 1)\nNumber of regular files transferred: %d\nTotal file size: %d bytes\nTotal transferred file size: %d bytes\n",
		totalFiles+1, totalFiles, files, totalBytes, bytes)
}

// copies scripts path's whole restore from the source: its size, a copy of
// files and bytes, each file's name and how far the copy's got, then its
// statistics, a file here no different, and a second pass that finds
// nothing.
func (c *restoring) copies(path string, files int, bytes int64) {
	c.carriesOn(path, files, bytes, files, bytes)
}

// carriesOn scripts path's restore from the source, as copies does, of
// files and bytes, the path holding totalFiles and totalBytes, the rest
// copied already.
func (c *restoring) carriesOn(path string, files int, bytes int64, totalFiles int, totalBytes int64) {
	c.carriesOnFrom(filepath.Join(c.old, strings.TrimPrefix(path, "~/")), path, files, bytes, totalFiles, totalBytes)
}

// copiesFrom scripts path's whole restore, as copies does, copying from
// from, a path in full.
func (c *restoring) copiesFrom(from, path string, files int, bytes int64) {
	c.carriesOnFrom(from, path, files, bytes, files, bytes)
}

// carriesOnFrom scripts path's restore, as carriesOn does, copying from
// from, a path in full.
func (c *restoring) carriesOnFrom(from, path string, files int, bytes int64, totalFiles int, totalBytes int64) {
	c.onFrom(sizing, from).Prints(statsOf(files, bytes, totalFiles, totalBytes))
	var out strings.Builder
	for i := range files {
		fmt.Fprintf(&out, "%s/file-%d\n", strings.TrimPrefix(path, "~/"), i)
		fmt.Fprintf(&out, "%d %d%% 1.00MB/s 0:00:01 (xfr#%d, to-chk=%d/%d)\r", bytes*int64(i+1)/int64(files), 100*(i+1)/files, i+1, files-i-1, files)
	}
	out.WriteString("\n" + statsOf(files, bytes, totalFiles, totalBytes))
	c.onFrom(copyingOp, from).Prints(out.String())
	c.onFrom(differing, from)
	c.onFrom(comparing, from)
}

// ran are rsync's runs, in order: each its op and the path it's on, from ~.
func (c *restoring) ran() []string {
	var out []string
	for _, cmd := range c.fake.Commands() {
		if cmd.Name != "rsync" {
			continue
		}
		from := strings.TrimSuffix(cmd.Args[len(cmd.Args)-2], "/")
		out = append(out, opOf(cmd.Args)+" ~/"+strings.TrimPrefix(from, c.old+"/"))
	}
	return out
}

// The check reads the record and what each path holds, contacting no
// source and changing nothing: fine once each path is restored or here,
// holding files of its own from before set-up began; else each says what's
// to come, missing, for kit apply to restore: restored; restored once what
// set-up wrote in it is set aside, a file or files; or carried on, part-way
// or copied, from where it began.
func TestRestoreChecks(t *testing.T) {
	for _, tt := range []struct {
		name    string
		prepare func(c *restoring)
		state   check.State
		summary string
		items   []check.Item
	}{
		{"nothing there", func(*restoring) {}, check.Attention, "3 declared, 3 to restore", []check.Item{
			{ID: "restore:~/Code", Name: "~/Code", State: "missing", Action: "restore"},
			{ID: "restore:~/.zsh_history", Name: "~/.zsh_history", State: "missing", Action: "restore"},
			{ID: "restore:~/Documents", Name: "~/Documents", State: "missing", Action: "restore"},
		}},
		{"all restored", func(c *restoring) {
			for _, p := range []string{"~/Code", "~/.zsh_history", "~/Documents"} {
				c.change(p, func(p *restore.Path) { p.State = restore.Done })
			}
		}, check.OK, "3 declared, all restored", nil},
		{"all here", func(c *restoring) {
			for _, p := range []string{"~/Code/a", "~/.zsh_history", "~/Documents/b"} {
				write(c.t, c.home, p, beforeIt)
			}
		}, check.OK, "3 declared, all here already", nil},
		{"some of each", func(c *restoring) {
			c.change("~/Code", func(p *restore.Path) { p.State = restore.Done })
			write(c.t, c.home, "~/Documents/b", beforeIt)
		}, check.Attention, "3 declared, 1 restored, 1 here already, 1 to restore", []check.Item{
			{ID: "restore:~/.zsh_history", Name: "~/.zsh_history", State: "missing", Action: "restore"},
		}},
		{"written during set-up", func(c *restoring) {
			write(c.t, c.home, "~/.zsh_history", duringIt)
			write(c.t, c.home, "~/Code/a", duringIt)
			write(c.t, c.home, "~/Code/b/c", duringIt)
			write(c.t, c.home, "~/Documents/d", beforeIt)
		}, check.Attention, "3 declared, 1 here already, 2 to restore", []check.Item{
			{ID: "restore:~/Code", Name: "~/Code", State: "missing", Detail: "2 files set-up wrote in it set aside first", Action: "restore"},
			{ID: "restore:~/.zsh_history", Name: "~/.zsh_history", State: "missing", Detail: "set-up's copy set aside first", Action: "restore"},
		}},
		{"part-way", func(c *restoring) {
			from := restore.Origin{Kind: restore.FromTimeMachine, Detail: snapshotX}
			c.change("~/Code", func(p *restore.Path) { p.State, p.From, p.Started = restore.Copying, from, duringIt })
			c.change("~/.zsh_history", func(p *restore.Path) { p.State, p.From, p.Started = restore.Checking, from, duringIt })
			c.change("~/Documents", func(p *restore.Path) { p.State = restore.Done })
		}, check.Attention, "3 declared, 1 restored, 2 to restore", []check.Item{
			{ID: "restore:~/Code", Name: "~/Code", State: "missing", Detail: "part-way from Time Machine's backup of 8 Nov 2026 14:19", Action: "restore"},
			{ID: "restore:~/.zsh_history", Name: "~/.zsh_history", State: "missing", Detail: "copied from Time Machine's backup of 8 Nov 2026 14:19, not yet compared with it", Action: "restore"},
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := newRestoring(t, "~/Code", "~/.zsh_history", "~/Documents")
			tt.prepare(c)
			before := c.record()
			res := c.step().Check(t.Context())
			if res.State != tt.state || res.Summary != tt.summary || !reflect.DeepEqual(res.Items, tt.items) {
				t.Errorf("the check = %s %q %+v\nwant %s %q %+v", res.State, res.Summary, res.Items, tt.state, tt.summary, tt.items)
			}
			if after := c.record(); !reflect.DeepEqual(after, before) {
				t.Errorf("the check changed the record: %+v, was %+v", after, before)
			}
			if calls := c.fake.Calls(); len(calls) > 0 {
				t.Errorf("the check ran %q", calls)
			}
		})
	}
}

// Once the restore has settled, a line added since, its path absent, isn't
// one to nag about: fine, each such path a quiet item saying what would
// bring it, the old Mac among them for a Mac started from one; until kit
// apply names a source, when it's to restore.
func TestRestoreSettledWaitsForAFlag(t *testing.T) {
	c := newRestoring(t, "~/Code", "~/.local/state/newtool", "~/.cache/x")
	c.change("~/Code", func(p *restore.Path) { p.State = restore.Done })
	if err := restore.Settle(c.state, duringIt); err != nil {
		t.Fatal(err)
	}
	res := c.step().Check(t.Context())
	quiet := func(path, flag string) check.Item {
		return check.Item{ID: "restore:" + path, Name: path, State: "not restored", Detail: "kit apply --restore-from " + flag + " brings it", Quiet: "listed since the restore settled"}
	}
	if want := []check.Item{quiet("~/.local/state/newtool", "mac|time-machine|arq"), quiet("~/.cache/x", "mac|time-machine|arq")}; res.State != check.OK || res.Summary != "3 declared, 1 restored, 2 not restored" || !reflect.DeepEqual(res.Items, want) {
		t.Errorf("settled, the check = %s %q %+v; want fine, %+v", res.State, res.Summary, res.Items, want)
	}
	c.r.From = ""
	if res := c.step().Check(t.Context()); len(res.Items) != 2 || res.Items[0] != quiet("~/.local/state/newtool", "time-machine|arq") {
		t.Errorf("on a Mac started from none, the check's items = %+v; want backups alone named", res.Items)
	}
	c.r.From = "laptop"
	if err := c.apply(t.Context()); err != nil || len(c.src.asked) > 0 {
		t.Errorf("settled, applying = %v, asking for %+v; want nothing restored", err, c.src.asked)
	}

	c.r.Flag = steps.RestoreFrom{Kind: restore.FromTimeMachine}
	res = c.step().Check(t.Context())
	if res.State != check.Attention || res.Summary != "3 declared, 1 restored, 2 to restore" || len(res.Items) != 2 {
		t.Errorf("with --restore-from, the check = %s %q %+v; want both to restore", res.State, res.Summary, res.Items)
	}
}

// Applied from a backup, the restore goes all the way: it records its
// first run, leaves what's here as it is, opens the source kit apply's
// flags name, measures each path's copy, then copies each in turn, what
// set-up wrote set aside first, comparing each with the backup; the record
// says each restored, from where, what it came to and when, the restore
// settled; the source closed; the check then fine.
func TestRestoreFromABackup(t *testing.T) {
	c := newRestoring(t, "~/Code", "~/.zsh_history", "~/Documents")
	c.r.Flag = steps.RestoreFrom{Kind: restore.FromTimeMachine}
	write(t, c.old, "~/Code/a", beforeIt)
	write(t, c.old, "~/.zsh_history", beforeIt)
	write(t, c.home, "~/.zsh_history", duringIt)
	write(t, c.home, "~/Documents/kept", beforeIt)
	c.copies("~/Code", 4, 3_000_000)
	c.copies("~/.zsh_history", 1, 2_000)
	partial := filepath.Join(c.state, "restore", "partial", "Code")
	write(t, partial, "~/left.tmp", duringIt)

	if err := c.apply(t.Context()); err != nil {
		t.Fatalf("apply = %v", err)
	}
	if want := []string{
		sizing + " ~/Code", sizing + " ~/.zsh_history",
		copyingOp + " ~/Code", comparing + " ~/Code",
		copyingOp + " ~/.zsh_history", differing + " ~/.zsh_history", comparing + " ~/.zsh_history",
	}; !slices.Equal(c.ran(), want) {
		t.Errorf("rsync ran\n%q\nwant\n%q", c.ran(), want)
	}
	if want := []opened{{restore.Origin{Kind: restore.FromTimeMachine}, time.Time{}, true}}; !reflect.DeepEqual(c.src.asked, want) || c.src.opens != 1 || c.src.closes != 1 {
		t.Errorf("the source was asked for %+v, opened %d and closed %d times; want %+v, once each", c.src.asked, c.src.opens, c.src.closes, want)
	}
	copied := c.fake.Commands()[2].Args
	for _, arg := range []string{"--partial-dir=" + partial, "--exclude-from=" + filepath.Join(c.state, "restore", "filters", "Code"), filepath.Join(c.old, "Code") + "/", filepath.Join(c.home, "Code") + "/"} {
		if !slices.Contains(copied, arg) {
			t.Errorf("the copy of ~/Code, %q, hasn't %s", copied, arg)
		}
	}
	if file := c.fake.Commands()[4].Args; file[len(file)-1] != filepath.Join(c.home, ".zsh_history") {
		t.Errorf("the copy of ~/.zsh_history, %q, isn't a file's", file)
	}
	if rules, err := os.ReadFile(filepath.Join(c.state, "restore", "filters", "Code")); err != nil || string(rules) != "- .DS_Store\n- .localized\n" {
		t.Errorf("~/Code's rules = %q, %v", rules, err)
	}
	if _, err := os.Stat(partial); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("~/Code's partial files are still there: %v", err)
	}

	rec := c.record()
	from := restore.Origin{Kind: restore.FromTimeMachine, Detail: snapshotX}
	code, history := rec.Paths["~/Code"], rec.Paths["~/.zsh_history"]
	switch {
	case rec.From != from || rec.First.Time.IsZero() || !slices.Equal(rec.First.Paths, []string{"~/Code", "~/.zsh_history", "~/Documents"}):
		t.Errorf("the record's source %+v and first run %+v", rec.From, rec.First)
	case code.State != restore.Done || code.From != from || code.Files != 4 || code.Bytes != 3_000_000 || code.Started.IsZero() || !code.Finished.After(code.Started) || code.Second == nil || code.Second.Copied != 0:
		t.Errorf("~/Code's record = %+v", code)
	case history.State != restore.Done || len(history.SetAside) != 1 || !slices.Equal(history.SetAside[0].Paths, []string{"~/.zsh_history"}):
		t.Errorf("~/.zsh_history's record = %+v", history)
	case rec.Paths["~/Documents"].State != restore.Here:
		t.Errorf("~/Documents's record = %+v; want it here", rec.Paths["~/Documents"])
	case rec.Settled.IsZero():
		t.Error("the restore didn't settle")
	}
	aside := history.SetAside[0].Dir
	if got, err := os.ReadFile(filepath.Join(aside, ".zsh_history")); err != nil || string(got) != "~/.zsh_history" {
		t.Errorf("set-up's ~/.zsh_history, set aside = %q, %v", got, err)
	}
	if want := []string{
		"Left ~/Documents as it is: it held files of its own",
		"Restored ~/Code: 4 files, 3.0 MB",
		"Set aside what set-up wrote in ~/.zsh_history, into " + aside,
		"Restored ~/.zsh_history: 1 file, 2 kB",
	}; !slices.Equal(c.logged, want) {
		t.Errorf("the log says\n%q\nwant\n%q", c.logged, want)
	}
	for _, d := range c.doing {
		if strings.Contains(d.Says, "file-") {
			t.Errorf("the step said a file's name: %q", d.Says)
		}
	}
	if res := c.step().Check(t.Context()); res.State != check.OK || res.Summary != "3 declared, 2 restored, 1 here already" {
		t.Errorf("restored, the check = %s %q", res.State, res.Summary)
	}
}

// With nothing to copy, applying opens no source: it records what's here,
// and settles.
func TestRestoreWithNothingToCopy(t *testing.T) {
	c := newRestoring(t, "~/Code", "~/Documents")
	c.change("~/Code", func(p *restore.Path) { p.State = restore.Done })
	write(t, c.home, "~/Documents/kept", beforeIt)
	if err := c.apply(t.Context()); err != nil {
		t.Fatalf("apply = %v", err)
	}
	if rec := c.record(); len(c.src.asked) > 0 || rec.Paths["~/Documents"].State != restore.Here || rec.Settled.IsZero() {
		t.Errorf("asked for %+v; the record %+v; want no source, ~/Documents here, settled", c.src.asked, rec)
	}
}

// The second pass compares each copy with its source. From a backup,
// nothing changes, so a file it copies again fails its path, naming it:
// the path stays checking, to compare again, its pass's finding recorded
// and the check saying so, while the rest go on, and the restore doesn't
// settle. From the old Mac in use, files change as they're
// copied: those it copies again are counted, the path restored.
func TestRestoreSecondPass(t *testing.T) {
	itemized := ">f.st...... Code/a\n>f+++++++++ Code/b\n.d..t...... Code/\n"

	c := newRestoring(t, "~/Code", "~/Documents")
	c.r.Flag = steps.RestoreFrom{Kind: restore.FromTimeMachine}
	write(t, c.old, "~/Code/a", beforeIt)
	write(t, c.old, "~/Documents/d", beforeIt)
	c.copies("~/Code", 2, 2_000)
	c.on(comparing, "~/Code").Prints(itemized)
	c.copies("~/Documents", 1, 1_000)
	err := c.apply(t.Context())
	if want := "couldn't restore ~/Code: its second pass found 2 files differing from Time Machine's backup of 8 Nov 2026 14:19: Code/a, Code/b"; err == nil || err.Error() != want {
		t.Errorf("apply = %v; want %q", err, want)
	}
	rec := c.record()
	if code := rec.Paths["~/Code"]; code.State != restore.Checking || code.Second == nil || code.Second.Copied != 2 || rec.Paths["~/Documents"].State != restore.Done || !rec.Settled.IsZero() || c.src.closes != 1 {
		t.Errorf("the record = %+v, closed %d times; want ~/Code checking, its pass finding 2, ~/Documents done, not settled, the source closed", rec, c.src.closes)
	}
	if res := c.step().Check(t.Context()); len(res.Items) != 1 || res.Items[0].Detail != "2 files differed from Time Machine's backup of 8 Nov 2026 14:19, copied again; compared again next run" {
		t.Errorf("after the pass, the check = %+v", res.Items)
	}

	c = newRestoring(t, "~/Code")
	c.src.live, c.src.origin = true, restore.Origin{Kind: restore.FromMac, Detail: "laptop"}
	write(t, c.old, "~/Code/a", beforeIt)
	c.copies("~/Code", 2, 2_000)
	c.on(comparing, "~/Code").Prints(itemized)
	if err := c.apply(t.Context()); err != nil {
		t.Fatalf("from a live Mac, apply = %v", err)
	}
	if code := c.record().Paths["~/Code"]; code.State != restore.Done || code.Second == nil || code.Second.Copied != 2 {
		t.Errorf("from a live Mac, ~/Code's record = %+v; want restored, 2 copied again", code)
	}
	if want := "Restored ~/Code: 2 files, 2 kB; 2 copied again, changed as they were copied"; !slices.Contains(c.logged, want) {
		t.Errorf("the log says %q, want %q", c.logged, want)
	}
}

// Stopped part-way, as by Ctrl-C, the record says the path's copy is under
// way, from that backup, and the source is closed. The check says so, and a
// run again carries on from the same backup, by its name, never a newer
// one, kit apply naming no source, or naming Time Machine; done, the record
// and the log say what the path holds, not what the run again copied.
func TestRestoreResumesFromTheSameBackup(t *testing.T) {
	c := newRestoring(t, "~/Code")
	c.r.Flag = steps.RestoreFrom{Kind: restore.FromTimeMachine}
	write(t, c.old, "~/Code/a", beforeIt)
	c.on(sizing, "~/Code").Prints(stats(2, 2_000))
	ctx, stop := context.WithCancel(t.Context())
	defer stop()
	c.on(copyingOp, "~/Code").Does(stop).Hangs()
	if err := c.apply(ctx); err == nil {
		t.Fatal("stopped, apply succeeded")
	}
	code := c.record().Paths["~/Code"]
	if code.State != restore.Copying || code.From != c.src.origin || code.Started.IsZero() || c.src.closes != 1 {
		t.Errorf("stopped, ~/Code's record = %+v, closed %d times; want it copying from %+v, the source closed", code, c.src.closes, c.src.origin)
	}
	if res := c.step().Check(t.Context()); len(res.Items) != 1 || res.Items[0].Detail != "part-way from Time Machine's backup of 8 Nov 2026 14:19" {
		t.Errorf("stopped, the check = %+v", res)
	}

	c.r.Flag = steps.RestoreFrom{}
	c.src.asked = nil
	c.carriesOn("~/Code", 1, 1_000, 2, 2_000)
	if err := c.apply(t.Context()); err != nil {
		t.Fatalf("again, apply = %v", err)
	}
	if want := []opened{{c.src.origin, time.Time{}, false}}; !reflect.DeepEqual(c.src.asked, want) {
		t.Errorf("again, the source asked for = %+v, want %+v", c.src.asked, want)
	}
	if again := c.record().Paths["~/Code"]; again.State != restore.Done || !again.Started.Equal(code.Started) || again.Files != 2 || again.Bytes != 2_000 {
		t.Errorf("again, ~/Code's record = %+v; want restored, holding 2 files of 2 kB, started as it first was", again)
	}
	if want := "Restored ~/Code: 2 files, 2 kB"; !slices.Contains(c.logged, want) {
		t.Errorf("again, the log says %q; want %q", c.logged, want)
	}

	c = newRestoring(t, "~/Code")
	c.change("~/Code", func(p *restore.Path) { p.State, p.From, p.Started = restore.Copying, c.src.origin, duringIt })
	c.r.Flag = steps.RestoreFrom{Kind: restore.FromTimeMachine}
	write(t, c.old, "~/Code/a", beforeIt)
	c.copies("~/Code", 1, 1_000)
	if err := c.apply(t.Context()); err != nil {
		t.Fatalf("with --restore-from time-machine, apply = %v", err)
	}
	if want := []opened{{c.src.origin, time.Time{}, true}}; !reflect.DeepEqual(c.src.asked, want) {
		t.Errorf("with --restore-from time-machine, the source asked for = %+v, want the same backup, %+v", c.src.asked, want)
	}
}

// kit apply's flags naming another source than a path part-way came from
// are an error naming the path and where it came from, the source never
// opened; naming another of Time Machine's backups, by --at, while the one
// it came from is still there, too, once it's opened, and closed.
func TestRestoreRefusesAnotherSourceMidway(t *testing.T) {
	c := newRestoring(t, "~/Documents", "~/Code")
	c.change("~/Code", func(p *restore.Path) { p.State, p.From, p.Started = restore.Copying, c.src.origin, duringIt })
	c.r.Flag = steps.RestoreFrom{Kind: restore.FromArq}
	want := "couldn't restore: ~/Code is part-way from Time Machine's backup of 8 Nov 2026 14:19, not Arq's restore: kit apply without --restore-from or --at carries on from there"
	if err := c.apply(t.Context()); err == nil || err.Error() != want {
		t.Errorf("--restore-from arq, apply = %v; want %q", err, want)
	}
	if len(c.src.asked) > 0 || len(c.fake.Calls()) > 0 {
		t.Errorf("--restore-from arq, the source asked for %+v, rsync ran %q", c.src.asked, c.fake.Calls())
	}

	at := time.Date(2026, 11, 1, 23, 59, 0, 0, time.UTC)
	c.r.Flag = steps.RestoreFrom{Kind: restore.FromTimeMachine, At: at}
	c.src.origin = restore.Origin{Kind: restore.FromTimeMachine, Detail: snapshotY}
	want = "couldn't restore: ~/Code is part-way from Time Machine's backup of 8 Nov 2026 14:19, not Time Machine's backup of 1 Nov 2026 09:00: kit apply without --restore-from or --at carries on from there"
	if err := c.apply(t.Context()); err == nil || err.Error() != want {
		t.Errorf("--at, apply = %v; want %q", err, want)
	}
	if asked := []opened{{restore.Origin{Kind: restore.FromTimeMachine, Detail: snapshotX}, at, true}}; !reflect.DeepEqual(c.src.asked, asked) || c.src.closes != 1 || len(c.fake.Calls()) > 0 {
		t.Errorf("--at, the source asked for %+v, closed %d times, rsync ran %q", c.src.asked, c.src.closes, c.fake.Calls())
	}
}

// The backup a restore began from gone, as Time Machine thins old ones,
// kit apply naming Time Machine has the backup it opens stand in for it:
// every path part-way from the one gone carries on from the new one,
// recorded so at once, so a run again, naming no source, carries on from it
// too; and the log says so. Naming no source, the one gone is asked for
// as it was, not chosen, which refuses (restore.TimeMachine).
func TestRestoreCarriesOnFromAnotherBackup(t *testing.T) {
	gone := restore.Origin{Kind: restore.FromTimeMachine, Detail: snapshotY}
	c := newRestoring(t, "~/Code", "~/Documents")
	for _, p := range []string{"~/Code", "~/Documents"} {
		c.change(p, func(p *restore.Path) { p.State, p.From, p.Started = restore.Copying, gone, duringIt })
	}
	c.src.err = errors.New("the backup the restore began from isn't on Backups of laptop any more")
	if err := c.apply(t.Context()); err == nil || !strings.HasSuffix(err.Error(), "isn't on Backups of laptop any more") {
		t.Errorf("naming no source, apply = %v; want the source's refusal", err)
	}
	if want := []opened{{gone, time.Time{}, false}}; !reflect.DeepEqual(c.src.asked, want) {
		t.Errorf("naming no source, the source asked for = %+v, want %+v", c.src.asked, want)
	}

	c.src.err, c.src.asked, c.src.replaces = nil, nil, gone
	c.r.Flag = steps.RestoreFrom{Kind: restore.FromTimeMachine}
	write(t, c.old, "~/Code/a", beforeIt)
	write(t, c.old, "~/Documents/d", beforeIt)
	c.on(sizing, "~/Code").Prints(stats(1, 1_000))
	c.on(sizing, "~/Documents").Prints(stats(1, 1_000))
	ctx, stop := context.WithCancel(t.Context())
	defer stop()
	c.on(copyingOp, "~/Code").Does(stop).Hangs()
	if err := c.apply(ctx); err == nil {
		t.Fatal("stopped, apply succeeded")
	}
	if want := []opened{{gone, time.Time{}, true}}; !reflect.DeepEqual(c.src.asked, want) {
		t.Errorf("with --restore-from time-machine, the source asked for = %+v, want %+v", c.src.asked, want)
	}
	rec := c.record()
	if rec.From != c.src.origin || rec.Paths["~/Code"].From != c.src.origin || rec.Paths["~/Documents"].From != c.src.origin {
		t.Errorf("the record = %+v; want it, ~/Code and ~/Documents from %+v", rec, c.src.origin)
	}
	if want := "Carrying on from Time Machine's backup of 8 Nov 2026 14:19: Time Machine's backup of 1 Nov 2026 09:00, which the restore began from, has gone"; !slices.Equal(c.logged, []string{want}) {
		t.Errorf("the log says %q; want %q", c.logged, want)
	}

	c.r.Flag, c.src.asked, c.src.replaces = steps.RestoreFrom{}, nil, restore.Origin{}
	c.src.err = errors.New("not attached")
	_ = c.apply(t.Context())
	if want := []opened{{c.src.origin, time.Time{}, false}}; !reflect.DeepEqual(c.src.asked, want) {
		t.Errorf("a run again, the source asked for = %+v, want %+v", c.src.asked, want)
	}
}

// Without room for the copies and a margin, a twentieth of them, at least
// 2 GB, nothing's copied, the numbers said, and the source closed.
func TestRestoreNeedsRoom(t *testing.T) {
	for _, tt := range []struct {
		name  string
		bytes int64
		free  uint64
		want  string
	}{
		{"a margin of 2 GB", 1_000_000_000, 2_900_000_000, "couldn't restore: 1.0 GB to copy needs 3.0 GB free, with a margin, and the disk has 2.9 GB"},
		{"a margin of 5%", 100_000_000_000, 104_000_000_000, "couldn't restore: 100.0 GB to copy needs 105.0 GB free, with a margin, and the disk has 104.0 GB"},
		{"room enough", 100_000_000_000, 105_000_000_000, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := newRestoring(t, "~/Code", "~/Documents")
			c.r.Flag, c.free = steps.RestoreFrom{Kind: restore.FromTimeMachine}, tt.free
			write(t, c.old, "~/Code/a", beforeIt)
			write(t, c.old, "~/Documents/d", beforeIt)
			c.copies("~/Code", 1, tt.bytes/2)
			c.copies("~/Documents", 1, tt.bytes/2)
			err := c.apply(t.Context())
			if tt.want == "" {
				if err != nil || c.record().Settled.IsZero() {
					t.Errorf("apply = %v; want it restored", err)
				}
				return
			}
			if err == nil || err.Error() != tt.want {
				t.Errorf("apply = %v; want %q", err, tt.want)
			}
			if want := []string{sizing + " ~/Code", sizing + " ~/Documents"}; !slices.Equal(c.ran(), want) || c.src.closes != 1 {
				t.Errorf("rsync ran %q, the source closed %d times; want only the sizes, and closed", c.ran(), c.src.closes)
			}
			if rec := c.record(); len(rec.Paths) > 0 {
				t.Errorf("the record = %+v; want nothing started", rec.Paths)
			}
		})
	}
}

// A path part-way that the source hasn't fails alone, naming the source;
// the rest are restored. A source that can't be opened fails the restore,
// saying why.
func TestRestoreSourceShortcomings(t *testing.T) {
	c := newRestoring(t, "~/Code", "~/.local/state/newtool")
	c.change("~/.local/state/newtool", func(p *restore.Path) { p.State, p.From, p.Started = restore.Copying, c.src.origin, duringIt })
	write(t, c.old, "~/Code/a", beforeIt)
	c.copies("~/Code", 1, 1_000)
	err := c.apply(t.Context())
	if want := "couldn't restore ~/.local/state/newtool: Time Machine's backup of 8 Nov 2026 14:19 hasn't it"; err == nil || err.Error() != want {
		t.Errorf("apply = %v; want %q", err, want)
	}
	if rec := c.record(); rec.Paths["~/Code"].State != restore.Done || rec.Paths["~/.local/state/newtool"].State != restore.Copying || !rec.Settled.IsZero() {
		t.Errorf("the record = %+v; want ~/Code restored, the other still part-way, not settled", rec.Paths)
	}

	c = newRestoring(t, "~/Code")
	c.src.err = errors.New("no Time Machine backup found: no Time Machine disk is attached")
	c.r.Flag = steps.RestoreFrom{Kind: restore.FromTimeMachine}
	if err := c.apply(t.Context()); err == nil || err.Error() != "couldn't restore: no Time Machine backup found: no Time Machine disk is attached" {
		t.Errorf("with no disk, apply = %v", err)
	}
}

// A listed path the source hasn't, nothing copied to it yet, as a tool's
// folder the old Mac never made, is no failure: it's recorded not there,
// from that source, the log saying so, and the restore settles; the check
// is fine, a quiet item saying what would bring it, and a run again leaves
// it be. Set-up's window is shut to it: written there weeks later, it's
// here, left as it is, even by kit apply naming a source that has it,
// nothing set aside, nothing copied. Still absent, such a source brings
// it, as a line added after the restore settled.
func TestRestoreAPathTheSourceHadnt(t *testing.T) {
	quiet := check.Item{ID: "restore:~/.local/state/newtool", Name: "~/.local/state/newtool", State: "not restored",
		Detail: "Time Machine's backup of 8 Nov 2026 14:19 hadn't it; kit apply --restore-from mac|time-machine|arq brings it from another", Quiet: "not in its source"}
	hadnt := func(t *testing.T) *restoring {
		t.Helper()
		c := newRestoring(t, "~/Code", "~/.local/state/newtool")
		write(t, c.old, "~/Code/a", beforeIt)
		c.copies("~/Code", 1, 1_000)
		if err := c.apply(t.Context()); err != nil {
			t.Fatalf("apply = %v", err)
		}
		rec := c.record()
		if tool := rec.Paths["~/.local/state/newtool"]; tool.State != restore.NotThere || tool.From != c.src.origin || rec.Settled.IsZero() {
			t.Fatalf("the record = %+v; want ~/.local/state/newtool not there, from the backup, settled", rec)
		}
		if want := "Nothing to restore in ~/.local/state/newtool: Time Machine's backup of 8 Nov 2026 14:19 hasn't it"; !slices.Contains(c.logged, want) {
			t.Errorf("the log says %q, want %q", c.logged, want)
		}
		if res := c.step().Check(t.Context()); res.State != check.OK || res.Summary != "2 declared, 1 restored, 1 not restored" || !reflect.DeepEqual(res.Items, []check.Item{quiet}) {
			t.Errorf("the check = %s %q %+v; want fine, %+v", res.State, res.Summary, res.Items, quiet)
		}
		c.src.asked = nil
		if err := c.apply(t.Context()); err != nil || len(c.src.asked) > 0 {
			t.Errorf("a run again = %v, asking for %+v; want nothing opened", err, c.src.asked)
		}
		return c
	}

	t.Run("written there since", func(t *testing.T) {
		c := hadnt(t)
		write(t, c.home, "~/.local/state/newtool/ledger.db", duringIt.Add(30*24*time.Hour))
		write(t, c.old, "~/.local/state/newtool/ledger.db", beforeIt)
		c.r.Flag = steps.RestoreFrom{Kind: restore.FromTimeMachine}
		ran := len(c.fake.Calls())
		if err := c.apply(t.Context()); err != nil {
			t.Fatalf("with --restore-from, apply = %v", err)
		}
		tool := c.record().Paths["~/.local/state/newtool"]
		if tool.State != restore.Here || len(tool.SetAside) > 0 || len(c.fake.Calls()) > ran {
			t.Errorf("with --restore-from, ~/.local/state/newtool = %+v, rsync running %q; want it here, nothing set aside or copied", tool, c.fake.Calls()[ran:])
		}
		if got, err := os.ReadFile(filepath.Join(c.home, ".local/state/newtool/ledger.db")); err != nil || string(got) != "~/.local/state/newtool/ledger.db" {
			t.Errorf("the tool's ledger.db = %q, %v; want it as it was", got, err)
		}
	})
	t.Run("the restore not settled", func(t *testing.T) {
		c := newRestoring(t, "~/Code", "~/.local/state/newtool")
		write(t, c.old, "~/Code/a", beforeIt)
		c.on(sizing, "~/Code").Prints(stats(1, 1_000))
		c.on(copyingOp, "~/Code").Exits(23)
		for range 2 {
			if err := c.apply(t.Context()); err == nil {
				t.Fatal("~/Code failing, apply succeeded")
			}
		}
		res := c.step().Check(t.Context())
		if rec := c.record(); !rec.Settled.IsZero() || len(res.Items) != 2 || res.Items[0].Name != "~/Code" || res.Items[1] != quiet {
			t.Errorf("~/Code failing, the check = %+v, settled %v; want ~/Code to restore, then %+v", res.Items, rec.Settled, quiet)
		}
		said := 0
		for _, l := range c.logged {
			if strings.HasPrefix(l, "Nothing to restore in ~/.local/state/newtool") {
				said++
			}
		}
		if said != 1 {
			t.Errorf("the log says %q; want ~/.local/state/newtool found not there once, the run again leaving it be", c.logged)
		}
	})
	t.Run("still absent", func(t *testing.T) {
		c := hadnt(t)
		write(t, c.old, "~/.local/state/newtool/ledger.db", beforeIt)
		c.copies("~/.local/state/newtool", 1, 1_000)
		c.r.Flag = steps.RestoreFrom{Kind: restore.FromTimeMachine}
		if err := c.apply(t.Context()); err != nil {
			t.Fatalf("with --restore-from, apply = %v", err)
		}
		if tool := c.record().Paths["~/.local/state/newtool"]; tool.State != restore.Done {
			t.Errorf("with --restore-from, ~/.local/state/newtool = %+v; want it restored", tool)
		}
	})
}

// Each path is judged again as its turn comes, as the paths before it can
// copy for hours: one absent at the start that set-up has written in
// since, as Claude's session folders, has that set aside first; one whose
// set-up's files were found at the start has those written since set aside
// with them; one that's come to hold older files, as copied in by hand, is
// here, left as it is, never copied into.
func TestRestoreJudgesEachPathAgain(t *testing.T) {
	c := newRestoring(t, "~/Code", "~/.claude", "~/Documents", "~/Music")
	c.r.Flag = steps.RestoreFrom{Kind: restore.FromTimeMachine}
	for _, p := range []string{"~/Code/a", "~/.claude/settings.json", "~/Documents/d", "~/Music/m.mp3"} {
		write(t, c.old, p, beforeIt)
	}
	write(t, c.home, "~/Documents/early.txt", duringIt)
	for _, p := range []string{"~/Code", "~/.claude", "~/Documents", "~/Music"} {
		c.copies(p, 1, 1_000)
	}
	c.on(copyingOp, "~/Code").Prints(stats(1, 1_000)).Does(func() {
		write(t, c.home, "~/.claude/projects/session.jsonl", duringIt.Add(2*time.Hour))
		write(t, c.home, "~/Documents/later.txt", duringIt.Add(2*time.Hour))
		write(t, c.home, "~/Music/old.mp3", beforeIt)
	})
	if err := c.apply(t.Context()); err != nil {
		t.Fatalf("apply = %v", err)
	}
	if want := []string{
		sizing + " ~/Code", sizing + " ~/.claude", sizing + " ~/Documents", sizing + " ~/Music",
		copyingOp + " ~/Code", comparing + " ~/Code",
		copyingOp + " ~/.claude", comparing + " ~/.claude",
		copyingOp + " ~/Documents", comparing + " ~/Documents",
	}; !slices.Equal(c.ran(), want) {
		t.Errorf("rsync ran\n%q\nwant\n%q", c.ran(), want)
	}
	rec := c.record()
	aside := func(path string) []string {
		var paths []string
		for _, a := range rec.Paths[path].SetAside {
			paths = append(paths, a.Paths...)
		}
		slices.Sort(paths)
		return paths
	}
	if got := aside("~/.claude"); rec.Paths["~/.claude"].State != restore.Done || !slices.Equal(got, []string{"~/.claude/projects/session.jsonl"}) {
		t.Errorf("~/.claude = %+v, set aside %q; want restored, the session set aside first", rec.Paths["~/.claude"], got)
	}
	if got := aside("~/Documents"); rec.Paths["~/Documents"].State != restore.Done || !slices.Equal(got, []string{"~/Documents/early.txt", "~/Documents/later.txt"}) {
		t.Errorf("~/Documents = %+v, set aside %q; want restored, both of set-up's files set aside first", rec.Paths["~/Documents"], got)
	}
	if music := rec.Paths["~/Music"]; music.State != restore.Here || len(music.SetAside) > 0 || rec.Settled.IsZero() {
		t.Errorf("~/Music = %+v, settled %v; want it here, nothing set aside, the restore settled", music, rec.Settled)
	}
	if !slices.Contains(c.logged, "Left ~/Music as it is: it held files of its own") {
		t.Errorf("the log says %q; want ~/Music left as it is", c.logged)
	}
}

// Kit apply naming no source, a Mac started from another copies from it;
// a run again, from the source the record says the last run chose.
func TestRestoreChoosesItsSource(t *testing.T) {
	c := newRestoring(t, "~/Code")
	c.src.err = errors.New("not built")
	_ = c.apply(t.Context())
	if want := []opened{{restore.Origin{Kind: restore.FromMac, Detail: "laptop"}, time.Time{}, false}}; !reflect.DeepEqual(c.src.asked, want) {
		t.Errorf("the source asked for = %+v, want %+v", c.src.asked, want)
	}

	c = newRestoring(t, "~/Code")
	c.src.err = errors.New("not built")
	if err := restore.UpdateRecord(c.state, func(r *restore.Record) { r.From = restore.Origin{Kind: restore.FromTimeMachine, Detail: snapshotY} }); err != nil {
		t.Fatal(err)
	}
	_ = c.apply(t.Context())
	if want := []opened{{restore.Origin{Kind: restore.FromTimeMachine, Detail: snapshotY}, time.Time{}, false}}; !reflect.DeepEqual(c.src.asked, want) {
		t.Errorf("with the record's, the source asked for = %+v, want %+v", c.src.asked, want)
	}
}

// How far the copies have got is said no more often than every quarter of
// a second, however fast rsync says it: all the paths' files counted
// together, the bar their bytes; each path's copy and comparison said as
// it starts.
func TestRestoreSaysHowFarItsGot(t *testing.T) {
	c := newRestoring(t, "~/Code", "~/Documents")
	c.r.Flag = steps.RestoreFrom{Kind: restore.FromTimeMachine}
	c.clock.every = 100 * time.Millisecond
	write(t, c.old, "~/Code/a", beforeIt)
	write(t, c.old, "~/Documents/d", beforeIt)
	c.copies("~/Code", 100, 100_000)
	c.copies("~/Documents", 100, 300_000)
	if err := c.apply(t.Context()); err != nil {
		t.Fatalf("apply = %v", err)
	}
	var says []string
	var copying []said
	for _, d := range c.doing {
		if len(says) == 0 || says[len(says)-1] != d.Says {
			says = append(says, d.Says)
		}
		if strings.HasPrefix(d.Says, "copying ") {
			copying = append(copying, d)
		}
	}
	if want := []string{"opening Time Machine's backup", "measuring ~/Code", "measuring ~/Documents", "copying ~/Code", "comparing ~/Code", "copying ~/Documents", "comparing ~/Documents"}; !slices.Equal(says, want) {
		t.Errorf("the step said\n%q\nwant\n%q", says, want)
	}
	if len(copying) < 10 || len(copying) > 100 {
		t.Errorf("the copies said how far they'd got %d times, for 200 of rsync's lines", len(copying))
	}
	for i, d := range copying {
		if d.Of != 200 || d.Done > 200 || d.Far > 1 {
			t.Errorf("the copy said %d of %d, %.2f of the way", d.Done, d.Of, d.Far)
		}
		if i == 0 || copying[i-1].Says != d.Says {
			continue
		}
		if gap := d.at.Sub(copying[i-1].at); gap < 250*time.Millisecond {
			t.Errorf("the copy said how far it'd got %s after it last did", gap)
		}
		if d.Far < copying[i-1].Far || d.Done < copying[i-1].Done {
			t.Errorf("the copy went back, from %d (%.2f) to %d (%.2f)", copying[i-1].Done, copying[i-1].Far, d.Done, d.Far)
		}
	}
	if last := copying[len(copying)-1]; last.Far <= 0.25 || last.Done <= 100 {
		t.Errorf("copying ~/Documents, last said %d of 200, %.2f of the way: want past ~/Code's 100 files and quarter", last.Done, last.Far)
	}
}

// Files vanishing as they're copied, rsync's exit 24, are the old Mac in
// use coming and going: its copy's restored all the same. From a backup,
// where nothing comes or goes, they fail the path.
func TestRestoreFilesVanishing(t *testing.T) {
	for _, tt := range []struct {
		name string
		live bool
		want restore.State
	}{
		{"from the old Mac", true, restore.Done},
		{"from a backup", false, restore.Copying},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := newRestoring(t, "~/Code")
			c.r.Flag = steps.RestoreFrom{Kind: restore.FromTimeMachine}
			c.src.live = tt.live
			write(t, c.old, "~/Code/a", beforeIt)
			c.copies("~/Code", 2, 2_000)
			c.on(copyingOp, "~/Code").Prints(stats(2, 2_000)).PrintsToStderr("file has vanished: \"/old/Code/b\"\n").Exits(24)
			err := c.apply(t.Context())
			if got := c.record().Paths["~/Code"].State; got != tt.want || (err == nil) != tt.live {
				t.Errorf("apply = %v, ~/Code %s; want %s, failing %v", err, got, tt.want, !tt.live)
			}
		})
	}
}

// A listed file written here as it's restored, between its judging and its
// copy, newer than its source, is kept by the copy, as --update keeps any
// newer file, and its second pass sees nothing: so once copied, a file
// that differs from its source is set aside, saying so, and copied again,
// once, before it's compared and recorded; a path resumed alike.
func TestRestoreCopiesOverAFileWrittenAsItWasRestored(t *testing.T) {
	for _, tt := range []struct {
		name   string
		before func(c *restoring)
	}{
		{"its first copy", func(*restoring) {}},
		{"resumed", func(c *restoring) {
			c.change("~/.zsh_history", func(p *restore.Path) { p.State, p.From, p.Started = restore.Copying, c.src.origin, duringIt })
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := newRestoring(t, "~/.zsh_history")
			c.r.Flag = steps.RestoreFrom{Kind: restore.FromTimeMachine}
			tt.before(c)
			write(t, c.old, "~/.zsh_history", beforeIt)
			c.copies("~/.zsh_history", 1, 100)
			c.on(copyingOp, "~/.zsh_history").Prints(stats(1, 100)).Does(func() { write(t, c.home, "~/.zsh_history", duringIt.Add(48*time.Hour)) }).Then().Prints(stats(1, 100))
			c.on(differing, "~/.zsh_history").Prints(">fcst...n.. .zsh_history\n")
			if err := c.apply(t.Context()); err != nil {
				t.Fatalf("apply = %v", err)
			}
			if want := []string{"sizing ~/.zsh_history", "copying ~/.zsh_history", "differing ~/.zsh_history", "copying ~/.zsh_history", "comparing ~/.zsh_history"}; !slices.Equal(c.ran(), want) {
				t.Errorf("rsync ran %q\nwant %q", c.ran(), want)
			}
			history := c.record().Paths["~/.zsh_history"]
			if history.State != restore.Done || len(history.SetAside) != 1 || !slices.Equal(history.SetAside[0].Paths, []string{"~/.zsh_history"}) {
				t.Fatalf("~/.zsh_history's record = %+v; want it restored, the one written here set aside", history)
			}
			aside := history.SetAside[0].Dir
			if got, err := os.ReadFile(filepath.Join(aside, ".zsh_history")); err != nil || string(got) != "~/.zsh_history" {
				t.Errorf("the one written here, set aside = %q, %v", got, err)
			}
			if want := "Set aside ~/.zsh_history, written here as it was restored, into " + aside; !slices.Contains(c.logged, want) {
				t.Errorf("the log says %q, want %q", c.logged, want)
			}
		})
	}
}

// A path recorded here, holding files of its own, is never touched: unless
// kit apply names a source and it's absent now, or holds nothing but kit's,
// when it's restored, so one lost since can be brought back.
func TestRestoreBringsBackAPathRecordedHere(t *testing.T) {
	c := newRestoring(t, "~/Code", "~/Documents")
	c.r.Began = time.Time{}
	for _, p := range []string{"~/Code", "~/Documents"} {
		c.change(p, func(p *restore.Path) { p.State = restore.Here })
	}
	write(t, c.home, "~/Documents/kept", beforeIt)
	write(t, c.old, "~/Code/a", beforeIt)
	if err := c.apply(t.Context()); err != nil || len(c.src.asked) > 0 {
		t.Errorf("naming no source, apply = %v, asking for %+v; want nothing restored", err, c.src.asked)
	}
	c.r.Flag = steps.RestoreFrom{Kind: restore.FromTimeMachine}
	if res := c.step().Check(t.Context()); len(res.Items) != 1 || res.Items[0].Name != "~/Code" {
		t.Errorf("with --restore-from, the check's items = %+v; want ~/Code's alone", res.Items)
	}
	c.copies("~/Code", 1, 1_000)
	if err := c.apply(t.Context()); err != nil {
		t.Fatalf("with --restore-from, apply = %v", err)
	}
	rec := c.record()
	if rec.Paths["~/Code"].State != restore.Done || rec.Paths["~/Documents"].State != restore.Here {
		t.Errorf("the record = %+v; want ~/Code restored, ~/Documents here, untouched", rec.Paths)
	}
	if want := []string{"sizing ~/Code", "copying ~/Code", "comparing ~/Code"}; !slices.Equal(c.ran(), want) {
		t.Errorf("rsync ran %q, want %q", c.ran(), want)
	}
}
