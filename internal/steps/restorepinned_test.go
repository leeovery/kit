package steps_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/restore"
	"github.com/leeovery/kit/internal/restore/oldmac"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// laptopName is the old Mac's local hostname, as kit-config records it;
// laptopAt, where it answers on the local network.
const (
	laptopName = "Someones-MacBook-Pro"
	laptopAt   = laptopName + ".local"
)

// record is what kit-config records for the old Mac: its host key and
// local hostname, "" for none.
type record struct{ key, name string }

// reaching has c restore from laptop, reached as kit-config records it,
// at, and, tried again, as kit-config's remote records it then, fetched,
// each fetch said in fetches. It returns the old Mac's source, as the
// restore opens it.
func (c *restoring) reaching(at, fetched record) (src *oldmac.Source, fetches *int) {
	fetches = new(int)
	src = &oldmac.Source{
		Finder: oldmac.Finder{Run: c.fake, StateDir: c.state, HostKey: at.key, LocalHostName: at.name}, From: "laptop", User: "someone", Home: c.home,
		Recorded: func(context.Context) (string, string, error) {
			*fetches++
			return fetched.key, fetched.name, nil
		},
	}
	c.r.Sources = func(restore.Origin, time.Time, bool) restore.Opener { return src }
	return src, fetches
}

// probed scripts ssh's probe of the old Mac: what it asks it, as it
// answers.
func (c *restoring) probed() *runnertest.Script {
	return c.fake.OnMatch("ssh", func(args []string) bool {
		return len(args) > 2 && args[len(args)-2] == laptopAt && strings.Contains(args[len(args)-1], "kit/machine")
	})
}

// laptopSays is the probe's answer from laptop: kit's on it, as laptop,
// someone at this Mac's home, its ~/Documents read, and Homebrew's rsync 3.
func (c *restoring) laptopSays() string {
	return "user=someone\nhome=" + c.home + "\nkit=laptop\nprotected=read Documents\n" +
		"rsync=rsync  version 3.4.1  protocol version 32\nrsync=Capabilities:\nrsync=    xattrs, crtimes\n"
}

// recorded is laptop as kit-config records it, by the new kit.
var recorded = record{pinnedKey, laptopName}

// pinnedKey is the old Mac's host key, as kit-config records it, plainly no
// real one.
const pinnedKey = "ssh-ed25519 ZmFrZSBlZDI1NTE5IGhvc3Qga2V5"

// The old Mac is reached at the local hostname kit-config records for it,
// then .local, as the Mac presenting the host key kit-config records, ssh
// told to trust that key alone, under the Mac's kit name, for the probe,
// the look and the copy; it's copied from at its first try, nothing
// fetched.
func TestRestoreFromTheOldMacPinned(t *testing.T) {
	c := newRestoring(t, "~/.zsh_history")
	_, fetches := c.reaching(recorded, record{})
	c.probed().Prints(c.laptopSays())
	c.fake.OnMatch("ssh", func(args []string) bool { return strings.Contains(args[len(args)-1], "\nlook ") }).Prints("file\x00")
	c.copiesFrom(laptopAt+":"+filepath.Join(c.home, ".zsh_history"), 1, 1_000)

	if err := c.apply(t.Context()); err != nil {
		t.Fatalf("apply = %v", err)
	}
	if rec := c.record(); rec.From != laptop || rec.Paths["~/.zsh_history"].State != restore.Done {
		t.Errorf("the record = %+v; want ~/.zsh_history restored from laptop", rec)
	}
	pinned := filepath.Join(c.state, "restore", "pinned_host")
	if data, err := os.ReadFile(pinned); err != nil || string(data) != "laptop "+pinnedKey+"\n" {
		t.Errorf("the pinned key's file = %q, %v; want laptop's key", data, err)
	}
	trusts := []string{"StrictHostKeyChecking=yes", "UserKnownHostsFile=" + pinned, "HostKeyAlias=laptop"}
	ran := 0
	for _, cmd := range c.fake.Commands() {
		said := strings.Join(cmd.Args, " ")
		copying := cmd.Name == "rsync" && opOf(cmd.Args) == copyingOp
		if cmd.Name != "ssh" && !copying {
			continue
		}
		ran++
		for _, want := range trusts {
			if !strings.Contains(said, want) {
				t.Errorf("%s %s\nwant it holding %s", cmd.Name, said, want)
			}
		}
	}
	if ran != 3 {
		t.Errorf("ssh and the copy ran %d times, want 3: the probe, the look and the copy", ran)
	}
	if *fetches != 0 {
		t.Errorf("fetched kit-config %d times; want none at the first try", *fetches)
	}
}

