package steps

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/restore"
	"github.com/leeovery/kit/internal/runner"
)

// RestoreName names the data restore's step, as its section is named.
const RestoreName = config.RestoreKind

// ActionRestore is what applying does about a listed path not restored.
const ActionRestore = "restore"

// rsync3 is what the restore waits for: rsync 3, as macOS's own, openrsync,
// is always on the PATH, and keeps no extended attributes.
const rsync3 = "rsync 3"

// sayEvery is how often, at most, the restore says how far its copy has
// got: rsync says so many times a second.
const sayEvery = 250 * time.Millisecond

// The restore's margin of free space, beyond what it copies: a share of
// it, and at least so much.
const (
	roomShare = 20 // a twentieth: 5%
	roomLeast = 2_000_000_000
)

// RestoreFrom is the source kit apply's flags name: Kind, one of the
// record's kinds of origin, "" for none; At, for Time Machine's, picks the
// latest backup at or before it, zero for the latest.
type RestoreFrom struct {
	Kind string
	At   time.Time
}

// Restoring is what the data restore's step works with.
type Restoring struct {
	// Lines are the Mac's [restore] lines.
	Lines []restore.Line
	// Owned reads what's kit's in the home folder, which the restore never
	// carries.
	Owned func(ctx context.Context) (restore.Owned, error)
	// Home is the home folder, and StateDir kit's state.
	Home, StateDir string
	// Began is when this Mac's set-up began (boot.Began): zero for a Mac in
	// use.
	Began time.Time
	// From is the Mac this one started from, as kit.toml says: "" for none.
	From string
	// Flag is the source kit apply's flags name.
	Flag RestoreFrom
	// Sources is the source an origin names, to open, picking Time
	// Machine's backup at a time, chosen saying whether kit apply's flags
	// named it (restore.Sources' For).
	Sources func(o restore.Origin, at time.Time, chosen bool) restore.Opener
	// Disks finds the Time Machine disks attached, which the card raised
	// when the old Mac doesn't answer offers (restore.Sources' Disks).
	Disks func(ctx context.Context) (restore.Disks, error)
	// Raise raises the restore's cards while kit applies at a terminal: nil
	// without one, where there's no one to answer them.
	Raise *Raise
	// Run runs what Arq's card opens, Arq; ArqAwait are what that card
	// waits for before it goes up, Arq installed.
	Run      runner.Runner
	ArqAwait []engine.Wait
	Rsync    restore.Rsync
	// Space is how much is free on the volume path is on, and its size, in
	// bytes (boot.Space).
	Space func(path string) (free, size uint64, err error)
	Now   func() time.Time
}

// Restore is the data restore: the paths the Mac's [restore] lines list,
// carried from the Mac it started from, or from a backup of it, what's
// kit's in them left out. Its check reads the record and what each path
// holds, contacting no source: fine once each is restored, or here, holding
// files of its own before anything was restored to it; else each says
// what's to come. A path its source hadn't, or, on a Mac whose restore has
// settled, a line added since, its path absent, waits for kit apply to
// name a source.
//
// Applying, it judges each path, records here what's here, and opens the
// source: kit apply's flags', else the one the restore began from, so a
// run again resumes from it, never a newer backup, else the Mac this one
// started from. Flags naming another source than a path part-way came
// from are refused, unless that source has gone, as a backup thinned away
// has: the flags' then stands in for it, the paths carrying on from it,
// the record and the log saying so. A path the source hasn't, nothing
// copied to it, is recorded not there. With room for the copies, each path
// is judged again as its turn comes, then copied, what set-up wrote in it
// set aside first, then compared with its source: from a backup, a file
// found differing fails the path; from a live Mac, what changed as it was
// copied is copied again, and counted. The record says where each path
// stands throughout, and, done, what it holds, so a run stopped resumes;
// the restore settles once it's finished with every path. The source is
// closed however applying ends. Applied whenever kit apply runs, it
// records what it finds even when there's nothing to copy.
func Restore(r Restoring) engine.Step {
	return engine.Step{
		Name: RestoreName, Title: "Data restore", Area: AreaBackups, Row: rowBackups, Verb: "restored", AlwaysApply: true,
		Waits: []engine.Wait{{Thing: rsync3, Ready: func(ctx context.Context) bool {
			found, err := r.Rsync.Find(ctx)
			return err == nil && found.Usable
		}}},
		Check: func(ctx context.Context) check.Result {
			js, rec, err := r.judged(ctx)
			if err != nil {
				return failed(err)
			}
			return r.result(js, rec)
		},
		Apply: r.apply,
	}
}

