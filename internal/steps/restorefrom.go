package steps

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/leeovery/kit/internal/backup/arq"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/hand"
	"github.com/leeovery/kit/internal/restore"
	"github.com/leeovery/kit/internal/restore/oldmac"
)

// The data restore's cards, in its row: the one asking where to restore
// from when the old Mac doesn't answer; Arq's, a step by hand; and the one
// saying what of Arq's restore isn't in its holding folders.
const (
	itemSource  = RestoreName + ":source"
	itemArq     = RestoreName + ":arq"
	itemLacking = RestoreName + ":arq-lacking"
)

// The cards' keys: on the source card, Time Machine's disk, when there's
// one alone (by number, when there are more), Arq, by hand, and the old
// Mac again; on the lacking card, looking again (keyAgain), and the
// backup hasn't those holding nothing.
const (
	keyDisk  = "t"
	keyArq   = "a"
	keyAgain = "enter"
	keyHasnt = "n"
)

// mostDisks is how many Time Machine disks the source card offers, a
// number key each.
const mostDisks = 9

// open opens s's source, the one its paths are copied from, and returns it
// with what of them are left to copy, and why the rest couldn't be
// (failed): Arq's restore, by hand (arq); else as Sources has it, and,
// should the old Mac not answer, the source the person chooses on a card
// instead (instead), standing in for it, or the old Mac again, for as long
// as they try. Arq chosen is errToArq: its paths join Arq's restore.
func (r Restoring) open(ctx context.Context, s sourced) (src *restore.Opened, todo []judged, failed, err error) {
	if s.want.Kind == restore.FromArq {
		return r.arq(ctx, s.at, s.todo)
	}
	opener := r.Sources(s.want, s.at, r.Flag.Kind != "")
	for {
		event.Say(ctx, event.Doing{Says: opening(s.want)})
		src, err := opener.Open(ctx)
		if !errors.Is(err, oldmac.ErrNoAnswer) {
			return src, s.todo, nil, err
		}
		f, err := r.instead(ctx, s.want, err)
		switch {
		case err != nil:
			return nil, nil, nil, err
		case f.arq:
			return nil, nil, nil, errToArq
		case f.disk != nil:
			s.want, opener = restore.Origin{Kind: restore.FromTimeMachine}, f.disk
		}
	}
}

// opening is what the restore says as it opens o: looking for the old Mac
// on the network, or opening a backup.
func opening(o restore.Origin) string {
	if o.Kind == restore.FromMac {
		return "looking for " + o.Says()
	}
	return "opening " + o.Says()
}

// fallback is what the person chose on the source card: a Time Machine
// disk (disk), Arq's restore by hand (arq), or, neither, the old Mac again.
type fallback struct {
	disk restore.Opener
	arq  bool
}

// instead asks, on a card in the restore's row, where to restore from, the
// old Mac, mac, not answering, as why says: one of the Time Machine disks
// attached holding the person's backups, Arq's restore by hand, or the old
// Mac again; the disks it couldn't look for, it says why, offering the
// rest. Without a terminal there's no one to ask: why is the error, which
// names kit apply's flag that answers it (unopened).
func (r Restoring) instead(ctx context.Context, mac restore.Origin, why error) (fallback, error) {
	if r.Raise == nil {
		return fallback{}, why
	}
	event.Say(ctx, event.Doing{Says: "looking for Time Machine disks"})
	disks, lookErr := r.Disks(ctx)
	keys, does := choices(mac.Says(), disks)
	c := &ownCard{says: sourceCard(why, disks, lookErr, keys)}
	if err := r.ask(ctx, c); err != nil {
		return fallback{}, err
	}
	return does[c.chosen()], nil
}

// ask raises c in the restore's row and waits till the person's answered
// it: the restore's job given up meanwhile, so the rest of the run goes
// on, and taken back once it's answered.
func (r Restoring) ask(ctx context.Context, c *ownCard) error {
	back := engine.Release(ctx)
	_, _, err := r.Raise.up(ctx, RestoreName, c, midRun(ctx, RestoreName))
	back()
	return err
}

// choices are the source card's keys, and what each chooses: each Time
// Machine disk holding the person's backups, t for one alone, else by its
// number, mostDisks at most; Arq, by hand; and the old Mac, called name,
// again.
func choices(name string, disks restore.Disks) ([]check.Key, map[hand.Answer]fallback) {
	var keys []check.Key
	does := map[hand.Answer]fallback{}
	add := func(key, says string, f fallback) {
		keys = append(keys, check.Key{Key: key, Does: says})
		does[hand.Answer(key)] = f
	}
	for i, d := range disks.Found[:min(len(disks.Found), mostDisks)] {
		key := strconv.Itoa(i + 1)
		if len(disks.Found) == 1 {
			key = keyDisk
		}
		add(key, "restores from the Time Machine disk "+d.Name, fallback{disk: d})
	}
	add(keyArq, "restores through Arq, by hand", fallback{arq: true})
	add(keyAgain, "tries "+name+" again", fallback{})
	return keys, does
}

