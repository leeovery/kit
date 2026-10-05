package linked_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/linked"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// mac is a home of a test's own with a config repository in it, whose
// files git keeps are the ones a test lists.
type mac struct {
	t     *testing.T
	home  string
	repo  string
	fake  *runnertest.Fake
	files *linked.Files
}

func newMac(t *testing.T) *mac {
	t.Helper()
	home := t.TempDir()
	m := &mac{t: t, home: home, repo: filepath.Join(home, ".config", "kit"), fake: runnertest.New(t)}
	m.files = &linked.Files{
		Repo:     gitrepo.Repo{Dir: m.repo, Run: m.fake},
		Home:     home,
		StateDir: filepath.Join(home, ".local", "state", "kit"),
		Scopes:   []string{"shared", "laptop"},
	}
	return m
}

// write writes a file at path, from the home, with mode.
func (m *mac) write(path, content string, mode os.FileMode) {
	m.t.Helper()
	full := filepath.Join(m.home, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		m.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), mode); err != nil {
		m.t.Fatal(err)
	}
}

// link links path, from the home, to target.
func (m *mac) link(path, target string) {
	m.t.Helper()
	full := filepath.Join(m.home, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		m.t.Fatal(err)
	}
	if err := os.Symlink(target, full); err != nil {
		m.t.Fatal(err)
	}
}

// keeps scripts git listing paths, in the repository, as the files it
// keeps in the home folders that exist.
func (m *mac) keeps(paths ...string) {
	var folders []string
	for _, scope := range []string{"shared", "laptop"} {
		if _, err := os.Stat(filepath.Join(m.repo, scope, "home")); err == nil {
			folders = append(folders, scope+"/home")
		}
	}
	args := append([]string{"-C", m.repo, "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--"}, folders...)
	m.fake.On("git", args...).Prints(strings.Join(paths, "\x00") + "\x00")
}

// read reads the file at path, from the home.
func (m *mac) read(path string) string {
	m.t.Helper()
	data, err := os.ReadFile(filepath.Join(m.home, path))
	if err != nil {
		m.t.Fatal(err)
	}
	return string(data)
}

// target is where the link at path, from the home, leads: "" when it isn't
// a link.
func (m *mac) target(path string) string {
	target, err := os.Readlink(filepath.Join(m.home, path))
	if err != nil {
		return ""
	}
	return target
}

// states are the items' states, by name.
func states(res check.Result) map[string]string {
	out := make(map[string]string)
	for _, it := range res.Items {
		out[it.Name] = it.State
	}
	return out
}

func TestCheckFindsHowEachFileStands(t *testing.T) {
	m := newMac(t)
	m.write(".config/kit/shared/home/.zshrc", "zsh\n", 0o644)
	m.write(".config/kit/shared/home/.config/app/conf", "conf\n", 0o644)
	m.link(".config/app/conf", filepath.Join(m.repo, "shared/home/.config/app/conf"))
	m.write(".config/kit/laptop/home/.ssh/config", "Host *\n", 0o644)
	m.link(".ssh/config", filepath.Join(m.repo, "laptop/home/.ssh/config"))
	m.write(".config/kit/shared/home/.same", "same\n", 0o644)
	m.write(".same", "same\n", 0o644)
	m.write(".config/kit/shared/home/.differs", "kit's\n", 0o644)
	m.write(".differs", "the Mac's\n", 0o644)
	m.write(".config/kit/shared/home/.elsewhere", "kit's\n", 0o644)
	m.write("other/.elsewhere", "another\n", 0o644)
	m.link(".elsewhere", filepath.Join(m.home, "other/.elsewhere"))
	m.link(".config/app/old", filepath.Join(m.repo, "shared/home/.config/app/old"))
	m.write(".config/kit/shared/home/.cache-file", "ignored\n", 0o644)
	m.keeps("shared/home/.zshrc", "shared/home/.config/app/conf", "laptop/home/.ssh/config",
		"shared/home/.same", "shared/home/.differs", "shared/home/.elsewhere", "shared/home/.gone")

	res := m.files.Check(t.Context())
	want := map[string]string{
		"~/.zshrc":          linked.Missing,
		"~/.ssh/config":     linked.Changed,
		"~/.same":           linked.Changed,
		"~/.differs":        linked.Diverged,
		"~/.elsewhere":      linked.Diverged,
		"~/.config/app/old": linked.Dead,
	}
	if got := states(res); !maps(got, want) {
		t.Errorf("states = %v, want %v", got, want)
	}
	if res.State != check.Attention || res.Summary != "2 of 6 linked" {
		t.Errorf("result = %s %q, want attention, 2 of 6 linked", res.State, res.Summary)
	}
	for _, it := range res.Items {
		if (it.State == linked.Diverged) != (it.Action == "") {
			t.Errorf("%s: %s with action %q: only a diverged file is left to kit reconcile", it.Name, it.State, it.Action)
		}
	}
}