// judged is a listed path judged, and its filter.
type judged struct {
	restore.Judgement
	line   restore.Line
	filter *restore.Filter
}

// judged judges each listed path by the record, read as it stands, and
// what the path holds: the restore's first run as applying records it, if
// it's yet to.
func (r Restoring) judged(ctx context.Context) ([]judged, restore.Record, error) {
	owned, err := r.Owned(ctx)
	if err != nil {
		return nil, restore.Record{}, err
	}
	rec, err := restore.LoadRecord(r.StateDir)
	if err != nil {
		return nil, rec, err
	}
	if rec.First.Time.IsZero() {
		rec.First = restore.Run{Time: r.Now(), Paths: r.paths()}
	}
	out := make([]judged, 0, len(r.Lines))
	for _, l := range r.Lines {
		f, err := restore.NewFilter(l, owned)
		if err != nil {
			return nil, rec, err
		}
		j, err := restore.Judge(r.Home, f, rec, r.Began)
		if err != nil {
			return nil, rec, err
		}
		out = append(out, judged{Judgement: j, line: l, filter: f})
	}
	return out, rec, nil
}

// paths are the listed paths, in order.
func (r Restoring) paths() []string {
	paths := make([]string, len(r.Lines))
	for i, l := range r.Lines {
		paths[i] = l.Path
	}
	return paths
}

// waitsForFlag reports whether j's path, absent and never restored, waits
// for kit apply to name a source: on a Mac whose restore has settled, as a
// line added since has it; or its source found without it.
func (r Restoring) waitsForFlag(j restore.Judgement, rec restore.Record) bool {
	return j.Verdict == restore.VerdictRestore && r.Flag.Kind == "" && (!rec.Settled.IsZero() || rec.Paths[j.Path].State == restore.NotThere)
}

// result is how the restore stands, js judged: an item for each path to
// restore, saying what's to come; and, quiet, for each waiting for kit
// apply to name a source, what would bring it.
func (r Restoring) result(js []judged, rec restore.Record) check.Result {
	var items, waiting []check.Item
	restored, here := 0, 0
	for _, j := range js {
		switch {
		case j.Verdict == restore.VerdictDone:
			restored++
		case j.Verdict == restore.VerdictHere:
			here++
		case r.waitsForFlag(j.Judgement, rec):
			waiting = append(waiting, awaitingSource(j.Path, rec.Paths[j.Path]))
		default:
			items = append(items, toRestore(j.Judgement, rec))
		}
	}
	res := check.Result{State: check.OK, Items: slices.Concat(items, waiting), Counts: map[string]int{"declared": len(js), "restored": restored, "here": here}}
	if len(items) > 0 {
		res.State, res.Counts["to restore"] = check.Attention, len(items)
	}
	res.Summary = restoreSummary(len(js), map[string]int{"restored": restored, "here already": here, "to restore": len(items), notRestored: len(waiting)})
	return res
}

// notRestored is the state of a listed path never restored, on a Mac whose
// restore has settled, or its source found without it.
const notRestored = "not restored"

// awaitingSource is the quiet item of path, absent, never restored, p its
// record, waiting for kit apply to name a source: its source found without
// it, or listed since the restore settled.
func awaitingSource(path string, p restore.Path) check.Item {
	it := check.Item{ID: RestoreName + ":" + path, Name: path, State: notRestored, Detail: "kit apply --restore-from time-machine|arq brings it", Quiet: "listed since the restore settled"}
	if p.State == restore.NotThere {
		it.Detail, it.Quiet = p.From.Says()+" hadn't it; kit apply --restore-from time-machine|arq brings it from another", "not in its source"
	}
	return it
}

