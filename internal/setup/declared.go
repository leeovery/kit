package setup

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/leeovery/kit/internal/boot"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kinds"
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/prefs"
	"github.com/leeovery/kit/internal/snapshot"
	"github.com/leeovery/kit/internal/tilde"
)

// snapshotStep looks at the Mac as it stands, for what it would declare,
// and what the questions offer (snapshot.Find): done once it's looked.
func (s *Setup) snapshotStep() engine.Step {
	return engine.Step{
		Name: SnapshotStep, Title: "Declared", Waiting: "everything on this Mac, as it is",
		Check: func(context.Context) check.Result {
			found, _ := s.snapshot()
			if found == nil {
				return check.Result{State: check.Attention, Summary: "not looked at yet"}
			}
			return counted(found.Parts)
		},
		Apply: func(ctx context.Context, _ check.Result) error {
			cfg, err := s.config()
			if err != nil {
				return err
			}
			s.doing(SnapshotStep, "looking at what's on this Mac")
			found, m, err := s.Snapshot(ctx, cfg, s.Mac())
			if err != nil {
				return err
			}
			s.mu.Lock()
			s.found, s.mac = &found, m
			s.mu.Unlock()
			return nil
		},
	}
}

// snapshot is what the snapshot found, and the Mac it looked at: nil till
// it's looked.
func (s *Setup) snapshot() (*snapshot.Snapshot, snapshot.Mac) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.found, s.mac
}

// counted says what each of the snapshot's parts declares, as a count, by
// its kind: those with none left out of the summary, and those that
// couldn't be looked at each an item, saying why.
func counted(ps []snapshot.Part) check.Result {
	res := check.Result{State: check.OK, Counts: map[string]int{}}
	var parts []string
	total := 0
	for _, p := range ps {
		if p.Err != nil {
			res.Items = append(res.Items, check.Item{ID: SnapshotStep + ":" + p.Kind, Name: p.Label, State: "not looked at", Detail: p.Err.Error()})
			continue
		}
		n := len(p.Lines)
		res.Counts[p.Kind] = n
		total += n
		if n > 0 {
			parts = append(parts, strconv.Itoa(n)+" "+p.Label)
		}
	}
	res.Summary = strconv.Itoa(total) + ": " + says(parts...)
	if total == 0 {
		res.Summary = "nothing more to declare"
	}
	return res
}

// question is one of the snapshot's questions: what the record says it's
// answered, as its row says it, done when it's answered, unless its flag
// answers it anew; else whether there's anything to ask, the row saying so
// when there isn't. ask asks it, its flag answering first, recording the
// answer. Nothing acts on an answer till the commit's edits, so a flag,
// one of flags, given again replaces what's recorded till then, and is
// refused after.
type question struct {
	name, title, flags string
	answered           func(rec record) (string, bool)
	anew               func(rec record) bool
	nothing            func(ctx context.Context, found *snapshot.Snapshot) (string, bool)
	ask                func(ctx context.Context, found *snapshot.Snapshot) error
}

// anew reports whether a flag gives an answer, given, other than the one
// recorded.
func anew[T any](given, recorded *T) bool {
	return given != nil && (recorded == nil || !reflect.DeepEqual(*given, *recorded))
}

func (s *Setup) questionStep(q question) engine.Step {
	return engine.Step{
		Name: q.name, Title: q.title,
		Check: func(ctx context.Context) check.Result {
			rec := s.record()
			said, ok := q.answered(rec)
			switch {
			case q.anew(rec):
				return check.Result{State: check.Attention, Summary: "answered anew"}
			case ok:
				return check.Result{State: check.OK, Summary: said}
			}
			found, _ := s.snapshot()
			if found == nil {
				return check.Result{State: check.Attention, Summary: "not asked yet"}
			}
			if said, ok := q.nothing(ctx, found); ok {
				return check.Result{State: check.OK, Summary: said}
			}
			return check.Result{State: check.Attention, Summary: "not asked yet"}
		},
		Apply: func(ctx context.Context, _ check.Result) error {
			if rec := s.record(); rec.Declared && q.anew(rec) {
				said, ok := q.answered(rec)
				if !ok {
					said = "unasked"
				}
				return fmt.Errorf("kit setup, part-way, has made its edits in kit-config, %s %s: run kit setup again without %s", strings.ToLower(q.title), said, q.flags)
			}
			found, _ := s.snapshot()
			return q.ask(ctx, found)
		},
	}
}

