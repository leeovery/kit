package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/runner/runnertest"
	"github.com/leeovery/kit/internal/steps"
)

// --at reads a date or a time in the Mac's own time zone, meaning the end
// of it, so a backup taken in it counts: a date, the whole of that day, its
// clocks going back or forward or not; a time to the minute, that minute;
// to the second, that second. With --restore-from time-machine, it's the
// restore's source's.
func TestRestoreAtReadsADateOrATime(t *testing.T) {
	zone, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no time zone database:", err)
	}
	for _, tt := range []struct {
		given string
		want  time.Time
	}{
		{"2026-11-08", time.Date(2026, 11, 8, 23, 59, 59, 999_999_999, zone)},
		{"2026-11-01", time.Date(2026, 11, 1, 23, 59, 59, 999_999_999, zone)},
		{"2026-11-08 14:30", time.Date(2026, 11, 8, 14, 30, 59, 999_999_999, zone)},
		{"2026-11-08 14:30:15", time.Date(2026, 11, 8, 14, 30, 15, 999_999_999, zone)},
	} {
		at := restoreAt{now: func() time.Time { return time.Date(2026, 11, 20, 9, 0, 0, 0, zone) }}
		if err := at.Set(tt.given); err != nil || !at.at.Equal(tt.want) || at.String() != tt.given {
			t.Errorf("--at %s = %s, %v; want %s", tt.given, at.at, err, tt.want)
		}
		chosen, err := restoreFlags{from: "time-machine", at: at}.chosen()
		if want := (steps.RestoreFrom{Kind: "time-machine", At: tt.want}); err != nil || chosen != want {
			t.Errorf("--restore-from time-machine --at %s = %+v, %v; want %+v", tt.given, chosen, err, want)
		}
	}
}

// On moving day, tried again, the new Mac reads what kit-config's remote
// records for the old one, its host key and local hostname, fetched now,
// as kit apply on the old Mac may have recorded them since this run began,
// after the boot cloned kit-config: nothing merged, the remote out of
// reach an error, never a record. What git prints is what real git does.
func TestTheOldMacsRecordIsFetched(t *testing.T) {
	const dir = "/Users/someone/.config/kit"
	fetching := func() *runnertest.Fake {
		fake := runnertest.New(t)
		fake.On("git", "-C", dir, "remote").Prints("origin\n")
		fake.On("git", "-C", dir, "symbolic-ref", "-q", "HEAD").Prints("refs/heads/main\n")
		fake.On("git", "-C", dir, "for-each-ref", "--format=%(upstream) %(upstream:remoteref)", "refs/heads/main").Prints("refs/remotes/origin/main refs/heads/main\n")
		return fake
	}
	fake := fetching()
	fake.On("git", "-C", dir, "fetch", "--prune", "--quiet", "origin")
	fake.On("git", "-C", dir, "show", "refs/remotes/origin/main:kit.toml").Prints("format = 1\n\n[macs.laptop]\nhost_key = \"ssh-ed25519 AAAAC3Nza\"\nlocal_hostname = \"Someones-MacBook-Pro\"\n\n[macs.studio]\n")
	key, name, err := fetchedRecord(context.Background(), gitrepo.Repo{Dir: dir, Run: fake}, "laptop")
	if key != "ssh-ed25519 AAAAC3Nza" || name != "Someones-MacBook-Pro" || err != nil {
		t.Errorf("fetchedRecord() = %q, %q, %v", key, name, err)
	}
	for _, c := range fake.Calls() {
		if strings.Contains(c, " merge") || strings.Contains(c, " rebase") || strings.Contains(c, " pull") || strings.Contains(c, " reset") {
			t.Errorf("ran %s; want nothing merged", c)
		}
	}

	fake = fetching()
	fake.On("git", "-C", dir, "fetch", "--prune", "--quiet", "origin").Exits(128).PrintsToStderr("ssh: Could not resolve hostname github.com: nodename nor servname provided, or not known\nfatal: Could not read from remote repository.\n")
	if key, name, err := fetchedRecord(context.Background(), gitrepo.Repo{Dir: dir, Run: fake}, "laptop"); key != "" || name != "" || err == nil || err.Error() != "couldn't reach origin" {
		t.Errorf("fetchedRecord(), offline = %q, %q, %v", key, name, err)
	}
}