// restoreSummary says how the restore stands: of declared paths, how many
// are restored, here already, to restore and not restored, by those words.
func restoreSummary(declared int, counts map[string]int) string {
	switch declared {
	case counts["restored"]:
		return fmt.Sprintf("%d declared, all restored", declared)
	case counts["here already"]:
		return fmt.Sprintf("%d declared, all here already", declared)
	}
	parts := []string{fmt.Sprintf("%d declared", declared)}
	for _, says := range []string{"restored", "here already", "to restore", notRestored} {
		if n := counts[says]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, says))
		}
	}
	return strings.Join(parts, ", ")
}

// toRestore is the item of j's path, to restore: what set-up wrote in it
// set aside first, or its copy resumed, saying so.
func toRestore(j restore.Judgement, rec restore.Record) check.Item {
	it := check.Item{ID: RestoreName + ":" + j.Path, Name: j.Path, State: "missing", Action: ActionRestore}
	switch p := rec.Paths[j.Path]; j.Verdict {
	case restore.VerdictSetAside:
		if len(j.New) == 1 && j.New[0] == j.Path {
			it.Detail = "set-up's copy set aside first"
			break
		}
		it.Detail = fmt.Sprintf("%d %s set-up wrote in it set aside first", len(j.New), plural(len(j.New), "file", "files"))
	case restore.VerdictResume:
		switch {
		case p.State == restore.Checking && p.Second != nil:
			it.Detail = fmt.Sprintf("%d %s differed from %s, copied again; compared again next run", p.Second.Copied, plural(p.Second.Copied, "file", "files"), p.From.Says())
		case p.State == restore.Checking:
			it.Detail = "copied from " + p.From.Says() + ", not yet compared with it"
		default:
			it.Detail = "part-way from " + p.From.Says()
		}
	}
	return it
}

// apply restores what's to restore, as Restore says.
func (r Restoring) apply(ctx context.Context, _ check.Result) error {
	if err := restore.RecordFirstRun(r.StateDir, r.Now(), r.paths()); err != nil {
		return err
	}
	js, rec, err := r.judged(ctx)
	if err != nil {
		return err
	}
	var todo []judged
	for _, j := range js {
		switch {
		case j.Verdict == restore.VerdictHere:
			err = errors.Join(err, r.recordHere(ctx, j.Path, rec))
		case j.Verdict == restore.VerdictDone, r.waitsForFlag(j.Judgement, rec):
		default:
			todo = append(todo, j)
		}
	}
	if len(todo) > 0 {
		err = errors.Join(err, r.restoreAll(ctx, rec, todo))
	}
	return errors.Join(err, r.settle())
}

// recordHere records path here, for good, unless the record says so: its
// holding folder of Arq's, if it had one, goes.
func (r Restoring) recordHere(ctx context.Context, path string, rec restore.Record) error {
	if rec.Paths[path].State == restore.Here {
		return nil
	}
	var held int
	err := r.change(path, func(p *restore.Path) { p.State, held, p.Holding = restore.Here, p.Holding, 0 })
	if err != nil {
		return err
	}
	event.Log(ctx, event.Logged{Line: "Left " + path + " as it is: it held files of its own"})
	return r.letGo(path, held, true)
}

// settle settles the restore, once it's finished with every listed path.
func (r Restoring) settle() error {
	rec, err := restore.LoadRecord(r.StateDir)
	if err != nil {
		return err
	}
	for _, path := range r.paths() {
		if !rec.Finished(path) {
			return nil
		}
	}
	return restore.Settle(r.StateDir, r.Now())
}

// restoreAll restores todo's paths from the source it chooses, opened
// once, and closed however it ends: each path in turn, one failing leaving
// the rest to go on, till kit's stopped.
func (r Restoring) restoreAll(ctx context.Context, rec restore.Record, todo []judged) (err error) {
	want, at, err := r.source(rec, todo)
	if err != nil {
		return err
	}
	src, err := r.open(ctx, want, at, todo)
	if err != nil {
		return fmt.Errorf("couldn't restore: %w", err)
	}
	defer func() { err = errors.Join(err, src.Close(ctx)) }()
	if err := partWay(rec, todo, src.Origin, src.Replaces); err != nil {
		return err
	}
	if err := r.recordSource(ctx, todo, src); err != nil {
		return err
	}
	seen, err := lookAt(ctx, src, todo)
	if err != nil {
		return fmt.Errorf("couldn't restore: %w", err)
	}
	copies, errs := r.plan(ctx, src, todo, seen)
	if err := r.room(src, copies); err != nil {
		return errors.Join(errs, fmt.Errorf("couldn't restore: %w", err))
	}
	p := newProgress(ctx, r.Now, copies)
	for _, c := range copies {
		if ctx.Err() != nil {
			return errors.Join(errs, ctx.Err())
		}
		errs = errors.Join(errs, r.restoreOne(ctx, src, c, p))
		p.next(c)
	}
	return errs
}