// dataStep asks which of the home folder's folders a new Mac brings back
// from the backups, each suggested as the snapshot ticks it, declared in
// [restore] as kit restore add declares a path.
func (s *Setup) dataStep() engine.Step {
	sized := func(found *snapshot.Snapshot, paths []string) string {
		if len(paths) == 0 {
			return "none"
		}
		var size int64
		for _, f := range found.Folders {
			if slices.Contains(paths, f.Path) {
				size += f.Size
			}
		}
		parts := []string{fmt.Sprintf("%d %s", len(paths), look.Plural(len(paths), "folder", "folders"))}
		if size > 0 {
			parts = append(parts, look.Size(size))
		}
		return says(parts...)
	}
	return s.questionStep(question{
		name: DataStep, title: snapshot.FoldersLabel, flags: "--restore or --no-restore",
		answered: func(rec record) (string, bool) {
			if rec.Restore == nil {
				return "", false
			}
			found, _ := s.snapshot()
			if found == nil {
				found = &snapshot.Snapshot{}
			}
			return sized(found, *rec.Restore), true
		},
		anew: func(rec record) bool { return anew(s.Given.Restore, rec.Restore) },
		nothing: func(_ context.Context, found *snapshot.Snapshot) (string, bool) {
			return "no folders to bring back", len(found.Folders) == 0
		},
		ask: func(ctx context.Context, found *snapshot.Snapshot) error {
			paths, err := s.restoreAnswer(ctx, found)
			if err != nil {
				return err
			}
			return s.update(func(r *record) { r.Restore = &paths })
		},
	})
}

func (s *Setup) restoreAnswer(ctx context.Context, found *snapshot.Snapshot) ([]string, error) {
	if s.Given.Restore != nil {
		return *s.Given.Restore, nil
	}
	items := make([]Item, len(found.Folders))
	var offered []string
	for i, f := range found.Folders {
		items[i] = Item{Name: f.Path, More: look.Size(f.Size), Ticked: f.Ticked}
		if !f.Whole {
			items[i].More = "over " + items[i].More
		}
		said := f.Path + " " + items[i].More
		if f.Ticked {
			said += ", suggested"
		}
		offered = append(offered, said)
	}
	if s.Ask == nil {
		return nil, s.unanswered("--restore <path> for each folder a new Mac brings back from your backups (" + strings.Join(offered, "; ") + "), or --no-restore")
	}
	return s.ticked(ctx, DataStep, "which folders should a new Mac bring back from your backups?", items)
}

// ticked asks which of items to keep: those kept, by name.
func (s *Setup) ticked(ctx context.Context, step, question string, items []Item) ([]string, error) {
	kept, err := s.Ask.Tick(ctx, step, question, items)
	if err != nil {
		return nil, err
	}
	names := []string{}
	for i, it := range items {
		if i < len(kept) && kept[i] {
			names = append(names, it.Name)
		}
	}
	return names, nil
}

// prefsStep asks whether apps' settings are saved every night, suggested
// so: yes switches the prefs feature on for this Mac and starts afresh from
// its settings as they are, as kit prefs start-fresh does. Asked unless
// capture's switched on for this Mac already, as its own record of apps'
// settings says, whatever kit-config declares: a Mac joining as a copy of
// one saving them declares the feature, but has saved nothing.
func (s *Setup) prefsStep() engine.Step {
	return s.questionStep(question{
		name: PrefsStep, title: "Apps' settings", flags: "--prefs or --no-prefs",
		answered: func(rec record) (string, bool) {
			switch {
			case rec.Prefs == nil:
				return "", false
			case *rec.Prefs:
				return "every night", true
			}
			return "not saved", true
		},
		anew: func(rec record) bool { return anew(s.Given.Prefs, rec.Prefs) },
		nothing: func(context.Context, *snapshot.Snapshot) (string, bool) {
			return "every night already", s.capturing()
		},
		ask: func(ctx context.Context, _ *snapshot.Snapshot) error {
			yes, err := s.prefsAnswer(ctx)
			if err != nil {
				return err
			}
			return s.update(func(r *record) { r.Prefs = &yes })
		},
	})
}

