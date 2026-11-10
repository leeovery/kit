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
	"testing"
	"time"

	"github.com/leeovery/kit/internal/backup/arq"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/hand"
	"github.com/leeovery/kit/internal/restore"
	"github.com/leeovery/kit/internal/restore/oldmac"
	"github.com/leeovery/kit/internal/steps"
)

// laptop is the old Mac, as the restore records it.
var laptop = restore.Origin{Kind: restore.FromMac, Detail: "laptop"}

// errSilent is the old Mac not answering over SSH, as oldmac says so: the Mac
// found refusing this Mac's key.
var errSilent = fmt.Errorf("laptop %w: Someone’s MacBook Pro refused this Mac's key, which the old Mac's ~/.ssh/authorized_keys must hold", oldmac.ErrNoAnswer)

// fromLaptop has c restore from the old Mac, laptop, in use.
func (c *restoring) fromLaptop() {
	c.src.live, c.src.origin = true, laptop
}

// atTerminal has c's restore raise its cards at a terminal, where k's desk
// answers them, opening apps through k's programs; the Time Machine disks
// attached, disks.
func (c *restoring) atTerminal(t *testing.T, disks restore.Disks) *card {
	k := newCard(t)
	k.fake.On("open", "-b", arq.Bundle)
	c.r.Raise, c.r.Run = k.raise, k.fake
	c.r.Disks = func(context.Context) (restore.Disks, error) { return disks, nil }
	return k
}

// applying applies c's step, as kit apply does, aside, for its cards to be
// answered.
func (c *restoring) applying(t *testing.T) <-chan error {
	step := c.step()
	found := step.Check(t.Context())
	done := make(chan error, 1)
	go func() { done <- step.Apply(c.ctx(t.Context()), found) }()
	return done
}