// lookAt is what's at each of todo's paths at src, a source on another
// Mac, by path, in one look: nil for a source on this Mac, whose paths are
// each looked at as they're planned (Stat).
func lookAt(ctx context.Context, src *restore.Opened, todo []judged) (map[string]restore.There, error) {
	if src.Look == nil {
		return nil, nil
	}
	paths := make([]string, len(todo))
	for i, j := range todo {
		paths[i] = j.Path
	}
	event.Say(ctx, event.Doing{Says: "looking at the listed paths on " + src.Origin.Says()})
	return src.Look(ctx, paths)
}

// source is where the restore copies from, and, for Time Machine's
// backups, the time picking one: kit apply's flags' source, from where the
// listed paths part-way came, should they agree; else from there, never a
// newer backup mid-restore; else the record's, the source the last run
// chose, so a run again resumes from it; else the Mac this one started
// from. Flags naming another kind of source than one of todo's paths
// part-way came from are an error naming it.
func (r Restoring) source(rec restore.Record, todo []judged) (restore.Origin, time.Time, error) {
	resuming, _ := rec.Resuming(r.Lines)
	switch {
	case r.Flag.Kind != "":
		if resuming.Kind == r.Flag.Kind {
			return resuming, r.Flag.At, nil
		}
		return restore.Origin{Kind: r.Flag.Kind}, r.Flag.At, partWay(rec, todo, restore.Origin{Kind: r.Flag.Kind}, restore.Origin{})
	case resuming.Kind != "":
		return resuming, time.Time{}, nil
	case rec.From.Kind != "":
		return rec.From, time.Time{}, nil
	}
	return restore.Origin{Kind: restore.FromMac, Detail: r.From}, time.Time{}, nil
}

// OverSSH reports whether the restore copies from a Mac over SSH, as
// things stand: the one this one started from, kit apply's flags naming no
// source, and no listed path part-way from, nor the last run's source,
// another; or the one a listed path's part-way from.
func (r Restoring) OverSSH() (bool, error) {
	rec, err := restore.LoadRecord(r.StateDir)
	if err != nil {
		return false, err
	}
	from, _, err := r.source(rec, nil)
	return from.Kind == restore.FromMac && from.Detail != "", err
}

// partWay is an error naming the first of todo's paths whose copy is
// part-way from somewhere other than from: a kind of source, or, its
// detail given, the very one, or the one it stands in for, gone.
func partWay(rec restore.Record, todo []judged, from, standsIn restore.Origin) error {
	for _, j := range todo {
		was := rec.Paths[j.Path].From
		if j.Verdict != restore.VerdictResume || was.Kind == from.Kind && (from.Detail == "" || was.Detail == from.Detail || was == standsIn) {
			continue
		}
		return fmt.Errorf("couldn't restore: %s is part-way from %s, not %s: kit apply without --restore-from or --at carries on from there", j.Path, was.Says(), from.Says())
	}
	return nil
}

// recordSource records src as the source the restore copies from; and,
// when it stands in for one gone, as the source of todo's paths part-way
// from that one, so a run again carries on from it, saying so.
func (r Restoring) recordSource(ctx context.Context, todo []judged, src *restore.Opened) error {
	moved := false
	err := restore.UpdateRecord(r.StateDir, func(rec *restore.Record) {
		rec.From = src.Origin
		if src.Replaces == (restore.Origin{}) {
			return
		}
		for _, j := range todo {
			if j.Verdict == restore.VerdictResume && rec.Paths[j.Path].From == src.Replaces {
				rec.Change(j.Path, func(p *restore.Path) { p.From = src.Origin })
				moved = true
			}
		}
	})
	if err == nil && moved {
		event.Log(ctx, event.Logged{Line: fmt.Sprintf("Carrying on from %s: %s, which the restore began from, has gone", src.Origin.Says(), src.Replaces.Says())})
	}
	return err
}