// A Mac at the old Mac's name presenting another host key than
// kit-config's isn't the old Mac, whatever it says its name is: nothing's
// copied, and, without a terminal, the restore fails, saying so and the
// ways on.
func TestRestoreRefusesAnotherHostKey(t *testing.T) {
	c := newRestoring(t, "~/.zsh_history")
	c.reaching(recorded, recorded)
	c.probed().PrintsToStderr("Host key for laptop has changed and you have requested strict checking.\nHost key verification failed.\n").Exits(255)

	err := c.apply(t.Context())
	want := "couldn't restore: laptop doesn't answer over SSH: " + laptopAt + " isn't laptop: its host key doesn't match the one kit-config records (kit apply on laptop records a new one): " +
		"kit apply --restore-from time-machine|arq restores ~/.zsh_history from another source, or kit restore remove ~/.zsh_history drops its line"
	if err == nil || err.Error() != want {
		t.Errorf("apply = %v\nwant %q", err, want)
	}
	if ran := c.ran(); len(ran) > 0 {
		t.Errorf("rsync ran %q; want nothing copied", ran)
	}
}

// Without the old Mac's host key or local hostname in kit-config, as for a
// Mac an older kit recorded, it isn't reached, never trusted on first
// sight: the card for an old Mac that doesn't answer goes up, saying why
// and the way on, kit apply there, then trying again, offering Arq too.
// Nothing answering at the name recorded is said the same way, as a Mac
// renamed since answers elsewhere.
func TestRestoreUnrecordedAsks(t *testing.T) {
	for _, tt := range []struct {
		name         string
		at           record
		title, why   string
		silentLaptop bool
	}{
		{"no host key", record{"", laptopName}, "laptop can't be restored from", "Laptop's SSH host key isn't in kit-config: kit apply on laptop records it, then try again.", false},
		{"no local hostname", record{pinnedKey, ""}, "laptop can't be restored from", "Laptop's local hostname isn't in kit-config: kit apply on laptop records it, then try again.", false},
		{"nothing answering at it", recorded, "laptop doesn't answer over SSH",
			"Nothing answered at " + laptopAt + ", its local hostname in kit-config: with laptop awake on this network, kit apply on laptop records the one it has now, then try again.", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := newRestoring(t, "~/.zsh_history")
			c.reaching(tt.at, tt.at)
			if tt.silentLaptop {
				c.probed().PrintsToStderr("ssh: Could not resolve hostname " + laptopAt + ": nodename nor servname provided, or not known\n").Exits(255)
			}
			k := c.atTerminal(t, restore.Disks{})
			step := c.step()
			ctx, stop := context.WithCancel(t.Context())
			found := step.Check(ctx)
			done := make(chan error, 1)
			go func() { done <- step.Apply(c.ctx(ctx), found) }()

			card := up(t, k, 1)[0]
			want := event.NeedsYou{Time: card.Time, Step: "restore", Item: "restore:source", Title: tt.title,
				Why: tt.why + " No Time Machine disk holding your backups is attached: attach it, then try again.",
				Keys: []check.Key{
					{Key: "a", Does: "restores through Arq, by hand"},
					{Key: "enter", Does: "tries laptop again"},
				}}
			if !reflect.DeepEqual(card, want) {
				t.Errorf("the card = %+v\nwant %+v", card, want)
			}
			stop()
			if err := ended(t, done); !errors.Is(err, context.Canceled) {
				t.Errorf("apply = %v; want it stopped with the run", err)
			}
			if calls := c.fake.Calls(); !tt.silentLaptop && slices.ContainsFunc(calls, func(call string) bool { return strings.HasPrefix(call, "ssh") }) {
				t.Errorf("ran %q; want the old Mac not reached", calls)
			}
		})
	}
}

// ssh having no route to the old Mac, as till macOS lets the terminal find
// devices on local networks, the card says so, naming the terminal: choose
// Allow, then try again, or turn it on in System Settings.
func TestRestoreSaysTheLocalNetworkMayBeAsked(t *testing.T) {
	c := newRestoring(t, "~/.zsh_history")
	src, _ := c.reaching(recorded, recorded)
	src.Finder.Terminal = "Ghostty"
	c.probed().PrintsToStderr("ssh: connect to host " + laptopAt + " port 22: No route to host\n").Exits(255)
	k := c.atTerminal(t, restore.Disks{})
	step := c.step()
	ctx, stop := context.WithCancel(t.Context())
	found := step.Check(ctx)
	done := make(chan error, 1)
	go func() { done <- step.Apply(c.ctx(ctx), found) }()

	card := up(t, k, 1)[0]
	want := event.NeedsYou{Time: card.Time, Step: "restore", Item: "restore:source", Title: "laptop doesn't answer over SSH",
		Why: laptopAt + " didn't answer: macOS may be asking whether Ghostty can find devices on local networks: choose Allow, then try again, or, " +
			"if Don't Allow was chosen, turn on Ghostty in System Settings › Privacy & Security › Local Network. " +
			"No Time Machine disk holding your backups is attached: attach it, then try again.",
		Keys: []check.Key{
			{Key: "a", Does: "restores through Arq, by hand"},
			{Key: "enter", Does: "tries laptop again"},
		}}
	if !reflect.DeepEqual(card, want) {
		t.Errorf("the card = %+v\nwant %+v", card, want)
	}
	stop()
	if err := ended(t, done); !errors.Is(err, context.Canceled) {
		t.Errorf("apply = %v; want it stopped with the run", err)
	}
}

