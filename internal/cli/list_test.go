package cli_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestList(t *testing.T) {
	out, _, code := laptopWorld(t).run(t, "list")
	want := `kit list · laptop
brew  jq              shared
brew  owner/tap/tool  shared
brew  go              laptop
cask  ghostty         shared
`
	if out != want || code != 0 {
		t.Errorf("kit list printed\n%s exit %d\nwant\n%s", out, code, want)
	}
}

func TestListSaysWhatsMissing(t *testing.T) {
	w := missingWorld(t)
	w.writeSection(t, "laptop", "homebrew formulae", "# Search\nripgrep   # fast grep\ngo\n")
	out, _, _ := w.run(t, "list", "brew")
	if !strings.Contains(out, "brew  ripgrep         laptop  (missing)  # fast grep\n") || strings.Contains(out, "cask") {
		t.Errorf("kit list brew printed\n%s\nwant ripgrep missing, with its note, and no casks", out)
	}
}

func TestListJSON(t *testing.T) {
	out, _, _ := laptopWorld(t).run(t, "list", "--json")
	var doc struct {
		Schema  int `json:"schema"`
		Entries []struct {
			Kind, Name, Scope, File string
			Line                    int
			Installed               bool
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || doc.Schema != 2 || len(doc.Entries) != 4 || strings.Contains(out, `"group"`) {
		t.Fatalf("kit list --json printed %q: %v", out, err)
	}
	if e := doc.Entries[2]; e.Kind != "brew" || e.Name != "go" || e.Scope != "laptop" || e.File != "laptop/declarations" || e.Line != 2 || !e.Installed {
		t.Errorf("third entry = %+v", e)
	}
}

func TestWhy(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "homebrew formulae", "# Go\ngo   # for kit\n")
	w.fake.On("brew", "uses", "--installed", "go").Prints("golangci-lint\ngoreleaser\n")
	w.fake.On("brew", "uses", "--installed", "ffmpeg")

	out, _, code := w.run(t, "why", "go")
	want := "go (brew)\n  declared in laptop/declarations, line 3: for kit\n  installed here, by kit apply's Formulae step\n  needed by golangci-lint, goreleaser\n"
	if out != want || code != 0 {
		t.Errorf("kit why go printed\n%s exit %d\nwant\n%s", out, code, want)
	}
	out, _, _ = w.run(t, "why", "ffmpeg")
	want = "ffmpeg (brew)\n  declared in studio/declarations, line 2\n  installed here, not declared for this Mac: kit reconcile brew:ffmpeg\n  needed by nothing installed\n"
	if out != want {
		t.Errorf("kit why ffmpeg printed\n%s\nwant\n%s", out, want)
	}
}

func TestWhyOfSomethingUnknown(t *testing.T) {
	out, errOut, code := laptopWorld(t).run(t, "why", "nosuch")
	if out != "" || errOut != "kit: nosuch isn't declared for any Mac, nor installed here\n" || code != 1 {
		t.Errorf("kit why nosuch printed %q, %q, exit %d", out, errOut, code)
	}
}

func TestWhyJSON(t *testing.T) {
	w := laptopWorld(t)
	w.fake.On("brew", "uses", "--installed", "jq")
	out, _, _ := w.run(t, "why", "jq", "--json")
	var doc struct {
		Schema int    `json:"schema"`
		Name   string `json:"name"`
		Kinds  []struct {
			Kind       string `json:"kind"`
			ForThisMac bool   `json:"for_this_mac"`
			Installed  bool   `json:"installed"`
			Declared   []struct{ Scope string }
		} `json:"kinds"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || doc.Schema != 2 || doc.Name != "jq" || len(doc.Kinds) != 1 || strings.Contains(out, `"Group"`) {
		t.Fatalf("kit why --json printed %q: %v", out, err)
	}
	if k := doc.Kinds[0]; k.Kind != "brew" || !k.ForThisMac || !k.Installed || k.Declared[0].Scope != "shared" {
		t.Errorf("kind = %+v", k)
	}
}
