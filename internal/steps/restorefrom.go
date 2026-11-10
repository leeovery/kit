package steps

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
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
// backup hasn't them.
const (
	keyDisk  = "t"
	keyArq   = "a"
	keyAgain = "enter"
	keyHasnt = "n"
)

// mostDisks is how many Time Machine disks the source card offers, a
// number key each.
const mostDisks = 9

// open opens s's source, the one the restore copies from, and returns it
// with what of todo's paths are left to copy: Arq's restore, by hand (arq);
// else as Sources has it, and, should the old Mac not answer, the source
// the person chooses on a card instead (instead), standing in for it, or
// the old Mac again, for as long as they try.
func (r Restoring) open(ctx context.Context, rec restore.Record, s sourced, todo []judged) (*restore.Opened, []judged, error) {
	if s.want.Kind == restore.FromArq {
		return r.arq(ctx, s.at, s.standsIn, todo)
	}
	opener := r.Sources(s.want, s.at, r.Flag.Kind != "")
	for {
		event.Say(ctx, event.Doing{Says: opening(s.want)})
		src, err := opener.Open(ctx)
		if !errors.Is(err, oldmac.ErrNoAnswer) {
			if err == nil && src.Replaces == (restore.Origin{}) {
				src.Replaces = s.standsIn
			}
			return src, todo, err
		}
		f, err := r.instead(ctx, s.want, err)
		switch {
		case err != nil:
			return nil, nil, err
		case f.arq:
			if err := partWay(rec, todo, restore.Origin{Kind: restore.FromArq}, s.want); err != nil {
				return nil, nil, err
			}
			return r.arq(ctx, time.Time{}, s.want, todo)
		case f.disk != nil:
			s, opener = sourced{want: restore.Origin{Kind: restore.FromTimeMachine}, standsIn: s.want}, f.disk
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
// rest. Without a terminal there's no one to ask: why is an error, naming
// kit apply's flag that answers it.
func (r Restoring) instead(ctx context.Context, mac restore.Origin, why error) (fallback, error) {
	if r.Raise == nil {
		return fallback{}, fmt.Errorf("%w: kit apply --restore-from time-machine|arq restores from a backup instead", why)
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

// lacked are lacks' paths.
func lacked(lacks []lack) []string {
	paths := make([]string, len(lacks))
	for i, l := range lacks {
		paths[i] = l.path
	}
	return paths
}

// arq opens Arq's restore, by hand, of todo's paths, standing in for
// standsIn, should paths be part-way from it, and returns it with what of
// todo's paths are left to copy: each restored by the person in Arq's app,
// from the old Mac's backup, at's or its latest, into a holding folder kit
// makes for it (restore.Hold), for kit to copy from. A card asks for those
// whose holding folders don't hold them yet, once Arq's installed
// (ArqAwait), opening it; once the person says it's done, a holding folder
// still not holding its path is never taken as one the backup hadn't, as
// Arq may still be restoring it, or have put it in another's: the person
// says so (hasnt). Without a terminal there's no card: an error says what
// it would ask.
func (r Restoring) arq(ctx context.Context, at time.Time, standsIn restore.Origin, todo []judged) (*restore.Opened, []judged, error) {
	paths := make([]string, len(todo))
	for i, j := range todo {
		paths[i] = j.Path
	}
	holds, err := restore.Hold(r.StateDir, paths)
	if err != nil {
		return nil, nil, err
	}
	lacks, err := lacking(paths, holds)
	if err != nil {
		return nil, nil, err
	}
	a := restore.Arq{StateDir: r.StateDir, Home: r.Home, Holds: holds}
	if len(lacks) > 0 {
		if r.Raise == nil {
			return nil, nil, fmt.Errorf("restoring through Arq is by hand: in Arq, restore %s, then run kit apply again; at a terminal, it asks for them", r.into(lacks, holds))
		}
		if err := awaitAll(ctx, r.ArqAwait); err != nil {
			return nil, nil, err
		}
		if err := r.ask(ctx, &ownCard{says: r.arqCard(at, standsIn, lacked(lacks), holds), run: r.Run, app: arq.Bundle}); err != nil {
			return nil, nil, err
		}
		gone, err := r.hasnt(ctx, lacked(lacks), a)
		if err != nil {
			return nil, nil, err
		}
		todo = slices.DeleteFunc(todo, func(j judged) bool { return slices.Contains(gone, j.Path) })
		for _, p := range gone {
			delete(a.Holds, p)
		}
	}
	src, err := a.Open(ctx)
	if err == nil {
		src.Replaces = standsIn
	}
	return src, todo, err
}

// hasnt are those of asked, the paths Arq's card asked for, that Arq's
// backup hasn't, as the person says: while any's holding folder, as a has
// it, doesn't hold it, a card says which, and what each holds instead,
// asking to look again, or whether the backup hasn't them, which records
// them not there, from a, their holding folders deleted.
func (r Restoring) hasnt(ctx context.Context, asked []string, a restore.Arq) ([]string, error) {
	for {
		lacks, err := lacking(asked, a.Holds)
		if err != nil || len(lacks) == 0 {
			return nil, err
		}
		c := &ownCard{says: lackingCard(lacks, a.Holds)}
		if err := r.ask(ctx, c); err != nil {
			return nil, err
		}
		asked = lacked(lacks)
		if c.chosen() != keyHasnt {
			continue
		}
		var errs error
		for _, p := range asked {
			errs = errors.Join(errs, r.recordNotThere(ctx, p, a.Origin()), os.RemoveAll(a.Holds[p]))
		}
		return asked, errs
	}
}

// lackingCard is the card saying which of Arq's holding folders, as holds
// has them, don't hold their paths, once the person says Arq's restored
// them all, and what each holds instead: to look again, or say the backup
// hasn't them.
func lackingCard(lacks []lack, holds map[string]string) event.NeedsYou {
	steps := make([]string, len(lacks))
	for i, l := range lacks {
		hold := "…/arq/" + filepath.Base(holds[l.path])
		steps[i] = l.path + ": " + hold + " holds nothing yet"
		if len(l.instead) > 0 {
			steps[i] = fmt.Sprintf("%s: %s holds %s, not %s", l.path, hold, strings.Join(l.instead, ", "), path.Base(l.path))
		}
	}
	return event.NeedsYou{
		Item: itemLacking, Title: "Arq's restore isn't all there", Steps: steps,
		Why:  "Should Arq still be restoring, let it finish; should a path be in another's holding folder, move it into its own. Then look again.",
		Keys: []check.Key{{Key: keyAgain, Does: "looks again"}, {Key: keyHasnt, Does: "says the backup hasn't these, restoring the rest"}},
	}
}

// arqCard is Arq's card, asking the person to restore asks, each into its
// holding folder, as holds has them, from the backup at at, or its latest,
// of the old Mac, as whoseBackup says, standsIn's should it name one.
func (r Restoring) arqCard(at time.Time, standsIn restore.Origin, asks []string, holds map[string]string) event.NeedsYou {
	backup := "its latest backup"
	if !at.IsZero() {
		backup = "its last backup at or before " + at.Format("2 Jan 2006 15:04")
	}
	steps := []string{
		"Open " + r.whoseBackup(standsIn) + " backup in Arq, " + backup,
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

// whoseBackup is whose backup Arq restores from: the old Mac's, the one
// mac names, a path's part-way from, else the one this Mac started from;
// or, started from none, this Mac's own.
func (r Restoring) whoseBackup(mac restore.Origin) string {
	if name := cmp.Or(mac.Detail, r.From); name != "" {
		return name + "'s"
	}
	return "this Mac's"
}

// into says where each of lacks is restored into, as holds has them: a
// holding folder each, and what it holds instead, if anything.
func (r Restoring) into(lacks []lack, holds map[string]string) string {
	each := make([]string, len(lacks))
	for i, l := range lacks {
		each[i] = l.path + " into " + r.tilde(holds[l.path])
		if len(l.instead) > 0 {
			each[i] += " (holding " + strings.Join(l.instead, ", ") + " instead)"
		}
	}
	return strings.Join(each, ", ")
}

// tilde is path, from ~ when it's in the home folder.
func (r Restoring) tilde(path string) string {
	if rest, ok := strings.CutPrefix(path, r.Home+"/"); ok {
		return "~/" + rest
	}
	return path
}
