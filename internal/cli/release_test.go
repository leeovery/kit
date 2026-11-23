package cli_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/leeovery/kit/internal/boot"
)

// latestRelease serves GitHub's latest release of kit in the world, on this
// machine, redirecting to tag: how many times it's asked.
func (w *world) latestRelease(t *testing.T, tag string) *atomic.Int32 {
	t.Helper()
	asks := new(atomic.Int32)
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		asks.Add(1)
		if r.URL.Path != "/leeovery/kit/releases/latest" {
			http.NotFound(rw, r)
			return
		}
		http.Redirect(rw, r, "/leeovery/kit/releases/tag/"+tag, http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	w.gitHub = boot.GitHub{Client: &http.Client{}, Site: srv.URL, API: srv.URL}
	return asks
}

// outLine is kit status's line saying kit 0.2.0 is out, in the world
// running 0.1.0.
const outLine = "kit-version problem kit 0.2.0 is out; this Mac runs 0.1.0 (brew upgrade leeovery/tools/kit installs it)\n"

// kit status says a newer kit is out, as kit's record has it, the command
// that upgrades it its to-do, and never asks GitHub itself.
func TestStatusSaysANewerKitIsOut(t *testing.T) {
	w := laptopWorld(t)
	asks := w.latestRelease(t, "v0.2.0")
	w.write(t, filepath.Join(".local", "state", "kit", "release.json"), `{"newest": "0.2.0", "asked": "2026-01-02T00:00:00Z"}`)
	out, _, _ := w.run(t, "status")
	if !strings.Contains(out, "kit-version attention 0.1.0\n"+outLine) {
		t.Errorf("kit status printed\n%s\nwant kit 0.2.0 out", out)
	}
	if n := asks.Load(); n != 0 {
		t.Errorf("kit status asked GitHub %d times, want none", n)
	}
}

// kit nightly asks GitHub for kit's newest release once a day, and its
// checks show it.
func TestNightlyRecordsTheNewestKit(t *testing.T) {
	w := laptopWorld(t)
	asks := w.latestRelease(t, "v0.2.0")
	w.run(t, "nightly")
	record := w.read(t, filepath.Join(".local", "state", "kit", "release.json"))
	if !strings.Contains(record, `"newest": "0.2.0"`) || asks.Load() != 1 {
		t.Fatalf("release.json = %s, asked %d times; want 0.2.0, asked once", record, asks.Load())
	}
	out, _, _ := w.run(t, "nightly")
	if !strings.Contains(out, outLine) {
		t.Errorf("kit nightly printed\n%s\nwant kit 0.2.0 out", out)
	}
	if n := asks.Load(); n != 1 {
		t.Errorf("asked GitHub %d times in a day, want once", n)
	}
}