// copying is a path to copy: its judgement, the copy, and what it comes to.
type copying struct {
	judged
	copy restore.Copy
	size restore.Stats
}

// plan readies the copy of each of todo's paths from src, what's at each
// as seen says, else as src's Stat finds it, its rules written for rsync,
// and finds what it comes to; a path that can't be copied is left out, its
// error among errs. A path src hasn't, nothing copied to it yet, is
// recorded not there, as the old Mac never made it: the restore's done
// with it.
func (r Restoring) plan(ctx context.Context, src *restore.Opened, todo []judged, seen map[string]restore.There) (copies []copying, errs error) {
	for _, j := range todo {
		c, err := r.planOne(ctx, src, j, seen)
		switch {
		case errors.Is(err, errNotThere) && j.Verdict != restore.VerdictResume:
			errs = errors.Join(errs, r.recordNotThere(ctx, j.Path, src.Origin))
		case errors.Is(err, errNotThere):
			errs = errors.Join(errs, fmt.Errorf("couldn't restore %s: %s hasn't it", j.Path, src.Origin.Says()))
		case err != nil:
			errs = errors.Join(errs, fmt.Errorf("couldn't restore %s: %w", j.Path, err))
		default:
			copies = append(copies, c)
		}
	}
	return copies, errs
}

// errNotThere is a path its source hasn't.
var errNotThere = errors.New("not in the source")

// recordNotThere records path not there, from, its source, saying so:
// its holding folder of Arq's, empty, goes.
func (r Restoring) recordNotThere(ctx context.Context, path string, from restore.Origin) error {
	var held int
	err := r.change(path, func(p *restore.Path) {
		p.State, p.From, held, p.Holding = restore.NotThere, from, p.Holding, 0
	})
	if err != nil {
		return err
	}
	event.Log(ctx, event.Logged{Line: fmt.Sprintf("Nothing to restore in %s: %s hasn't it", path, from.Says())})
	return r.letGo(path, held, false)
}

// letGo deletes path's holding folder, numbered n, of Arq's, if it had one,
// as the restore's done with it: whole, with Arq's restore of it in it, or
// only should it hold nothing.
func (r Restoring) letGo(path string, n int, whole bool) error {
	if n == 0 {
		return nil
	}
	hold := restore.Holding(r.StateDir, n)
	if held, err := restore.Holds(hold, path); !whole && (err != nil || held) {
		return nil
	}
	return os.RemoveAll(hold)
}

// planOne readies the copy of j's path from src, what's there as seen says,
// else as src's Stat finds it: errNotThere when src hasn't it.
func (r Restoring) planOne(ctx context.Context, src *restore.Opened, j judged, seen map[string]restore.There) (copying, error) {
	there, err := thereAt(src, seen, j.Path)
	switch {
	case err != nil:
		return copying{}, err
	case there.Kind == restore.Missing:
		return copying{}, errNotThere
	}
	f, err := j.filter.With(there.Skips)
	if err != nil {
		return copying{}, err
	}
	j.filter = f
	filter := j.line.FilterFile(r.StateDir)
	if err := os.MkdirAll(filepath.Dir(filter), 0o700); err != nil {
		return copying{}, err
	}
	if err := j.filter.Write(filter); err != nil {
		return copying{}, err
	}
	c := copying{judged: j, copy: restore.Copy{
		From: src.Source(j.Path), To: filepath.Join(r.Home, strings.TrimPrefix(j.Path, "~/")), File: there.Kind == restore.File,
		Filter: filter, Partial: j.line.Partial(r.StateDir),
	}}
	event.Say(ctx, event.Doing{Says: "measuring " + j.Path})
	c.size, err = r.Rsync.Size(quietly(ctx), c.copy)
	return c, err
}

// thereAt is what's at path at src: as seen, the look at a source on
// another Mac, says, when there's one; else as src's Stat finds it.
func thereAt(src *restore.Opened, seen map[string]restore.There, path string) (restore.There, error) {
	if seen != nil {
		return seen[path], nil
	}
	return src.Stat(path)
}

