package release_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/release"
	"github.com/leeovery/kit/internal/state"
)

var now = time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

// record writes rec as the state directory's record of kit's newest
// release.
func record(t *testing.T, dir string, rec release.Record) {
	t.Helper()
	if err := state.Update(dir, "release.json", func(r *release.Record) { *r = rec }); err != nil {
		t.Fatal(err)
	}
}

// A newer release than the kit running needs the person to upgrade it,
// naming both versions; the same or an older one, a kit that isn't a
// release's, or no record yet, claims nothing.
func TestCheck(t *testing.T) {
	for _, tt := range []struct {
		name    string
		running string
		newest  string
		state   check.State
		item    string
	}{
		{"a newer release out", "0.5.1", "0.5.2", check.Attention, "kit 0.5.2 is out; this Mac runs 0.5.1"},
		{"the newest", "0.5.1", "0.5.1", check.OK, ""},
		{"newer than the newest", "0.6.0", "0.5.2", check.OK, ""},
		{"a newer release, by number", "0.5.9", "0.5.10", check.Attention, "kit 0.5.10 is out; this Mac runs 0.5.9"},
		{"newer, by number", "0.5.10", "0.5.9", check.OK, ""},
		{"a build of its own", "dev", "0.5.2", check.OK, ""},
		{"no record yet", "0.5.1", "", check.OK, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.newest != "" {
				record(t, dir, release.Record{Newest: tt.newest, Asked: now})
			}
			res := release.Check(tt.running, "owner/tap/kit", dir).Check(context.Background())
			if res.State != tt.state || res.Summary != tt.running {
				t.Fatalf("Check = %s %q, want %s %q", res.State, res.Summary, tt.state, tt.running)
			}
			if tt.item == "" {
				if len(res.Items) > 0 {
					t.Errorf("items = %v, want none", res.Items)
				}
				return
			}
			want := check.Item{ID: "kit-version:out", Name: tt.item, State: check.Problem, Detail: "brew upgrade owner/tap/kit installs it"}
			if !slices.Equal(res.Items, []check.Item{want}) {
				t.Errorf("items = %+v, want %+v", res.Items, want)
			}
		})
	}
}

// github is GitHub's latest release, on this machine: it redirects to
// location, or answers status without one, counting the asks.
func github(t *testing.T, status int, location string) (site string, asks *atomic.Int32) {
	t.Helper()
	asks = new(atomic.Int32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asks.Add(1)
		if r.Method != http.MethodHead || r.URL.Path != "/leeovery/kit/releases/latest" {
			t.Errorf("asked %s %s", r.Method, r.URL.Path)
		}
		if location != "" {
			w.Header().Set("Location", location)
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, asks
}

// The scheduled run asks for the newest release once a day, reading the tag
// GitHub's latest release redirects to; an ask that fails, or names no
// release's tag, keeps the last answer.
func TestAsk(t *testing.T) {
	last := release.Record{Newest: "0.5.0", Asked: now.Add(-25 * time.Hour)}
	for _, tt := range []struct {
		name     string
		before   release.Record
		status   int
		location string
		asked    bool
		want     release.Record
	}{
		{"a redirect to a tag", release.Record{}, http.StatusFound, "https://github.com/leeovery/kit/releases/tag/v0.5.2", true, release.Record{Newest: "0.5.2", Asked: now}},
		{"a relative redirect", last, http.StatusFound, "/leeovery/kit/releases/tag/v0.5.2", true, release.Record{Newest: "0.5.2", Asked: now}},
		{"asked within the day", release.Record{Newest: "0.5.0", Asked: now.Add(-23 * time.Hour)}, http.StatusFound, "/leeovery/kit/releases/tag/v0.5.2", false, release.Record{Newest: "0.5.0", Asked: now.Add(-23 * time.Hour)}},
		{"asked in the future", release.Record{Newest: "0.5.0", Asked: now.Add(time.Hour)}, http.StatusFound, "/leeovery/kit/releases/tag/v0.5.2", true, release.Record{Newest: "0.5.2", Asked: now}},
		{"GitHub down", last, http.StatusServiceUnavailable, "", true, last},
		{"no release yet", last, http.StatusFound, "https://github.com/leeovery/kit/releases", true, last},
		{"a tag that isn't a release's", last, http.StatusFound, "https://github.com/leeovery/kit/releases/tag/nightly", true, last},
		{"not a tag", last, http.StatusFound, "https://github.com/leeovery/kit/tree/v0.5.2", true, last},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.before != (release.Record{}) {
				record(t, dir, tt.before)
			}
			site, asks := github(t, tt.status, tt.location)
			err := release.Ask(context.Background(), &http.Client{}, site, dir, now)
			if got := asks.Load() > 0; got != tt.asked {
				t.Errorf("asked = %v, want %v", got, tt.asked)
			}
			if failed := tt.asked && tt.want == last; (err != nil) != failed {
				t.Errorf("Ask = %v, want an error %v", err, failed)
			}
			if got := release.Load(dir); !got.Asked.Equal(tt.want.Asked) || got.Newest != tt.want.Newest {
				t.Errorf("record = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// An ask with nothing answering fails, keeping the last answer.
func TestAskOffline(t *testing.T) {
	dir := t.TempDir()
	last := release.Record{Newest: "0.5.0", Asked: now.Add(-48 * time.Hour)}
	record(t, dir, last)
	if err := release.Ask(context.Background(), &http.Client{}, "http://127.0.0.1:1", dir, now); err == nil {
		t.Error("Ask = nil, want an error")
	}
	if got := release.Load(dir); got.Newest != last.Newest || !got.Asked.Equal(last.Asked) {
		t.Errorf("record = %+v, want %+v", got, last)
	}
}

// A record that won't read, empty or broken, counts as none: the check
// claims nothing, and the next answer replaces it.
func TestUnreadableRecord(t *testing.T) {
	for _, content := range []string{"", "{not json"} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "release.json"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if res := release.Check("0.5.1", "owner/tap/kit", dir).Check(context.Background()); res.State != check.OK || len(res.Items) > 0 {
			t.Errorf("with %q recorded, Check = %+v, want ok, claiming nothing", content, res)
		}
		site, _ := github(t, http.StatusFound, "/leeovery/kit/releases/tag/v0.5.2")
		if err := release.Ask(context.Background(), &http.Client{}, site, dir, now); err != nil {
			t.Errorf("with %q recorded, Ask = %v", content, err)
		}
		if got := release.Load(dir); got.Newest != "0.5.2" || !got.Asked.Equal(now) {
			t.Errorf("with %q recorded, the record after asking = %+v, want 0.5.2 asked now", content, got)
		}
	}
}