// up waits till n cards have gone up (raised), and returns them.
func up(t *testing.T, k *card, n int) []event.NeedsYou {
	t.Helper()
	for range 5000 {
		if cards := raised(k); len(cards) >= n {
			return cards
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("%d cards never went up", n)
	return nil
}

// raised are the cards gone up in k so far, each as it first went up, none
// being answered.
func raised(k *card) []event.NeedsYou {
	k.sink.mu.Lock()
	defer k.sink.mu.Unlock()
	var cards []event.NeedsYou
	for _, e := range k.sink.seen {
		if c, ok := e.(event.NeedsYou); ok && c.Answering == "" {
			cards = append(cards, c)
		}
	}
	return cards
}

// disk is a Time Machine disk attached holding the old Mac's backups, its
// latest snapshot, the old Mac's home folder in it as c's source has it.
func (c *restoring) disk(name, snapshot string) (restore.Disk, *fakeSource) {
	src := &fakeSource{home: c.old, origin: restore.Origin{Kind: restore.FromTimeMachine, Detail: snapshot}}
	return restore.Disk{Name: name, Opener: src}, src
}

// From the old Mac, what's at each listed path is what the old Mac's look
// says, in one look for them all, never what's at its paths here: a file
// copied as one; a folder less what its backups leave out, as each line's
// skips are; a path it hasn't, recorded not there, from it.
func TestRestoreFromTheOldMac(t *testing.T) {
	c := newRestoring(t, "~/Code", "~/.zsh_history", "~/Gone")
	c.fromLaptop()
	c.src.look = map[string]restore.There{
		"~/Code":         {Kind: restore.Folder, Skips: []string{"app/node_modules", `web \[x]/vendor`}},
		"~/.zsh_history": {Kind: restore.File},
		"~/Gone":         {Kind: restore.Missing},
	}
	write(t, c.old, "~/Gone/here-not-there", beforeIt)
	c.copies("~/Code", 2, 2_000)
	c.copies("~/.zsh_history", 1, 1_000)

	if err := c.apply(t.Context()); err != nil {
		t.Fatalf("apply = %v", err)
	}
	if want := []opened{{laptop, time.Time{}, false}}; !reflect.DeepEqual(c.src.asked, want) {
		t.Errorf("the source asked for %+v, want %+v", c.src.asked, want)
	}
	if want := [][]string{{"~/Code", "~/.zsh_history", "~/Gone"}}; !reflect.DeepEqual(c.src.looked, want) {
		t.Errorf("the old Mac was looked at for %q, want %q, once", c.src.looked, want)
	}
	if rules, err := os.ReadFile(filepath.Join(c.state, "restore", "filters", "Code")); err != nil || string(rules) != "- .DS_Store\n- .localized\n- /app/node_modules\n- /web \\[x]/vendor\n" {
		t.Errorf("~/Code's rules = %q, %v; want the old Mac's skips anchored at it", rules, err)
	}
	for _, cmd := range c.fake.Commands() {
		if from := cmd.Args[len(cmd.Args)-2]; opOf(cmd.Args) == copyingOp && strings.HasSuffix(from, ".zsh_history") && (strings.HasSuffix(from, "/") || cmd.Args[len(cmd.Args)-1] != filepath.Join(c.home, ".zsh_history")) {
			t.Errorf("~/.zsh_history was copied as a folder: %q", cmd.Args)
		}
	}
	rec := c.record()
	if rec.From != laptop || rec.Paths["~/Code"].State != restore.Done || rec.Paths["~/.zsh_history"].State != restore.Done || rec.Paths["~/Gone"].State != restore.NotThere || rec.Paths["~/Gone"].From != laptop || rec.Settled.IsZero() {
		t.Errorf("the record = %+v; want the two restored from laptop, ~/Gone not there, settled", rec)
	}
	if want := "Nothing to restore in ~/Gone: laptop hasn't it"; !slices.Contains(c.logged, want) {
		t.Errorf("the log says %q, want %q", c.logged, want)
	}
	if !slices.ContainsFunc(c.doing, func(d said) bool { return d.Says == "looking for laptop" }) {
		t.Error("the restore never said it was looking for laptop")
	}
}

// What stops the old Mac being copied from, as Check finds it, fails the
// restore with its own fix, and the ways on: it answers, so there's no
// card.
func TestRestoreFromTheOldMacFailsOnWhatStopsIt(t *testing.T) {
	c := newRestoring(t, "~/Code")
	c.fromLaptop()
	c.src.err = errors.New("SSH can't read ~/Documents on laptop: turn on Allow full disk access for remote users there, in System Settings › General › Sharing › Remote Login › ⓘ")
	k := c.atTerminal(t, restore.Disks{})
	err := ended(t, c.applying(t))
	if want := "couldn't restore: " + c.src.err.Error() + ": kit apply --restore-from time-machine|arq restores ~/Code from another source, or kit restore remove ~/Code drops its line"; err == nil || err.Error() != want {
		t.Errorf("apply = %v; want %q", err, want)
	}
	if cards := raised(k); len(cards) > 0 || c.src.tries != 1 {
		t.Errorf("cards went up, %+v, and laptop was tried %d times; want none, tried once", cards, c.src.tries)
	}
}

// The old Mac not answering, a card goes up in the restore's row saying
// why, each Mac found's reason, and offering the Time Machine disk holding
// the person's backups, by name, Arq, by hand, or trying it again; a disk
// locked is said, with what to do. Each answer is where the restore copies
// from: the disk, its latest backup; or the old Mac again, once it
// answers. The source answered is the run's, in the record, so a run again
// carries on from it, asking nothing.
func TestRestoreAsksWhenTheOldMacDoesntAnswer(t *testing.T) {
	for _, tt := range []struct {
		name  string
		key   hand.Answer
		from  restore.Origin
		tries int
	}{
		{"its Time Machine disk", "t", restore.Origin{Kind: restore.FromTimeMachine, Detail: snapshotX}, 1},
		{"trying it again", "enter", laptop, 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := newRestoring(t, "~/Code", "~/Documents")
			c.fromLaptop()
			c.src.fails = []error{errSilent}
			d, disk := c.disk("Backups of laptop", snapshotX)
			k := c.atTerminal(t, restore.Disks{Found: []restore.Disk{d}, Locked: []string{"Vault"}})
			write(t, c.old, "~/Code/a", beforeIt)
			write(t, c.old, "~/Documents/d", beforeIt)
			c.copies("~/Code", 1, 1_000)
			c.on(sizing, "~/Documents").Prints(stats(1, 1_000))
			c.on(copyingOp, "~/Documents").Exits(23)
			applied := c.applying(t)

			card := up(t, k, 1)[0]
			want := event.NeedsYou{Time: card.Time, Step: "restore", Item: "restore:source", Title: "laptop doesn't answer over SSH",
				Why: "Someone’s MacBook Pro refused this Mac's key, which the old Mac's ~/.ssh/authorized_keys must hold. The Time Machine disk Vault is locked: unlock it in Disk Utility, then try again.",
				Keys: []check.Key{
					{Key: "t", Does: "restores from the Time Machine disk Backups of laptop"},
					{Key: "a", Does: "restores through Arq, by hand"},
					{Key: "enter", Does: "tries laptop again"},
				}}
			if !reflect.DeepEqual(card, want) {
				t.Errorf("the card = %+v\nwant %+v", card, want)
			}
			k.answer(t, "restore", tt.key)
			if err := ended(t, applied); err == nil || !strings.HasPrefix(err.Error(), "couldn't restore ~/Documents: rsync exited 23") {
				t.Errorf("apply = %v; want ~/Documents failing alone", err)
			}
			rec := c.record()
			if rec.From != tt.from || rec.Paths["~/Code"].State != restore.Done || rec.Paths["~/Code"].From != tt.from {
				t.Errorf("the record = %+v; want ~/Code restored from %+v, the run's source", rec, tt.from)
			}
			if c.src.tries != tt.tries || disk.opens != 2-tt.tries {
				t.Errorf("laptop tried %d times, the disk opened %d; want laptop tried %d times", c.src.tries, disk.opens, tt.tries)
			}

			c.src.asked, c.src.origin = nil, tt.from
			c.on(copyingOp, "~/Documents").Prints(stats(1, 1_000))
			c.on(comparing, "~/Documents")
			if err := ended(t, c.applying(t)); err != nil {
				t.Fatalf("a run again, apply = %v", err)
			}
			if want := []opened{{tt.from, time.Time{}, false}}; !reflect.DeepEqual(c.src.asked, want) || len(up(t, k, 1)) != 1 {
				t.Errorf("a run again asked for %+v, raising %d cards; want %+v, no card", c.src.asked, len(up(t, k, 1)), want)
			}
		})
	}
}

// Several Time Machine disks holding the person's backups, the card offers
// each by name, a number each, the one chosen the one opened; with none,
// it says so, offering Arq and trying again alone.
func TestRestoreOffersEachDisk(t *testing.T) {
	c := newRestoring(t, "~/Code")
	c.fromLaptop()
	c.src.fails = []error{errSilent}
	first, _ := c.disk("Backups (disk7s2)", snapshotY)
	second, chosen := c.disk("Backups (disk9s2)", snapshotX)
	k := c.atTerminal(t, restore.Disks{Found: []restore.Disk{first, second}})
	write(t, c.old, "~/Code/a", beforeIt)
	c.copies("~/Code", 1, 1_000)
	applied := c.applying(t)
	card := up(t, k, 1)[0]
	if want := []check.Key{
		{Key: "1", Does: "restores from the Time Machine disk Backups (disk7s2)"},
		{Key: "2", Does: "restores from the Time Machine disk Backups (disk9s2)"},
		{Key: "a", Does: "restores through Arq, by hand"},
		{Key: "enter", Does: "tries laptop again"},
	}; !reflect.DeepEqual(card.Keys, want) || card.Why != "Someone’s MacBook Pro refused this Mac's key, which the old Mac's ~/.ssh/authorized_keys must hold." {
		t.Errorf("the card says %q, offering %+v", card.Why, card.Keys)
	}
	k.answer(t, "restore", "2")
	if err := ended(t, applied); err != nil {
		t.Fatal(err)
	}
	if rec := c.record(); chosen.opens != 1 || rec.From != chosen.origin {
		t.Errorf("the disk chosen opened %d times, the record from %+v; want it, once", chosen.opens, rec.From)
	}

	c = newRestoring(t, "~/Code")
	c.fromLaptop()
	c.src.fails = []error{errSilent, errSilent}
	k = c.atTerminal(t, restore.Disks{})
	write(t, c.old, "~/Code/a", beforeIt)
	c.copies("~/Code", 1, 1_000)
	applied = c.applying(t)
	card = up(t, k, 1)[0]
	if want := []check.Key{{Key: "a", Does: "restores through Arq, by hand"}, {Key: "enter", Does: "tries laptop again"}}; !reflect.DeepEqual(card.Keys, want) ||
		!strings.HasSuffix(card.Why, " No Time Machine disk holding your backups is attached: attach it, then try again.") {
		t.Errorf("with no disk, the card says %q, offering %+v", card.Why, card.Keys)
	}
	k.answer(t, "restore", "enter")
	up(t, k, 2)
	k.answer(t, "restore", "enter")
	if err := ended(t, applied); err != nil || c.src.tries != 3 || c.record().From != laptop {
		t.Errorf("tried again twice, apply = %v, laptop tried %d times; want it restored from laptop, tried 3 times", err, c.src.tries)
	}
}

// Without a terminal there's no one to ask: the old Mac not answering fails
// the restore, saying why and the flag that answers it.
func TestRestoreWithoutATerminalNamesTheFlag(t *testing.T) {
	c := newRestoring(t, "~/Code")
	c.fromLaptop()
	c.src.fails = []error{errSilent}
	c.r.Disks = func(context.Context) (restore.Disks, error) {
		t.Error("looked for Time Machine disks with no one to offer them to")
		return restore.Disks{}, nil
	}
	err := c.apply(t.Context())
	if want := "couldn't restore: " + errSilent.Error() + ": kit apply --restore-from time-machine|arq restores ~/Code from another source, or kit restore remove ~/Code drops its line"; err == nil || err.Error() != want {
		t.Errorf("apply = %v\nwant %q", err, want)
	}
}

// arqHold is holding folder n, in c's state.
func (c *restoring) arqHold(n int) string {
	return filepath.Join(c.state, "restore", "arq", fmt.Sprint(n))
}

// stateAtHome has c's kit state in its home folder, as kit's is.
func (c *restoring) stateAtHome() {
	c.state = filepath.Join(c.home, ".local", "state", "kit")
	c.r.StateDir = c.state
}

// Restored through Arq, by hand: a card goes up in the restore's row, once
// Arq's installed, opening Arq, asking the person to restore each listed
// path from the old Mac's latest backup into a holding folder of its own
// that kit makes, numbered, said from ~; once they say it's done, kit
// copies each from where Arq put it, in its holding folder under its own
// name, as from a backup, and deletes the holding folder once the path's
// restored. One still empty is never taken as a path the backup hadn't: a
// card says so, and only the person saying the backup hasn't it records it
// not there, from Arq's restore, its folder gone too, the rest restored.
func TestRestoreThroughArq(t *testing.T) {
	c := newRestoring(t, "~/Code", "~/.zsh_history", "~/Gone")
	c.stateAtHome()
	c.r.Flag = steps.RestoreFrom{Kind: restore.FromArq}
	k := c.atTerminal(t, restore.Disks{})
	c.r.ArqAwait = []engine.Wait{{Thing: "Arq", Ready: func(context.Context) bool { return true }}}
	c.copiesFrom(filepath.Join(c.arqHold(1), "Code"), "~/Code", 2, 2_000)
	c.copiesFrom(filepath.Join(c.arqHold(2), ".zsh_history"), "~/.zsh_history", 1, 100)
	applied := c.applying(t)

	card := up(t, k, 1)[0]
	want := event.NeedsYou{Time: card.Time, Step: "restore", Item: "restore:arq", Title: "Restore your data in Arq",
		Why: "kit can't restore from Arq itself. Once Arq has finished restoring them all, kit copies each into place, then deletes its holding folder.",
		Steps: []string{
			"Open laptop's backup in Arq, its latest backup",
			"Restore each of these into its holding folder, in ~/.local/state/kit/restore/arq:",
			"~/Code → …/arq/1", "~/.zsh_history → …/arq/2", "~/Gone → …/arq/3",
		}}
	if !reflect.DeepEqual(card, want) {
		t.Errorf("Arq's card = %+v\nwant %+v", card, want)
	}
	for n := range 3 {
		if info, err := os.Stat(c.arqHold(n + 1)); err != nil || !info.IsDir() {
			t.Errorf("holding folder %d isn't made: %v", n+1, err)
		}
	}
	write(t, c.arqHold(1), "~/Code/main.go", beforeIt)
	write(t, c.arqHold(2), "~/.zsh_history", beforeIt)
	k.answer(t, "restore", hand.Done)

	lack := up(t, k, 2)[1]
	if want := (event.NeedsYou{Time: lack.Time, Step: "restore", Item: "restore:arq-lacking", Title: "Arq's restore isn't all there",
		Why:   "Should Arq still be restoring, let it finish. Should a holding folder hold something else, move it into its own, or empty it. Then look again.",
		Steps: []string{"~/Gone: …/arq/3 holds nothing yet"},
		Keys:  []check.Key{{Key: "enter", Does: "looks again"}, {Key: "n", Does: "says the backup hasn't those holding nothing, restoring the rest"}},
	}); !reflect.DeepEqual(lack, want) {
		t.Errorf("once done, the card = %+v\nwant %+v", lack, want)
	}
	if rec := c.record(); rec.Paths["~/Gone"].State == restore.NotThere || len(c.fake.Calls()) > 0 {
		t.Errorf("before the person says, ~/Gone is %+v, rsync ran %q; want nothing recorded, nothing copied", rec.Paths["~/Gone"], c.fake.Calls())
	}
	k.answer(t, "restore", "n")
	if err := ended(t, applied); err != nil {
		t.Fatalf("apply = %v", err)
	}
	k.raise.Wait()
	if calls := k.fake.Calls(); !slices.Equal(calls, []string{"open -b com.haystacksoftware.Arq"}) {
		t.Errorf("as its card went up, ran %q; want Arq opened", calls)
	}
	arq := restore.Origin{Kind: restore.FromArq, Detail: "~/.local/state/kit/restore/arq"}
	rec := c.record()
	code, history, gone := rec.Paths["~/Code"], rec.Paths["~/.zsh_history"], rec.Paths["~/Gone"]
	if rec.From != arq || code.State != restore.Done || code.From != arq || code.Holding != 0 || history.State != restore.Done || gone.State != restore.NotThere || gone.From != arq || gone.Holding != 0 || rec.Settled.IsZero() {
		t.Errorf("the record = %+v; want both restored from Arq's restore, ~/Gone not there, their holding folders let go, settled", rec)
	}
	for _, cmd := range c.fake.Commands() {
		if opOf(cmd.Args) == copyingOp && strings.HasSuffix(cmd.Args[len(cmd.Args)-1], "Code/") && cmd.Args[len(cmd.Args)-2] != filepath.Join(c.arqHold(1), "Code")+"/" {
			t.Errorf("~/Code was copied from %s", cmd.Args[len(cmd.Args)-2])
		}
	}
	if entries, err := os.ReadDir(filepath.Join(c.state, "restore", "arq")); err != nil || len(entries) > 0 {
		t.Errorf("Arq's holding folders hold %v, %v; want each gone", entries, err)
	}
	if want := "Nothing to restore in ~/Gone: Arq's restore in ~/.local/state/kit/restore/arq hasn't it"; !slices.Contains(c.logged, want) {
		t.Errorf("the log says %q, want %q", c.logged, want)
	}
	c.r.Flag = steps.RestoreFrom{}
	if res := c.step().Check(t.Context()); len(res.Items) != 1 || res.Items[0].Detail != "Arq's restore in ~/.local/state/kit/restore/arq hadn't it; kit apply --restore-from mac|time-machine|arq brings it from another" {
		t.Errorf("the check's items = %+v; want ~/Gone's, its source said from ~", res.Items)
	}
}

// Said done while Arq's still restoring, or with a path in another's
// holding folder, the restore records nothing not there: its card says
// which holding folders don't hold their paths, and what each holds
// instead; looking again once they do, each is copied.
func TestRestoreThroughArqLooksAgain(t *testing.T) {
	c := newRestoring(t, "~/Code", "~/.claude", "~/.zsh_history")
	c.r.Flag = steps.RestoreFrom{Kind: restore.FromArq}
	k := c.atTerminal(t, restore.Disks{})
	c.copiesFrom(filepath.Join(c.arqHold(1), "Code"), "~/Code", 1, 1_000)
	c.copiesFrom(filepath.Join(c.arqHold(2), ".claude"), "~/.claude", 1, 1_000)
	c.copiesFrom(filepath.Join(c.arqHold(3), ".zsh_history"), "~/.zsh_history", 1, 100)
	applied := c.applying(t)
	up(t, k, 1)
	write(t, c.arqHold(1), "~/.claude/history.jsonl", beforeIt)
	write(t, c.arqHold(2), "~/Code/main.go", beforeIt)
	k.answer(t, "restore", hand.Done)

	lack := up(t, k, 2)[1]
	if want := []string{"~/Code: …/arq/1 holds .claude, not Code", "~/.claude: …/arq/2 holds Code, not .claude", "~/.zsh_history: …/arq/3 holds nothing yet"}; !slices.Equal(lack.Steps, want) {
		t.Errorf("said done early, the card's steps = %q\nwant %q", lack.Steps, want)
	}
	if err := os.Rename(filepath.Join(c.arqHold(1), ".claude"), filepath.Join(c.arqHold(2), ".claude")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(c.arqHold(2), "Code"), filepath.Join(c.arqHold(1), "Code")); err != nil {
		t.Fatal(err)
	}
	k.answer(t, "restore", "enter")
	if lack := up(t, k, 3)[2]; !slices.Equal(lack.Steps, []string{"~/.zsh_history: …/arq/3 holds nothing yet"}) {
		t.Errorf("looked again, the card's steps = %q; want ~/.zsh_history's alone", lack.Steps)
	}
	write(t, c.arqHold(3), "~/.zsh_history", beforeIt)
	k.answer(t, "restore", "enter")
	if err := ended(t, applied); err != nil {
		t.Fatalf("apply = %v", err)
	}
	rec := c.record()
	for _, p := range []string{"~/Code", "~/.claude", "~/.zsh_history"} {
		if rec.Paths[p].State != restore.Done {
			t.Errorf("%s's record = %+v; want it restored", p, rec.Paths[p])
		}
	}
}

// The old Mac not answering, Arq chosen on the card has Arq's card go up
// next, the backup it names the latest; kit apply's flags naming Arq with
// --at, Arq's card names the backup by that date or time; on a Mac started
// from none, the backup it names is this Mac's own. Its card waits for Arq
// to be installed: without it, the restore isn't yet, asking nothing, and
// naming no ways on, as it waits.
func TestRestoreThroughArqChosenOrNamed(t *testing.T) {
	restored := func(c *restoring, k *card, applied <-chan error) error {
		t.Helper()
		write(t, c.arqHold(1), "~/Code/a", beforeIt)
		c.copiesFrom(filepath.Join(c.arqHold(1), "Code"), "~/Code", 1, 1_000)
		k.answer(t, "restore", hand.Done)
		return ended(t, applied)
	}
	c := newRestoring(t, "~/Code")
	c.fromLaptop()
	c.src.fails = []error{errSilent}
	k := c.atTerminal(t, restore.Disks{})
	applied := c.applying(t)
	up(t, k, 1)
	k.answer(t, "restore", "a")
	if card := up(t, k, 2)[1]; card.Item != "restore:arq" || card.Steps[0] != "Open laptop's backup in Arq, its latest backup" {
		t.Errorf("after Arq's chosen, the card = %+v; want Arq's, of the latest backup", card)
	}
	if err := restored(c, k, applied); err != nil || c.record().From.Kind != restore.FromArq {
		t.Errorf("apply = %v, the record from %+v; want Arq's restore", err, c.record().From)
	}

	c = newRestoring(t, "~/Code")
	c.r.Flag = steps.RestoreFrom{Kind: restore.FromArq, At: time.Date(2026, 11, 8, 23, 59, 59, 0, time.UTC)}
	k = c.atTerminal(t, restore.Disks{})
	applied = c.applying(t)
	if card := up(t, k, 1)[0]; card.Steps[0] != "Open laptop's backup in Arq, its last backup at or before 8 Nov 2026 23:59" {
		t.Errorf("with --at, Arq's card's first step = %q", card.Steps[0])
	}
	if err := restored(c, k, applied); err != nil {
		t.Fatal(err)
	}

	c = newRestoring(t, "~/Code")
	c.r.From, c.r.Flag = "", steps.RestoreFrom{Kind: restore.FromArq}
	k = c.atTerminal(t, restore.Disks{})
	applied = c.applying(t)
	if card := up(t, k, 1)[0]; card.Steps[0] != "Open this Mac's backup in Arq, its latest backup" {
		t.Errorf("on a Mac started from none, Arq's card's first step = %q", card.Steps[0])
	}
	if err := restored(c, k, applied); err != nil {
		t.Fatal(err)
	}

	c = newRestoring(t, "~/Code")
	c.r.Flag = steps.RestoreFrom{Kind: restore.FromArq}
	k = c.atTerminal(t, restore.Disks{})
	c.r.ArqAwait = []engine.Wait{{Thing: "Arq", Ready: func(context.Context) bool { return false }}}
	if err := ended(t, c.applying(t)); !engine.IsNotYet(err) || !strings.Contains(err.Error(), "Arq") || strings.Contains(err.Error(), "kit restore remove") || len(raised(k)) > 0 {
		t.Errorf("without Arq, apply = %v, raising %d cards; want it not yet, waiting for Arq, naming no ways on, no card", err, len(raised(k)))
	}
}

// Through Arq, as from any backup, the second pass must find nothing: a
// file differing fails the path, its holding folder kept. A run again
// carries on from Arq's restore, asking nothing for the paths whose
// holding folders hold them, and none for those restored.
func TestRestoreThroughArqResumes(t *testing.T) {
	c := newRestoring(t, "~/Code", "~/Documents")
	c.r.Flag = steps.RestoreFrom{Kind: restore.FromArq}
	k := c.atTerminal(t, restore.Disks{})
	code, docs := filepath.Join(c.arqHold(1), "Code"), filepath.Join(c.arqHold(2), "Documents")
	c.copiesFrom(code, "~/Code", 2, 2_000)
	c.onFrom(comparing, code).Prints(">f.st...... Code/a\n").Then()
	c.copiesFrom(docs, "~/Documents", 1, 1_000)
	applied := c.applying(t)
	up(t, k, 1)
	write(t, c.arqHold(1), "~/Code/a", beforeIt)
	write(t, c.arqHold(2), "~/Documents/d", beforeIt)
	k.answer(t, "restore", hand.Done)
	if err := ended(t, applied); err == nil || !strings.Contains(err.Error(), "its second pass found 1 file differing from Arq's restore") {
		t.Errorf("apply = %v; want ~/Code's second pass failing it", err)
	}
	if rec := c.record(); rec.Paths["~/Code"].State != restore.Checking || rec.Paths["~/Code"].Holding != 1 || rec.Paths["~/Documents"].State != restore.Done {
		t.Errorf("the record = %+v; want ~/Code checking, its holding folder kept, ~/Documents restored", rec.Paths)
	}
	if _, err := os.Stat(c.arqHold(1)); err != nil {
		t.Errorf("~/Code's holding folder went: %v", err)
	}

	c.r.Flag = steps.RestoreFrom{}
	if err := ended(t, c.applying(t)); err != nil {
		t.Fatalf("a run again, apply = %v", err)
	}
	if cards := up(t, k, 1); len(cards) != 1 {
		t.Errorf("a run again raised %d cards; want none more", len(cards)-1)
	}
	if rec := c.record(); rec.Paths["~/Code"].State != restore.Done || rec.Settled.IsZero() {
		t.Errorf("a run again, the record = %+v; want ~/Code restored, settled", rec.Paths)
	}
}

// Without a terminal, Arq's card can't go up: each path whose holding
// folder doesn't hold it fails, saying what to restore where in Arq, and
// what its folder holds instead, if anything, each holding folder made,
// kept, and never recorded not there; those it holds are copied. Restored
// there by hand, a run again copies the rest, asking nothing.
func TestRestoreThroughArqWithoutATerminal(t *testing.T) {
	c := newRestoring(t, "~/Code", "~/.zsh_history", "~/Documents")
	c.r.Flag = steps.RestoreFrom{Kind: restore.FromArq}
	write(t, c.arqHold(2), "~/.zsh_history", beforeIt)
	write(t, c.arqHold(3), "~/app/main.go", beforeIt)
	c.copiesFrom(filepath.Join(c.arqHold(2), ".zsh_history"), "~/.zsh_history", 1, 100)
	err := c.apply(t.Context())
	want := []string{
		fmt.Sprintf("couldn't restore ~/Code: restoring through Arq is by hand: in Arq, restore ~/Code into %s, then run kit apply again; at a terminal, it asks for it", c.arqHold(1)),
		fmt.Sprintf("couldn't restore ~/Documents: restoring through Arq is by hand: in Arq, restore ~/Documents into %s (holding app instead), then run kit apply again; at a terminal, it asks for it", c.arqHold(3)),
	}
	if err == nil || err.Error() != strings.Join(want, "\n") {
		t.Errorf("apply = %v\nwant %q", err, want)
	}
	rec := c.record()
	if rec.Paths["~/.zsh_history"].State != restore.Done || rec.Paths["~/Code"].State != "" || rec.Paths["~/Documents"].State != "" || !rec.Settled.IsZero() {
		t.Errorf("the record = %+v; want ~/.zsh_history restored, the others untouched, not settled", rec.Paths)
	}
	if _, err := os.Stat(filepath.Join(c.arqHold(3), "app", "main.go")); err != nil {
		t.Errorf("~/Documents' holding folder's files went: %v", err)
	}

	write(t, c.arqHold(1), "~/Code/a", beforeIt)
	if err := os.RemoveAll(filepath.Join(c.arqHold(3), "app")); err != nil {
		t.Fatal(err)
	}
	write(t, c.arqHold(3), "~/Documents/d", beforeIt)
	c.copiesFrom(filepath.Join(c.arqHold(1), "Code"), "~/Code", 1, 1_000)
	c.copiesFrom(filepath.Join(c.arqHold(3), "Documents"), "~/Documents", 1, 1_000)
	if err := c.apply(t.Context()); err != nil || c.record().Settled.IsZero() {
		t.Errorf("restored in Arq by hand, a run again = %v; want the rest copied, settled", err)
	}
}

// Arq's holding folders already take their room on the disk, and each goes
// once its path's restored: so the room needed is the largest copy's, and
// a margin, not all of them together. As each path's turn comes, the room
// is found again, as one that failed keeps its holding folder.
func TestRestoreThroughArqNeedsRoomForTheLargest(t *testing.T) {
	for _, tt := range []struct {
		name string
		free uint64
		want string
	}{
		{"room for the largest", 33_000_000_000, ""},
		{"not even that", 31_000_000_000, "couldn't restore: 30.0 GB copied at most at once, as each holding folder goes once it's restored, needs 32.0 GB free, with a margin, and the disk has 31.0 GB"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := newRestoring(t, "~/Code", "~/Documents")
			c.r.Flag, c.free = steps.RestoreFrom{Kind: restore.FromArq}, tt.free
			write(t, c.arqHold(1), "~/Code/a", beforeIt)
			write(t, c.arqHold(2), "~/Documents/d", beforeIt)
			c.copiesFrom(filepath.Join(c.arqHold(1), "Code"), "~/Code", 1, 30_000_000_000)
			c.copiesFrom(filepath.Join(c.arqHold(2), "Documents"), "~/Documents", 1, 20_000_000_000)
			if err := c.apply(t.Context()); tt.want == "" && err != nil || tt.want != "" && (err == nil || err.Error() != tt.want) {
				t.Errorf("apply = %v; want %q", err, tt.want)
			}
		})
	}

	c := newRestoring(t, "~/Code", "~/Documents")
	c.r.Flag, c.free = steps.RestoreFrom{Kind: restore.FromArq}, 33_000_000_000
	write(t, c.arqHold(1), "~/Code/a", beforeIt)
	write(t, c.arqHold(2), "~/Documents/d", beforeIt)
	c.onFrom(sizing, filepath.Join(c.arqHold(1), "Code")).Prints(stats(1, 30_000_000_000))
	c.onFrom(copyingOp, filepath.Join(c.arqHold(1), "Code")).Exits(23).Does(func() { c.free = 3_000_000_000 })
	c.copiesFrom(filepath.Join(c.arqHold(2), "Documents"), "~/Documents", 1, 20_000_000_000)
	err := c.apply(t.Context())
	if want := "couldn't restore ~/Documents: 20.0 GB copied at most at once, as each holding folder goes once it's restored, needs 22.0 GB free, with a margin, and the disk has 3.0 GB"; err == nil || !strings.HasSuffix(err.Error(), want) {
		t.Errorf("~/Code failing, holding its room, apply = %v; want ~/Documents failing for room: %q", err, want)
	}
}

