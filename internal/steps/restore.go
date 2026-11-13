package steps

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
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
// record's kinds of origin, the old Mac's the one this one started from,
// "" for none; At, for Time Machine's, picks the latest backup at or
// before it, zero for the latest.
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
	// SetUp is whether the boot set this Mac up (boot.SetUp): false for a
	// Mac in use.
	SetUp bool
	// Mac is this Mac's name, and From the Mac it started from, as kit.toml
	// says: "" for none.
	Mac, From string
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
// carried from the Mac it started from, or from a backup of it, or, started
// from none, from its own backups, what's kit's in them left out. Its check
// reads the record and what each path holds, contacting no source: fine
// once each is restored, or here, holding files of its own before anything
// was restored to it; else each says what's to come. A path its source hadn't, or, on a Mac whose restore has
// settled, a line added since, its path absent, waits for kit apply to
// name a source.
//
// Applying, it judges each path, records here what's here, and copies the
// rest, each from its source: all from the one kit apply's flags name,
// standing in for whatever source a path part-way began from, as the one
// chosen on the card does for the old Mac not answering, each path
// carrying on from it once its own copy begins, the record and the log
// saying so. The flags naming none, a path part-way carries on from the
// one it began from, never a newer backup; the rest copy from the first
// such path's, should there be one, else the one a copy last began from,
// so a run again resumes from it, else the Mac this one started from, or,
// started from none, the one of its own backups the person chooses.
// Arq's restore, by hand, comes last, taking in the
// paths the card sends to it, so its card goes up once a run. A path a
// source hasn't, nothing copied to it, is recorded not there; part-way, it
// fails, keeping the source it began from, the error naming the ways on:
// another source, or its line dropped, as one for a source that won't open
// names them for all its paths. With room for the copies, each path is
// judged again as its turn comes, then copied, what set-up found in it set
// aside first, then compared with its source: from a backup, a file found
// differing fails the path; from a live Mac, what changed as it was copied
// is copied again, and counted. A file the copy kept, newer here, in
// set-up's window before the path's copy first began, is set aside and
// copied again; any other is kept. The record says where each path stands
// throughout, and, done, what it holds, so a run stopped resumes; the
// restore settles once it's finished with every path. Each source is
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

// RestoreNothing is the data restore on a Mac in set-up whose [restore]
// lists nothing, kit's state in stateDir: fine, nothing listed; applied, it
// records the restore's first run, listing nothing, and settles it, closing
// set-up's window, so a line added later waits for kit apply to name a
// source, never setting aside what was written since set-up began.
func RestoreNothing(stateDir string, now func() time.Time) engine.Step {
	return engine.Step{
		Name: RestoreName, Title: "Data restore", Area: AreaBackups, Row: rowBackups, Verb: "restored", AlwaysApply: true,
		Check: func(context.Context) check.Result {
			return check.Result{State: check.OK, Summary: "nothing listed", Counts: map[string]int{"declared": 0}}
		},
		Apply: func(context.Context, check.Result) error {
			return errors.Join(restore.RecordFirstRun(stateDir, now(), nil), restore.Settle(stateDir, now()))
		},
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
		j, err := r.judge(f, rec)
		if err != nil {
			return nil, rec, err
		}
		out = append(out, judged{Judgement: j, line: l, filter: f})
	}
	return out, rec, nil
}

