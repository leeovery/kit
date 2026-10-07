package exclusion_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/exclusion"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

func TestExclusions(t *testing.T) {
	home := t.TempDir()
	for _, dir := range []string{"Library/Application Support/One/GPUCache", "Library/Application Support/Two/GPUCache", "Library/Application Support/Three/Other"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fake := runnertest.New(t)
	x := exclusion.New(fake, home, "/prefs")
	list, err := x.Expand(config.List{Entries: []config.Entry{
		{Name: "~/Library/Application Support/*/GPUCache", Scope: "shared", Line: 3},
		{Name: "~/.cache", Scope: "shared", Line: 4},
		{Name: "/Applications", Scope: "shared", Line: 5},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := list.Names(); !slices.Equal(got, []string{"~/Library/Application Support/One/GPUCache", "~/Library/Application Support/Two/GPUCache", "~/.cache", "/Applications"}) {
		t.Errorf("Expand() = %q", got)
	}
	fake.On("defaults", "export", "/prefs", "-").Prints("<plist><dict><key>SkipPaths</key><array><string>" + home + "/.cache</string><string>" + home + "/Movies</string></array></dict></plist>")
	one, two := home+"/Library/Application Support/One/GPUCache", home+"/Library/Application Support/Two/GPUCache"
	fake.On("tmutil", "isexcluded", one, two).Prints("[Included]  " + one + "\n[Excluded]  " + two + "\n")
	var got []string
	for _, it := range kind.Compare(t.Context(), x, list).Items {
		got = append(got, it.Name+" "+it.State)
	}
	want := []string{"/Applications missing", "~/Library/Application Support/One/GPUCache missing", "~/Movies extra"}
	if !slices.Equal(got, want) {
		t.Errorf("items = %q, want %q (Two's match excluded where it is)", got, want)
	}
	missing := []string{"/Applications", "~/Library/Application Support/One/GPUCache"}
	if admin, _ := x.NeedsAdmin(t.Context(), missing); !slices.Equal(admin, []string{"/Applications"}) {
		t.Errorf("NeedsAdmin() = %q; want the plain path alone", admin)
	}
	if unattended := x.Unattended(missing); !slices.Equal(unattended, []string{"~/Library/Application Support/One/GPUCache"}) {
		t.Errorf("Unattended() = %q; want the glob's match alone", unattended)
	}
	x.SetAdmin(func(context.Context) bool { return true })
	fake.On("tmutil", "addexclusion", one)
	fake.On("sudo", "-n", "tmutil", "addexclusion", "-p", "/Applications").Exits(1)
	if err := x.Install(t.Context(), missing); err == nil || !strings.Contains(err.Error(), "/Applications") {
		t.Errorf("Install() = %v; want the one that failed named, the other done", err)
	}
	if calls := strings.Join(fake.Calls(), "\n"); !strings.Contains(calls, "tmutil addexclusion '"+one+"'") {
		t.Errorf("ran\n%s\nwant the match excluded where it is, with no password", calls)
	}
}