// capturing reports whether capture's switched on for this Mac, as its
// record of apps' settings says.
func (s *Setup) capturing() bool {
	rec, err := (&prefs.Prefs{State: s.Dirs.State}).Load()
	return err == nil && rec.On != ""
}

func (s *Setup) prefsAnswer(ctx context.Context) (bool, error) {
	switch {
	case s.Given.Prefs != nil:
		return *s.Given.Prefs, nil
	case s.Ask == nil:
		return false, s.unanswered("--prefs saves apps' settings every night, so a new Mac gets them back (suggested), or --no-prefs")
	}
	i, err := s.Ask.Choose(ctx, PrefsStep, "save them every night, so a new Mac gets them back?", []boot.Answer{{Label: "Save them every night"}, {Label: "Not now"}})
	return i == 0, err
}

// filesStep asks which of the shell's and tools' files kit-config keeps,
// each moved in and linked back, as kit file add does: all suggested.
func (s *Setup) filesStep() engine.Step {
	return s.questionStep(question{
		name: FilesStep, title: "Linked files", flags: "--link or --no-link",
		answered: func(rec record) (string, bool) {
			if rec.Link == nil {
				return "", false
			}
			if len(*rec.Link) == 0 {
				return "none", true
			}
			return strconv.Itoa(len(*rec.Link)), true
		},
		anew: func(rec record) bool { return anew(s.Given.Link, rec.Link) },
		nothing: func(_ context.Context, found *snapshot.Snapshot) (string, bool) {
			return "none to link", len(found.Files) == 0
		},
		ask: func(ctx context.Context, found *snapshot.Snapshot) error {
			paths, err := s.linkAnswer(ctx, found)
			if err != nil {
				return err
			}
			return s.update(func(r *record) { r.Link = &paths })
		},
	})
}

func (s *Setup) linkAnswer(ctx context.Context, found *snapshot.Snapshot) ([]string, error) {
	switch {
	case s.Given.Link != nil:
		return *s.Given.Link, nil
	case s.Ask == nil:
		return nil, s.unanswered("--link <path> for each file kit-config keeps, moved in and linked back (" + strings.Join(found.Files, ", ") + ", all suggested), or --no-link")
	}
	items := make([]Item, len(found.Files))
	for i, f := range found.Files {
		items[i] = Item{Name: f, Ticked: true}
	}
	return s.ticked(ctx, FilesStep, "which should kit-config keep, each moved in and linked back?", items)
}

// NothingNew is what the install step says on a kit-config setup started:
// there's nothing in it for this Mac to install, as there is joining one.
const NothingNew = "nothing: kit-config is new"

// installStep asks, joining a config, whether what it declares for this
// Mac that the Mac hasn't is installed with the rest, or setup stops to
// look first: kit apply runs, or doesn't. What couldn't be told is asked
// about too, saying so, never taken for nothing.
func (s *Setup) installStep() engine.Step {
	return s.questionStep(question{
		name: InstallStep, title: "To install", flags: "--apply or --no-apply",
		answered: func(rec record) (string, bool) {
			switch {
			case rec.Apply == nil:
				return "", false
			case *rec.Apply:
				return "with the rest", true
			}
			return "stopped here to look first: kit status, then kit apply", true
		},
		anew: func(rec record) bool { return anew(s.Given.Apply, rec.Apply) },
		nothing: func(ctx context.Context, _ *snapshot.Snapshot) (string, bool) {
			if s.record().Empty {
				return NothingNew, true
			}
			missing := s.missing(ctx)
			return "nothing more", missing != nil && len(missing.names) == 0 && len(missing.unknown) == 0
		},
		ask: func(ctx context.Context, _ *snapshot.Snapshot) error {
			yes, err := s.installAnswer(ctx)
			if err != nil {
				return err
			}
			return s.update(func(r *record) { r.Apply = &yes })
		},
	})
}

func (s *Setup) installAnswer(ctx context.Context) (bool, error) {
	if s.Given.Apply != nil {
		return *s.Given.Apply, nil
	}
	missing := s.missing(ctx)
	if missing == nil {
		return false, errors.New("couldn't tell what kit-config declares that this Mac hasn't")
	}
	if s.Ask == nil {
		return false, s.unanswered(missing.flags())
	}
	i, err := s.Ask.Choose(ctx, InstallStep, missing.says(), []boot.Answer{{Label: "Install them with the rest"}, {Label: "Stop here to look first", Cmd: "kit status"}})
	return i == 0, err
}