// The restore copies over SSH, and so needs the key kit's secrets write,
// only from the Mac this one started from: not when kit apply's flags name
// a backup, nor when a path's part-way from one, nor when the last run's
// source was one; but, the flags naming none, to carry on any path
// part-way from the old Mac, after one from a backup too; on a Mac that
// started from none, only to carry on a path part-way from one.
func TestRestoreOverSSH(t *testing.T) {
	tm := restore.Origin{Kind: restore.FromTimeMachine, Detail: snapshotX}
	for _, tt := range []struct {
		name    string
		prepare func(c *restoring)
		want    bool
	}{
		{"from the old Mac", func(*restoring) {}, true},
		{"from a backup the flags name", func(c *restoring) { c.r.Flag = steps.RestoreFrom{Kind: restore.FromArq} }, false},
		{"part-way from a backup", func(c *restoring) {
			c.change("~/Code", func(p *restore.Path) { p.State, p.From, p.Started = restore.Copying, tm, duringIt })
		}, false},
		{"part-way from the old Mac", func(c *restoring) {
			c.change("~/Code", func(p *restore.Path) { p.State, p.From, p.Started = restore.Copying, laptop, duringIt })
		}, true},
		{"the last run's source a backup", func(c *restoring) {
			if err := restore.UpdateRecord(c.state, func(r *restore.Record) { r.From = tm }); err != nil {
				c.t.Fatal(err)
			}
		}, false},
		{"started from none", func(c *restoring) { c.r.From = "" }, false},
		{"started from none, part-way from the old Mac", func(c *restoring) {
			c.r.From = ""
			c.change("~/Code", func(p *restore.Path) { p.State, p.From, p.Started = restore.Copying, laptop, duringIt })
		}, true},
		{"part-way from a backup, then the old Mac", func(c *restoring) {
			c.change("~/Code", func(p *restore.Path) { p.State, p.From, p.Started = restore.Copying, tm, duringIt })
			c.change("~/Documents", func(p *restore.Path) { p.State, p.From, p.Started = restore.Copying, laptop, duringIt })
		}, true},
		{"part-way from a backup, then the old Mac, the flags naming a backup", func(c *restoring) {
			c.change("~/Code", func(p *restore.Path) { p.State, p.From, p.Started = restore.Copying, tm, duringIt })
			c.change("~/Documents", func(p *restore.Path) { p.State, p.From, p.Started = restore.Copying, laptop, duringIt })
			c.r.Flag = steps.RestoreFrom{Kind: restore.FromTimeMachine}
		}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := newRestoring(t, "~/Code", "~/Documents")
			tt.prepare(c)
			if got, err := c.r.OverSSH(); err != nil || got != tt.want {
				t.Errorf("OverSSH() = %v, %v; want %v", got, err, tt.want)
			}
		})
	}
}