func maps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func TestApplyLinksAndNeverOverwritesADifferentFile(t *testing.T) {
	m := newMac(t)
	m.write(".config/kit/shared/home/.zshrc", "zsh\n", 0o644)
	m.write(".config/kit/laptop/home/.ssh/config", "Host *\n", 0o644)
	m.write(".config/kit/shared/home/.same", "same\n", 0o644)
	m.write(".same", "same\n", 0o644)
	m.write(".config/kit/shared/home/.differs", "kit's\n", 0o644)
	m.write(".differs", "the Mac's\n", 0o644)
	m.link(".old", filepath.Join(m.repo, "shared/home/.old"))
	m.keeps("shared/home/.zshrc", "laptop/home/.ssh/config", "shared/home/.same", "shared/home/.differs")

	if err := m.files.Apply(t.Context(), m.files.Check(t.Context())); err != nil {
		t.Fatalf("Apply() = %v", err)
	}
	for _, name := range []string{".zshrc", ".same"} {
		if got := m.target(name); got != filepath.Join(m.repo, "shared/home", name) {
			t.Errorf("~/%s leads to %q", name, got)
		}
	}
	if got := m.target(".ssh/config"); got != filepath.Join(m.repo, "laptop/home/.ssh/config") {
		t.Errorf("~/.ssh/config leads to %q", got)
	}
	if m.target(".differs") != "" || m.read(".differs") != "the Mac's\n" {
		t.Error("a different file was overwritten")
	}
	if _, err := os.Lstat(filepath.Join(m.home, ".old")); !os.IsNotExist(err) {
		t.Errorf("the dead link is still there: %v", err)
	}
	if info, _ := os.Stat(filepath.Join(m.repo, "laptop/home/.ssh/config")); info.Mode().Perm() != 0o600 {
		t.Errorf("ssh's config is %v, want it private", info.Mode().Perm())
	}
	if info, _ := os.Stat(filepath.Join(m.home, ".ssh")); info.Mode().Perm() != 0o700 {
		t.Errorf("~/.ssh is %v, want it private", info.Mode().Perm())
	}
	if got := states(m.files.Check(t.Context())); !maps(got, map[string]string{"~/.differs": linked.Diverged}) {
		t.Errorf("after applying, states = %v; want only the different file left", got)
	}
}

func TestADeadLinkKitMadeIsFoundWithItsFolderGone(t *testing.T) {
	m := newMac(t)
	m.write(".config/kit/shared/home/.config/tool/a", "a\n", 0o644)
	m.keeps("shared/home/.config/tool/a")
	if err := m.files.Apply(t.Context(), m.files.Check(t.Context())); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(m.repo, "shared/home/.config")); err != nil {
		t.Fatal(err)
	}
	m.keeps()
	if got := states(m.files.Check(t.Context())); !maps(got, map[string]string{"~/.config/tool/a": linked.Dead}) {
		t.Errorf("states = %v, want the dead link found", got)
	}
}

func TestAFileInTwoHomeFoldersIsRefused(t *testing.T) {
	m := newMac(t)
	m.write(".config/kit/shared/home/.zshrc", "a\n", 0o644)
	m.write(".config/kit/laptop/home/.zshrc", "b\n", 0o644)
	m.keeps("laptop/home/.zshrc", "shared/home/.zshrc")
	res := m.files.Check(t.Context())
	if res.State != check.Failed || !strings.Contains(res.Reason, "~/.zshrc is in laptop/home/.zshrc and shared/home/.zshrc") {
		t.Errorf("result = %s %q", res.State, res.Reason)
	}
}

