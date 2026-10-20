package secret

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/secret/store"
	"github.com/leeovery/kit/internal/runner"
)

// Install syncs every secret: those missing, and the rest, in one sitting.
// One that failed fails the step; one held back by its store, the rest
// synced, has it not done yet.
func (s *Secrets) Install(ctx context.Context, _ []string) error {
	report, err := s.Sync(ctx)
	switch {
	case err != nil:
		return err
	case len(report.Failed) > 0:
		return report
	case len(report.Held) > 0:
		return engine.NotYet(report.why())
	}
	return nil
}

// Report is what a sync did: how many it synced, how many of those changed,
// those that failed and those their store held back, not synced yet, each
// with why, keeping their previous values.
type Report struct {
	Synced, Changed int
	Failed, Held    map[string]string
}

func (r Report) Error() string {
	var said []string
	if len(r.Failed) > 0 {
		said = append(said, "couldn't sync "+listed(r.Failed))
	}
	if len(r.Held) > 0 {
		said = append(said, "not yet "+listed(r.Held))
	}
	return strings.Join(said, "; ") + " (each keeps its previous value)"
}

// listed is each secret of why, by name, and why.
func listed(why map[string]string) string {
	var parts []string
	for _, name := range slices.Sorted(maps.Keys(why)) {
		parts = append(parts, name+": "+why[name])
	}
	return strings.Join(parts, "; ")
}

// why is why the secrets held back aren't synced yet, each store's reason
// once.
func (r Report) why() string {
	return strings.Join(slices.Compact(slices.Sorted(maps.Values(r.Held))), "; ")
}

// Summary says what the sync did, values never shown.
func (r Report) Summary() string {
	text := fmt.Sprintf("%d synced, %d changed", r.Synced, r.Changed)
	if len(r.Failed) > 0 {
		text += fmt.Sprintf(", %d failed", len(r.Failed))
	}
	if len(r.Held) > 0 {
		text += fmt.Sprintf(", %d not yet", len(r.Held))
	}
	return text
}

// heldBack is why a store's secrets aren't synced yet, as it's signed out,
// or its program isn't there: not yet, as the run reads it (engine.NotYet),
// rather than failed.
type heldBack struct{ notYet error }

func (h heldBack) Error() string { return h.notYet.Error() }
func (h heldBack) Unwrap() error { return h.notYet }

// holdBack is reason a store's secrets aren't synced yet.
func holdBack(reason string) error { return heldBack{engine.NotYet(reason)} }

// isHeld reports whether err holds a store's secrets back, rather than
// failing them.
func isHeld(err error) bool {
	_, ok := errors.AsType[heldBack](err)
	return ok
}

// ready says whether d answers: nil for a session open already, or one it
// opens, the person approving it (store.Driver.Unlock). Signed out, it's
// held back, as signedIn says.
func (s *Secrets) ready(ctx context.Context, d store.Driver) error {
	switch d.Session(ctx, s.run) {
	case store.Open:
		return nil
	case store.SignedOut:
		return holdBack(d.About().SignedOut)
	}
	if err := d.Unlock(ctx, s.run); err != nil {
		return errors.New(d.About().NoAnswer)
	}
	return nil
}

// signedIn is nil when d is signed in, asking no one; else its secrets are
// held back, said at once in d's words.
func (s *Secrets) signedIn(ctx context.Context, d store.Driver) error {
	if d.Session(ctx, s.run) == store.SignedOut {
		return holdBack(d.About().SignedOut)
	}
	return nil
}

// signInName names the step of store d's sign-in (Cards).
func signInName(d store.Driver) string { return d.About().ID + "-sign-in" }

// Cards are the sign-ins of the stores the secrets declared are in, each a
// card kit raises, watched till its store says it's signed in, waiting for
// the store's program and app, which kit opens: syncing waits for each
// before the secrets of its store alone.
func (s *Secrets) Cards() []kind.Card {
	var out []kind.Card
	for _, d := range s.used() {
		a := d.About()
		out = append(out, kind.Card{
			Name: signInName(d), Title: a.SignIn.Title, Why: "Your secrets come from " + a.Name + ".", Steps: a.SignIn.Steps,
			Done:    func(ctx context.Context) bool { return d.Session(ctx, s.run) != store.SignedOut },
			Program: a.Program, App: a.SignIn.App, AppName: a.SignIn.AppName,
		})
	}
	return out
}

