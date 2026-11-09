package steps

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
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
// from when the old Mac doesn't answer, and Arq's, a step by hand.
const (
	itemSource = RestoreName + ":source"
	itemArq    = RestoreName + ":arq"
)

// The source card's keys: Time Machine's disk, when there's one alone (by
// number, when there are more); Arq, by hand; and the old Mac again.
const (
	keyDisk  = "t"
	keyArq   = "a"
	keyAgain = "enter"
)

// mostDisks is how many Time Machine disks the source card offers, a
// number key each.
const mostDisks = 9

// open opens want, the source the restore copies from, at picking Time
// Machine's backup: Arq's restore, by hand (arq); else as Sources has it,
// and, should the old Mac not answer, the source the person chooses on a
// card instead (instead), or the old Mac again, for as long as they try.
func (r Restoring) open(ctx context.Context, want restore.Origin, at time.Time, todo []judged) (*restore.Opened, error) {
	if want.Kind == restore.FromArq {
		return r.arq(ctx, at, todo)
	}
	opener := r.Sources(want, at, r.Flag.Kind != "")
	for {
		event.Say(ctx, event.Doing{Says: opening(want)})
		src, err := opener.Open(ctx)
		if !errors.Is(err, oldmac.ErrNoAnswer) {
			return src, err
		}
		f, err := r.instead(ctx, err)
		switch {
		case err != nil:
			return nil, err
		case f.arq:
			return r.arq(ctx, time.Time{}, todo)
		case f.disk != nil:
			want, opener = restore.Origin{Kind: restore.FromTimeMachine}, f.disk
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
// old Mac not answering, as why says: one of the Time Machine disks
// attached holding the person's backups, Arq's restore by hand, or the old
// Mac again. Without a terminal there's no one to ask: why is an error,
// naming kit apply's flag that answers it.
func (r Restoring) instead(ctx context.Context, why error) (fallback, error) {
	if r.Raise == nil {
		return fallback{}, fmt.Errorf("%w: kit apply --restore-from time-machine|arq restores from a backup instead", why)
	}
	event.Say(ctx, event.Doing{Says: "looking for Time Machine disks"})
	disks, err := r.Disks(ctx)
	if err != nil {
		return fallback{}, err
	}
	keys, does := choices(r.oldMac(), disks)
	c := &ownCard{says: sourceCard(why, disks, keys)}
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

// oldMac is the Mac this one started from, as kit names it.
func (r Restoring) oldMac() string {
	return restore.Origin{Kind: restore.FromMac, Detail: r.From}.Says()
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
// each Mac found's reason; then of the Time Machine disks, those locked,
// or that there's none, and what to do.
func sourceCard(why error, disks restore.Disks, keys []check.Key) event.NeedsYou {
	title, reasons, ok := strings.Cut(why.Error(), ": ")
	if !ok {
		title, reasons = "The old Mac doesn't answer", why.Error()
	}
	says := []string{sentence(reasons)}
	switch {
	case len(disks.Locked) == 1:
		says = append(says, "The Time Machine disk "+disks.Locked[0]+" is locked: unlock it in Disk Utility, then try again.")
	case len(disks.Locked) > 1:
		says = append(says, "The Time Machine disks "+strings.Join(disks.Locked, " and ")+" are locked: unlock them in Disk Utility, then try again.")
	case len(disks.Found) == 0:
		says = append(says, "No Time Machine disk holding your backups is attached: attach it, then try again.")
	}
	return event.NeedsYou{Item: itemSource, Title: title, Why: strings.Join(says, " "), Keys: keys}
}

// sentence is s as a sentence: its first letter a capital, a full stop
// at its end.
func sentence(s string) string {
	if s == "" {
		return ""
	}
	first, n := utf8.DecodeRuneInString(s)
	s = string(unicode.ToUpper(first)) + s[n:]
	if !strings.HasSuffix(s, ".") {
		s += "."
	}
	return s
}

// arq opens Arq's restore, by hand, of todo's paths: each restored by the
// person in Arq's app, from the old Mac's backup, at's or its latest, into
// a holding folder kit makes for it (restore.Hold), for kit to copy from. A
// card asks for those whose holding folders hold nothing yet, once Arq's
// installed (ArqAwait), opening it; once the person says it's done, a
// holding folder still empty is a path Arq's backup hadn't. Without a
// terminal there's no card: an error says what it would ask.
func (r Restoring) arq(ctx context.Context, at time.Time, todo []judged) (*restore.Opened, error) {
	paths := make([]string, len(todo))
	for i, j := range todo {
		paths[i] = j.Path
	}
	holds, err := restore.Hold(r.StateDir, paths)
	if err != nil {
		return nil, err
	}
	var asks []string
	for _, p := range paths {
		if held, err := restore.Holds(holds[p], p); err == nil && !held {
			asks = append(asks, p)
		}
	}
	if len(asks) > 0 {
		if r.Raise == nil {
			return nil, fmt.Errorf("restoring through Arq is by hand: in Arq, restore %s, then run kit apply again; at a terminal, it asks for them", r.into(asks, holds))
		}
		if err := awaitAll(ctx, r.ArqAwait); err != nil {
			return nil, err
		}
		c := &ownCard{says: r.arqCard(at, asks, holds), run: r.Run, app: arq.Bundle}
		if err := r.ask(ctx, c); err != nil {
			return nil, err
		}
	}
	return restore.Arq{StateDir: r.StateDir, Holds: holds}.Open(ctx)
}

// arqCard is Arq's card, asking the person to restore asks, each into its
// holding folder, as holds has them, from the old Mac's backup at at, or
// its latest.
func (r Restoring) arqCard(at time.Time, asks []string, holds map[string]string) event.NeedsYou {
	backup := "its latest backup"
	if !at.IsZero() {
		backup = "its last backup at or before " + at.Format("2 Jan 2006 15:04")
	}
	steps := []string{
		"Open " + r.oldMac() + "'s backup in Arq, " + backup,
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

// into says where each of paths is restored into, as holds has them: a
// holding folder each.
func (r Restoring) into(paths []string, holds map[string]string) string {
	each := make([]string, len(paths))
	for i, p := range paths {
		each[i] = p + " into " + r.tilde(holds[p])
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