// A backup stands in for the old Mac a path's part-way from: named by kit
// apply's flags, or chosen on the card when it doesn't answer, Time
// Machine's or Arq's, never refused. Once a copy starts, every path
// part-way from the old Mac carries on from the backup, --update keeping
// the newer files the live copy brought, the record and the log saying so;
// stopped before one does, nothing's recorded, so a run again tries the
// old Mac.
func TestRestoreBackupStandsInForTheOldMac(t *testing.T) {
	partWay := func(t *testing.T) *restoring {
		c := newRestoring(t, "~/Code", "~/Documents")
		for _, p := range []string{"~/Code", "~/Documents"} {
			c.change(p, func(p *restore.Path) { p.State, p.From, p.Started = restore.Copying, laptop, duringIt })
		}
		write(t, c.old, "~/Code/a", beforeIt)
		write(t, c.old, "~/Documents/d", beforeIt)
		return c
	}
	carriedOn := func(t *testing.T, c *restoring, from restore.Origin) {
		t.Helper()
		rec := c.record()
		if rec.From != from || rec.Paths["~/Code"].From != from || rec.Paths["~/Documents"].From != from || rec.Paths["~/Code"].State != restore.Done {
			t.Errorf("the record = %+v; want both carried on from %+v", rec, from)
		}
		for _, p := range []string{"~/Code", "~/Documents"} {
			if want := "Carrying on " + p + " from " + from.Says() + " in place of laptop, which its restore began from"; !slices.Contains(c.logged, want) {
				t.Errorf("the log says %q; want %q", c.logged, want)
			}
		}
	}

	t.Run("named by the flags", func(t *testing.T) {
		c := partWay(t)
		c.r.Flag = steps.RestoreFrom{Kind: restore.FromTimeMachine}
		c.copies("~/Code", 1, 1_000)
		c.copies("~/Documents", 1, 1_000)
		if err := c.apply(t.Context()); err != nil {
			t.Fatalf("apply = %v", err)
		}
		if want := []opened{{restore.Origin{Kind: restore.FromTimeMachine}, time.Time{}, true}}; !reflect.DeepEqual(c.src.asked, want) {
			t.Errorf("the source asked for %+v, want %+v", c.src.asked, want)
		}
		carriedOn(t, c, c.src.origin)
	})
	t.Run("its disk chosen on the card", func(t *testing.T) {
		c := partWay(t)
		c.fromLaptop()
		c.src.fails = []error{errSilent}
		d, disk := c.disk("Backups of laptop", snapshotX)
		k := c.atTerminal(t, restore.Disks{Found: []restore.Disk{d}})
		c.copies("~/Code", 1, 1_000)
		c.copies("~/Documents", 1, 1_000)
		applied := c.applying(t)
		up(t, k, 1)
		k.answer(t, "restore", "t")
		if err := ended(t, applied); err != nil {
			t.Fatalf("apply = %v", err)
		}
		carriedOn(t, c, disk.origin)
	})
	t.Run("Arq chosen on the card", func(t *testing.T) {
		c := partWay(t)
		c.fromLaptop()
		c.src.fails = []error{errSilent}
		k := c.atTerminal(t, restore.Disks{})
		applied := c.applying(t)
		up(t, k, 1)
		k.answer(t, "restore", "a")
		if card := up(t, k, 2)[1]; card.Item != "restore:arq" {
			t.Fatalf("after Arq's chosen, the card = %+v; want Arq's", card)
		}
		write(t, c.arqHold(1), "~/Code/a", beforeIt)
		write(t, c.arqHold(2), "~/Documents/d", beforeIt)
		c.copiesFrom(filepath.Join(c.arqHold(1), "Code"), "~/Code", 1, 1_000)
		c.copiesFrom(filepath.Join(c.arqHold(2), "Documents"), "~/Documents", 1, 1_000)
		k.answer(t, "restore", hand.Done)
		if err := ended(t, applied); err != nil {
			t.Fatalf("apply = %v", err)
		}
		carriedOn(t, c, restore.Origin{Kind: restore.FromArq, Detail: filepath.Join(c.state, "restore", "arq")})
	})
	t.Run("stopped before a copy", func(t *testing.T) {
		c := partWay(t)
		c.r.Flag = steps.RestoreFrom{Kind: restore.FromTimeMachine}
		c.on(sizing, "~/Code").Exits(23)
		c.on(sizing, "~/Documents").Exits(23)
		if err := c.apply(t.Context()); err == nil {
			t.Fatal("sizing failing, apply succeeded")
		}
		rec := c.record()
		if rec.From != (restore.Origin{}) || rec.Paths["~/Code"].From != laptop || slices.ContainsFunc(c.logged, func(l string) bool { return strings.HasPrefix(l, "Carrying on") }) {
			t.Errorf("the record = %+v, the log %q; want nothing carried on, no source recorded", rec, c.logged)
		}
		c.r.Flag, c.src.asked, c.src.err = steps.RestoreFrom{}, nil, errors.New("not answering")
		_ = c.apply(t.Context())
		if want := []opened{{laptop, time.Time{}, false}}; !reflect.DeepEqual(c.src.asked, want) {
			t.Errorf("a run again asked for %+v, want laptop, %+v", c.src.asked, want)
		}
	})
}