// room is an error unless the home folder's volume has room for copies
// from src, and a margin: for them all, as each copy stays; or, from a
// source each of whose paths goes once it's restored (Frees), already
// taking its room on the disk, for the largest, as each goes before the
// next is copied.
func (r Restoring) room(src *restore.Opened, copies []copying) error {
	var total, largest int64
	for _, c := range copies {
		total += c.size.Bytes
		largest = max(largest, c.size.Bytes)
	}
	if total == 0 {
		return nil
	}
	copied, says := total, "to copy"
	if src.Frees {
		copied, says = largest, "copied at most at once, as each holding folder goes once it's restored,"
	}
	need := copied + max(copied/roomShare, roomLeast)
	free, _, err := r.Space(r.Home)
	if err != nil {
		return fmt.Errorf("find the free space: %w", err)
	}
	if int64(free) < need {
		return fmt.Errorf("%s %s needs %s free, with a margin, and the disk has %s", size(copied), says, size(need), size(int64(free)))
	}
	return nil
}

// restoreOne restores c's path from src, judged again first, as what it
// holds can change while the paths before it copy: holding older files
// now, it's here, left as it is; else what set-up has written in it by now
// is set aside. The record says where it stands throughout: copying;
// checking, and, a second pass that found differences, how many; done,
// with what it holds, once its second pass has passed. Its partial files
// go once it's done.
func (r Restoring) restoreOne(ctx context.Context, src *restore.Opened, c copying, p *progress) error {
	failed := func(err error) error { return fmt.Errorf("couldn't restore %s: %w", c.Path, err) }
	rec, err := restore.LoadRecord(r.StateDir)
	if err != nil {
		return failed(err)
	}
	j, err := restore.Judge(r.Home, c.filter, rec, r.Began)
	if err != nil {
		return failed(err)
	}
	if j.Verdict == restore.VerdictHere {
		return r.recordHere(ctx, c.Path, rec)
	}
	// What came before it may not have freed its room, as a path failing
	// keeps its holding folder.
	if src.Frees {
		if err := r.room(src, []copying{c}); err != nil {
			return failed(err)
		}
	}
	if j.Verdict == restore.VerdictSetAside {
		if err := r.setAside(ctx, j); err != nil {
			return failed(err)
		}
	}
	err = r.change(c.Path, func(rp *restore.Path) {
		rp.State, rp.From = restore.Copying, src.Origin
		if rp.Started.IsZero() {
			rp.Started = r.Now()
		}
	})
	if err != nil {
		return failed(err)
	}
	p.start(c, "copying")
	stats, err := r.Rsync.Copy(quietly(ctx), c.copy, p.copied)
	if copyFailed(src, err) {
		return failed(err)
	}
	if err := r.change(c.Path, func(rp *restore.Path) { rp.State = restore.Checking }); err != nil {
		return failed(err)
	}
	p.start(c, "comparing")
	again, err := r.Rsync.Compare(quietly(ctx), c.copy)
	if copyFailed(src, err) {
		return failed(fmt.Errorf("its second pass: %w", err))
	}
	if !src.Live && again.Count > 0 {
		differs := fmt.Errorf("its second pass found %d %s differing from %s: %s", again.Count, plural(again.Count, "file", "files"), src.Origin.Says(), recopied(again))
		return failed(errors.Join(differs, r.change(c.Path, func(rp *restore.Path) { rp.Second = &restore.Pass{Copied: again.Count} })))
	}
	var held int
	err = r.change(c.Path, func(rp *restore.Path) {
		rp.State, rp.Files, rp.Bytes, rp.Finished = restore.Done, stats.TotalFiles, stats.TotalBytes, r.Now()
		rp.Second, held, rp.Holding = &restore.Pass{Copied: again.Count}, rp.Holding, 0
	})
	if err != nil {
		return failed(err)
	}
	line := fmt.Sprintf("Restored %s: %d %s, %s", c.Path, stats.TotalFiles, plural(stats.TotalFiles, "file", "files"), size(stats.TotalBytes))
	if again.Count > 0 {
		line += fmt.Sprintf("; %d copied again, changed as they were copied", again.Count)
	}
	event.Log(ctx, event.Logged{Line: line})
	if err := errors.Join(os.RemoveAll(c.copy.Partial), r.letGo(c.Path, held, true)); err != nil {
		return failed(err)
	}
	return nil
}

