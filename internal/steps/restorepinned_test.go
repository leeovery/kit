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

// laptopAt is where the old Mac answers on the network, offering Remote
// Login as the instance Laptop.
const laptopAt = "laptop.example.com"

// onTheNetwork has c restore from laptop as kit finds it on the network,
// the Mac it looks for the one presenting key, kit-config's host key for
// it ("" for none), and, tried again, fetched, the one kit-config's remote
// records then: one Mac offers Remote Login, answering at laptopAt. It
// returns the old Mac's source, as the restore opens it.
func (c *restoring) onTheNetwork(key, fetched string) *oldmac.Source {
	src := &oldmac.Source{Finder: oldmac.Finder{Run: c.fake, StateDir: c.state, HostKey: key}, From: "laptop", User: "someone", Home: c.home,
		HostKey: func(context.Context) (string, error) { return fetched, nil }}
	c.r.Sources = func(restore.Origin, time.Time, bool) restore.Opener { return src }
	c.fake.On("dns-sd", "-B", "_ssh._tcp", "local.").Prints("Browsing for _ssh._tcp.local.\nTimestamp     A/R    Flags  if Domain               Service Type         Instance Name\n" +
		"21:38:59.104  Add        3  13 local.               _ssh._tcp.           Laptop\n")
	c.fake.On("dns-sd", "-L", "Laptop", "_ssh._tcp", "local.").Prints("Lookup Laptop._ssh._tcp.local.\n" +
		"21:39:08.624  Laptop._ssh._tcp.local. can be reached at " + laptopAt + ".:22 (interface 13) Flags: 1\n")
	return src
}

// probed scripts ssh's probe of the old Mac: what it asks it, as it
// answers.
func (c *restoring) probed() *runnertest.Script {
	return c.fake.OnMatch("ssh", func(args []string) bool {
		return len(args) > 2 && args[len(args)-2] == laptopAt && strings.Contains(args[len(args)-1], "kit/machine")
	})
}

// laptopSays is the probe's answer from laptop, presenting key: kit's on
// it, as laptop, someone at this Mac's home, its ~/Documents read, and
// Homebrew's rsync 3.
func (c *restoring) laptopSays(key string) string {
	return "user=someone\nhome=" + c.home + "\nkit=laptop\nprotected=read Documents\n" +
		"rsync=rsync  version 3.4.1  protocol version 32\nrsync=Capabilities:\nrsync=    xattrs, crtimes\nhostkey=" + key + "\n"
}

// pinnedKey is the old Mac's host key, as kit-config records it, plainly no
// real one.
const pinnedKey = "ssh-ed25519 ZmFrZSBlZDI1NTE5IGhvc3Qga2V5"

// The old Mac is found on the network as the Mac presenting the host key
// kit-config records for it, ssh told to trust that key alone, under the
// Mac's kit name, for the probe, the look and the copy; it's copied from.
func TestRestoreFromTheOldMacPinned(t *testing.T) {
	c := newRestoring(t, "~/.zsh_history")
	c.onTheNetwork(pinnedKey, pinnedKey)
	c.probed().Prints(c.laptopSays(pinnedKey))
	c.fake.OnMatch("ssh", func(args []string) bool { return strings.Contains(args[len(args)-1], "\nlook ") }).Prints("file\x00")
	c.copiesFrom(laptopAt+":"+filepath.Join(c.home, ".zsh_history"), "~/.zsh_history", 1, 1_000)

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
}

// A Mac on the network presenting another host key than kit-config's isn't
// the old Mac, whatever it says its name is: none answers as it, nothing's
// copied, and, without a terminal, the restore fails, saying so and the
// ways on.
func TestRestoreRefusesAnotherHostKey(t *testing.T) {
	c := newRestoring(t, "~/.zsh_history")
	c.onTheNetwork(pinnedKey, pinnedKey)
	c.probed().PrintsToStderr("Host key for laptop has changed and you have requested strict checking.\nHost key verification failed.\n").Exits(255)

	err := c.apply(t.Context())
	want := "couldn't restore: laptop doesn't answer over SSH: Laptop isn't laptop: its host key doesn't match the one kit-config records (kit apply on laptop records a new one): " +
		"kit apply --restore-from time-machine|arq restores ~/.zsh_history from another source, or kit restore remove ~/.zsh_history drops its line"
	if err == nil || err.Error() != want {
		t.Errorf("apply = %v\nwant %q", err, want)
	}
	if ran := c.ran(); len(ran) > 0 {
		t.Errorf("rsync ran %q; want nothing copied", ran)
	}
}