// The run's source is recorded only once a copy from it starts: the old
// Mac asleep, a disk chosen on the card, then the run stopped before any
// copy, a run again tries the old Mac. kit apply --restore-from mac goes
// back to it after a backup was copied from, as trying it again on the
// card does.
func TestRestoreRecordsItsSourceOnceACopyStarts(t *testing.T) {
	c := newRestoring(t, "~/Code")
	c.fromLaptop()
	c.src.fails = []error{errSilent}
	d, _ := c.disk("Backups of laptop", snapshotX)
	k := c.atTerminal(t, restore.Disks{Found: []restore.Disk{d}})
	write(t, c.old, "~/Code/a", beforeIt)
	c.on(sizing, "~/Code").Exits(23)
	applied := c.applying(t)
	up(t, k, 1)
	k.answer(t, "restore", "t")
	if err := ended(t, applied); err == nil {
		t.Fatal("sizing failing, apply succeeded")
	}
	if rec := c.record(); rec.From != (restore.Origin{}) {
		t.Errorf("nothing copied, the record's source = %+v; want none", rec.From)
	}
	c.src.asked = nil
	c.copies("~/Code", 1, 1_000)
	if err := ended(t, c.applying(t)); err != nil {
		t.Fatalf("a run again, apply = %v", err)
	}
	if want := []opened{{laptop, time.Time{}, false}}; !reflect.DeepEqual(c.src.asked, want) || c.record().From != laptop {
		t.Errorf("a run again asked for %+v, recording %+v; want laptop", c.src.asked, c.record().From)
	}

	tm := restore.Origin{Kind: restore.FromTimeMachine, Detail: snapshotX}
	c = newRestoring(t, "~/Code", "~/Documents")
	c.fromLaptop()
	if err := restore.UpdateRecord(c.state, func(r *restore.Record) { r.From = tm }); err != nil {
		t.Fatal(err)
	}
	c.r.Flag = steps.RestoreFrom{Kind: restore.FromMac}
	write(t, c.old, "~/Code/a", beforeIt)
	write(t, c.old, "~/Documents/d", beforeIt)
	c.copies("~/Code", 1, 1_000)
	c.copies("~/Documents", 1, 1_000)
	if err := c.apply(t.Context()); err != nil {
		t.Fatalf("--restore-from mac, apply = %v", err)
	}
	if want := []opened{{laptop, time.Time{}, true}}; !reflect.DeepEqual(c.src.asked, want) || c.record().From != laptop {
		t.Errorf("--restore-from mac asked for %+v, recording %+v; want laptop", c.src.asked, c.record().From)
	}
}