// copyFailed reports whether err fails a copy from src: any error but
// files vanished as they were copied from a live Mac, where files come and
// go.
func copyFailed(src *restore.Opened, err error) bool {
	return err != nil && (!src.Live || !errors.Is(err, restore.ErrVanished))
}

// setAside sets aside what set-up wrote in j's path, saying where.
func (r Restoring) setAside(ctx context.Context, j restore.Judgement) error {
	if err := restore.SetAside(r.StateDir, r.Home, j, r.Now()); err != nil {
		return err
	}
	rec, err := restore.LoadRecord(r.StateDir)
	if err != nil {
		return err
	}
	if asides := rec.Paths[j.Path].SetAside; len(asides) > 0 {
		event.Log(ctx, event.Logged{Line: fmt.Sprintf("Set aside what set-up wrote in %s, into %s", j.Path, r.tilde(asides[len(asides)-1].Dir))})
	}
	return nil
}

// change changes the record of the listed path path.
func (r Restoring) change(path string, change func(*restore.Path)) error {
	return restore.UpdateRecord(r.StateDir, func(rec *restore.Record) { rec.Change(path, change) })
}

// quietly is ctx for running rsync: its lines, a file each, and millions,
// are never the step's output, as a command's lines are while it changes
// things; the step says how far it's got itself.
func quietly(ctx context.Context) context.Context {
	return event.WithChecking(ctx)
}

// recopied names what a second pass copied again, as many as it says, and
// how many more.
func recopied(again restore.Recopied) string {
	named := strings.Join(again.Paths, ", ")
	if more := again.Count - len(again.Paths); more > 0 {
		named += fmt.Sprintf(" and %d more", more)
	}
	return named
}

// size is n bytes, as kit says it: in kB, MB or GB, a thousand each.
func size(n int64) string {
	switch {
	case n >= 1e9:
		return fmt.Sprintf("%.1f GB", float64(n)/1e9)
	case n >= 1e6:
		return fmt.Sprintf("%.1f MB", float64(n)/1e6)
	}
	return fmt.Sprintf("%d kB", (n+999)/1000)
}

// progress says how far the restore's copies have got, all of them
// together, as rsync says each has: what it's doing to which path, the
// files copied of those to copy, and, the bar, the bytes; never more often
// than every sayEvery, but as each path's copy or comparison starts.
type progress struct {
	ctx context.Context
	now func() time.Time

	mu sync.Mutex
	// files and bytes are what all the copies come to; doneFiles and
	// doneBytes, those of the paths done with.
	files, doneFiles int
	bytes, doneBytes int64
	// doing is what it's doing, as in "copying ~/Code"; at, how far the
	// copy under way has got, never past size, what it comes to, so the
	// count never goes back as the next starts; said, when it last said so.
	doing string
	at    restore.Progress
	size  restore.Stats
	said  time.Time
}

// newProgress is how far copies have got, nothing copied yet.
func newProgress(ctx context.Context, now func() time.Time, copies []copying) *progress {
	p := &progress{ctx: ctx, now: now}
	for _, c := range copies {
		p.files += c.size.Files
		p.bytes += c.size.Bytes
	}
	return p
}

// start says what's under way: doing, to c's path.
func (p *progress) start(c copying, doing string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.doing, p.size = doing+" "+c.Path, c.size
	if doing == "copying" {
		p.at = restore.Progress{}
	}
	p.say()
}

// copied is how far the copy under way has got, as rsync says it: said
// once sayEvery has passed since it was last said.
func (p *progress) copied(at restore.Progress) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.at = restore.Progress{Files: min(at.Files, p.size.Files), Bytes: min(at.Bytes, p.size.Bytes)}
	if p.now().Sub(p.said) >= sayEvery {
		p.say()
	}
}

// next moves on from c's path, done with however it went.
func (p *progress) next(c copying) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.doneFiles += c.size.Files
	p.doneBytes += c.size.Bytes
	p.at = restore.Progress{}
}

// say says how far the copies have got. p.mu must be held.
func (p *progress) say() {
	p.said = p.now()
	d := event.Doing{Says: p.doing}
	if p.files > 0 {
		d.Done, d.Of = p.doneFiles+p.at.Files, p.files
	}
	if p.bytes > 0 {
		d.Far = float64(p.doneBytes+p.at.Bytes) / float64(p.bytes)
	}
	event.Say(p.ctx, d)
}
