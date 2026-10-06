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
		{Name: "~/Library/Application Support/*/GPUCache", Group: "Caches", Scope: "shared", Line: 3},
		{Name: "~/.cache", Scope: "shared", Line: 4},
		{Name: "/Applications", Scope: "shared", Line: 5},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := list.Names(); !slices.Equal(got, []string{"~/Library/Application Support/One/GPUCache", "~/Library/Application Support/Two/GPUCache", "~/.cache", "/Applications"}) {
		t.Errorf("Expand() = %q", got)
	}
	fake.On("defaults", "export", "/prefs", "-").Prints("<plist><dict><key>SkipPaths</key><array><string>" + home + "/.cache</string><string>/Applications</string><string>" + home + "/Movies</string></array></dict></plist>")
	var got []string
	for _, it := range kind.Compare(t.Context(), x, list).Items {
		got = append(got, it.Name+" "+it.State)
	}
	want := []string{"~/Library/Application Support/One/GPUCache missing", "~/Library/Application Support/Two/GPUCache missing", "~/Movies extra"}
	if !slices.Equal(got, want) {
		t.Errorf("items = %q, want %q", got, want)
	}
	x.SetAdmin(func(context.Context) bool { return true })
	fake.On("sudo", "-n", "tmutil", "addexclusion", "-p", home+"/Library/Application Support/One/GPUCache")
	fake.On("sudo", "-n", "tmutil", "addexclusion", "-p", home+"/Library/Application Support/Two/GPUCache").Exits(1)
	if err := x.Install(t.Context(), []string{"~/Library/Application Support/One/GPUCache", "~/Library/Application Support/Two/GPUCache"}); err == nil || !strings.Contains(err.Error(), "Two/GPUCache") {
		t.Errorf("Install() = %v; want the one that failed named, the other done", err)
	}
}