// readAll reads refs from d in as few reads as it can, as a store may have
// the person approve each: one, when every ref reads. Should that fail,
// once d answers, they're read in halves, so that a ref that won't read
// costs the rest nothing. A value that won't read is left out.
func (s *Secrets) readAll(ctx context.Context, d store.Driver, refs []string) (map[string]string, error) {
	if len(refs) == 0 {
		return map[string]string{}, nil
	}
	if values, err := d.ReadMany(ctx, s.run, refs); err == nil {
		return values, nil
	}
	if err := s.ready(ctx, d); err != nil {
		return nil, err
	}
	values := map[string]string{}
	s.halves(ctx, d, refs, values)
	return values, nil
}

// halves reads each half of refs from d into values, as readSome does.
func (s *Secrets) halves(ctx context.Context, d store.Driver, refs []string, values map[string]string) {
	half := len(refs) / 2
	s.readSome(ctx, d, refs[:half], values)
	s.readSome(ctx, d, refs[half:], values)
}

// readSome reads refs from d into values: one alone; more at once, or,
// should that fail, in halves.
func (s *Secrets) readSome(ctx context.Context, d store.Driver, refs []string, values map[string]string) {
	switch len(refs) {
	case 0:
	case 1:
		if v, err := d.Read(ctx, s.run, refs[0]); err == nil {
			values[refs[0]] = v
		}
	default:
		got, err := d.ReadMany(ctx, s.run, refs)
		if err != nil {
			s.halves(ctx, d, refs, values)
			return
		}
		maps.Copy(values, got)
	}
}

// refs are the references of the declared secrets d keeps, each once, in
// turn.
func (s *Secrets) refs(d store.Driver) []string {
	var refs []string
	for _, sec := range s.declared {
		if sameStore(sec.store, d) && !slices.Contains(refs, sec.Ref) {
			refs = append(refs, sec.Ref)
		}
	}
	return refs
}

// Sync reads every declared secret from its store and puts each in place:
// first the secrets of the stores ready now, signed in with their programs
// on kit's PATH; then, waiting for the rest together, each other's as soon
// as what it waits for has come, in a run that applies: its program, and
// its sign-in, raised at a terminal (Cards). ~/.secrets.zsh is written whole each time (a variable
// not read keeps its line, so a partial read never shrinks it), each file
// beside its place then renamed into it. A store signed out, or whose
// program never comes, holds back its own secrets alone (Report.Held); one
// that doesn't answer fails its own (Report.Failed). When every store's
// down and nothing's read, the sync goes no further, saying why in each
// store's words: not yet when each is held back.
func (s *Secrets) Sync(ctx context.Context) (Report, error) {
	old, oldOrder, err := s.envLines()
	if err != nil {
		return Report{}, err
	}
	sy := &syncing{s: s, used: s.used(), old: old, oldOrder: oldOrder, values: map[string]string{}, placed: map[string]bool{}, down: map[string]error{},
		report: Report{Failed: map[string]string{}, Held: map[string]string{}}}
	var later []store.Driver
	for _, d := range sy.used {
		switch {
		case !runner.Has(s.run, d.About().Program):
			later = append(later, d)
		case d.Session(ctx, s.run) != store.SignedOut:
			sy.read(ctx, d)
		case engine.Takes(ctx, signInName(d)):
			later = append(later, d)
		default:
			sy.down[d.About().ID] = holdBack(d.About().SignedOut)
		}
	}
	if err := sy.put(ctx); err != nil {
		return sy.report, err
	}
	if err := sy.awaitStores(ctx, later); err != nil {
		return sy.report, err
	}
	return sy.finish()
}

