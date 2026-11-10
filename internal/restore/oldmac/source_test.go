package oldmac

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/restore"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// The old Mac, as the restore opens it: found by its kit name, checked,
// and opened live, what's at the listed paths there looked at in one go
// through it, as the restore takes it: each path's kind, and what its
// backups leave out as a line's --skip has it, escaped.
func TestSourceOpensTheOldMac(t *testing.T) {
	fake := runnertest.New(t)
	browses(fake, browsing(laptop))
	resolves(fake, laptop, reaching(laptop, laptopAt, 22))
	asked(fake, laptopAt).Prints(says(t, "laptop"))
	looked(fake, laptopAt).Prints("folder\x00./app/node_modules\x00./web [x]/vendor\x00folder\x00file\x00none\x00")
	src := &Source{Finder: finder(t, fake), From: "laptop", User: user, Home: home}

	opened, err := src.Open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if opened.Host != laptopAt || opened.Home != home || !opened.Live || opened.Origin != (restore.Origin{Kind: restore.FromMac, Detail: "laptop"}) {
		t.Errorf("Open() = %+v; want laptop, live, by its name", opened)
	}
	got, err := opened.Look(t.Context(), paths)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]restore.There{
		"~/Code":                               {Kind: restore.Folder, Skips: []string{"app/node_modules", `web \[x]/vendor`}},
		"~/Library/Application Support/Claude": {Kind: restore.Folder},
		"~/.zsh_history":                       {Kind: restore.File},
		"~/Gone":                               {Kind: restore.Missing},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Look() = %+v\nwant %+v", got, want)
	}
}

// No Mac answering as the old one is ErrNoAnswer, saying why of each found:
// what the restore offers its card on. What stops one that answers being
// copied from is Check's, naming its fix, and never ErrNoAnswer.
func TestSourceSaysWhyItCantOpen(t *testing.T) {
	t.Run("none answering", func(t *testing.T) {
		fake := runnertest.New(t)
		browses(fake, browsing(mini))
		resolves(fake, mini, reaching(mini, miniAt, 22))
		refused(asked(fake, miniAt))
		_, err := (&Source{Finder: finder(t, fake), From: "laptop", User: user, Home: home}).Open(t.Context())
		if want := "laptop doesn't answer over SSH: " + mini + " " + errRefusedKey.Error(); !errors.Is(err, ErrNoAnswer) || err.Error() != want {
			t.Errorf("Open() = %v; want ErrNoAnswer, %q", err, want)
		}
	})
	t.Run("what Check finds", func(t *testing.T) {
		fake := runnertest.New(t)
		browses(fake, browsing(laptop))
		resolves(fake, laptop, reaching(laptop, laptopAt, 22))
		asked(fake, laptopAt).Prints(strings.Replace(says(t, "laptop"), "protected=read", "protected=denied", 1))
		_, err := (&Source{Finder: finder(t, fake), From: "laptop", User: user, Home: home}).Open(t.Context())
		if err == nil || errors.Is(err, ErrNoAnswer) || !strings.Contains(err.Error(), "turn on Allow full disk access for remote users there") {
			t.Errorf("Open() = %v; want Check's error, naming its fix", err)
		}
	})
}

// Tried again, the old Mac is looked for by the host key kit-config's
// remote records for it now, as kit apply on it may have recorded it since
// the run began: never on the first try, which takes the run's. One kit
// can't fetch is ErrNoHostKey, saying why, for the card to offer again.
func TestSourceTriedAgainFetchesTheHostKey(t *testing.T) {
	fake := runnertest.New(t)
	browses(fake, browsing(laptop))
	resolves(fake, laptop, reaching(laptop, laptopAt, 22))
	asked(fake, laptopAt).Prints(says(t, "laptop"))
	f := finder(t, fake)
	f.HostKey = ""
	fetched := []error{errors.New("couldn't reach origin"), nil}
	fetches := 0
	src := &Source{Finder: f, From: "laptop", User: user, Home: home, HostKey: func(context.Context) (string, error) {
		err := fetched[fetches]
		fetches++
		return laptopKey, err
	}}

	if _, err := src.Open(t.Context()); !errors.Is(err, ErrNoHostKey) || fetches != 0 {
		t.Fatalf("Open() = %v, fetched %d times; want ErrNoHostKey, the run's key, none fetched", err, fetches)
	}
	_, err := src.Open(t.Context())
	if want := "laptop can't be restored from: kit couldn't fetch its SSH host key from kit-config: couldn't reach origin"; !errors.Is(err, ErrNoHostKey) || err.Error() != want {
		t.Fatalf("Open() again, offline = %v; want %q", err, want)
	}
	if opened, err := src.Open(t.Context()); err != nil || opened.Host != laptopAt {
		t.Errorf("Open() again = %+v, %v; want laptop, found by the key fetched", opened, err)
	}
}

// A host key in kit-config that isn't one is as none: ErrNoHostKey, saying
// so, and no Mac looked for.
func TestFindsNothingWithAKeyThatIsntOne(t *testing.T) {
	fake := runnertest.New(t)
	f := finder(t, fake)
	f.HostKey = "ssh-ed25519 not*base64"
	_, err := f.Find(t.Context(), "laptop")
	if want := "laptop can't be restored from: laptop's SSH host key in kit-config isn't a valid key: kit apply on laptop records it"; !errors.Is(err, ErrNoHostKey) || err.Error() != want {
		t.Errorf("Find() = %v\nwant %q", err, want)
	}
	if calls := fake.Calls(); len(calls) > 0 {
		t.Errorf("ran %q; want nothing", calls)
	}
}
