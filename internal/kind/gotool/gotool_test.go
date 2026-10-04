package gotool_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/gotool"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// binWorld is a Go bin folder holding goimports and staticcheck, built from
// their packages, a program go can't say the package of, and a file that
// isn't a program; go env says where it is as GOPATH, GOBIN unset.
func binWorld(t *testing.T) (*runnertest.Fake, string) {
	t.Helper()
	gopath := t.TempDir()
	bin := filepath.Join(gopath, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, mode := range map[string]os.FileMode{"goimports": 0o755, "handmade": 0o755, "staticcheck": 0o755, "notes.txt": 0o644} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("x"), mode); err != nil {
			t.Fatal(err)
		}
	}
	fake := runnertest.New(t)
	fake.On("go", "env", "GOBIN", "GOPATH").Prints("\n" + gopath + "\n")
	p := func(name string) string { return filepath.Join(bin, name) }
	fake.On("go", "version", "-m", p("goimports"), p("handmade"), p("staticcheck")).Prints(fmt.Sprintf(
		"%s: go1.27.0\n\tpath\tgolang.org/x/tools/cmd/goimports\n\tmod\tgolang.org/x/tools\tv0.49.0\th1:x=\n%s: go1.26.0\n\tpath\thonnef.co/go/tools/cmd/staticcheck\n",
		p("goimports"), p("staticcheck"))).Exits(1).PrintsToStderr(p("handmade") + ": could not read Go build info")
	return fake, bin
}

func declared(names ...string) config.List {
	l := config.List{Kind: "go"}
	for _, n := range names {
		l.Entries = append(l.Entries, config.Entry{Name: n, File: "go"})
	}
	return l
}

func TestInstalled(t *testing.T) {
	fake, _ := binWorld(t)
	got, err := gotool.New(fake).Installed(t.Context())
	want := []kind.Installed{
		{Name: "golang.org/x/tools/cmd/goimports", Explicit: true},
		{Name: "handmade", Explicit: true},
		{Name: "honnef.co/go/tools/cmd/staticcheck", Explicit: true},
	}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("Installed() = %+v, %v; want %+v", got, err, want)
	}
}

func TestInstalledWithGOBIN(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("go", "env", "GOBIN", "GOPATH").Prints(filepath.Join(t.TempDir(), "none") + "\n/elsewhere\n")
	if got, err := gotool.New(fake).Installed(t.Context()); err != nil || len(got) != 0 {
		t.Errorf("Installed() of a bin folder not made yet = %+v, %v; want nothing", got, err)
	}
}

func TestCompare(t *testing.T) {
	fake, _ := binWorld(t)
	got := kind.Compare(t.Context(), gotool.New(fake), declared("golang.org/x/tools/cmd/goimports@v0.49.0", "gotest.tools/gotestsum"))
	want := []check.Item{
		{ID: "go:gotest.tools/gotestsum", Name: "gotest.tools/gotestsum", State: kind.Missing, Action: kind.Install},
		{ID: "go:handmade", Name: "handmade", State: kind.Extra},
		{ID: "go:honnef.co/go/tools/cmd/staticcheck", Name: "honnef.co/go/tools/cmd/staticcheck", State: kind.Extra},
	}
	if got.Summary != "2 declared, 1 installed" || !slices.Equal(got.Items, want) {
		t.Errorf("Compare() = %+v\nwant items %+v", got, want)
	}
}

func TestInstallEachAtItsVersion(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("go", "install", "gotest.tools/gotestsum@latest")
	fake.On("go", "install", "golang.org/x/tools/cmd/goimports@v0.49.0")
	if err := gotool.New(fake).Install(t.Context(), []string{"gotest.tools/gotestsum", "golang.org/x/tools/cmd/goimports@v0.49.0"}); err != nil {
		t.Fatal(err)
	}
}

// Go has no uninstall: removing deletes the program built from the package.
func TestRemoveDeletesTheProgram(t *testing.T) {
	fake, bin := binWorld(t)
	if err := gotool.New(fake).Remove(t.Context(), []string{"honnef.co/go/tools/cmd/staticcheck", "handmade"}); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(bin)
	var left []string
	for _, e := range entries {
		left = append(left, e.Name())
	}
	if want := []string{"goimports", "notes.txt"}; !slices.Equal(left, want) {
		t.Errorf("left %q, want %q", left, want)
	}
	goimports := filepath.Join(bin, "goimports")
	fake.On("go", "version", "-m", goimports).Prints(goimports + ": go1.27.0\n\tpath\tgolang.org/x/tools/cmd/goimports\n")
	if err := gotool.New(fake).Remove(t.Context(), []string{"gotest.tools/gotestsum"}); err == nil {
		t.Error("Remove() of a package not installed = nil error")
	}
}