// syncing is a sync as it goes: the stores the secrets are in, what
// ~/.secrets.zsh held as it began, the values read so far, by reference,
// the secrets put in place, each store down so far, by its ID, with why,
// and the report.
type syncing struct {
	s        *Secrets
	used     []store.Driver
	old      map[string]string
	oldOrder []string
	values   map[string]string
	placed   map[string]bool
	down     map[string]error
	report   Report
}

// read reads d's secrets' values: d down, when it doesn't answer.
func (sy *syncing) read(ctx context.Context, d store.Driver) {
	got, err := sy.s.readAll(ctx, d, sy.s.refs(d))
	if err != nil {
		sy.down[d.About().ID] = err
		return
	}
	maps.Copy(sy.values, got)
}

// awaitStores waits, partway through applying, for the stores held at
// first, together, each read and its secrets put in place as soon as it's
// ready, whatever the rest are waiting for: one store never holds back
// another's secrets. Those still waiting once the run's gone quiet, with
// nothing at work that could bring what they wait for, or stopped, are held
// back.
func (sy *syncing) awaitStores(ctx context.Context, held []store.Driver) error {
	for len(held) > 0 {
		settled := func(ctx context.Context) bool {
			return slices.ContainsFunc(held, func(d store.Driver) bool { return sy.pending(ctx, d) == "" })
		}
		awaited := sy.awaited(ctx, held)
		came := ctx.Err() == nil && engine.Await(ctx, engine.Wait{Thing: awaited, Ready: settled}, "waiting for "+awaited)
		var still []store.Driver
		for _, d := range held {
			switch {
			case sy.pending(ctx, d) == "":
				if err := sy.settle(ctx, d); err != nil {
					return err
				}
			case !came:
				sy.down[d.About().ID] = sy.heldBack(d)
			default:
				still = append(still, d)
			}
		}
		held = still
	}
	return nil
}

// pending is what store d, held at first, still waits for: its program on
// kit's PATH, or its sign-in, while the run's step for it hasn't finished.
// "" once it's settled: its program there, and signed in, or with no
// sign-in to come.
func (sy *syncing) pending(ctx context.Context, d store.Driver) string {
	program := d.About().Program
	if !runner.Has(sy.s.run, program) {
		return program
	}
	name := signInName(d)
	if d.Session(ctx, sy.s.run) == store.SignedOut && engine.Takes(ctx, name) && !finished(ctx, name) {
		return engine.Title(ctx, name)
	}
	return ""
}

// awaited says what the stores held wait for, each once.
func (sy *syncing) awaited(ctx context.Context, held []store.Driver) string {
	var what []string
	for _, d := range held {
		if w := sy.pending(ctx, d); w != "" && !slices.Contains(what, w) {
			what = append(what, w)
		}
	}
	return strings.Join(what, ", ")
}

// heldBack is why store d's secrets are held back, its wait over without
// it settling: its program isn't there, or it isn't signed in.
func (sy *syncing) heldBack(d store.Driver) error {
	if program := d.About().Program; !runner.Has(sy.s.run, program) {
		return holdBack(engine.NotThere([]string{program}))
	}
	return holdBack(d.About().SignedOut)
}

// settle reads store d's secrets and puts them in place, now it's settled:
// held back when it's still signed out.
func (sy *syncing) settle(ctx context.Context, d store.Driver) error {
	if err := sy.s.signedIn(ctx, d); err != nil {
		sy.down[d.About().ID] = err
		return nil
	}
	sy.read(ctx, d)
	return sy.put(ctx)
}

// finished reports whether the run's step called name has finished.
func finished(ctx context.Context, name string) bool {
	return slices.ContainsFunc(engine.Finished(ctx), func(r engine.StepResult) bool { return r.Step == name })
}