// toInstall is what the config declares for this Mac that the Mac
// hasn't, by name; unknown, the kinds it couldn't be told for, each
// with why.
type toInstall struct {
	names, unknown []string
}

// says is the question: what there is to install, and whether some
// couldn't be told; then, on a line of its own, which, and why.
func (t toInstall) says() string {
	couldnt := "couldn't tell for " + strings.Join(t.unknown, "; ")
	switch {
	case len(t.unknown) == 0:
		return t.declares() + "\n" + some(t.names)
	case len(t.names) == 0:
		return "couldn't tell what kit-config declares that this Mac hasn't\n" + couldnt
	}
	return t.declares() + ", and couldn't tell for some\n" + some(t.names) + "; " + couldnt
}

// declares says how many things there are to install.
func (t toInstall) declares() string {
	n := len(t.names)
	return fmt.Sprintf("kit-config declares %d %s this Mac hasn't", n, look.Plural(n, "thing", "things"))
}

// flags is what the flags answer, and what couldn't be told.
func (t toInstall) flags() string {
	what := "kit-config declares that this Mac hasn't"
	if len(t.names) > 0 {
		what = t.declares() + " (" + some(t.names) + ")"
	}
	flags := "--apply installs what " + what + " with the rest, or --no-apply stops to look first"
	if len(t.unknown) > 0 {
		flags += "; couldn't tell what kit-config declares that this Mac hasn't, for " + strings.Join(t.unknown, "; ")
	}
	return flags
}

// some names the first few of names, and how many more.
func some(names []string) string {
	const shown = 5
	if len(names) <= shown {
		return look.InWords(names)
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:shown], ", "), len(names)-shown)
}

// missing is what the config declares for this Mac that the Mac hasn't,
// as each kind's check finds it, everything a kind declares while its
// program isn't here, and the kinds it couldn't be told for: found once,
// nil when the config couldn't be read.
func (s *Setup) missing(ctx context.Context) *toInstall {
	s.mu.Lock()
	known := s.toInstall
	s.mu.Unlock()
	if known != nil {
		return known
	}
	_, m := s.snapshot()
	if m.Config == nil {
		return nil
	}
	deps := &kind.Deps{Run: m.Run, Home: m.Home, ConfigHome: m.ConfigHome, StateDir: m.Dirs.State, AppDirs: m.AppDirs}
	t := &toInstall{names: []string{}}
	for _, d := range kinds.All {
		k := d.New(deps)
		list, unread, err := kind.Declared(m.Config, k, m.Name)
		if err = cmp.Or(err, unread); err != nil {
			t.unknown = append(t.unknown, fmt.Sprintf("%s (%s)", d.Label, oneLine(err.Error())))
			continue
		}
		if len(list.On()) == 0 {
			continue
		}
		res := kind.Compare(ctx, k, list)
		switch res.State {
		case check.Deferred:
			t.names = append(t.names, list.On()...)
			continue
		case check.Failed:
			t.unknown = append(t.unknown, fmt.Sprintf("%s (%s)", d.Label, oneLine(res.Reason)))
			continue
		}
		for _, it := range res.Items {
			switch {
			case it.Action == kind.Install:
				t.names = append(t.names, it.Name)
			case it.State == check.NotAllowed:
				t.unknown = append(t.unknown, fmt.Sprintf("%s (%s: %s)", d.Label, res.Summary, oneLine(it.Detail)))
			}
		}
	}
	s.mu.Lock()
	s.toInstall = t
	s.mu.Unlock()
	return t
}

// oneLine is why, its lines run together, to sit in a question's line.
func oneLine(why string) string {
	return strings.Join(strings.Fields(why), " ")
}