// A path part-way from a Time Machine disk gone for good fails each run
// naming no source, with the paths of its source yet to begin, the error
// naming the ways on for every one; kit apply --restore-from mac carries
// it on from the old Mac, the log saying so, and copies the rest too.
func TestRestoreFlagsCarryOnFromADiskGone(t *testing.T) {
	tm := restore.Origin{Kind: restore.FromTimeMachine, Detail: snapshotX}
	c := newRestoring(t, "~/Code", "~/Documents")
	c.change("~/Code", func(p *restore.Path) { p.State, p.From, p.Started = restore.Copying, tm, duringIt })
	c.src.err = errors.New("no Time Machine disk holding backups of /Users/someone is attached")
	want := "couldn't restore: no Time Machine disk holding backups of /Users/someone is attached: " +
		"kit apply --restore-from mac|time-machine|arq restores ~/Code and ~/Documents from another source, or kit restore remove drops the line of any not wanted"
	if err := c.apply(t.Context()); err == nil || err.Error() != want {
		t.Errorf("naming no source, apply = %v\nwant %q", err, want)
	}

	c.src.err, c.src.asked = nil, nil
	c.fromLaptop()
	c.r.Flag = steps.RestoreFrom{Kind: restore.FromMac}
	write(t, c.old, "~/Code/a", beforeIt)
	write(t, c.old, "~/Documents/d", beforeIt)
	c.copies("~/Code", 1, 1_000)
	c.copies("~/Documents", 1, 1_000)
	if err := c.apply(t.Context()); err != nil {
		t.Fatalf("--restore-from mac, apply = %v", err)
	}
	if want := []opened{{laptop, time.Time{}, true}}; !reflect.DeepEqual(c.src.asked, want) {
		t.Errorf("--restore-from mac asked for %+v, want %+v", c.src.asked, want)
	}
	rec := c.record()
	for _, p := range []string{"~/Code", "~/Documents"} {
		if rec.Paths[p].State != restore.Done || rec.Paths[p].From != laptop {
			t.Errorf("%s's record = %+v; want it restored from laptop", p, rec.Paths[p])
		}
	}
	if want := "Carrying on ~/Code from laptop in place of Time Machine's backup of 8 Nov 2026 14:19, which its restore began from"; !slices.Contains(c.logged, want) {
		t.Errorf("the log says %q; want %q", c.logged, want)
	}
}

// kit apply's flags naming Time Machine stand in for every path part-way,
// whatever it began from: one from the old Mac and one from a backup
// thinned away both carry on from the backup Time Machine opens, each
// saying so.
func TestRestoreFlagsStandInForEveryPathPartWay(t *testing.T) {
	gone := restore.Origin{Kind: restore.FromTimeMachine, Detail: snapshotY}
	c := newRestoring(t, "~/Code", "~/Documents")
	c.change("~/Code", func(p *restore.Path) { p.State, p.From, p.Started = restore.Copying, laptop, duringIt })
	c.change("~/Documents", func(p *restore.Path) { p.State, p.From, p.Started = restore.Copying, gone, duringIt })
	c.r.Flag = steps.RestoreFrom{Kind: restore.FromTimeMachine}
	write(t, c.old, "~/Code/a", beforeIt)
	write(t, c.old, "~/Documents/d", beforeIt)
	c.copies("~/Code", 1, 1_000)
	c.copies("~/Documents", 1, 1_000)
	if err := c.apply(t.Context()); err != nil {
		t.Fatalf("apply = %v", err)
	}
	if want := []opened{{gone, time.Time{}, true}}; !reflect.DeepEqual(c.src.asked, want) {
		t.Errorf("the source asked for %+v, want %+v", c.src.asked, want)
	}
	rec := c.record()
	for p, was := range map[string]string{"~/Code": "laptop", "~/Documents": "Time Machine's backup of 1 Nov 2026 09:00"} {
		if rec.Paths[p].State != restore.Done || rec.Paths[p].From != c.src.origin {
			t.Errorf("%s's record = %+v; want it restored from %+v", p, rec.Paths[p], c.src.origin)
		}
		if want := "Carrying on " + p + " from Time Machine's backup of 8 Nov 2026 14:19 in place of " + was + ", which its restore began from"; !slices.Contains(c.logged, want) {
			t.Errorf("the log says %q; want %q", c.logged, want)
		}
	}
}