func TestAdoptTakesTheMacsCopy(t *testing.T) {
	m := newMac(t)
	m.write(".config/kit/shared/home/.differs", "kit's\n", 0o644)
	m.write(".differs", "the Mac's\n", 0o600)
	m.keeps("shared/home/.differs")
	l, err := m.files.Adopt(t.Context(), "~/.differs")
	if err != nil || l.Path != "shared/home/.differs" {
		t.Fatalf("Adopt() = %+v, %v", l, err)
	}
	if m.read(".config/kit/shared/home/.differs") != "the Mac's\n" || m.target(".differs") == "" {
		t.Error("the Mac's copy isn't in kit-config, linked")
	}
}

func TestRevertBinsTheMacsCopy(t *testing.T) {
	m := newMac(t)
	m.write(".config/kit/shared/home/.differs", "kit's\n", 0o644)
	m.write(".differs", "the Mac's\n", 0o644)
	m.write(".Trash/.differs", "binned before\n", 0o644)
	m.keeps("shared/home/.differs")
	binned, err := m.files.Revert(t.Context(), "~/.differs")
	if err != nil || binned != filepath.Join(m.home, ".Trash", ".differs 2") {
		t.Fatalf("Revert() = %q, %v", binned, err)
	}
	if m.read(".Trash/.differs 2") != "the Mac's\n" || m.read(".differs") != "kit's\n" || m.target(".differs") == "" {
		t.Error("the Mac's copy isn't in the Bin, kit's linked in its place")
	}
}

func TestAddMovesFilesIntoKitConfig(t *testing.T) {
	m := newMac(t)
	m.write(".config/tool/config", "tool\n", 0o600)
	m.write(".config/tool/themes/dark", "dark\n", 0o644)
	m.write(".config/tool/.DS_Store", "x", 0o644)
	m.write(".zshrc", "zsh\n", 0o644)
	m.keeps()

	links, err := m.files.Add(t.Context(), filepath.Join(m.home, ".config/tool"), "laptop")
	if err != nil {
		t.Fatalf("Add() = %v", err)
	}
	var names []string
	for _, l := range links {
		names = append(names, l.Path)
	}
	if want := []string{"laptop/home/.config/tool/config", "laptop/home/.config/tool/themes/dark"}; !slices.Equal(names, want) {
		t.Errorf("Add() added %q, want %q", names, want)
	}
	if m.read(".config/kit/laptop/home/.config/tool/config") != "tool\n" || m.target(".config/tool/config") == "" || m.read(".config/tool/config") != "tool\n" {
		t.Error("the file isn't in kit-config, linked back")
	}
	if info, _ := os.Stat(filepath.Join(m.repo, "laptop/home/.config/tool/config")); info.Mode().Perm() != 0o600 {
		t.Errorf("its mode is %v, want it kept", info.Mode().Perm())
	}

	m.keeps("laptop/home/.config/tool/config", "laptop/home/.config/tool/themes/dark")
	for path, want := range map[string]string{
		".config/tool/config":  "linked already",
		".zshrc/..":            "isn't in the home folder",
		".config/kit/kit.toml": "isn't in the home folder",
	} {
		if _, err := m.files.Add(t.Context(), filepath.Join(m.home, path), "laptop"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Add(%s) = %v, want it refused: %s", path, err, want)
		}
	}
}

func TestRemovePutsACopyBack(t *testing.T) {
	m := newMac(t)
	m.write(".config/kit/laptop/home/.config/tool/config", "tool\n", 0o600)
	m.link(".config/tool/config", filepath.Join(m.repo, "laptop/home/.config/tool/config"))
	m.keeps("laptop/home/.config/tool/config")
	l, err := m.files.Remove(t.Context(), "~/.config/tool/config")
	if err != nil || l.Path != "laptop/home/.config/tool/config" {
		t.Fatalf("Remove() = %+v, %v", l, err)
	}
	if m.target(".config/tool/config") != "" || m.read(".config/tool/config") != "tool\n" {
		t.Error("a copy isn't back in place of the link")
	}
	if _, err := os.Stat(filepath.Join(m.repo, "laptop/home/.config")); !os.IsNotExist(err) {
		t.Errorf("the emptied folders are still in kit-config: %v", err)
	}
}