// newKey is the old Mac's host key once it's set up again, plainly no real
// one.
const newKey = "ssh-ed25519 ZmFrZSBuZXcgaG9zdCBrZXk="

// Tried again from the card, kit-config is fetched, and the old Mac
// reached as its remote records it then, as kit apply on it records it
// meanwhile, after the boot cloned kit-config: no local hostname recorded
// when the run began, as for a Mac an older kit recorded; none at all; a
// name nothing answers at, as for a Mac renamed since; or the key it had
// before it was set up again. Reached as recorded since, it's copied from.
func TestRestoreTriedAgainTakesWhatsRecordedSince(t *testing.T) {
	renamed := record{pinnedKey, "Someones-Old-Name"}
	for _, tt := range []struct {
		name  string
		at    record
		title string
	}{
		{"no local hostname", record{pinnedKey, ""}, "laptop can't be restored from"},
		{"nothing recorded", record{}, "laptop can't be restored from"},
		{"renamed since", renamed, "laptop doesn't answer over SSH"},
		{"set up again", record{pinnedKey, laptopName}, "laptop doesn't answer over SSH"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := newRestoring(t, "~/.zsh_history")
			_, fetches := c.reaching(tt.at, record{newKey, laptopName})
			probe := c.probed()
			switch tt.at {
			case renamed:
				c.fake.OnMatch("ssh", func(args []string) bool { return len(args) > 2 && args[len(args)-2] == renamed.name+".local" }).
					PrintsToStderr("ssh: Could not resolve hostname " + renamed.name + ".local: nodename nor servname provided, or not known\n").Exits(255)
			case record{pinnedKey, laptopName}:
				probe.PrintsToStderr("Host key verification failed.\n").Exits(255).Then()
			}
			probe.Prints(c.laptopSays())
			c.fake.OnMatch("ssh", func(args []string) bool { return strings.Contains(args[len(args)-1], "\nlook ") }).Prints("file\x00")
			c.copiesFrom(laptopAt+":"+filepath.Join(c.home, ".zsh_history"), 1, 1_000)
			k := c.atTerminal(t, restore.Disks{})
			applied := c.applying(t)

			if card := up(t, k, 1)[0]; card.Title != tt.title {
				t.Errorf("the card's title = %q, want %q", card.Title, tt.title)
			}
			k.answer(t, "restore", "enter")
			if err := ended(t, applied); err != nil {
				t.Fatalf("apply = %v", err)
			}
			if rec := c.record(); rec.Paths["~/.zsh_history"].State != restore.Done {
				t.Errorf("the record = %+v; want ~/.zsh_history restored", rec)
			}
			if *fetches != 1 {
				t.Errorf("fetched kit-config %d times, want once, as it was tried again", *fetches)
			}
			if data, err := os.ReadFile(filepath.Join(c.state, "restore", "pinned_host")); err != nil || string(data) != "laptop "+newKey+"\n" {
				t.Errorf("the pinned key's file = %q, %v; want the key fetched", data, err)
			}
		})
	}
}

// Without a terminal, the old Mac not recorded as it is fails the restore,
// naming the way on: kit apply on it, then here.
func TestRestoreUnrecordedNamesTheWayOn(t *testing.T) {
	for _, tt := range []struct {
		name, why string
		at        record
	}{
		{"no host key", "laptop can't be restored from: laptop's SSH host key isn't in kit-config: kit apply on laptop records it", record{"", laptopName}},
		{"no local hostname", "laptop can't be restored from: laptop's local hostname isn't in kit-config: kit apply on laptop records it", record{pinnedKey, ""}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := newRestoring(t, "~/.zsh_history")
			c.reaching(tt.at, tt.at)
			err := c.apply(t.Context())
			want := "couldn't restore: " + tt.why + ", then kit apply here: " +
				"kit apply --restore-from time-machine|arq restores ~/.zsh_history from another source, or kit restore remove ~/.zsh_history drops its line"
			if err == nil || err.Error() != want {
				t.Errorf("apply = %v\nwant %q", err, want)
			}
		})
	}
}
