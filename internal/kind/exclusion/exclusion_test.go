package exclusion_test

import (
	"context"
	"errors"
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
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/exclusion"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// fakeExcluder keeps paths out of a tool's backups: fixed, those in fixed;
// where they are, those in here. Keeping one out fixed needs the password;
// one in fail fails. It notes what it's asked to do.
type fakeExcluder struct {
	fixed, here []string
	fail        string
	did         []string
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
	if path == f.fail {
		return errors.New("refused")
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
