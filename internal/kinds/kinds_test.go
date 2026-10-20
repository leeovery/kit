package kinds_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kinds"
	"github.com/leeovery/kit/internal/render"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// deps are what the kinds are made from in a test: a fake for every
// program, and folders of its own.
func deps(t *testing.T) *kind.Deps {
	return &kind.Deps{Run: runnertest.New(t), Home: t.TempDir(), StateDir: t.TempDir()}
}

// Every kind says all kit's shared code asks of it: its names, its group
// and row, how it's made, its section unless it's declared in a file of its
// own, its command's help, and the steps it needs, made by its own setup.
func TestEveryKindIsComplete(t *testing.T) {
	d := deps(t)
	groups := []kind.Group{kind.Packages, kind.Settings, kind.Secrets}
	for i, k := range kinds.All {
		if k.Name == "" || k.Label == "" || k.Row == "" || k.New == nil || !slices.Contains(groups, k.Group) {
			t.Errorf("kind %d (%s) = %+v: want a name, label, row, group and New", i, k.Name, k)
			continue
		}
		made := k.New(d)
		if made.Name() != k.Name {
			t.Errorf("%s makes a kind named %s", k.Name, made.Name())
		}
		_, outside := made.(kind.Declarer)
		switch {
		case k.Section == nil && !outside:
			t.Errorf("%s has no section, and isn't declared in a file of its own", k.Name)
		case k.Section != nil && outside:
			t.Errorf("%s has a section, and is declared in a file of its own", k.Name)
		case k.Section != nil && (k.Section.Header == "" || !strings.EqualFold(k.Label, k.Section.Header)):
			t.Errorf("%s's label is %q, want its section's words, %q", k.Name, k.Label, k.Section.Header)
		case k.Section != nil && k.Section.Kind != k.Name:
			t.Errorf("%s's section declares %q, want it", k.Name, k.Section.Kind)
		}
		if !slices.Contains(render.Rows(), k.Row) {
			t.Errorf("%s sits in the row %q, want one of applying's: %v", k.Name, k.Row, render.Rows())
		}
		if h := k.Help; h != nil && (h.What == "" || h.Add == "" || h.Remove == "") {
			t.Errorf("%s's help = %+v: want what it manages, and what adding and removing do", k.Name, *h)
		}
		helped := k.Help != nil && k.Help.Switch != nil
		if _, switches := made.(kind.Switcher); switches != helped {
			t.Errorf("%s: its things switch off and on %v, its help has on and off %v: want both or neither", k.Name, switches, helped)
		}
		if helped && (k.Help.Switch.On == "" || k.Help.Switch.Off == "" || k.Help.Switch.Holder == "") {
			t.Errorf("%s's switches' help = %+v: want on, off and what holds its things", k.Name, *k.Help.Switch)
		}
		var setup []string
		if k.Setup != nil {
			for _, s := range k.Setup(d) {
				setup = append(setup, s.Name)
			}
		}
		if !slices.Equal(setup, k.Needs) {
			t.Errorf("%s needs %v, and its setup makes %v: want the same", k.Name, k.Needs, setup)
		}
		for _, after := range k.After {
			if j := slices.IndexFunc(kinds.All, func(o kind.Descriptor) bool { return o.Name == after }); j < 0 || j > i {
				t.Errorf("%s comes after %s, which isn't a kind before it in the run", k.Name, after)
			}
		}
	}
}

// No two kinds share a name, or a section's header.
func TestNamesAndHeadersAreUnique(t *testing.T) {
	names, headers := map[string]bool{}, map[string]bool{}
	for _, k := range kinds.All {
		if names[k.Name] {
			t.Errorf("two kinds are named %s", k.Name)
		}
		names[k.Name] = true
		if k.Section == nil {
			continue
		}
		if headers[k.Section.Header] {
			t.Errorf("two kinds' sections are [%s]", k.Section.Header)
		}
		headers[k.Section.Header] = true
	}
}

// A run takes the kinds in the order it always has; a declarations file
// has the Mac's settings, then its packages, then its secrets, each in
// that order.
func TestTheOrders(t *testing.T) {
	var run []string
	for _, k := range kinds.All {
		run = append(run, k.Name)
	}
	want := []string{"brew", "cask", "mas", "npm", "composer", "go", "gh", "tmux", "login-item", "claude-mcp", "claude-skill",
		"secret", "git-config", "defaults", "power", "backup-exclusion", "spotlight-exclusion"}
	if !slices.Equal(run, want) {
		t.Errorf("the run's order is %v\nwant %v", run, want)
	}
	var file []string
	for _, s := range kinds.Sections() {
		file = append(file, s.Kind)
	}
	want = []string{"git-config", "defaults", "power", "backup-exclusion", "spotlight-exclusion",
		"brew", "cask", "mas", "npm", "composer", "go", "gh", "login-item", "claude-mcp", "claude-skill", "secret"}
	if !slices.Equal(file, want) {
		t.Errorf("the file's order is %v\nwant %v", file, want)
	}
}

// Each kind's section, empty, declares that kind and no other, among the
// config's own sections.
func TestEachSectionDeclaresItsKind(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	for _, s := range kinds.Sections() {
		b.WriteString("[" + s.Header + "]\n")
	}
	for name, content := range map[string]string{
		config.File:                    "format = 1\nprimary = \"laptop\"\n[macs.laptop]\n[macs.studio]\n",
		config.DeclFile("laptop"):      b.String(),
		config.DeclFile(config.Shared): "[features]\n",
	} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := config.Load(dir, kinds.Sections())
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range kinds.All {
		if k.Section == nil {
			continue
		}
		for mac, want := range map[string]bool{"laptop": true, "studio": false} {
			if list, err := cfg.List(k.Name, mac); err != nil || list.Declared != want || len(list.Entries) != 0 {
				t.Errorf("List(%s, %s) = %+v, %v; want declared %v, nothing in it", k.Name, mac, list, err, want)
			}
		}
	}
}

func TestLabelAndOf(t *testing.T) {
	if got := kinds.Label("brew"); got != "Homebrew formulae" {
		t.Errorf("Label(brew) = %q, want Homebrew formulae", got)
	}
	if got := kinds.Label("mise"); got != "mise" {
		t.Errorf("Label(mise) = %q, want a step that isn't a kind's named as it is", got)
	}
	if d, ok := kinds.Of("homebrew"); !ok || d.Name != "brew" {
		t.Errorf("Of(homebrew) = %s, %v; want the formulae, the first kind needing it", d.Name, ok)
	}
	if _, ok := kinds.Of("mise"); ok {
		t.Error("Of(mise) found a kind, want none")
	}
}
