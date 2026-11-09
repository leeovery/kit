package cli_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// reinstallRecord is the reinstall's record, in kit's state directory.
var reinstallRecord = filepath.Join(".local", "state", "kit", "reinstall.json")

// The reinstall is a step of a Mac whose run has the data restore, yet to
// settle, as a new Mac's first does before anything's recorded; or whose
// restore has done a path not yet searched for projects, or whose projects
// have been found, the restore in the run or not. On a Mac without any of
// these, it's none.
func TestReinstallIsThereWithWhatTheRestoreBrings(t *testing.T) {
	for _, tt := range []struct {
		name                string
		from                bool
		restored, reinstall string
		want                bool
	}{
		{"restoring, started from another", true, "", "", true},
		{"restoring, settled", true, `{"settled": "2026-11-20T12:00:00Z"}`, `{"searched": ["~/Code"]}`, false},
		{"a path done, not searched", false, `{"paths": {"~/Code": {"state": "done"}}, "settled": "2026-11-20T12:00:00Z"}`, "", true},
		{"projects found", false, "", `{"searched": ["~/Code"], "installs": [{"dir": "~/Code/app", "tool": "npm", "outcome": "installed"}]}`, true},
		{"nothing restored", false, "", "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := laptopWorld(t)
			w.writeSection(t, "laptop", "restore", "~/Code\n")
			if tt.from {
				w.startedFrom(t)
			}
			if tt.restored != "" {
				w.write(t, restoreRecord, tt.restored)
			}
			if tt.reinstall != "" {
				w.write(t, reinstallRecord, tt.reinstall)
			}
			if got := slices.Contains(w.statusSteps(t), "reinstall"); got != tt.want {
				t.Errorf("kit status has the reinstall: %v, want %v", got, tt.want)
			}
		})
	}
}

// An install that failed runs again only when kit apply names the
// reinstall: applying everything leaves it, as some old projects never
// install.
func TestReinstallRetriesWhenNamed(t *testing.T) {
	w := laptopWorld(t)
	w.write(t, reinstallRecord, `{"searched": ["~/Code"], "installs": [{"dir": "~/Code/app", "tool": "npm", "started": true, "outcome": "failed", "error": "npm ERR! code E404"}]}`)
	w.fake.On("npm", "ci")

	out, _, _ := w.run(t, "apply", "--plan")
	if want := "reinstall ok 0 installed; 1 failed\n"; !strings.Contains(out, want) {
		t.Errorf("kit apply --plan printed\n%s\nwant it holding %q", out, want)
	}
	out, _, _ = w.run(t, "apply", "--plan", "reinstall")
	if want := "reinstall attention 1 to install\n"; !strings.Contains(out, want) {
		t.Errorf("kit apply --plan reinstall printed\n%s\nwant it holding %q", out, want)
	}
	if calls := w.fake.Calls(); slices.ContainsFunc(calls, func(c string) bool { return strings.HasPrefix(c, "npm") }) {
		t.Fatalf("planning, kit ran %q", calls)
	}

	out, errOut, code := w.run(t, "apply", "reinstall")
	if !slices.Contains(w.fake.Calls(), "npm ci") || code != 0 {
		t.Errorf("kit apply reinstall printed\n%s%s\nexit %d, running %q; want npm ci run", out, errOut, code, w.fake.Calls())
	}
	if got := w.read(t, reinstallRecord); !strings.Contains(got, `"outcome": "installed"`) {
		t.Errorf("the reinstall's record = %s; want the install installed", got)
	}
}

// The reinstall comes after the restore, whose projects it installs, and
// after the secrets, which write what private packages need; a step of the
// user's own can come before it, as one putting a tool in place, or after
// it. kit agent order places it, as a new Mac started from this one would
// have it, the restore settled here or not.
func TestReinstallComesAfterTheRestoreAndTheSecrets(t *testing.T) {
	for _, settled := range []bool{false, true} {
		w := restoreWorld(t)
		w.writeSection(t, "laptop", "steps", `alone "stands alone"
tool-first "puts a tool in place" --before reinstall
uses-deps "uses the projects" --after reinstall
`)
		if settled {
			w.write(t, restoreRecord, `{"paths": {"~/Code": {"state": "done"}}, "settled": "2026-11-20T12:00:00Z"}`)
			w.write(t, reinstallRecord, `{"searched": ["~/Code"]}`)
		}
		before := names(w.order(t, "reinstall").Before)
		for _, want := range []string{"restore", "secret", "tool-first"} {
			if !slices.Contains(before, want) {
				t.Errorf("settled %v: before the reinstall = %q; want %s", settled, before, want)
			}
		}
		if before := names(w.order(t, "uses-deps").Before); !slices.Contains(before, "reinstall") {
			t.Errorf("settled %v: before uses-deps = %q; want the reinstall", settled, before)
		}
		doc := w.order(t, "alone")
		i := slices.IndexFunc(doc.Unordered, func(s spot) bool { return s.Step == "reinstall" })
		if i < 0 || doc.Unordered[i].Says != "the restored projects' dependencies installed again, by their lockfiles, where their tools are installed" {
			t.Errorf("settled %v: in no order with alone = %+v; want the reinstall, saying what it does", settled, doc.Unordered)
		}
	}
}