// sourceCard is the card asking where to restore from, the old Mac not
// answering, as why says, with keys: titled as why starts; then why,
// each Mac found's reason; then of the Time Machine disks, why kit
// couldn't look for them (lookErr), those locked, or that there's none,
// and what to do.
func sourceCard(why error, disks restore.Disks, lookErr error, keys []check.Key) event.NeedsYou {
	title, reasons, ok := strings.Cut(why.Error(), ": ")
	if !ok {
		title, reasons = "The old Mac doesn't answer", why.Error()
	}
	says := []string{sentence(reasons)}
	switch {
	case lookErr != nil:
		says = append(says, sentence("kit couldn't look for Time Machine disks: "+lookErr.Error()))
	case len(disks.Locked) == 1:
		says = append(says, "The Time Machine disk "+disks.Locked[0]+" is locked: unlock it in Disk Utility, then try again.")
	case len(disks.Locked) > 1:
		says = append(says, "The Time Machine disks "+strings.Join(disks.Locked, " and ")+" are locked: unlock them in Disk Utility, then try again.")
	case len(disks.Found) == 0:
		says = append(says, "No Time Machine disk holding your backups is attached: attach it, then try again.")
	}
	return event.NeedsYou{Item: itemSource, Title: title, Why: strings.Join(says, " "), Keys: keys}
}

// sentence is s as a sentence: its first letter a capital, but for kit's
// own name, which never takes one; a full stop at its end.
func sentence(s string) string {
	if s == "" {
		return ""
	}
	if first, n := utf8.DecodeRuneInString(s); !strings.HasPrefix(s, "kit ") {
		s = string(unicode.ToUpper(first)) + s[n:]
	}
	if !strings.HasSuffix(s, ".") {
		s += "."
	}
	return s
}

// lack is a listed path whose holding folder of Arq's doesn't hold Arq's
// restore of it: empty, or holding what's in instead.
type lack struct {
	path    string
	instead []string
}

func (l lack) pathOf() string { return l.path }

// lacking are those of paths whose holding folders, as holds has them,
// don't hold Arq's restore of them (restore.Holds).
func lacking(paths []string, holds map[string]string) ([]lack, error) {
	var out []lack
	for _, p := range paths {
		held, instead, err := restore.Holds(holds[p], p)
		if err != nil {
			return nil, err
		}
		if !held {
			out = append(out, lack{path: p, instead: instead})
		}
	}
	return out, nil
}