// commitStep declares what the snapshot found, then the answers, and
// commits and pushes them as one (Declare), once every URL git pushes the
// clone to is proven private (gitrepo.ProvePrivate): done once it's
// pushed, which ends setup's part. Till then, a run again declares only
// what isn't, and pushes what's committed. Its edits begun are recorded,
// as the clone can't tell them from the first files the Mac's step wrote.
func (s *Setup) commitStep() engine.Step {
	return engine.Step{
		Name: CommitStep, Title: "Committed",
		Check: func(context.Context) check.Result {
			s.mu.Lock()
			declared := s.declared
			s.mu.Unlock()
			if declared != nil {
				return *declared
			}
			return check.Result{State: check.Attention, Summary: "not committed yet"}
		},
		Apply: func(ctx context.Context, _ check.Result) error {
			rec := s.record()
			if err := s.provenPrivate(ctx); err != nil {
				return err
			}
			if err := s.update(func(r *record) { r.Declared = true }); err != nil {
				return err
			}
			found, _ := s.snapshot()
			d := Declaring{Found: found, Empty: rec.Empty, Prefs: s.capturing()}
			if rec.Prefs != nil {
				d.Prefs = *rec.Prefs
			}
			if rec.Restore != nil {
				d.Restore = *rec.Restore
			}
			if rec.Link != nil {
				d.Link = *rec.Link
			}
			res := s.Declare(ctx, d)
			if res.State != check.OK {
				res = s.stopped(res)
			}
			s.mu.Lock()
			s.declared = &res
			s.mu.Unlock()
			if res.State != check.OK {
				return nil
			}
			return s.update(func(r *record) { r.Done = true })
		},
	}
}

// provenPrivate is why the clone isn't pushed to a repository proven
// private, each URL git pushes it to, after any pushurl or pushInsteadOf:
// nil when every one is.
func (s *Setup) provenPrivate(ctx context.Context) error {
	err := gitrepo.Repo{Dir: s.Dirs.Config, Run: s.Run}.ProvePrivate(ctx)
	notPrivate, isNot := errors.AsType[gitrepo.NotPrivate](err)
	switch {
	case isNot && notPrivate.Place.Repo != "":
		return fmt.Errorf("%s, and kit-config is pushed only to a private repository: make it private, then run kit setup again", notPrivate)
	case isNot:
		return fmt.Errorf("%s: kit-config is pushed only to a repository kit can prove private", notPrivate)
	case err != nil:
		return fmt.Errorf("find where kit-config is pushed: %w", err)
	}
	return nil
}

// stopped is res, setup's commit or push that didn't go, saying a run
// again carries on: kit-config's folder holds files moved in from the
// home folder, and linked back, so it's never to be deleted part-way.
func (s *Setup) stopped(res check.Result) check.Result {
	_, m := s.snapshot()
	said := "kit setup again carries on; never delete " + tilde.Shown(m.Home, s.Dirs.Config) + " part-way, as files are moved into it and linked back"
	if res.State == check.Failed {
		res.Reason += "; " + said
	} else {
		res.Summary += "; " + said
	}
	return res
}

// byHandStep says which apps were installed by hand: kit leaves them to
// the person, to install through Homebrew or the App Store should kit set
// the Mac up again.
func (s *Setup) byHandStep() engine.Step {
	return engine.Step{
		Name: ByHandStep, Title: "Installed by hand",
		Check: func(context.Context) check.Result {
			found, _ := s.snapshot()
			switch {
			case found == nil:
				return check.Result{State: check.Attention, Summary: "not looked at yet"}
			case found.ByHandErr != nil:
				return check.Result{State: check.OK, Summary: "couldn't tell which: " + found.ByHandErr.Error()}
			case len(found.ByHand) == 0:
				return check.Result{State: check.OK, Summary: "none"}
			}
			res := check.Result{State: check.OK, Summary: fmt.Sprintf("%d, which kit doesn't look after: if kit sets this Mac up again, they're yours to install, through Homebrew or the App Store, if they're there", len(found.ByHand))}
			for _, app := range found.ByHand {
				res.Items = append(res.Items, check.Item{ID: ByHandStep + ":" + app, Name: app, State: "installed by hand"})
			}
			return res
		},
	}
}

// secretsStep says where secrets go: kit has no way to tell them, so it
// only says.
func (s *Setup) secretsStep() engine.Step {
	return engine.Step{
		Name: SecretsStep, Title: "Secrets",
		Check: func(context.Context) check.Result {
			return check.Result{State: check.OK, Summary: "kit keeps secrets in a store, never in kit-config: kit secret add"}
		},
	}
}
