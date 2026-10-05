package steps_test

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/runner/runnertest"
	"github.com/leeovery/kit/internal/steps"
)

// now is the checks' clock: 5 Oct 2026, 12:00 UTC.
var now = func() time.Time { return time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC) }

// checked runs s's check, and says what it found: its state, summary or
// reason, and its items' ids.
func checked(t *testing.T, s engine.Step) (check.State, string, []string) {
	t.Helper()
	res := s.Check(t.Context())
	text := res.Summary
	if res.State == check.Failed {
		text = res.Reason
	}
	var ids []string
	for _, it := range res.Items {
		ids = append(ids, it.ID)
		if it.State != steps.Problem || it.Name == "" || it.Detail == "" {
			t.Errorf("%s: item %+v, want a problem saying what's wrong and what to do", s.Name, it)
		}
	}
	return res.State, text, ids
}

func TestDisk(t *testing.T) {
	df := func(avail string) string {
		return "Filesystem 1024-blocks Used Available Capacity Mounted on\n/dev/disk3s5 1000000000 0 " + avail + " 51% /System/Volumes/Data\n"
	}
	for _, tt := range []struct {
		out   string
		state check.State
		text  string
		ids   []string
	}{
		{df("480000000"), check.OK, "48% free", nil},
		{df("80000000"), check.Attention, "8% free", []string{"disk:low"}},
		{"Filesystem\n", check.Failed, "couldn't read df's answer", nil},
	} {
		fake := runnertest.New(t)
		fake.On("/bin/df", "-P", "-k", "/System/Volumes/Data").Prints(tt.out)
		state, text, ids := checked(t, steps.Disk(fake))
		if state != tt.state || text != tt.text || !slices.Equal(ids, tt.ids) {
			t.Errorf("disk with %q = %s %q %q; want %s %q %q", tt.out, state, text, ids, tt.state, tt.text, tt.ids)
		}
	}
}

func TestMemory(t *testing.T) {
	swap := func(level, used string) string {
		return level + "\ntotal = 2048.00M  used = " + used + "M  free = 1729.19M  (encrypted)\n"
	}
	for _, tt := range []struct {
		out   string
		state check.State
		text  string
		ids   []string
	}{
		{swap("1", "318.81"), check.OK, "0.3 GB swap, pressure normal", nil},
		{swap("4", "20480.00"), check.Attention, "20.0 GB swap, pressure critical", []string{"memory:critical", "memory:swap"}},
		{swap("2", "17408.00"), check.Attention, "17.0 GB swap, pressure normal", []string{"memory:swap"}},
		{"1\n", check.Failed, "couldn't read sysctl's answer", nil},
	} {
		fake := runnertest.New(t)
		fake.On("sysctl", "-n", "kern.memorystatus_vm_pressure_level", "vm.swapusage").Prints(tt.out)
		state, text, ids := checked(t, steps.Memory(fake))
		if state != tt.state || text != tt.text || !slices.Equal(ids, tt.ids) {
			t.Errorf("memory with %q = %s %q %q; want %s %q %q", tt.out, state, text, ids, tt.state, tt.text, tt.ids)
		}
	}
}

func TestFileEvents(t *testing.T) {
	top := func(mem string) string {
		return "Processes: 812 total\nLoad Avg: 2.1\n\nMEM\n" + mem + "\n"
	}
	for _, tt := range []struct {
		top   string
		state check.State
		text  string
		ids   []string
	}{
		{top("212M"), check.OK, "fseventsd using 0.2 GB", nil},
		{top("57G+"), check.Attention, "fseventsd using 57.0 GB", []string{"file-events:fseventsd"}},
		{"nothing\n", check.Failed, "couldn't read top's answer", nil},
	} {
		fake := runnertest.New(t)
		fake.On("pgrep", "-x", "fseventsd").Prints("412\n")
		fake.On("top", "-l", "1", "-pid", "412", "-stats", "mem").Prints(tt.top)
		state, text, ids := checked(t, steps.FileEvents(fake))
		if state != tt.state || text != tt.text || !slices.Equal(ids, tt.ids) {
			t.Errorf("file events with %q = %s %q %q; want %s %q %q", tt.top, state, text, ids, tt.state, tt.text, tt.ids)
		}
	}
	fake := runnertest.New(t)
	fake.On("pgrep", "-x", "fseventsd").Exits(1)
	if state, text, _ := checked(t, steps.FileEvents(fake)); state != check.OK || text != "fseventsd isn't running" {
		t.Errorf("without fseventsd = %s %q", state, text)
	}
}