// With no host key recorded for the old Mac, it isn't looked for, never
// trusted on first sight: the card for an old Mac that doesn't answer goes
// up, saying why and how kit-config comes to record it, offering Arq or
// trying again.
func TestRestoreWithoutTheOldMacsHostKeyAsks(t *testing.T) {
	c := newRestoring(t, "~/.zsh_history")
	c.onTheNetwork("", "")
	k := c.atTerminal(t, restore.Disks{})
	step := c.step()
	ctx, stop := context.WithCancel(t.Context())
	found := step.Check(ctx)
	done := make(chan error, 1)
	go func() { done <- step.Apply(c.ctx(ctx), found) }()

	card := up(t, k, 1)[0]
	want := event.NeedsYou{Time: card.Time, Step: "restore", Item: "restore:source", Title: "laptop can't be restored from",
		Why: "Laptop's SSH host key isn't in kit-config: kit apply on laptop records it. No Time Machine disk holding your backups is attached: attach it, then try again.",
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
	if calls := c.fake.Calls(); slices.ContainsFunc(calls, func(call string) bool { return strings.HasPrefix(call, "dns-sd") || strings.HasPrefix(call, "ssh") }) {
		t.Errorf("ran %q; want no Mac looked for", calls)
	}
}

// ssh having no route to the old Mac, as till macOS lets the terminal find
// devices on local networks, the card says so, naming the terminal: choose
// Allow, then try again, or turn it on in System Settings.
func TestRestoreSaysTheLocalNetworkMayBeAsked(t *testing.T) {
	c := newRestoring(t, "~/.zsh_history")
	c.onTheNetwork(pinnedKey, pinnedKey).Finder.Terminal = "Ghostty"
	c.probed().PrintsToStderr("ssh: connect to host " + laptopAt + " port 22: No route to host\n").Exits(255)
	k := c.atTerminal(t, restore.Disks{})
	step := c.step()
	ctx, stop := context.WithCancel(t.Context())
	found := step.Check(ctx)
	done := make(chan error, 1)
	go func() { done <- step.Apply(c.ctx(ctx), found) }()

	card := up(t, k, 1)[0]
	want := event.NeedsYou{Time: card.Time, Step: "restore", Item: "restore:source", Title: "laptop doesn't answer over SSH",
		Why: "Laptop didn't answer: macOS may be asking whether Ghostty may find devices on local networks: choose Allow, then try again, or, " +
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

// Tried again from the card, the old Mac is looked for by the host key
// kit-config's remote records for it then, as kit apply on it records it
// meanwhile: none recorded when the run began, or the key it had before
// it was set up again; found by the new one, it's copied from.
func TestRestoreTriedAgainTakesTheHostKeyRecordedSince(t *testing.T) {
	for _, tt := range []struct {
		name, key, title string
	}{
		{"none recorded", "", "laptop can't be restored from"},
		{"set up again", pinnedKey, "laptop doesn't answer over SSH"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := newRestoring(t, "~/.zsh_history")
			c.onTheNetwork(tt.key, newKey)
			probe := c.probed()
			if tt.key != "" {
				probe.PrintsToStderr("Host key verification failed.\n").Exits(255).Then()
			}
			probe.Prints(c.laptopSays(newKey))
			c.fake.OnMatch("ssh", func(args []string) bool { return strings.Contains(args[len(args)-1], "\nlook ") }).Prints("file\x00")
			c.copiesFrom(laptopAt+":"+filepath.Join(c.home, ".zsh_history"), "~/.zsh_history", 1, 1_000)
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
			if data, err := os.ReadFile(filepath.Join(c.state, "restore", "pinned_host")); err != nil || string(data) != "laptop "+newKey+"\n" {
				t.Errorf("the pinned key's file = %q, %v; want the key fetched", data, err)
			}
		})
	}
}

// Without a terminal, no host key recorded for the old Mac fails the
// restore, naming the way on: kit apply on it, then here.
func TestRestoreWithoutTheOldMacsHostKeyNamesTheWayOn(t *testing.T) {
	c := newRestoring(t, "~/.zsh_history")
	c.onTheNetwork("", "")
	err := c.apply(t.Context())
	want := "couldn't restore: laptop can't be restored from: laptop's SSH host key isn't in kit-config: kit apply on laptop records it, then kit apply here: " +
		"kit apply --restore-from time-machine|arq restores ~/.zsh_history from another source, or kit restore remove ~/.zsh_history drops its line"
	if err == nil || err.Error() != want {
		t.Errorf("apply = %v\nwant %q", err, want)
	}
}
