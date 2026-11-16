package oldmac

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/restore"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// The old Mac, as the restore opens it: reached at its recorded name,
// checked, and opened live, what's at the listed paths there looked at in one go
// through it, as the restore takes it: each path's kind, and what its
// backups leave out as a line's --skip has it, escaped.
func TestSourceOpensTheOldMac(t *testing.T) {
	fake := runnertest.New(t)
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

// The old Mac not answering is ErrNoAnswer, saying why: what the restore
// offers its card on. What stops one that answers being copied from is
// Check's, naming its fix, and never ErrNoAnswer.
func TestSourceSaysWhyItCantOpen(t *testing.T) {
	t.Run("not answering", func(t *testing.T) {
		fake := runnertest.New(t)
		refused(asked(fake, laptopAt))
		_, err := (&Source{Finder: finder(t, fake), From: "laptop", User: user, Home: home}).Open(t.Context())
		if want := "laptop doesn't answer over SSH: " + laptopAt + " " + errRefusedKey.Error(); !errors.Is(err, ErrNoAnswer) || err.Error() != want {
			t.Errorf("Open() = %v; want ErrNoAnswer, %q", err, want)
		}
	})
	t.Run("what Check finds", func(t *testing.T) {
		fake := runnertest.New(t)
		asked(fake, laptopAt).Prints(strings.Replace(says(t, "laptop"), "protected=read", "protected=denied", 1))
		_, err := (&Source{Finder: finder(t, fake), From: "laptop", User: user, Home: home}).Open(t.Context())
		if err == nil || errors.Is(err, ErrNoAnswer) || !strings.Contains(err.Error(), "turn on Allow full disk access for remote users there") {
			t.Errorf("Open() = %v; want Check's error, naming its fix", err)
		}
	})
}

// Tried again, the old Mac is reached by what kit-config's remote records
// for it now, its host key and local hostname, as kit apply on it may have
// recorded them since the run began: never on the first try, which takes
// the run's. What kit can't fetch, the run's record not whole, is
// ErrUnrecorded, saying why, for the card to offer again.
func TestSourceTriedAgainFetchesTheRecord(t *testing.T) {
	fake := runnertest.New(t)
	asked(fake, laptopAt).Prints(says(t, "laptop"))
	f := finder(t, fake)
	f.HostKey, f.LocalHostName = laptopKey, ""
	fetched := []error{errors.New("couldn't reach origin"), nil}
	fetches := 0
	src := &Source{Finder: f, From: "laptop", User: user, Home: home, Recorded: func(context.Context) (string, string, error) {
		err := fetched[fetches]
		fetches++
		return laptopKey, laptopName, err
	}}

	if _, err := src.Open(t.Context()); !errors.Is(err, ErrUnrecorded) || fetches != 0 {
		t.Fatalf("Open() = %v, fetched %d times; want ErrUnrecorded, the run's record, none fetched", err, fetches)
	}
	_, err := src.Open(t.Context())
	if want := "laptop can't be restored from: kit couldn't fetch kit-config to read its record of laptop: couldn't reach origin"; !errors.Is(err, ErrUnrecorded) || err.Error() != want {
		t.Fatalf("Open() again, offline = %v; want %q", err, want)
	}
	if opened, err := src.Open(t.Context()); err != nil || opened.Host != laptopAt {
		t.Errorf("Open() again = %+v, %v; want laptop, reached at the name fetched", opened, err)
	}
}

// Tried again, kit-config out of reach, the old Mac is reached as the
// run's record has it, while that's whole, the run's log saying the fetch
// failed.
func TestSourceTriedAgainOfflineKeepsTheRecord(t *testing.T) {
	fake := runnertest.New(t)
	asked(fake, laptopAt).PrintsToStderr("ssh: connect to host " + laptopAt + " port 22: Operation timed out\n").Exits(255).Then().Prints(says(t, "laptop"))
	src := &Source{Finder: finder(t, fake), From: "laptop", User: user, Home: home, Recorded: func(context.Context) (string, string, error) {
		return "", "", errors.New("couldn't reach origin")
	}}
	var logged []event.Logged
	ctx := event.WithLogging(t.Context(), func(l event.Logged) { logged = append(logged, l) })

	if _, err := src.Open(ctx); !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("Open() = %v; want ErrNoAnswer", err)
	}
	if opened, err := src.Open(ctx); err != nil || opened.Host != laptopAt {
		t.Errorf("Open() again, offline = %+v, %v; want laptop, reached as the run's record has it", opened, err)
	}
	want := []event.Logged{{Line: "kit couldn't fetch kit-config, so it reaches laptop as its record was: couldn't reach origin", Alert: true}}
	if !reflect.DeepEqual(logged, want) {
		t.Errorf("logged %+v; want %+v", logged, want)
	}
}