// Stopped while the card asks where to restore from, the restore fails,
// naming no ways on: no source failed to open.
func TestRestoreStoppedAtTheCardNamesNoWaysOn(t *testing.T) {
	c := newRestoring(t, "~/Code")
	c.fromLaptop()
	c.src.fails = []error{errSilent}
	k := c.atTerminal(t, restore.Disks{})
	ctx, stop := context.WithCancel(t.Context())
	defer stop()
	step := c.step()
	found := step.Check(ctx)
	applied := make(chan error, 1)
	go func() { applied <- step.Apply(c.ctx(ctx), found) }()
	up(t, k, 1)
	stop()
	if err := ended(t, applied); err == nil || strings.Contains(err.Error(), "kit restore remove") {
		t.Errorf("stopped, apply = %v; want it failing, naming no ways on", err)
	}
}

// Should kit not manage to look for Time Machine disks, the card still goes
// up, saying why, offering Arq and trying again.
func TestRestoreAsksWithoutTheDisks(t *testing.T) {
	c := newRestoring(t, "~/Code")
	c.fromLaptop()
	c.src.fails = []error{errSilent}
	k := c.atTerminal(t, restore.Disks{})
	c.r.Disks = func(context.Context) (restore.Disks, error) {
		return restore.Disks{}, errors.New("list the disks: diskutil exited 1")
	}
	write(t, c.old, "~/Code/a", beforeIt)
	c.copies("~/Code", 1, 1_000)
	applied := c.applying(t)
	card := up(t, k, 1)[0]
	if want := []check.Key{{Key: "a", Does: "restores through Arq, by hand"}, {Key: "enter", Does: "tries laptop again"}}; !reflect.DeepEqual(card.Keys, want) ||
		!strings.HasSuffix(card.Why, " kit couldn't look for Time Machine disks: list the disks: diskutil exited 1.") {
		t.Errorf("the card says %q, offering %+v", card.Why, card.Keys)
	}
	k.answer(t, "restore", "enter")
	if err := ended(t, applied); err != nil {
		t.Errorf("tried again, apply = %v", err)
	}
}

// A backup standing in for the old Mac carries on only the paths it has:
// one part-way from the old Mac the backup hasn't fails, keeping the old
// Mac as its source, never recorded as the backup's. So the old Mac back,
// a run again finishes it from there, named by --restore-from mac or
// naming no source; and a path still part-way from each, a run again
// naming no source copies each from its own.
func TestRestoreStandInLackingAPath(t *testing.T) {
	standIn := func(t *testing.T, stop bool) (*restoring, error) {
		c := newRestoring(t, "~/Code", "~/Documents")
		for _, p := range []string{"~/Code", "~/Documents"} {
			c.change(p, func(p *restore.Path) { p.State, p.From, p.Started = restore.Copying, laptop, duringIt })
		}
		c.r.Flag = steps.RestoreFrom{Kind: restore.FromTimeMachine}
		write(t, c.old, "~/Code/a", beforeIt)
		c.copies("~/Code", 1, 1_000)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		if stop {
			c.on(copyingOp, "~/Code").Does(cancel).Hangs()
		}
		err := c.apply(ctx)
		if want := "couldn't restore ~/Documents: Time Machine's backup of 8 Nov 2026 14:19 hasn't it"; err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("standing in, apply = %v; want ~/Documents failing: %q", err, want)
		}
		rec := c.record()
		if docs := rec.Paths["~/Documents"]; docs.State != restore.Copying || docs.From != laptop || !rec.Settled.IsZero() {
			t.Errorf("~/Documents' record = %+v; want it still part-way from laptop, not settled", docs)
		}
		if slices.ContainsFunc(c.logged, func(l string) bool { return strings.HasPrefix(l, "Carrying on ~/Documents") }) {
			t.Errorf("the log says %q; want nothing carried on for ~/Documents", c.logged)
		}
		c.src.asked = nil
		return c, err
	}
	for _, tt := range []struct {
		name string
		flag steps.RestoreFrom
	}{
		{"--restore-from mac", steps.RestoreFrom{Kind: restore.FromMac}},
		{"naming no source", steps.RestoreFrom{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := standIn(t, false)
			c.r.Flag = tt.flag
			c.fromLaptop()
			write(t, c.old, "~/Documents/d", beforeIt)
			c.copies("~/Documents", 1, 1_000)
			if err := c.apply(t.Context()); err != nil {
				t.Fatalf("the old Mac back, apply = %v", err)
			}
			if want := []opened{{laptop, time.Time{}, tt.flag.Kind != ""}}; !reflect.DeepEqual(c.src.asked, want) {
				t.Errorf("the source asked for %+v, want %+v", c.src.asked, want)
			}
			if rec := c.record(); rec.Paths["~/Documents"].State != restore.Done || rec.Paths["~/Documents"].From != laptop || rec.Settled.IsZero() {
				t.Errorf("the record = %+v; want ~/Documents restored from laptop, settled", rec)
			}
		})
	}
	t.Run("each still part-way", func(t *testing.T) {
		c, _ := standIn(t, true)
		tm := restore.Origin{Kind: restore.FromTimeMachine, Detail: snapshotX}
		if code := c.record().Paths["~/Code"]; code.State != restore.Copying || code.From != tm {
			t.Fatalf("stopped, ~/Code's record = %+v; want it part-way from the backup", code)
		}
		c.r.Flag, c.src.err = steps.RestoreFrom{}, errors.New("not attached")
		err := c.apply(t.Context())
		if want := []opened{{tm, time.Time{}, false}, {laptop, time.Time{}, false}}; !reflect.DeepEqual(c.src.asked, want) || err == nil {
			t.Errorf("a run again = %v, asking for %+v; want each path's own source, %+v, neither attached", err, c.src.asked, want)
		}
	})
}

// The person saying Arq's backup hasn't a path part-way from elsewhere
// fails it, saying so, and naming the ways on: it's never recorded not
// there, keeping the source it began from, and the restore doesn't settle.
func TestRestoreThroughArqHasntAPartWayPath(t *testing.T) {
	c := newRestoring(t, "~/Code", "~/Documents")
	for _, p := range []string{"~/Code", "~/Documents"} {
		c.change(p, func(p *restore.Path) { p.State, p.From, p.Started = restore.Copying, laptop, duringIt })
	}
	c.fromLaptop()
	c.src.fails = []error{errSilent}
	k := c.atTerminal(t, restore.Disks{})
	applied := c.applying(t)
	up(t, k, 1)
	k.answer(t, "restore", "a")
	up(t, k, 2)
	write(t, c.arqHold(1), "~/Code/a", beforeIt)
	c.copiesFrom(filepath.Join(c.arqHold(1), "Code"), "~/Code", 1, 1_000)
	k.answer(t, "restore", hand.Done)
	if lack := up(t, k, 3)[2]; !slices.Equal(lack.Steps, []string{"~/Documents: …/arq/2 holds nothing yet"}) {
		t.Errorf("the card's steps = %q; want ~/Documents' alone", lack.Steps)
	}
	k.answer(t, "restore", "n")
	err := ended(t, applied)
	if want := "couldn't restore ~/Documents: Arq's restore in " + filepath.Join(c.state, "restore", "arq") + " hasn't it: " +
		"kit apply --restore-from mac|time-machine|arq restores ~/Documents from another source, or kit restore remove ~/Documents drops its line"; err == nil || err.Error() != want {
		t.Errorf("apply = %v; want %q", err, want)
	}
	rec := c.record()
	if docs := rec.Paths["~/Documents"]; docs.State != restore.Copying || docs.From != laptop || !rec.Settled.IsZero() || rec.Paths["~/Code"].State != restore.Done {
		t.Errorf("the record = %+v; want ~/Code restored, ~/Documents still part-way from laptop, not settled", rec)
	}
}

