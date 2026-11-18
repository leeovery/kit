package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/askpass"
	"github.com/leeovery/kit/internal/boot"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/kinds"
	"github.com/leeovery/kit/internal/linked"
	"github.com/leeovery/kit/internal/logs"
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/render"
	"github.com/leeovery/kit/internal/restore"
	"github.com/leeovery/kit/internal/setup"
	"github.com/leeovery/kit/internal/snapshot"
	"github.com/leeovery/kit/internal/status"
	"github.com/leeovery/kit/internal/steps"
)

func newSetupCommand(a *app) *cobra.Command {
	var given setup.Answers
	var restores, links []string
	var noRestore, noLink, prefs, noPrefs, apply, noApply bool
	cmd := &cobra.Command{
		Use:         "setup",
		Short:       "Set up a Mac in use: kit-config made or joined, the Mac declared as it is",
		Annotations: map[string]string{brief: "set up a Mac in use"},
		Long: `Set up a Mac in use, all the way: running it again carries on where it
stopped. A blank Mac takes the install line (kit bootstrap) instead.

It signs in to GitHub, as GitHub's CLI is signed in, else on your phone or
in your browser; then creates kit-config, a private repository on your
account (--create, kit-config suggested), or joins one of yours (--join),
and clones it. It names this Mac (--mac, suggested from its model): joining,
one of kit-config's Macs, or a new one, a copy of another with --from.

Then it declares everything on this Mac as it is, in its own file: every
kind whose program is here, the macOS settings set now, git's settings, the
features on and the backups set up. It asks:
  which folders a new Mac brings back from your backups (--restore,
  --no-restore), declared as kit restore add does;
  whether apps' settings are saved every night (--prefs, --no-prefs);
  which of the shell's and tools' files kit-config keeps, each moved in and
  linked back as kit file add does (--link, --no-link);
  joining, whether what kit-config declares that this Mac hasn't installs
  with the rest (--apply), or setup stops here to look first (--no-apply).
It commits and pushes all of it as one, says what kit doesn't look after
(the apps installed by hand) and where secrets go (kit secret add), then
runs kit apply.

It refuses when the config folder holds a config already. Without a
terminal it asks nothing: a question its flag doesn't answer stops it,
naming the flag, and running it again with the flag carries on. Run
again, a question's flag replaces the answer given before, and a --create,
--join or --mac naming another than the run part-way chose is refused.
It's done once its commit is pushed: if the push fails, running it again
pushes it. Never delete the config folder part-way: files are moved into
it and linked back.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			given.Restore, given.Link = listFlag(restores, noRestore), listFlag(links, noLink)
			given.Prefs, given.Apply = boolFlag(prefs, noPrefs), boolFlag(apply, noApply)
			return a.setup(cmd.Context(), given)
		},
	}
	f := cmd.Flags()
	f.StringVar(&given.Create, "create", "", "create the config repository, private, on your account, named so: kit-config, say")
	f.StringVar(&given.Join, "join", "", "join your config repository, as owner/name")
	f.StringVar(&given.Mac, "mac", "", "this Mac's name: one of kit-config's, or a new one's")
	f.StringVar(&given.From, "from", "", "the Mac a new one starts as a copy of")
	f.StringArrayVar(&restores, "restore", nil, "a folder a new Mac brings back from your backups, as often as wanted")
	f.BoolVar(&noRestore, "no-restore", false, "no folders for a new Mac to bring back")
	f.BoolVar(&prefs, "prefs", false, "save apps' settings every night")
	f.BoolVar(&noPrefs, "no-prefs", false, "don't save apps' settings")
	f.StringArrayVar(&links, "link", nil, "a file kit-config keeps, moved in and linked back, as often as wanted")
	f.BoolVar(&noLink, "no-link", false, "no files for kit-config to keep")
	f.BoolVar(&apply, "apply", false, "run kit apply once setup's done, installing what kit-config declares")
	f.BoolVar(&noApply, "no-apply", false, "stop once setup's committed, to look first")
	for _, pair := range [][]string{{"create", "join"}, {"restore", "no-restore"}, {"prefs", "no-prefs"}, {"link", "no-link"}, {"apply", "no-apply"}} {
		cmd.MarkFlagsMutuallyExclusive(pair...)
	}
	return cmd
}

// listFlag is the list a flag gives, as often as it's given, or none, as
// its --no- flag says: nil when neither is given.
func listFlag(list []string, none bool) *[]string {
	switch {
	case none:
		return &[]string{}
	case list != nil:
		return &list
	}
	return nil
}

// boolFlag is yes or no, as a flag and its --no- flag say: nil when
// neither is given.
func boolFlag(yes, no bool) *bool {
	if !yes && !no {
		return nil
	}
	return &yes
}

// setup runs kit setup's steps, shown in plain lines, then kit apply, as
// the command would, unless the person stops to look first. With --json,
// one document: setup's steps, and apply's run.
func (a *app) setup(ctx context.Context, given setup.Answers) error {
	dirs, err := a.dirs()
	if err != nil {
		return err
	}
	home, err := a.HomeDir()
	if err != nil {
		return fmt.Errorf("find the home directory: %w", err)
	}
	log, err := logs.Open(dirs.Logs, a.Now(), "setup", a.verbose)
	if err != nil {
		return err
	}
	defer func() { _ = log.Close() }()
	var doc *built
	var face render.Face = render.NewPlain(a.Stdout)
	if a.json {
		doc = &built{}
		face = doc
	}
	sink := event.NewFanout(face, log)
	noAsks := func() *askpass.Server { return nil }
	run := a.commands(bootPath, childEnv(a.Getenv, home, bootPath), sink, noAsks)
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	s := &setup.Setup{
		Run: run, Sink: sink, GitHub: a.GitHub(), Now: a.Now,
		Dirs: dirs, Kinds: kinds.Sections(), Getenv: a.Getenv, Version: a.Version, Given: given,
		Snapshot: func(ctx context.Context, cfg *config.Config, mac string) (snapshot.Snapshot, snapshot.Mac, error) {
			path, err := cfg.SearchPath(home, mac)
			if err != nil {
				return snapshot.Snapshot{}, snapshot.Mac{}, err
			}
			path = distinct(slices.Concat(path, bootPath))
			m := snapshot.Mac{
				Name: mac, Config: cfg, Run: a.commands(path, childEnv(a.Getenv, home, path), sink, noAsks), Home: home,
				ConfigHome: a.configHome(), Dirs: dirs, AppDirs: a.appFolders(home).Dirs, SudoLocal: a.SudoLocal, Now: a.Now,
			}
			found, err := snapshot.Find(ctx, m)
			return found, m, err
		},
		Declare: func(ctx context.Context, d setup.Declaring) check.Result { return a.declareSetup(ctx, sink, d) },
	}
	if err := s.Begin(ctx); err != nil {
		return err
	}
	s.Model = setup.Model(ctx, run)
	steps := s.Steps()
	if a.pretty(a.Stdout) {
		s.Ask = &setupAsker{a: a, stop: stop, titles: titles(steps)}
	}
	p, err := engine.New(steps...)
	if err != nil {
		return err
	}
	report, err := p.Apply(ctx, sink, engine.Options{Command: "setup", Machine: cmp.Or(s.Mac(), s.Model), Version: a.Version, Jobs: 1, Now: a.Now})
	if closeErr := face.Close(); err == nil {
		err = closeErr
	}
	switch {
	case err != nil:
		return err
	case ctx.Err() != nil:
		return errors.Join(doc.write(a), ask.ErrCancelled)
	case len(s.Wanted()) > 0:
		return errors.Join(doc.write(a), fmt.Errorf("%w: answer with %s", setup.ErrNoTerminal, strings.Join(s.Wanted(), "; ")))
	case !s.Done() || report.Failed():
		return errors.Join(doc.write(a), failure{})
	case !s.Applies():
		return doc.write(a)
	}
	return a.applyAfterSetup(ctx, doc)
}

// applyAfterSetup runs kit apply once setup's done, as the command would:
// with --json, its run is the apply of setup's document.
func (a *app) applyAfterSetup(ctx context.Context, doc *built) error {
	var r *run
	var err error
	applied := &built{}
	if doc != nil {
		if r, err = a.build("apply", "apply", applied, false); err == nil {
			a.start(r, applied)
		}
	} else {
		r, err = a.prepare("apply", "apply")
	}
	if err != nil {
		return errors.Join(doc.write(a), err)
	}
	report, err := a.applyRun(ctx, r, nil, false)
	if doc != nil {
		applying := applied.Document()
		doc.apply = &applying
	}
	switch {
	case err != nil:
		return errors.Join(doc.write(a), err)
	case report.Failed():
		return errors.Join(doc.write(a), failure{})
	}
	return doc.write(a)
}

// distinct is dirs, each once, where it first comes.
func distinct(dirs []string) []string {
	var out []string
	for _, d := range dirs {
		if !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	return out
}

// declareSetup declares what kit setup's snapshot found in this Mac's
// file, then its answers, as kit restore add, kit file add, kit feature on
// and kit prefs start-fresh would, saying on sink what it runs; then
// commits kit.toml and this Mac's folder, and pushes, as one. What's
// refused, and what's declared as it is for want of telling whether it
// needs a secret, are items, each saying why.
func (a *app) declareSetup(ctx context.Context, sink event.Sink, d setup.Declaring) check.Result {
	r, err := a.build("setup", "", sinkFace{sink}, false)
	if err != nil {
		return failed(err)
	}
	defer func() { _ = r.close() }()
	if err := snapshot.Write(snapshot.Mac{Name: r.machine, Config: r.cfg}, d.Found); err != nil {
		return failed(err)
	}
	counts, total := map[string]int{}, 0
	var items []check.Item
	for _, p := range d.Found.Parts {
		counts[p.Kind] = len(p.Lines)
		total += len(p.Lines)
		for _, ref := range p.Refused {
			items = append(items, check.Item{ID: p.Kind + ":" + ref.Entry.Name, Name: ref.Entry.Name, State: "refused", Detail: ref.Err.Error()})
		}
		for _, u := range p.Unchecked {
			items = append(items, check.Item{ID: p.Kind + ":" + u.Name, Name: u.Name, State: "declared unchecked", Detail: u.Err.Error()})
		}
	}
	items = append(items, r.declareRestores(ctx, d.Restore)...)
	items = append(items, r.linkFiles(ctx, d.Link)...)
	if d.Prefs {
		if err := a.prefsOn(r); err != nil {
			return failed(err)
		}
	}
	repo := r.repo
	repo.BranchMayBeMissing = d.Empty
	files := []string{config.File, r.machine}
	res := syncing{repo: repo, files: files, what: config.File + " and " + r.machine + "/"}.commit(ctx, fmt.Sprintf("kit setup (%s): this Mac as it is", r.machine))
	if res.State != check.Failed {
		res.Summary = fmt.Sprintf("%d declared · %s", total, res.Summary)
	}
	res.Counts, res.Items = counts, append(res.Items, items...)
	return res
}

// declareRestores declares each of paths in this Mac's [restore], as kit
// restore add would, but those declared already, as by setup's run
// part-way: each refused an item saying why.
func (r *run) declareRestores(ctx context.Context, paths []string) []check.Item {
	list, err := r.cfg.List(config.RestoreKind, r.machine)
	if err != nil {
		return []check.Item{{ID: config.RestoreKind, Name: strings.Join(paths, ", "), State: "refused", Detail: err.Error()}}
	}
	var refused []check.Item
	for _, p := range r.tildePaths(paths) {
		line, err := restore.ReadLine(p, "")
		e := config.Entry{Name: line.Path}
		if err == nil && slices.Contains(list.Names(), line.Path) {
			continue
		}
		if err == nil {
			line.Pos = config.DeclFile(r.machine)
			err = r.checkRestore(ctx, r.machine, line, e)
		}
		if err == nil {
			err = r.cfg.Declare(config.RestoreKind, r.machine, e)
		}
		if err != nil {
			refused = append(refused, check.Item{ID: config.RestoreKind + ":" + p, Name: p, State: "refused", Detail: err.Error()})
		}
	}
	return refused
}

// linkFiles moves each file at paths into kit-config, for this Mac, and
// links it back, as kit file add would, but those linked already, as by
// setup's run part-way: each refused an item saying why.
func (r *run) linkFiles(ctx context.Context, paths []string) []check.Item {
	full, shown, err := r.filePaths(paths)
	if err == nil && len(full) > 0 {
		var declared []linked.Link
		if declared, err = r.files.Declared(ctx); err == nil {
			full, shown = unlinked(declared, full, shown)
		}
	}
	if err != nil {
		return []check.Item{{ID: "files", Name: strings.Join(paths, ", "), State: "refused", Detail: err.Error()}}
	}
	var refused []check.Item
	for i, path := range full {
		if _, _, err := r.files.Add(ctx, path, r.machine); err != nil {
			refused = append(refused, check.Item{ID: "files:" + shown[i], Name: shown[i], State: "refused", Detail: err.Error()})
		}
	}
	return refused
}

// unlinked are full, files to link, those declared linked already left
// out, and how each is shown: a folder's left out when any file in it is.
func unlinked(declared []linked.Link, full, shown []string) (unlinkedFull, unlinkedShown []string) {
	for i, s := range shown {
		if !slices.ContainsFunc(declared, func(l linked.Link) bool { return l.Name == s || strings.HasPrefix(l.Name, s+"/") }) {
			unlinkedFull, unlinkedShown = append(unlinkedFull, full[i]), append(unlinkedShown, s)
		}
	}
	return unlinkedFull, unlinkedShown
}

// prefsOn switches the prefs feature on for this Mac, as kit feature on
// would, and starts this Mac's saved settings afresh from its settings as
// they are, as kit prefs start-fresh does, unless capture's on already, as
// by setup's run part-way.
func (a *app) prefsOn(r *run) error {
	list, err := r.cfg.List(config.FeaturesKind, r.machine)
	if err != nil {
		return err
	}
	if !slices.Contains(list.Names(), steps.FeaturePrefs) {
		if err := r.cfg.Declare(config.FeaturesKind, r.machine, config.Entry{Name: steps.FeaturePrefs}); err != nil {
			return err
		}
	}
	p, err := a.prefs(r)
	if err != nil {
		return err
	}
	if rec, err := p.Load(); err != nil || rec.On != "" {
		return err
	}
	return p.StartFresh()
}

// sinkFace shows a run on the run that started it: what it emits, sink's;
// closing it closes nothing.
type sinkFace struct{ event.Sink }

func (sinkFace) Close() error { return nil }

// built is a face that builds a run's document, as kit status --json's,
// writing nothing: kit setup's, its apply's run after it when it ran.
type built struct {
	status.Builder
	apply *status.Document
}

func (*built) Close() error { return nil }

// write writes kit setup's document, setup's steps and its apply's, when
// there's one: nothing without --json.
func (b *built) write(a *app) error {
	if b == nil {
		return nil
	}
	return render.WriteJSON(a.Stdout, struct {
		status.Document
		Apply *status.Document `json:"apply,omitempty"`
	}{b.Document(), b.apply})
}

// titles are steps' titles, by their names.
func titles(steps []engine.Step) map[string]string {
	out := make(map[string]string, len(steps))
	for _, s := range steps {
		out[s.Name] = s.Title
	}
	return out
}

// setupAsker asks kit setup's questions at the terminal, in place, under
// the lines it's printed: a question cancelled stops setup, for a run again
// to carry on.
type setupAsker struct {
	a      *app
	stop   context.CancelFunc
	titles map[string]string
	picked bool
}

func (s *setupAsker) about(step, question string) look.Row {
	return look.Row{State: look.NeedsYou, Name: s.titles[step], Says: look.Words(look.NeedsYou, question)}
}

// asked is err, a question's, stopping setup when the person cancelled it.
func (s *setupAsker) asked(err error) error {
	if errors.Is(err, ask.ErrCancelled) {
		s.stop()
	}
	return err
}

func (s *setupAsker) Choose(ctx context.Context, step, question string, answers []boot.Answer) (int, error) {
	choices := make([]look.Choice, len(answers))
	for i, an := range answers {
		choices[i] = look.Choice{Label: an.Label, Does: an.Does}
	}
	i, err := s.a.Choose(ctx, ask.Question{About: s.about(step, question), Answers: choices})
	return i, s.asked(err)
}

func (s *setupAsker) Text(ctx context.Context, step, question, suggested string) (string, error) {
	text, err := s.a.ReadText(ctx, s.about(step, question), suggested)
	return strings.TrimSpace(text), s.asked(err)
}

// Tick asks of each item in turn whether to keep it, its suggested answer
// first.
func (s *setupAsker) Tick(ctx context.Context, _, question string, items []setup.Item) ([]bool, error) {
	keep, leave := look.Choice{Label: "Keep"}, look.Choice{Label: "Leave"}
	qs := make([]ask.Question, len(items))
	for i, it := range items {
		answers := []look.Choice{leave, keep}
		if it.Ticked {
			answers = []look.Choice{keep, leave}
		}
		qs[i] = ask.Question{About: look.Row{State: look.NeedsYou, Name: it.Name, Says: it.More}, Answers: answers}
	}
	answers, err := s.a.Walk(ctx, question, qs)
	if err != nil {
		return nil, s.asked(err)
	}
	kept := make([]bool, len(items))
	for i, it := range items {
		kept[i] = (answers[i] == 0) == it.Ticked
	}
	return kept, nil
}

// Pick asks the first time how to sign in, then waits, as no key will
// come while the sign-in's code waits to be entered.
func (s *setupAsker) Pick(ctx context.Context, step string, keys []boot.Key) (string, error) {
	if s.picked || len(keys) == 0 {
		<-ctx.Done()
		return "", ctx.Err()
	}
	s.picked = true
	choices := make([]look.Choice, len(keys))
	for i, k := range keys {
		label := cmp.Or(k.Does, k.Key)
		if label != "" {
			label = strings.ToUpper(label[:1]) + label[1:]
		}
		choices[i] = look.Choice{Label: label}
	}
	i, err := s.a.Choose(ctx, ask.Question{About: s.about(step, "how will you sign in?"), Answers: choices})
	if err != nil {
		return "", s.asked(err)
	}
	return keys[i].Key, nil
}
