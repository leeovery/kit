package exclusion_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/backup"
	"github.com/leeovery/kit/internal/backup/arq"
	"github.com/leeovery/kit/internal/backup/timemachine"
	"github.com/leeovery/kit/internal/backup/tool"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/exclusion"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// fakeExcluder keeps paths out of a tool's backups: fixed, those in fixed;
// where they are, those in here. Keeping one out fixed needs the password;
// one in fail fails; one in refuse is refused for want of Full Disk Access.
// It notes what it's asked to do.
type fakeExcluder struct {
	fixed, here  []string
	fail, refuse string
	did          []string
}

func (f *fakeExcluder) Program() string { return "keeper" }

func (f *fakeExcluder) Excluded(_ context.Context, _ runner.Runner, here []string) ([]string, error) {
	out := slices.Clone(f.fixed)
	for _, p := range here {
		if slices.Contains(f.here, p) {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f *fakeExcluder) Exclude(_ context.Context, _ runner.Runner, path string, fixed bool) error {
	f.did = append(f.did, map[bool]string{true: "fixed ", false: "here "}[fixed]+path)
	switch path {
	case f.fail:
		return errors.New("refused")
	case f.refuse:
		return tool.ErrNoFullDiskAccess
	}
	return nil
}

func (f *fakeExcluder) Include(_ context.Context, _ runner.Runner, path string) error {
	f.did = append(f.did, "include "+path)
	return nil
}

func (f *fakeExcluder) NeedsAdmin(fixed bool) bool { return fixed }

// home is a home folder with three folders, two matching a glob.
func home(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for _, dir := range []string{"Library/Application Support/One/GPUCache", "Library/Application Support/Two/GPUCache", "Library/Application Support/Three/Other"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

// declared are the exclusions the tests declare: a glob, and two plain
// paths.
var declared = config.List{Entries: []config.Entry{
	{Name: "~/Library/Application Support/*/GPUCache", Scope: "shared", Line: 3},
	{Name: "~/.cache", Scope: "shared", Line: 4},
	{Name: "/Applications", Scope: "shared", Line: 5},
}}

// compared are the items of x's check of declared, each its name and
// state.
func compared(t *testing.T, x *exclusion.Exclusions) []string {
	t.Helper()
	list, err := x.Expand(declared)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range kind.Compare(t.Context(), x, list).Items {
		got = append(got, it.Name+" "+it.State)
	}
	return got
}

// The exclusions go through the excluders of the tools in use: a glob is
// what matches it now, each kept out where it is, with no password, so the
// scheduled run does it; a plain path is kept out fixed, which needs the
// password, one at a time so one that fails leaves the rest.
func TestExclusions(t *testing.T) {
	h := home(t)
	one, two := h+"/Library/Application Support/One/GPUCache", h+"/Library/Application Support/Two/GPUCache"
	keeper := &fakeExcluder{fixed: []string{h + "/.cache", h + "/Movies"}, here: []string{two}, fail: "/Applications"}
	x := exclusion.New(runnertest.New(t), h, []tool.Excluder{keeper})
	want := []string{"/Applications missing", "~/Library/Application Support/One/GPUCache missing", "~/Movies extra"}
	if got := compared(t, x); !slices.Equal(got, want) {
		t.Errorf("items = %q, want %q (Two's match kept out where it is)", got, want)
	}
	missing := []string{"/Applications", "~/Library/Application Support/One/GPUCache"}
	if admin, _ := x.NeedsAdmin(t.Context(), missing); !slices.Equal(admin, []string{"/Applications"}) {
		t.Errorf("NeedsAdmin() = %q; want the plain path alone", admin)
	}
	if unattended := x.Unattended(missing); !slices.Equal(unattended, []string{"~/Library/Application Support/One/GPUCache"}) {
		t.Errorf("Unattended() = %q; want the glob's match alone", unattended)
	}
	x.SetAdmin(func(context.Context) bool { return false })
	if blocked, _ := x.Blocked(t.Context(), map[string]string{"/Applications": "/Applications", missing[1]: missing[1]}, nil); len(blocked) != 1 || blocked["/Applications"].Why == "" {
		t.Errorf("Blocked() = %+v; want the plain path held for the password alone", blocked)
	}
	if err := x.Install(t.Context(), missing); err == nil || !strings.Contains(err.Error(), "/Applications: refused") {
		t.Errorf("Install() = %v; want the one that failed named, the other done", err)
	}
	if want := []string{"here " + one, "fixed /Applications"}; !slices.Equal(keeper.did, want) {
		t.Errorf("did %q, want %q: the match where it is, then the plain path fixed", keeper.did, want)
	}
	if err := x.Remove(t.Context(), []string{"~/Movies"}); err != nil || keeper.did[len(keeper.did)-1] != "include "+h+"/Movies" {
		t.Errorf("Remove() = %v, did %q", err, keeper.did)
	}
	if x.Program() != "keeper" {
		t.Errorf("Program() = %q, want the excluder's", x.Program())
	}
}

// With several excluders, a path declared is kept out only while each
// keeps it out, so each is asked to; one declared nowhere is extra while
// any keeps it out.
func TestExclusionsKeptByEach(t *testing.T) {
	h := home(t)
	first := &fakeExcluder{fixed: []string{h + "/.cache", "/Applications", "/Volumes/Old"}}
	second := &fakeExcluder{fixed: []string{h + "/.cache"}}
	x := exclusion.New(runnertest.New(t), h, []tool.Excluder{first, second})
	want := []string{"/Applications missing", "~/Library/Application Support/One/GPUCache missing", "~/Library/Application Support/Two/GPUCache missing", "/Volumes/Old extra"}
	if got := compared(t, x); !slices.Equal(got, want) {
		t.Errorf("items = %q, want %q", got, want)
	}
	if err := x.Install(t.Context(), []string{"/Applications"}); err != nil || !slices.Equal(first.did, []string{"fixed /Applications"}) || !slices.Equal(second.did, []string{"fixed /Applications"}) {
		t.Errorf("Install() = %v, the first did %q, the second %q; want each to keep it out", err, first.did, second.did)
	}
	first.did, second.did = nil, nil
	if err := x.Remove(t.Context(), []string{"/Volumes/Old"}); err != nil || !slices.Equal(first.did, []string{"include /Volumes/Old"}) || len(second.did) != 0 {
		t.Errorf("Remove() = %v, the first did %q, the second %q; want it put back by the one keeping it out alone", err, first.did, second.did)
	}
}

// A path kept out that a glob declared matches is kit's own, its folder
// there or not: one deleted since, by a cache cleaner, is never extra, so
// reconcile never asks about it. One the glob doesn't match still is.
func TestExclusionsGlobsFolderGone(t *testing.T) {
	h := home(t)
	gone := h + "/Library/Application Support/Gone/GPUCache"
	keeper := &fakeExcluder{fixed: []string{h + "/.cache", "/Applications", gone, h + "/Library/Application Support/Gone/Other"}}
	x := exclusion.New(runnertest.New(t), h, []tool.Excluder{keeper})
	want := []string{"~/Library/Application Support/One/GPUCache missing", "~/Library/Application Support/Two/GPUCache missing", "~/Library/Application Support/Gone/Other extra"}
	if got := compared(t, x); !slices.Equal(got, want) {
		t.Errorf("items = %q, want %q: the glob's path with no folder never extra", got, want)
	}
}

// The kind runs its excluders' programs, each once, which applying waits
// for while they aren't on kit's PATH.
func TestExclusionsCommands(t *testing.T) {
	keeper, other := &fakeExcluder{}, &fakeExcluder{}
	x := exclusion.New(runnertest.New(t), home(t), []tool.Excluder{keeper, other})
	if got := kind.Commands(x); !slices.Equal(got, []string{"keeper"}) || x.Program() != "keeper" {
		t.Errorf("Commands() = %q, Program() = %q; want the excluders' program, once", got, x.Program())
	}
	if got := kind.Commands(exclusion.New(runnertest.New(t), home(t), nil)); len(got) != 0 {
		t.Errorf("Commands() with no excluder = %q, want none", got)
	}
}

// A Mac whose backup tools keep no exclusions, or that names none, can't
// keep them out: the kind's check fails, saying where the tools go.
func TestExclusionsWithNoTool(t *testing.T) {
	x := exclusion.New(runnertest.New(t), home(t), nil)
	list, err := x.Expand(declared)
	if err != nil {
		t.Fatal(err)
	}
	if res := kind.Compare(t.Context(), x, list); res.Reason == "" || !strings.Contains(res.Reason, "[backups]") {
		t.Errorf("Compare() = %+v; want it failed, naming [backups]", res)
	}
}

// The exclusions are written through tmutil whichever of the tools are
// listed: Time Machine's own, and Arq's alone by Time Machine's list, which
// Arq follows, on a Mac whose Time Machine was never set up, with no
// settings at all.
func TestExclusionsThroughTheTools(t *testing.T) {
	for _, tools := range [][]string{{timemachine.ID}, {arq.ID}, {timemachine.ID, arq.ID}} {
		t.Run(strings.Join(tools, " and "), func(t *testing.T) {
			h := home(t)
			fake := runnertest.New(t)
			x := exclusion.New(fake, h, backup.Excluders(tools))
			list, err := x.Expand(config.List{Entries: []config.Entry{{Name: "/Applications", Scope: "shared", Line: 3}}})
			if err != nil {
				t.Fatal(err)
			}
			// Time Machine never set up: it has no settings at all.
			fake.On("defaults", "export", timemachine.Prefs, "-").Exits(1).PrintsToStderr("Domain " + timemachine.Prefs + " does not exist\n")
			if items := kind.Compare(t.Context(), x, list).Items; len(items) != 1 || items[0].Name != "/Applications" || items[0].State != "missing" {
				t.Errorf("items = %+v, want /Applications missing", items)
			}
			fake.On("sudo", "-n", "tmutil", "addexclusion", "-p", "/Applications")
			if err := x.Install(t.Context(), []string{"/Applications"}); err != nil {
				t.Fatal(err)
			}
			if calls := fake.Calls(); !slices.Contains(calls, "sudo -n tmutil addexclusion -p /Applications") || len(calls) != 2 {
				t.Errorf("ran %q; want it excluded once, through Time Machine", calls)
			}
		})
	}
}

// noAccess is what tmutil prints on its standard error refusing verb for
// want of Full Disk Access, exiting 80.
func noAccess(verb string) string {
	return "tmutil: " + verb + ` requires Full Disk Access privileges.
To allow this operation, select Full Disk Access in the Privacy
tab of the Security & Privacy preference pane, and add Terminal
to the list of applications which are allowed Full Disk Access.
`
}

// refusesAll has fake's tmutil refuse every exclusion added or removed, as
// it does without Full Disk Access, through sudo or not.
func refusesAll(fake *runnertest.Fake) {
	for _, verb := range []string{"addexclusion", "removeexclusion"} {
		fake.OnMatch("tmutil", func(args []string) bool { return args[0] == verb }).Exits(80).PrintsToStderr(noAccess(verb))
		fake.OnMatch("sudo", func(args []string) bool { return slices.Contains(args, verb) }).Exits(80).PrintsToStderr(noAccess(verb))
	}
}

// tmutil refusing for want of Full Disk Access refuses every path alike: the
// first refusal ends the install, or the removal, said once, as the kind was
// told to say it, given the program; told nothing, it's held back (not
// failed), saying so.
func TestExclusionsRefusedFullDiskAccess(t *testing.T) {
	told := errors.New("told")
	for _, tt := range []struct {
		name   string
		told   bool
		remove bool
		want   string
	}{
		{"install, told", true, false, "told tmutil"},
		{"install, held back", false, false, "tmutil needs Full Disk Access"},
		{"remove, told", true, true, "told tmutil"},
		{"remove, held back", false, true, "tmutil needs Full Disk Access"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := home(t)
			fake := runnertest.New(t)
			x := exclusion.New(fake, h, backup.Excluders([]string{timemachine.ID}))
			if tt.told {
				x.SetAccess(kind.Access{Refused: func(_ context.Context, program string) error { return fmt.Errorf("%w %s", told, program) }})
			}
			list, err := x.Expand(declared)
			if err != nil {
				t.Fatal(err)
			}
			fake.On("defaults", "export", timemachine.Prefs, "-").Prints("<plist><dict><key>SkipPaths</key><array><string>/Volumes/Old</string><string>/Volumes/Older</string></array></dict></plist>")
			fake.OnMatch("tmutil", func(args []string) bool { return args[0] == "isexcluded" })
			refusesAll(fake)
			var names []string
			for _, e := range list.Entries {
				names = append(names, e.Name)
			}
			if tt.remove {
				err = x.Remove(t.Context(), []string{"/Volumes/Old", "/Volumes/Older"})
			} else {
				err = x.Install(t.Context(), names)
			}
			if err == nil || err.Error() != tt.want || errors.Is(err, told) != tt.told || engine.IsNotYet(err) == tt.told {
				t.Errorf("err = %v; want %q, once", err, tt.want)
			}
			var tried int
			for _, call := range fake.Calls() {
				if strings.Contains(call, "addexclusion") || strings.Contains(call, "removeexclusion") {
					tried++
				}
			}
			if tried != 1 {
				t.Errorf("ran %q; want tmutil asked once, the rest left", fake.Calls())
			}
		})
	}
}

// Held for want of Full Disk Access, the kind changes nothing, saying why,
// each held told, though its check still looks, finding what's missing; it
// needs no password and holds nothing for one, as access is what it waits
// for. Granted, it does both again.
func TestExclusionsHeldForFullDiskAccess(t *testing.T) {
	h := home(t)
	keeper := &fakeExcluder{fixed: []string{h + "/.cache", h + "/Movies"}}
	x := exclusion.New(runnertest.New(t), h, []tool.Excluder{keeper})
	granted, told := false, 0
	x.SetAccess(kind.Access{
		Granted: func(context.Context) bool { return granted },
		Held:    func(context.Context) error { told++; return engine.NotYet("needs Full Disk Access") },
	})
	x.SetAdmin(func(context.Context) bool { return false })
	if got := compared(t, x); !slices.Contains(got, "/Applications missing") {
		t.Errorf("items = %q; want the check to look all the same", got)
	}
	for name, err := range map[string]error{
		"Install": x.Install(t.Context(), []string{"/Applications"}),
		"Remove":  x.Remove(t.Context(), []string{"~/Movies"}),
	} {
		if !engine.IsNotYet(err) || err.Error() != "needs Full Disk Access" {
			t.Errorf("%s() = %v; want it held, saying why", name, err)
		}
	}
	if len(keeper.did) != 0 || told != 2 {
		t.Errorf("did %q, told held %d times; want nothing done, each told", keeper.did, told)
	}
	missing := map[string]string{"/Applications": "/Applications"}
	admin, _ := x.NeedsAdmin(t.Context(), []string{"/Applications"})
	blocked, _ := x.Blocked(t.Context(), missing, nil)
	if len(admin) != 0 || len(blocked) != 0 {
		t.Errorf("held, NeedsAdmin() = %q, Blocked() = %+v; want neither", admin, blocked)
	}
	granted = true
	admin, _ = x.NeedsAdmin(t.Context(), []string{"/Applications"})
	blocked, _ = x.Blocked(t.Context(), missing, nil)
	if len(admin) != 1 || len(blocked) != 1 {
		t.Errorf("granted, NeedsAdmin() = %q, Blocked() = %+v; want the password needed, and waited for", admin, blocked)
	}
}

// A refusal for want of Full Disk Access ends the install, said first, the
// failures before it kept after it.
func TestExclusionsRefusalKeepsEarlierFailures(t *testing.T) {
	h := home(t)
	keeper := &fakeExcluder{fail: "/Applications", refuse: h + "/.cache"}
	x := exclusion.New(runnertest.New(t), h, []tool.Excluder{keeper})
	if _, err := x.Expand(declared); err != nil {
		t.Fatal(err)
	}
	err := x.Install(t.Context(), []string{"/Applications", "~/.cache", "/Volumes/Later"})
	if !engine.IsNotYet(err) || !strings.HasPrefix(err.Error(), "keeper needs Full Disk Access\n") || !strings.Contains(err.Error(), "/Applications: refused") {
		t.Errorf("Install() = %q; want the refusal, then the failure before it", err)
	}
	if want := []string{"fixed /Applications", "fixed " + h + "/.cache"}; !slices.Equal(keeper.did, want) {
		t.Errorf("did %q, want %q: nothing after the refusal", keeper.did, want)
	}
}