// Of the holding folders not holding their paths, the person can say the
// backup hasn't only those holding nothing: one holding something else
// stays on the card, its files kept, till it's moved into its own.
func TestRestoreThroughArqKeepsAFolderHoldingSomethingElse(t *testing.T) {
	c := newRestoring(t, "~/Code", "~/Gone")
	c.r.Flag = steps.RestoreFrom{Kind: restore.FromArq}
	k := c.atTerminal(t, restore.Disks{})
	c.copiesFrom(filepath.Join(c.arqHold(1), "Code"), "~/Code", 1, 1_000)
	applied := c.applying(t)
	up(t, k, 1)
	write(t, c.arqHold(1), "~/app/main.go", beforeIt)
	k.answer(t, "restore", hand.Done)
	lack := up(t, k, 2)[1]
	if want := []string{"~/Code: …/arq/1 holds app, not Code", "~/Gone: …/arq/2 holds nothing yet"}; !slices.Equal(lack.Steps, want) || len(lack.Keys) != 2 {
		t.Errorf("the card = %+v; want both, n offered", lack)
	}
	k.answer(t, "restore", "n")
	lack = up(t, k, 3)[2]
	if want := []check.Key{{Key: "enter", Does: "looks again"}}; !slices.Equal(lack.Steps, []string{"~/Code: …/arq/1 holds app, not Code"}) || !reflect.DeepEqual(lack.Keys, want) {
		t.Errorf("after n, the card = %+v; want ~/Code's still, n no longer offered", lack)
	}
	if _, err := os.Stat(filepath.Join(c.arqHold(1), "app", "main.go")); err != nil {
		t.Errorf("~/Code's holding folder's files went: %v", err)
	}
	if err := os.Rename(filepath.Join(c.arqHold(1), "app"), filepath.Join(c.arqHold(1), "Code")); err != nil {
		t.Fatal(err)
	}
	k.answer(t, "restore", "enter")
	if err := ended(t, applied); err != nil {
		t.Fatalf("apply = %v", err)
	}
	if rec := c.record(); rec.Paths["~/Code"].State != restore.Done || rec.Paths["~/Gone"].State != restore.NotThere {
		t.Errorf("the record = %+v; want ~/Code restored, ~/Gone not there", rec.Paths)
	}
}

// kit apply --restore-from arq stands in for a path part-way from Time
// Machine, as any source the flags name does: Arq's card asks for it, and
// it carries on from Arq's restore, the log saying so.
func TestRestoreThroughArqStandsInForABackup(t *testing.T) {
	tm := restore.Origin{Kind: restore.FromTimeMachine, Detail: snapshotX}
	c := newRestoring(t, "~/Code")
	c.change("~/Code", func(p *restore.Path) { p.State, p.From, p.Started = restore.Copying, tm, duringIt })
	c.r.Flag = steps.RestoreFrom{Kind: restore.FromArq}
	k := c.atTerminal(t, restore.Disks{})
	c.copiesFrom(filepath.Join(c.arqHold(1), "Code"), "~/Code", 1, 1_000)
	applied := c.applying(t)
	if card := up(t, k, 1)[0]; !slices.Equal(card.Steps[2:], []string{"~/Code → …/arq/1"}) {
		t.Errorf("Arq's card asks for %q; want ~/Code", card.Steps[2:])
	}
	write(t, c.arqHold(1), "~/Code/a", beforeIt)
	k.answer(t, "restore", hand.Done)
	if err := ended(t, applied); err != nil {
		t.Fatalf("apply = %v", err)
	}
	arqd := restore.Origin{Kind: restore.FromArq, Detail: filepath.Join(c.state, "restore", "arq")}
	if code := c.record().Paths["~/Code"]; code.State != restore.Done || code.From != arqd {
		t.Errorf("~/Code's record = %+v; want it restored from %+v", code, arqd)
	}
	if want := "Carrying on ~/Code from " + arqd.Says() + " in place of Time Machine's backup of 8 Nov 2026 14:19, which its restore began from"; !slices.Contains(c.logged, want) {
		t.Errorf("the log says %q; want %q", c.logged, want)
	}
}

// The old Mac not answering, the person choosing Arq on its card in a run
// already restoring through Arq, its paths join that restore: Arq's card
// goes up once, after the old Mac's, asking for all those its holding
// folders don't hold yet.
func TestRestoreThroughArqOnceARun(t *testing.T) {
	c := newRestoring(t, "~/Documents", "~/Notes", "~/Code")
	arqd := restore.Origin{Kind: restore.FromArq, Detail: filepath.Join(c.state, "restore", "arq")}
	c.change("~/Documents", func(p *restore.Path) { p.State, p.From, p.Started, p.Holding = restore.Copying, arqd, duringIt, 1 })
	c.change("~/Code", func(p *restore.Path) { p.State, p.From, p.Started = restore.Copying, laptop, duringIt })
	write(t, c.arqHold(1), "~/Documents/d", beforeIt)
	c.fromLaptop()
	c.src.fails = []error{errSilent}
	k := c.atTerminal(t, restore.Disks{})
	c.copiesFrom(filepath.Join(c.arqHold(1), "Documents"), "~/Documents", 1, 1_000)
	c.copiesFrom(filepath.Join(c.arqHold(2), "Notes"), "~/Notes", 1, 1_000)
	c.copiesFrom(filepath.Join(c.arqHold(3), "Code"), "~/Code", 1, 1_000)
	applied := c.applying(t)
	if card := up(t, k, 1)[0]; card.Item != "restore:source" {
		t.Fatalf("the first card = %+v; want the old Mac's", card)
	}
	k.answer(t, "restore", "a")
	if card := up(t, k, 2)[1]; card.Item != "restore:arq" || !slices.Equal(card.Steps[2:], []string{"~/Notes → …/arq/2", "~/Code → …/arq/3"}) {
		t.Errorf("Arq's card = %+v; want it asking for ~/Notes and ~/Code", card)
	}
	write(t, c.arqHold(2), "~/Notes/n", beforeIt)
	write(t, c.arqHold(3), "~/Code/a", beforeIt)
	k.answer(t, "restore", hand.Done)
	if err := ended(t, applied); err != nil {
		t.Fatalf("apply = %v", err)
	}
	if cards := raised(k); len(cards) != 2 {
		t.Errorf("%d cards went up; want the old Mac's and Arq's, once", len(cards))
	}
	rec := c.record()
	for _, p := range []string{"~/Documents", "~/Notes", "~/Code"} {
		if rec.Paths[p].State != restore.Done || rec.Paths[p].From != arqd {
			t.Errorf("%s's record = %+v; want it restored from %+v", p, rec.Paths[p], arqd)
		}
	}
}

// The person saying the backup hasn't the paths whose holding folders hold
// nothing, those are looked at again: one Arq filled while the card was up
// is copied, never recorded not there. A holding folder filled after that,
// as its path's recorded not there, is kept, and keeps its number, so it's
// never handed to another path.
func TestRestoreThroughArqHasntLooksAgain(t *testing.T) {
	c := newRestoring(t, "~/Code", "~/Documents", "~/Gone")
	c.r.Flag = steps.RestoreFrom{Kind: restore.FromArq}
	k := c.atTerminal(t, restore.Disks{})
	code := filepath.Join(c.arqHold(1), "Code")
	c.copiesFrom(code, "~/Code", 1, 1_000)
	c.onFrom(sizing, code).Prints(stats(1, 1_000)).Does(func() { write(t, c.arqHold(3), "~/Gone/late", beforeIt) })
	c.copiesFrom(filepath.Join(c.arqHold(2), "Documents"), "~/Documents", 1, 1_000)
	applied := c.applying(t)
	up(t, k, 1)
	write(t, c.arqHold(1), "~/Code/a", beforeIt)
	k.answer(t, "restore", hand.Done)
	if lack := up(t, k, 2)[1]; !slices.Equal(lack.Steps, []string{"~/Documents: …/arq/2 holds nothing yet", "~/Gone: …/arq/3 holds nothing yet"}) {
		t.Errorf("the card's steps = %q; want ~/Documents' and ~/Gone's", lack.Steps)
	}
	write(t, c.arqHold(2), "~/Documents/d", beforeIt)
	k.answer(t, "restore", "n")
	if err := ended(t, applied); err != nil {
		t.Fatalf("apply = %v", err)
	}
	rec := c.record()
	if docs, gone := rec.Paths["~/Documents"], rec.Paths["~/Gone"]; docs.State != restore.Done || gone.State != restore.NotThere || gone.Holding != 3 {
		t.Errorf("the record = %+v; want ~/Documents restored, ~/Gone not there, keeping its holding folder's number", rec.Paths)
	}
	if _, err := os.Stat(filepath.Join(c.arqHold(3), "Gone", "late")); err != nil {
		t.Errorf("~/Gone's holding folder's files went: %v", err)
	}
}