// judge judges f's line's path, as restore.Judge does: but, kit apply's
// flags naming a source, a path recorded here that's absent now, or holds
// nothing but kit's, is restored, so one lost since it was recorded can be
// brought back.
func (r Restoring) judge(f *restore.Filter, rec restore.Record) (restore.Judgement, error) {
	j, err := restore.Judge(r.Home, f, rec, r.SetUp)
	if err != nil || j.Verdict != restore.VerdictHere || r.Flag.Kind == "" || rec.Paths[j.Path].State != restore.Here {
		return j, err
	}
	afresh := rec
	afresh.Paths = maps.Clone(rec.Paths)
	delete(afresh.Paths, j.Path)
	again, err := restore.Judge(r.Home, f, afresh, r.SetUp)
	if err != nil || again.Verdict != restore.VerdictRestore {
		return j, err
	}
	return again, nil
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
			waiting = append(waiting, r.awaitingSource(j.Path, rec.Paths[j.Path]))
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
func (r Restoring) awaitingSource(path string, p restore.Path) check.Item {
	it := check.Item{ID: RestoreName + ":" + path, Name: path, State: notRestored, Detail: r.bringsIt("") + " brings it", Quiet: "listed since the restore settled"}
	switch {
	case p.State == restore.NotThere && p.From == ownBackups:
		it.Detail, it.Quiet = "nothing restored, as asked; "+r.bringsIt("")+" brings it", "nothing restored, as asked"
	case p.State == restore.NotThere:
		it.Detail, it.Quiet = p.From.Says()+" hadn't it; "+r.bringsIt(p.From.Kind)+" brings it from another", "not in its source"
	}
	return it
}

// bringsIt is kit apply's flag naming each source that could bring a path
// a source of kind hadn't: the old Mac, only for a Mac started from one,
// and not when it was the old Mac; a backup, Time Machine's or Arq's,
// always, as another backup can hold it.
func (r Restoring) bringsIt(hadnt string) string {
	sources := []string{restore.FromTimeMachine, restore.FromArq}
	if r.From != "" && hadnt != restore.FromMac {
		sources = slices.Insert(sources, 0, restore.FromMac)
	}
	return "kit apply --restore-from " + strings.Join(sources, "|")
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

// toRestore is the item of j's path, to restore: what's there set aside
// first, or its copy resumed, saying so.
func toRestore(j restore.Judgement, rec restore.Record) check.Item {
	it := check.Item{ID: RestoreName + ":" + j.Path, Name: j.Path, State: "missing", Action: ActionRestore}
	switch p := rec.Paths[j.Path]; j.Verdict {
	case restore.VerdictSetAside:
		it.Detail = "what's here set aside first"
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
	err := r.change(path, func(p *restore.Path) { p.State, held = restore.Here, p.Holding })
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

// restoreAll restores todo's paths, each from its source (sources), one
// source after another, each opened once, and closed however it ends; a
// path failing leaves the rest to go on, till kit's stopped. The paths of
// an old Mac not answering that the person sends to Arq on its card join
// Arq's restore (toArq).
func (r Restoring) restoreAll(ctx context.Context, rec restore.Record, todo []judged) error {
	sources := r.sources(rec, todo)
	var errs error
	for len(sources) > 0 {
		if ctx.Err() != nil {
			return errors.Join(errs, ctx.Err())
		}
		s := sources[0]
		sources = sources[1:]
		err := r.restoreFrom(ctx, s)
		if errors.Is(err, errToArq) {
			sources, err = toArq(sources, s.todo), nil
		}
		errs = errors.Join(errs, err)
	}
	return errs
}

// errToArq is the old Mac not answering, the person choosing Arq on its
// card; errNothing, the person choosing on the own-backups card to restore
// nothing.
var (
	errToArq   = errors.New("restoring through Arq instead")
	errNothing = errors.New("restoring nothing")
)

// toArq is sources, those still to restore from, with todo, the paths of
// an old Mac the person chose Arq for on its card: in Arq's restore still
// to come, so its card goes up once a run; else in one of its own.
func toArq(sources []sourced, todo []judged) []sourced {
	i := slices.IndexFunc(sources, func(s sourced) bool { return s.want.Kind == restore.FromArq })
	if i < 0 {
		return append(sources, sourced{want: restore.Origin{Kind: restore.FromArq}, todo: todo})
	}
	sources[i].todo = append(sources[i].todo, todo...)
	return sources
}

// restoreFrom restores s's paths from its source, opened once, and closed
// however it ends: each path in turn, one failing leaving the rest to go
// on, till kit's stopped. The person choosing on the own-backups card to
// restore nothing, none is (keepAsIs).
func (r Restoring) restoreFrom(ctx context.Context, s sourced) (err error) {
	src, todo, failed, err := r.open(ctx, &s)
	switch {
	case errors.Is(err, errToArq):
		return err
	case errors.Is(err, errNothing):
		return r.keepAsIs(ctx, s.todo)
	case err != nil:
		return r.unopened(ctx, s, err)
	}
	defer func() { err = errors.Join(err, src.Close(ctx)) }()
	seen, err := lookAt(ctx, src, todo)
	if err != nil {
		return errors.Join(failed, fmt.Errorf("couldn't restore: %w", err))
	}
	copies, errs := r.plan(ctx, src, todo, seen)
	errs = errors.Join(failed, errs)
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
	event.Say(ctx, event.Doing{Says: "looking at the listed paths on " + src.Origin.Says()})
	return src.Look(ctx, pathsOf(todo))
}

// pathed is what has a listed path.
type pathed interface{ pathOf() string }

func (j judged) pathOf() string { return j.Path }

// pathsOf are each of items' listed paths, in order.
func pathsOf[T pathed](items []T) []string {
	paths := make([]string, len(items))
	for i, it := range items {
		paths[i] = it.pathOf()
	}
	return paths
}

// sourced is a source the restore copies from, and the paths it copies
// from it (todo): want, to open; at, picking the backup.
type sourced struct {
	want restore.Origin
	at   time.Time
	todo []judged
}

// sources are where todo's paths are copied from, a source for each, in
// their lines' order, Arq's restore, by hand, last (arqLast): kit apply's
// flags naming one, all from the restore's main source (mainSource),
// standing in for any a path's part-way from; else a path part-way from
// where it began, and the rest from the main source.
func (r Restoring) sources(rec restore.Record, todo []judged) []sourced {
	main := r.mainSource(rec)
	var out []sourced
	for _, j := range todo {
		s := main
		if r.Flag.Kind == "" && j.Verdict == restore.VerdictResume {
			s = sourced{want: rec.Paths[j.Path].From}
		}
		i := slices.IndexFunc(out, func(o sourced) bool { return o.want == s.want })
		if i < 0 {
			out, i = append(out, s), len(out)
		}
		out[i].todo = append(out[i].todo, j)
	}
	slices.SortStableFunc(out, func(a, b sourced) int { return cmp.Compare(arqLast(a), arqLast(b)) })
	return out
}

// arqLast orders Arq's restore, by hand, after the sources that ask
// nothing, so the paths the person sends to it on the old Mac's card join
// it (toArq).
func arqLast(s sourced) int {
	if s.want.Kind == restore.FromArq {
		return 1
	}
	return 0
}

// ownBackups is the source of a Mac started from no other, clean-installed
// under its own name: its own backups, the one to copy from chosen on a
// card (ownBackup). Never recorded: the record keeps the backup chosen.
var ownBackups = restore.Origin{}

// mainSource is where the restore copies a path that isn't part-way from:
// kit apply's flags' source, a path part-way from one of its kind's should
// there be one, so never a newer backup mid-restore; the flags naming none,
// where the first part-way path came from, else where a copy last began
// from, so a run again resumes from it; else, or the flags naming it, the
// Mac this one started from, or, started from none, its own backups.
func (r Restoring) mainSource(rec restore.Record) sourced {
	if resuming, ok := rec.Resuming(r.Lines, r.Flag.Kind); ok {
		return sourced{want: resuming, at: r.Flag.At}
	}
	switch {
	case r.Flag.Kind == "" && rec.From.Kind != "":
		return sourced{want: rec.From}
	case r.Flag.Kind == "" && r.From == "":
		return sourced{want: ownBackups}
	case r.Flag.Kind == "", r.Flag.Kind == restore.FromMac:
		return sourced{want: restore.Origin{Kind: restore.FromMac, Detail: r.From}}
	}
	return sourced{want: restore.Origin{Kind: r.Flag.Kind}, at: r.Flag.At}
}

// OverSSH reports whether the restore copies from a Mac over SSH, as
// things stand: from its main source (mainSource), or, kit apply's flags
// naming none, to carry on a listed path part-way from a Mac.
func (r Restoring) OverSSH() (bool, error) {
	rec, err := restore.LoadRecord(r.StateDir)
	if err != nil {
		return false, err
	}
	overSSH := func(o restore.Origin) bool { return o.Kind == restore.FromMac && o.Detail != "" }
	was, partWay := rec.Resuming(r.Lines, restore.FromMac)
	return overSSH(r.mainSource(rec).want) || r.Flag.Kind == "" && partWay && overSSH(was), nil
}

// unopened is the error of s's source not opening, as err says, naming the
// ways on for every one of its paths (waysOut): but not when kit's been
// stopped, ctx done, nor for a source not ready yet, as Arq not installed,
// which the step waits for.
func (r Restoring) unopened(ctx context.Context, s sourced, err error) error {
	err = fmt.Errorf("couldn't restore: %w", err)
	if ctx.Err() != nil || engine.IsNotYet(err) {
		return err
	}
	return fmt.Errorf("%w: %s", err, r.waysOut(s.want.Kind, pathsOf(s.todo)))
}

// waysOut are the ways on for paths whose source, of kind, hasn't them or
// won't open: kit apply's flags naming another restores them from it, or,
// for this Mac's own backups (ownBackups), the backup they name; or kit
// restore remove drops the line of one not wanted.
func (r Restoring) waysOut(kind string, paths []string) string {
	drop := "kit restore remove " + paths[0] + " drops its line"
	if len(paths) > 1 {
		drop = "kit restore remove drops the line of any not wanted"
	}
	from := "another source"
	if kind == ownBackups.Kind {
		from = "the backup it names"
	}
	return fmt.Sprintf("%s restores %s from %s, or %s", r.bringsIt(kind), strings.Join(paths, " and "), from, drop)
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
// error among errs; one src hasn't, as hasntIt has it.
func (r Restoring) plan(ctx context.Context, src *restore.Opened, todo []judged, seen map[string]restore.There) (copies []copying, errs error) {
	for _, j := range todo {
		c, err := r.planOne(ctx, src, j, seen)
		switch {
		case errors.Is(err, errNotThere):
			errs = errors.Join(errs, r.hasntIt(ctx, j, src.Origin))
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

// hasntIt is what becomes of j's path, its source, from, not having it:
// one whose copy never began is recorded not there, as the old Mac never
// made it, the restore done with it; one part-way fails, never recorded
// not there, as what's been copied of it came from somewhere, which can
// still finish it, the error naming the ways on (waysOut).
func (r Restoring) hasntIt(ctx context.Context, j judged, from restore.Origin) error {
	if j.Verdict == restore.VerdictResume {
		return fmt.Errorf("couldn't restore %s: %s hasn't it: %s", j.Path, from.Says(), r.waysOut(from.Kind, []string{j.Path}))
	}
	return r.recordNotThere(ctx, j.Path, from)
}

// recordNotThere records path not there, from, its source, saying so.
func (r Restoring) recordNotThere(ctx context.Context, path string, from restore.Origin) error {
	if err := r.markNotThere(path, from, false); err != nil {
		return err
	}
	event.Log(ctx, event.Logged{Line: fmt.Sprintf("Nothing to restore in %s: %s hasn't it", path, from.Says())})
	return nil
}

// keepAsIs records each of todo's paths not there, from no source, as the
// person chose on the own-backups card, saying so: nothing copied, nothing
// set aside, and each holding folder of Arq's let go whole, as nothing is to
// come from it, the restore then settling, closing set-up's window; kit
// apply --restore-from brings any of them later, Arq's afresh.
func (r Restoring) keepAsIs(ctx context.Context, todo []judged) error {
	var err error
	for _, j := range todo {
		err = errors.Join(err, r.markNotThere(j.Path, ownBackups, true))
	}
	if err == nil {
		event.Log(ctx, event.Logged{Line: "Restored nothing, as asked: " + r.Mac + " keeps what it has"})
	}
	return err
}

// markNotThere records path not there, from, its source: its holding
// folder of Arq's goes, whole, or only should it hold nothing (letGo).
func (r Restoring) markNotThere(path string, from restore.Origin, whole bool) error {
	var held int
	err := r.change(path, func(p *restore.Path) { p.State, p.From, held = restore.NotThere, from, p.Holding })
	if err != nil {
		return err
	}
	return r.letGo(path, held, whole)
}

// letGo deletes path's holding folder, numbered n, of Arq's, if it had one,
// as the restore's done with it: whole, with Arq's restore of it in it, or
// only should it hold nothing; and, the folder gone, its number. A folder
// kept keeps its number, so it's never handed to another path.
func (r Restoring) letGo(path string, n int, whole bool) error {
	if n == 0 {
		return nil
	}
	hold := restore.Holding(r.StateDir, n)
	if held, instead, err := restore.Holds(hold, path); !whole && (err != nil || held || len(instead) > 0) {
		return nil
	}
	if err := os.RemoveAll(hold); err != nil {
		return err
	}
	return r.change(path, func(p *restore.Path) { p.Holding = 0 })
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
// holds can change while the paths before it copy: here now, it's left as
// it is; else, in set-up's window, all it holds by now is set aside. A file
// the copy kept, newer here, is set aside and copied again, or kept, as
// newerHere has it. The record says where it stands throughout: copying,
// from src from then (restore.Record's Begin); checking, and, a second
// pass that found differences, how many; done, with what it holds, once
// its second pass has passed. Its partial files go once it's done.
func (r Restoring) restoreOne(ctx context.Context, src *restore.Opened, c copying, p *progress) error {
	failed := func(err error) error { return fmt.Errorf("couldn't restore %s: %w", c.Path, err) }
	rec, err := restore.LoadRecord(r.StateDir)
	if err != nil {
		return failed(err)
	}
	j, err := r.judge(c.filter, rec)
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
		aside, err := restore.SetAside(r.StateDir, r.Home, c.filter, r.Now())
		r.saidAside(ctx, "what was in "+c.Path, aside)
		if err != nil {
			return failed(err)
		}
	}
	if err := r.begin(ctx, src, c.Path); err != nil {
		return failed(err)
	}
	p.start(c, "copying")
	stats, err := r.Rsync.Copy(quietly(ctx), c.copy, p.copied)
	if copyFailed(src, err) {
		return failed(err)
	}
	if len(stats.Newer) > 0 {
		if err := r.newerHere(ctx, src, c, rec, stats.Newer); err != nil {
			return failed(err)
		}
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
		rp.Second, held = &restore.Pass{Copied: again.Count}, rp.Holding
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

// begin records path's copy from src under way (restore.Record's Begin),
// saying so should src stand in for another source it was part-way from.
func (r Restoring) begin(ctx context.Context, src *restore.Opened, path string) error {
	var was restore.Origin
	err := restore.UpdateRecord(r.StateDir, func(rec *restore.Record) { was = rec.Begin(path, src.Origin, r.Now()) })
	if err == nil && was != (restore.Origin{}) && was != src.Origin {
		event.Log(ctx, event.Logged{Line: fmt.Sprintf("Carrying on %s from %s in place of %s, which its restore began from", path, src.Origin.Says(), was.Says())})
	}
	return err
}

// newerHere is what becomes of the files of c's path its copy from src
// kept, newer here, as --update keeps them and rsync names them, newer: in
// set-up's window before the path's copy first began (restore.Record's
// MaySetAside, rec the record from before this run's copy began), as a
// shell sharing its history with every tab writes it, or a tool its state,
// between the path's setting aside and the copy reaching them, they're set
// aside, saying so, and copied again, those alone, once, saying nothing of
// its progress, which would set the copy's back; else they're kept, saying
// so, as newer work always is, as are any the copy again keeps.
func (r Restoring) newerHere(ctx context.Context, src *restore.Opened, c copying, rec restore.Record, newer []string) error {
	again, files := newerIn(c, newer)
	if len(files) == 0 || !rec.MaySetAside(c.Path, r.SetUp) {
		keptNewer(ctx, src, c, newer)
		return nil
	}
	aside, err := restore.SetAsideFiles(r.StateDir, r.Home, c.Path, files, r.Now())
	r.saidAside(ctx, writtenHere(c, len(files)), aside)
	if err != nil {
		return err
	}
	stats, err := r.Rsync.Copy(quietly(ctx), again, nil)
	if copyFailed(src, err) {
		return err
	}
	keptNewer(ctx, src, c, stats.Newer)
	return nil
}

// newerIn is the copy again of the files of c's path its copy kept as
// newer, as rsync names them, newer, and those files, each from ~: a file's,
// its copy, of the file itself; a folder's, of those rsync names in it
// alone, never one whose name would reach out of it.
func newerIn(c copying, newer []string) (restore.Copy, []string) {
	again := c.copy
	if again.File {
		return again, []string{c.Path}
	}
	var files []string
	for _, name := range newer {
		if filepath.IsLocal(name) {
			again.Only = append(again.Only, name)
			files = append(files, c.Path+"/"+name)
		}
	}
	return again, files
}

// writtenHere says what of c's path its copy kept, newer here, n files, as
// set aside: the file itself, or how many in the folder.
func writtenHere(c copying, n int) string {
	if c.copy.File {
		return c.Path + ", written here as it was restored"
	}
	return fmt.Sprintf("%d %s in %s, written here as %s restored", n, plural(n, "file", "files"), c.Path, plural(n, "it was", "they were"))
}

// keptNewer says, of c's path, that its copy from src kept the files rsync
// names newer, newer here, if any.
func keptNewer(ctx context.Context, src *restore.Opened, c copying, newer []string) {
	switch n := len(newer); {
	case n == 0:
	case c.copy.File:
		event.Log(ctx, event.Logged{Line: fmt.Sprintf("Kept %s as it is: it's newer here than in %s", c.Path, src.Origin.Says())})
	default:
		event.Log(ctx, event.Logged{Line: fmt.Sprintf("Kept %d %s in %s as %s: newer here than in %s", n, plural(n, "file", "files"), c.Path, plural(n, "it is", "they are"), src.Origin.Says())})
	}
}

// copyFailed reports whether err fails a copy from src: any error but
// files vanished as they were copied from a live Mac, where files come and
// go.
func copyFailed(src *restore.Opened, err error) bool {
	return err != nil && (!src.Live || !errors.Is(err, restore.ErrVanished))
}

// saidAside says what was set aside, what, as in "what was in ~/Code",
// and where, aside, if anything was.
func (r Restoring) saidAside(ctx context.Context, what string, aside restore.Aside) {
	if len(aside.Paths) > 0 {
		event.Log(ctx, event.Logged{Line: fmt.Sprintf("Set aside %s, into %s", what, r.tilde(aside.Dir))})
	}
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
