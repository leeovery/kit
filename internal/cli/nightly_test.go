package cli_test

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// jobsWorld is laptopWorld with jobs of its own: one hourly, one nightly.
func jobsWorld(t *testing.T) *world {
	t.Helper()
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "hourly", "marks -- asimov --no-read-cache ~/Code   # dependency folders\n")
	w.writeSection(t, "laptop", "nightly", "tidy -- tidy-up --all\n")
	w.fake.On("asimov", "--no-read-cache", filepath.Join(w.home, "Code")).Prints("excluded 3 folders\n")
	w.fake.On("tidy-up", "--all").Exits(1).Prints("the archive disk isn't connected\n")
	return w
}

// The first run is the nightly one: every job runs, in order, then the
// checks; a job that fails is said, and never stops the rest.
func TestNightlyRunsWhatsDue(t *testing.T) {
	w := jobsWorld(t)
	out, _, code := w.run(t, "nightly")
	for _, want := range []string{"hourly:marks ok ran\n", "hourly:marks ran marks\n", "nightly:tidy failed the archive disk isn't connected\n", "disk ok 48% free\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("kit nightly printed\n%s\nwant it to hold %q", out, want)
		}
	}
	if code != 1 {
		t.Errorf("exit %d, want 1: a job failed", code)
	}
	calls := w.fake.Calls()
	marks := slices.IndexFunc(calls, func(c string) bool { return strings.HasPrefix(c, "asimov ") })
	tidy := slices.Index(calls, "tidy-up --all")
	disk := slices.IndexFunc(calls, func(c string) bool { return strings.HasPrefix(c, "/bin/df") })
	if marks < 0 || tidy < marks || disk < tidy {
		t.Errorf("ran %q, want the hourly job, the nightly one, then the checks", calls)
	}
	for _, c := range calls {
		if strings.HasPrefix(c, "brew install") || strings.HasPrefix(c, "brew uninstall") {
			t.Errorf("ran %q: kit nightly installs and removes nothing", c)
		}
	}
	record := w.read(t, filepath.Join(".local", "state", "kit", "nightly.json"))
	if !strings.Contains(record, `"hourly:marks": {`) || !strings.Contains(record, `"said": "the archive disk isn't connected"`) {
		t.Errorf("nightly.json = %s", record)
	}

	// An hour on, the nightly run has happened today: the hourly job alone.
	w.now = w.now.Add(time.Hour)
	before := len(w.fake.Calls())
	out, _, _ = w.run(t, "nightly")
	if !strings.Contains(out, "nightly:tidy ok not due\n") || slices.Contains(w.fake.Calls()[before:], "tidy-up --all") {
		t.Errorf("an hour on, kit nightly printed\n%s\nwant the nightly job not due", out)
	}
}

func TestNightlyPlanRunsNothing(t *testing.T) {
	w := jobsWorld(t)
	out, _, code := w.run(t, "nightly", "--plan")
	if !strings.Contains(out, "hourly:marks ok due\n") || !strings.Contains(out, "nightly:tidy ok due\n") || code != 1 {
		t.Errorf("kit nightly --plan printed\n%s exit %d", out, code)
	}
	for _, c := range w.fake.Calls() {
		if strings.HasPrefix(c, "asimov") || strings.HasPrefix(c, "tidy-up") {
			t.Errorf("--plan ran %q", c)
		}
	}
}

func TestNightlyRunsAJobByName(t *testing.T) {
	w := jobsWorld(t)
	out, _, _ := w.run(t, "nightly", "marks")
	if !strings.Contains(out, "hourly:marks ok ran\n") || !strings.Contains(out, "nightly:tidy ok not due\n") {
		t.Errorf("kit nightly marks printed\n%s", out)
	}
	_, errOut, code := w.run(t, "nightly", "nosuch")
	if !strings.Contains(errOut, "no job named nosuch: one of marks, tidy") || code != 2 {
		t.Errorf("kit nightly nosuch printed %q, exit %d", errOut, code)
	}
}

// The built-in jobs follow the user's own, settings capture last.
func TestNightlyBuiltInJobs(t *testing.T) {
	w := jobsWorld(t)
	w.writeSection(t, "laptop", "features", "scratch\nsettings-capture\n")
	w.fake.On("nice", "-n", "10", "prefsync", "capture").Prints("captured 2 changed domains\n")
	w.fake.On("mount").Prints("/dev/disk3s1 on / (apfs, local, journaled)\n")
	out, _, _ := w.run(t, "nightly", "--plan")
	order := []string{"hourly:marks ok due", "nightly:tidy ok due", "clean-scratch ok due", "capture-settings ok due"}
	last := -1
	for _, line := range order {
		i := strings.Index(out, line)
		if i < last {
			t.Errorf("kit nightly --plan printed\n%s\nwant %q after the jobs before it", out, line)
		}
		last = i
	}
	out, _, _ = w.run(t, "nightly", "capture-settings")
	if !strings.Contains(out, "capture-settings ok ran\n") {
		t.Errorf("kit nightly capture-settings printed\n%s", out)
	}
}

// --alerts prints what to notify about, in place of the run; every run
// leaves its report.
func TestNightlyAlertsAndReport(t *testing.T) {
	w := jobsWorld(t)
	out, _, _ := w.run(t, "nightly", "--alerts")
	var alerts []struct{ ID, Title, Body string }
	if err := json.Unmarshal([]byte(out), &alerts); err != nil {
		t.Fatalf("kit nightly --alerts printed %q: %v", out, err)
	}
	ids := map[string]string{}
	for _, a := range alerts {
		ids[a.ID] = a.Body
	}
	if ids["nightly:nightly:tidy"] != "tidy failed: the archive disk isn't connected" {
		t.Errorf("alerts = %+v, want tidy's failure", alerts)
	}
	var digest string
	for id, body := range ids {
		if strings.HasPrefix(id, "drift-") {
			digest = body
		}
	}
	if !strings.Contains(digest, "3 things differ from the config: brew ffmpeg, brew node@20, cask firefox") {
		t.Errorf("drift digest = %q", digest)
	}
	report := w.read(t, filepath.Join("Library", "Logs", "kit", "report.txt"))
	if !strings.HasPrefix(report, "kit nightly · laptop\n") || !strings.Contains(report, "disk ok 48% free\n") {
		t.Errorf("report.txt =\n%s", report)
	}
}