// arq opens Arq's restore, by hand, of todo's paths, and returns it with
// what of them are left to copy, and why the rest couldn't be (failed):
// each restored by the person in Arq's app, from the old Mac's backup,
// at's or its latest, into a holding folder kit makes for it
// (restore.Hold), for kit to copy from. A card asks for those whose
// holding folders don't hold them yet, once Arq's installed (ArqAwait),
// opening it; once the person says it's done, a holding folder still not
// holding its path is never taken as one the backup hadn't, as Arq may
// still be restoring it, or have put it in another's: the person says so
// (hasnt). Without a terminal there's no card: those paths fail, each
// saying what it would ask, and the rest are copied.
func (r Restoring) arq(ctx context.Context, at time.Time, todo []judged) (src *restore.Opened, left []judged, failed, err error) {
	holds, err := restore.Hold(r.StateDir, pathsOf(todo))
	if err != nil {
		return nil, nil, nil, err
	}
	lacks, err := lacking(pathsOf(todo), holds)
	if err != nil {
		return nil, nil, nil, err
	}
	a, asks := restore.Arq{StateDir: r.StateDir, Home: r.Home, Holds: holds}, pathsOf(lacks)
	switch {
	case len(lacks) == 0:
	case r.Raise == nil:
		for _, l := range lacks {
			failed = errors.Join(failed, fmt.Errorf("couldn't restore %s: restoring through Arq is by hand: in Arq, restore %s, then run kit apply again; at a terminal, it asks for it", l.path, r.into(l, holds)))
		}
		todo = slices.DeleteFunc(slices.Clone(todo), func(j judged) bool { return slices.Contains(asks, j.Path) })
	default:
		if err := awaitAll(ctx, r.ArqAwait); err != nil {
			return nil, nil, nil, err
		}
		if err := r.ask(ctx, &ownCard{says: r.arqCard(at, asks, holds), run: r.Run, app: arq.Bundle}); err != nil {
			return nil, nil, nil, err
		}
		if a.Lacks, err = r.hasnt(ctx, asks, holds); err != nil {
			return nil, nil, nil, err
		}
	}
	src, err = a.Open(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	return src, todo, failed, nil
}

// hasnt are those of asked, the paths Arq's card asked for, that Arq's
// backup hasn't, as the person says: while any's holding folder, as holds
// has it, doesn't hold it, a card says which, and what each holds instead,
// asking to look again, or, of those holding nothing, whether the backup
// hasn't them, those still holding nothing as they say so, looked at
// again, so one Arq filled while the card was up is copied. One holding
// something else stays on the card till it's moved into its own, or
// emptied. What the restore does with those it hasn't is plan's (hasntIt).
func (r Restoring) hasnt(ctx context.Context, asked []string, holds map[string]string) ([]string, error) {
	var hasnt []string
	for {
		lacks, err := lacking(asked, holds)
		if err != nil || len(lacks) == 0 {
			return hasnt, err
		}
		c := &ownCard{says: lackingCard(lacks, holds)}
		if err := r.ask(ctx, c); err != nil {
			return nil, err
		}
		asked = pathsOf(lacks)
		if c.chosen() != keyHasnt {
			continue
		}
		if lacks, err = lacking(asked, holds); err != nil {
			return nil, err
		}
		for _, l := range lacks {
			if len(l.instead) == 0 {
				hasnt = append(hasnt, l.path)
			}
		}
		asked = slices.DeleteFunc(asked, func(p string) bool { return slices.Contains(hasnt, p) })
	}
}

// lackingCard is the card saying which of Arq's holding folders, as holds
// has them, don't hold their paths, once the person says Arq's restored
// them all, and what each holds instead: to look again, or, of those
// holding nothing, say the backup hasn't them. One holding something else
// is never said to be one the backup hasn't: it's to move into its own.
func lackingCard(lacks []lack, holds map[string]string) event.NeedsYou {
	steps := make([]string, len(lacks))
	keys := []check.Key{{Key: keyAgain, Does: "looks again"}}
	empty := false
	for i, l := range lacks {
		hold := "…/arq/" + filepath.Base(holds[l.path])
		steps[i] = l.path + ": " + hold + " holds nothing yet"
		if len(l.instead) > 0 {
			steps[i] = fmt.Sprintf("%s: %s holds %s, not %s", l.path, hold, strings.Join(l.instead, ", "), path.Base(l.path))
		}
		empty = empty || len(l.instead) == 0
	}
	if empty {
		keys = append(keys, check.Key{Key: keyHasnt, Does: "says the backup hasn't those holding nothing, restoring the rest"})
	}
	return event.NeedsYou{
		Item: itemLacking, Title: "Arq's restore isn't all there", Steps: steps, Keys: keys,
		Why: "Should Arq still be restoring, let it finish. Should a holding folder hold something else, move it into its own, or empty it. Then look again.",
	}
}

// arqCard is Arq's card, asking the person to restore asks, each into its
// holding folder, as holds has them, from the backup at at, or its latest,
// of the old Mac, as whoseBackup says.
func (r Restoring) arqCard(at time.Time, asks []string, holds map[string]string) event.NeedsYou {
	backup := "its latest backup"
	if !at.IsZero() {
		backup = "its last backup at or before " + at.Format("2 Jan 2006 15:04")
	}
	steps := []string{
		"Open " + r.whoseBackup() + " backup in Arq, " + backup,
		"Restore each of these into its holding folder, in " + r.tilde(restore.ArqFolder(r.StateDir)) + ":",
	}
	for _, p := range asks {
		steps = append(steps, p+" → …/arq/"+filepath.Base(holds[p]))
	}
	return event.NeedsYou{
		Item: itemArq, Title: "Restore your data in Arq", Steps: steps,
		Why: "kit can't restore from Arq itself. Once Arq has finished restoring them all, kit copies each into place, then deletes its holding folder.",
	}
}

// whoseBackup is whose backup Arq restores from: the Mac this one started
// from's; or, started from none, this Mac's own.
func (r Restoring) whoseBackup() string {
	if r.From != "" {
		return r.From + "'s"
	}
	return "this Mac's"
}

// into says where l's path is restored into, as holds has it: its holding
// folder, and what it holds instead, if anything.
func (r Restoring) into(l lack, holds map[string]string) string {
	into := l.path + " into " + r.tilde(holds[l.path])
	if len(l.instead) > 0 {
		into += " (holding " + strings.Join(l.instead, ", ") + " instead)"
	}
	return into
}

// tilde is path, from ~ when it's in the home folder.
func (r Restoring) tilde(path string) string {
	if rest, ok := strings.CutPrefix(path, r.Home+"/"); ok {
		return "~/" + rest
	}
	return path
}
