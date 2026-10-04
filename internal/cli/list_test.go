package cli_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestList(t *testing.T) {
	out, _, code := laptopWorld(t).run(t, "list")
	want := `kit list · laptop
brew  jq              brew         Shell
brew  owner/tap/tool  brew         Shell
brew  go              brew.laptop
cask  ghostty         cask
`
	if out != want || code != 0 {
		t.Errorf("kit list printed\n%s exit %d\nwant\n%s", out, code, want)
	}
}

func TestListSaysWhatsMissing(t *testing.T) {
	w := missingWorld(t)
	w.write(t, brewLaptop, "# Search\nripgrep   # fast grep\ngo\n")
	out, _, _ := w.run(t, "list", "brew")
	if !strings.Contains(out, "brew  ripgrep         brew.laptop  Search  (missing)  # fast grep\n") || strings.Contains(out, "cask") {
		t.Errorf("kit list brew printed\n%s\nwant ripgrep missing, with its note, and no casks", out)
	}
}

func TestListJSON(t *testing.T) {
	out, _, _ := laptopWorld(t).run(t, "list", "--json")
	var doc struct {
		Schema  int `json:"schema"`
		Entries []struct {
			Kind, Name, File, Group string
			Line                    int
			Installed               bool
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || doc.Schema != 1 || len(doc.Entries) != 4 {
		t.Fatalf("kit list --json printed %q: %v", out, err)
	}
	if e := doc.Entries[2]; e.Kind != "brew" || e.Name != "go" || e.File != "brew.laptop" || e.Line != 1 || !e.Installed {
		t.Errorf("third entry = %+v", e)
	}
}

func TestWhy(t *testing.T) {
	w := laptopWorld(t)
	w.write(t, brewLaptop, "# Go\ngo   # for kit\n")
	w.fake.On("brew", "uses", "--installed", "go").Prints("golangci-lint\ngoreleaser\n")
	w.fake.On("brew", "uses", "--installed", "ffmpeg")

	out, _, code := w.run(t, "why", "go")
	want := "go (brew)\n  declared in brew.laptop, line 2, under Go: for kit\n  installed here, by kit apply's Formulae step\n  needed by golangci-lint, goreleaser\n"
	if out != want || code != 0 {
		t.Errorf("kit why go printed\n%s exit %d\nwant\n%s", out, code, want)
	}
	out, _, _ = w.run(t, "why", "ffmpeg")
	want = "ffmpeg (brew)\n  declared in brew.studio, line 1\n  installed here, not declared for this Mac: kit reconcile brew:ffmpeg\n  needed by nothing installed\n"
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
		Name  string `json:"name"`
		Kinds []struct {
			Kind       string `json:"kind"`
			ForThisMac bool   `json:"for_this_mac"`
			Installed  bool   `json:"installed"`
			Declared   []struct{ File string }
		} `json:"kinds"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || doc.Name != "jq" || len(doc.Kinds) != 1 {
		t.Fatalf("kit why --json printed %q: %v", out, err)
	}
	if k := doc.Kinds[0]; k.Kind != "brew" || !k.ForThisMac || !k.Installed || k.Declared[0].File != "brew" {
		t.Errorf("kind = %+v", k)
	}
}