// put puts in place what's been read and isn't yet, as Sync says, and has
// the steps waiting for the secrets look again: nothing, when nothing new's
// been read.
func (sy *syncing) put(ctx context.Context) error {
	fresh := false
	for _, sec := range sy.s.declared {
		_, read := sy.values[sec.Ref]
		fresh = fresh || read && !sy.placed[sec.Name]
	}
	if !fresh {
		return nil
	}
	var env []string
	for _, sec := range sy.s.declared {
		v, ok := sy.values[sec.Ref]
		switch {
		case !ok:
			if line, kept := sy.old[sec.Name]; kept && !sec.IsFile() {
				env = append(env, line)
			}
		case sec.IsFile():
			sy.putFile(sec, v)
		default:
			line := "export " + sec.Name + "=" + quote(v)
			if sec.Note != "" {
				line += "   # " + sec.Note
			}
			if !sy.placed[sec.Name] {
				sy.placed[sec.Name] = true
				sy.report.Synced++
				if sy.old[sec.Name] != line {
					sy.report.Changed++
				}
			}
			env = append(env, line)
		}
	}
	// A variable no line declares stays: taking it out is kit reconcile's.
	for _, name := range sy.oldOrder {
		if !slices.ContainsFunc(sy.s.declared, func(sec Secret) bool { return sec.Name == name }) {
			env = append(env, sy.old[name])
		}
	}
	if err := writeFile(filepath.Join(sy.s.home, EnvFile), header(sy.used)+strings.Join(env, "\n")+"\n", 0o600); err != nil {
		return err
	}
	engine.Changed(ctx)
	return nil
}

// putFile puts a file secret's value in place, once.
func (sy *syncing) putFile(sec Secret, value string) {
	if sy.placed[sec.Name] {
		return
	}
	sy.placed[sec.Name] = true
	changed, err := sy.s.writeFile(sec, value)
	if err != nil {
		sy.report.Failed[sec.Name] = err.Error()
		return
	}
	sy.report.Synced++
	sy.report.Changed += changed
}

// finish is the report, each secret not put in place held back or failed,
// as its store's down or not: or why the sync went no further, every store
// down and nothing read.
func (sy *syncing) finish() (Report, error) {
	if err := sy.allDown(); err != nil {
		return sy.report, err
	}
	for _, sec := range sy.s.declared {
		if sy.placed[sec.Name] {
			continue
		}
		switch err := sy.down[sec.store.About().ID]; {
		case err == nil:
			sy.report.Failed[sec.Name] = sec.store.About().Name + " couldn't read " + sec.Ref
		case isHeld(err):
			sy.report.Held[sec.Name] = err.Error()
		default:
			sy.report.Failed[sec.Name] = err.Error()
		}
	}
	return sy.report, nil
}

// allDown is why the sync went no further, when nothing was read and every
// store used is down alike, each held back or each failing: their reasons
// together, held back when each is. Nil otherwise, each store's secrets
// reported as it stands.
func (sy *syncing) allDown() error {
	if len(sy.values) > 0 || len(sy.used) == 0 || len(sy.down) < len(sy.used) {
		return nil
	}
	said := make([]string, len(sy.used))
	held := 0
	for i, d := range sy.used {
		err := sy.down[d.About().ID]
		said[i] = err.Error()
		if isHeld(err) {
			held++
		}
	}
	switch held {
	case len(said):
		return holdBack(strings.Join(said, "; "))
	case 0:
		return errors.New(strings.Join(said, "; "))
	}
	return nil
}

// header heads ~/.secrets.zsh, naming the stores used, its values' sources.
func header(used []store.Driver) string {
	from := "Written by kit"
	if len(used) > 0 {
		names := make([]string, len(used))
		for i, d := range used {
			names[i] = d.About().Name
		}
		from += " from " + strings.Join(names, " and ")
	}
	return "# " + from + ": kit-config's secrets sections. kit apply\n# and kit secret sync rewrite it whole; change a secret in its store, or with kit.\n"
}

// writeFile puts a file secret's value in place, with its mode, ended by a
// newline: 1 when it changed, else 0.
func (s *Secrets) writeFile(sec Secret, value string) (int, error) {
	path := s.path(sec)
	if old, err := os.ReadFile(path); err == nil && string(old) == value+"\n" {
		if info, err := os.Stat(path); err == nil && info.Mode().Perm() == sec.Mode {
			return 0, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return 0, err
	}
	return 1, writeFile(path, value+"\n", sec.Mode)
}
