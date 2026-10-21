package steps_test

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
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
// reason, and its items' ids. What needs attention is the person's to do,
// each item saying what's wrong and what to do.
func checked(t *testing.T, s engine.Step) (check.State, string, []string) {
	t.Helper()
	res := s.Check(t.Context())
	text := res.Summary
	if res.State == check.Failed {
		text = res.Reason
	}
	if res.State == check.Attention && !res.ToDo() {
		t.Errorf("%s: %+v, want it the person's to do", s.Name, res)
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
// push that failed, is a problem after an hour; and one kit holds back is
// one at once, saying why, as Push would refuse it.
func TestConfigSync(t *testing.T) {
	repo, gitDir := t.TempDir(), t.TempDir()
	git := func(args ...string) []string { return append([]string{"-C", repo}, args...) }
	const head = "0e0bf17a399109574ffb00e33300280a33f81ab7"
	log := git("log", "@{u}..HEAD", "--format=%ct")
	// waiting scripts the commits waiting, as times, and the repository as
	// Push finds it settled, git's status of it status, the remote's last
	// fetch behind by behind.
	waiting := func(times []time.Time, status, behind string) *runnertest.Fake {
		fake := runnertest.New(t)
		var out string
		for _, at := range times {
			out += itoa(at.Unix()) + "\n"
		}
		fake.On("git", log...).Prints(out)
		fake.On("git", git("rev-parse", "--absolute-git-dir")...).Prints(gitDir + "\n")
		fake.On("git", git("--literal-pathspecs", "status", "--porcelain", "-z", "--untracked-files=all", "--no-renames", "--")...).Prints(status)
		fake.On("git", git("rev-list", "--count", "HEAD..@{u}")...).Prints(behind + "\n")
		return fake
	}
	sync := func(fake *runnertest.Fake) engine.Step {
		return steps.ConfigSync(gitrepo.Repo{Dir: repo, Run: fake}, now)
	}

	fake := runnertest.New(t)
	fake.On("git", log...)
	if state, text, _ := checked(t, sync(fake)); state != check.OK || text != "pushed" {
		t.Errorf("all pushed = %s %q", state, text)
	}

	fake = waiting([]time.Time{now().Add(-3 * time.Hour), now().Add(-time.Minute)}, "", "0")
	state, _, ids := checked(t, sync(fake))
	if state != check.Attention || !slices.Equal(ids, []string{"config-sync:unpushed"}) {
		t.Errorf("waiting = %s %q; want the commits unpushed", state, ids)
	}

	fake = waiting([]time.Time{now().Add(-time.Minute)}, "", "0")
	if state, _, _ := checked(t, sync(fake)); state != check.OK {
		t.Errorf("a commit a minute old = %s, want ok: kit is pushing it", state)
	}

	// A change staged here, its file on disk as last committed, which the
	// remote's commits change too: drift says all's committed, so this says
	// why nothing's pushed.
	fake = waiting([]time.Time{now().Add(-time.Minute)}, "MM notes\x00", "1")
	fake.On("git", git("diff", "--name-only", "-z", "--no-renames", "HEAD...@{u}")...).Prints("notes\x00")
	fake.On("git", git("config", "--get", "--type=bool", "core.ignorecase")...).Prints("true\n")
	fake.On("git", git("ls-files", "-z", "--others", "--ignored", "--exclude-standard", "--directory")...)
	fake.On("git", git("rev-parse", "--verify", "--quiet", "HEAD")...).Prints(head + "\n")
	fake.On("git", git("read-tree", head)...)
	fake.On("git", git("diff", "--numstat", "-z", "--no-renames", "--no-ext-diff", "HEAD")...)
	res := sync(fake).Check(t.Context())
	if res.State != check.Attention || res.Summary != "pushes held back" || len(res.Items) != 1 ||
		res.Items[0].ID != "config-sync:held" || res.Items[0].Name != "1 commit not pushed" ||
		!strings.Contains(res.Items[0].Detail, "notes staged here, its file on disk as last committed: git's index is yours, to commit or unstage (git restore --staged notes)") {
		t.Errorf("held back by a staged change = %+v", res)
	}

	// A commit kit held back, as it went onto another commit than it was
	// made from.
	if err := os.WriteFile(filepath.Join(gitDir, "kit-held"), []byte(head+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fake = waiting([]time.Time{now().Add(-time.Minute)}, "", "0")
	fake.On("git", git("merge-base", "--is-ancestor", head, "HEAD")...)
	fake.On("git", git("rev-parse", "--verify", "--quiet", "HEAD")...).Prints(head + "\n")
	res = sync(fake).Check(t.Context())
	if res.State != check.Attention || len(res.Items) != 1 || res.Items[0].ID != "config-sync:held" ||
		!strings.Contains(res.Items[0].Detail, "git reset --soft HEAD~ takes it out, keeping its change, then run kit again") {
		t.Errorf("held back by kit's own commit = %+v", res)
	}

	// No upstream: nothing to push to.
	fake = runnertest.New(t)
	fake.On("git", log...).Exits(128).PrintsToStderr("fatal: no upstream configured for branch 'main'")
	if state, _, _ := checked(t, sync(fake)); state != check.OK {
		t.Errorf("no upstream = %s, want ok", state)
	}
}

// kit-config's edits, its files on disk that differ from the last commit,
// are each an item, with the lines it adds and removes: one to be added
// (git add -N) new; a file the user untracked, kept on this Mac as git
// ignores it, as it was, an item all the same, with no lines, as
// committing it deletes it from the other Macs; and a repository nested in
// kit-config what it is. One differing only in git's index, its file on
// disk as last committed, is none. Status and the lines are as real git
// prints them.
func TestConfigEdits(t *testing.T) {
	repo := t.TempDir()
	for _, name := range []string{"notes", "secret", "ita"} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte("a\nb\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	git := func(args ...string) []string { return append([]string{"-C", repo}, args...) }

	fake := runnertest.New(t)
	status := git("--literal-pathspecs", "status", "--porcelain", "-z", "--untracked-files=all", "--no-renames", "--")
	fake.On("git", status...)
	step := steps.ConfigEdits(gitrepo.Repo{Dir: repo, Run: fake})
	if res := step.Check(t.Context()); res.State != check.OK || res.Summary != "all committed" || step.Area != steps.AreaDrift {
		t.Errorf("nothing waiting = %s %q in %s", res.State, res.Summary, step.Area)
	}

	fake = runnertest.New(t)
	fake.On("git", status...).Prints(" D personal/declarations\x00MM same\x00 M shared/home/.zshrc\x00D  secret\x00 A ita\x00?? notes\x00?? vendor/tool/\x00")
	fake.On("git", git("rev-parse", "--verify", "--quiet", "HEAD")...).Prints("0e0bf17a399109574ffb00e33300280a33f81ab7\n")
	fake.On("git", git("read-tree", "0e0bf17a399109574ffb00e33300280a33f81ab7")...)
	fake.On("git", git("diff", "--numstat", "-z", "--no-renames", "--no-ext-diff", "HEAD")...).Prints("0\t40\tpersonal/declarations\x003\t1\tshared/home/.zshrc\x00")
	res := steps.ConfigEdits(gitrepo.Repo{Dir: repo, Run: fake}).Check(t.Context())
	var got []string
	for _, it := range res.Items {
		got = append(got, it.ID+" "+it.State+" "+it.Detail)
	}
	want := []string{
		"config:personal/declarations deleted +0 −40 lines", "config:shared/home/.zshrc edited +3 −1 lines", "config:secret untracked ",
		"config:ita added +2 −0 lines", "config:notes added +2 −0 lines", "config:vendor/tool/ repository kit-config can't keep it",
	}
	if res.State != check.Attention || res.Summary != "6 files not committed" || !slices.Equal(got, want) {
		t.Errorf("edits = %s %q %q, want %q", res.State, res.Summary, got, want)
	}
}