// A high load is a problem, but not in the half hour after the Mac starts.
func TestLoad(t *testing.T) {
	booted := func(ago time.Duration) string {
		return "{ sec = " + itoa(now().Add(-ago).Unix()) + ", usec = 0 } Mon Oct  5 08:00:00 2026"
	}
	for _, tt := range []struct {
		load, boot string
		state      check.State
		text       string
		ids        []string
	}{
		{"{ 9.98 7.10 5.34 }", booted(4 * time.Hour), check.OK, "load 5.3 on 10 cores", nil},
		{"{ 160.0 170.2 162.5 }", booted(4 * time.Hour), check.Attention, "load 162.5 on 10 cores", []string{"load:high"}},
		{"{ 160.0 170.2 162.5 }", booted(10 * time.Minute), check.OK, "load 162.5 on 10 cores, just after starting up", nil},
	} {
		fake := runnertest.New(t)
		fake.On("sysctl", "-n", "vm.loadavg", "hw.ncpu", "kern.boottime").Prints(tt.load + "\n10\n" + tt.boot + "\n")
		state, text, ids := checked(t, steps.Load(fake, now))
		if state != tt.state || text != tt.text || !slices.Equal(ids, tt.ids) {
			t.Errorf("load %q = %s %q %q; want %s %q %q", tt.load, state, text, ids, tt.state, tt.text, tt.ids)
		}
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// What kit commits and pushes at once never waits; an edit by hand, or a
// push that failed, is a problem after an hour.
func TestConfigSync(t *testing.T) {
	repo := t.TempDir()
	git := func(args ...string) []string { return append([]string{"-C", repo}, args...) }

	fake := runnertest.New(t)
	fake.On("git", git("log", "@{u}..HEAD", "--format=%ct")...)
	if state, text, _ := checked(t, steps.ConfigSync(fake, repo, now)); state != check.OK || text != "pushed" {
		t.Errorf("all pushed = %s %q", state, text)
	}

	fake = runnertest.New(t)
	fake.On("git", git("log", "@{u}..HEAD", "--format=%ct")...).Prints(itoa(now().Add(-3*time.Hour).Unix()) + "\n" + itoa(now().Add(-time.Minute).Unix()) + "\n")
	state, _, ids := checked(t, steps.ConfigSync(fake, repo, now))
	if state != check.Attention || !slices.Equal(ids, []string{"config-sync:unpushed"}) {
		t.Errorf("waiting = %s %q; want the commits unpushed", state, ids)
	}

	fake = runnertest.New(t)
	fake.On("git", git("log", "@{u}..HEAD", "--format=%ct")...).Prints(itoa(now().Add(-time.Minute).Unix()) + "\n")
	if state, _, _ := checked(t, steps.ConfigSync(fake, repo, now)); state != check.OK {
		t.Errorf("a commit a minute old = %s, want ok: kit is pushing it", state)
	}

	// No upstream: nothing to push to.
	fake = runnertest.New(t)
	fake.On("git", git("log", "@{u}..HEAD", "--format=%ct")...).Exits(128).PrintsToStderr("fatal: no upstream configured")
	if state, _, _ := checked(t, steps.ConfigSync(fake, repo, now)); state != check.OK {
		t.Errorf("no upstream = %s, want ok", state)
	}
}

func TestConfigEdits(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "notes"), []byte("a\nb\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) []string { return append([]string{"-C", repo}, args...) }

	fake := runnertest.New(t)
	fake.On("git", git("status", "--porcelain", "--untracked-files=all")...)
	step := steps.ConfigEdits(gitrepo.Repo{Dir: repo, Run: fake})
	if res := step.Check(t.Context()); res.State != check.OK || res.Summary != "all committed" || step.Area != steps.AreaDrift {
		t.Errorf("nothing waiting = %s %q in %s", res.State, res.Summary, step.Area)
	}

	fake = runnertest.New(t)
	fake.On("git", git("status", "--porcelain", "--untracked-files=all")...).Prints(" M shared/home/.zshrc\n?? notes\n D personal/declarations\n")
	fake.On("git", git("diff", "--numstat", "HEAD")...).Prints("3\t1\tshared/home/.zshrc\n0\t40\tpersonal/declarations\n")
	res := steps.ConfigEdits(gitrepo.Repo{Dir: repo, Run: fake}).Check(t.Context())
	var got []string
	for _, it := range res.Items {
		got = append(got, it.ID+" "+it.State+" "+it.Detail)
	}
	want := []string{"config:shared/home/.zshrc edited +3 −1 lines", "config:notes added +2 −0 lines", "config:personal/declarations deleted +0 −40 lines"}
	if res.State != check.Attention || res.Summary != "3 files not committed" || !slices.Equal(got, want) {
		t.Errorf("edits = %s %q %q, want %q", res.State, res.Summary, got, want)
	}
}
