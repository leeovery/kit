package cli_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// startedFrom has kit.toml say the test's Mac, laptop, started from
// studio.
func (w *world) startedFrom(t *testing.T) {
	t.Helper()
	w.write(t, ".config/kit/kit.toml", strings.Replace(twoMacs, "description = \"MacBook Pro\"\n", "description = \"MacBook Pro\"\nfrom = \"studio\"\n", 1))
}

// statusSteps are the steps kit status --json checks.
func (w *world) statusSteps(t *testing.T) []string {
	t.Helper()
	out, errOut, _ := w.run(t, "status", "--json")
	var doc struct{ Steps []struct{ ID string } }
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("kit status --json printed\n%s%s: %v", out, errOut, err)
	}
	var ids []string
	for _, s := range doc.Steps {
		ids = append(ids, s.ID)
	}
	return ids
}

// The data restore is a step of a Mac that lists paths in [restore] and
// started from another, as kit.toml says; on a Mac without either, it's
// none: a fresh Mac, or one in use.
func TestRestoreIsThereOnlyForAMacStartedFromAnother(t *testing.T) {
	for _, tt := range []struct {
		name          string
		lines, from   bool
		want          bool
		checkedPrints string
	}{
		{"listing paths, started from another", true, true, true, "1 declared, 1 to restore"},
		{"listing paths, started afresh", true, false, false, ""},
		{"listing nothing, started from another", false, true, false, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := laptopWorld(t)
			if tt.lines {
				w.writeSection(t, "laptop", "restore", "~/Code\n")
			}
			if tt.from {
				w.startedFrom(t)
			}
			if got := slices.Contains(w.statusSteps(t), "restore"); got != tt.want {
				t.Errorf("kit status has the restore: %v, want %v", got, tt.want)
			}
		})
	}
}

// kit apply --restore-from has a Mac restore its [restore] paths from a
// backup, started from another or not: from Time Machine's, on a disk
// attached, once rsync 3 is on kit's PATH; with no disk, the step fails
// saying so. Arq's isn't built yet.
func TestApplyRestoresFromTheBackupNamed(t *testing.T) {
	for _, tt := range []struct {
		source, says string
	}{
		{"time-machine", "no Time Machine disk is attached"},
		{"arq", "restoring through Arq isn't built yet"},
	} {
		t.Run(tt.source, func(t *testing.T) {
			w := laptopWorld(t)
			w.writeSection(t, "laptop", "restore", "~/Code\n")
			w.fake.On("rsync", "--version").Prints("rsync  version 3.4.1  protocol version 32\nCapabilities:\n    xattrs, crtimes\n")
			w.fake.On("diskutil", "apfs", "list", "-plist").Prints(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>Containers</key><array/></dict></plist>`)
			w.fake.On("diskutil", "list", "-plist").Prints(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>AllDisksAndPartitions</key><array/></dict></plist>`)
			out, errOut, code := w.run(t, "apply", "restore", "--restore-from", tt.source)
			if code == 0 || !strings.Contains(out, tt.says) {
				t.Errorf("kit apply restore --restore-from %s printed\n%s%s\nexit %d; want it failing, saying %q", tt.source, out, errOut, code, tt.says)
			}
		})
	}
}

// --restore-from names a backup kit knows, and --at a date or a time, with
// Time Machine's alone: anything else is an error naming the flag, before
// anything runs.
func TestApplyRestoreFlags(t *testing.T) {
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"--restore-from", "dropbox"}, `invalid argument "dropbox" for "--restore-from" flag: "dropbox" isn't a source: time-machine or arq`},
		{[]string{"--restore-from", "time-machine", "--at", "yesterday"}, `invalid argument "yesterday" for "--at" flag: "yesterday" isn't a date, as 2026-11-08, or a time, as "2026-11-08 14:30"`},
		{[]string{"--restore-from", "time-machine", "--at", "2026-11-08T14:30"}, `invalid argument "2026-11-08T14:30" for "--at" flag`},
		{[]string{"--at", "2026-11-08"}, "--at picks a Time Machine backup: give it with --restore-from time-machine"},
		{[]string{"--restore-from", "arq", "--at", "2026-11-08"}, "--at picks a Time Machine backup: give it with --restore-from time-machine"},
	} {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			w := laptopWorld(t)
			_, errOut, code := w.run(t, append([]string{"apply"}, tt.args...)...)
			if code != 2 || !strings.Contains(errOut, tt.want) {
				t.Errorf("kit apply %s printed %q, exit %d; want exit 2 saying %q", strings.Join(tt.args, " "), errOut, code, tt.want)
			}
			if calls := w.fake.Calls(); slices.ContainsFunc(calls, func(c string) bool { return !strings.HasPrefix(c, "sudo -n") }) {
				t.Errorf("kit apply ran %q", calls)
			}
		})
	}
}

// restoreWorld is orderWorld listing paths to restore, as a Mac started
// from another, with Claude's MCP servers and skills, and steps of the
// user's own placed after the restore, and before it.
func restoreWorld(t *testing.T) *world {
	t.Helper()
	w := orderWorld(t)
	w.writeSection(t, "laptop", "restore", "~/Code\n")
	w.writeSection(t, "laptop", "claude mcp", "docs --transport http https://docs.example.com/mcp\n")
	w.writeSection(t, "laptop", "claude skills", "acme/tools/review\n")
	w.writeSection(t, "laptop", "steps", `alone "stands alone"
uses-code "uses the code" --after restore
before-it "comes before it" --before restore
`)
	return w
}

// The kinds that write into the paths the restore brings come after it:
// Claude's MCP servers, added in a project's folder, and Claude's skills,
// in Claude's folder; so do the user's own steps that say so; one placed
// before it, before it. kit agent order places it, as a new Mac started
// from this one would have it, whether or not this one did.
func TestRestoreComesBeforeWhatWritesIntoItsPaths(t *testing.T) {
	for _, from := range []bool{true, false} {
		w := restoreWorld(t)
		if from {
			w.startedFrom(t)
		}
		for _, name := range []string{"claude-mcp", "claude-skill", "uses-code"} {
			if before := names(w.order(t, name).Before); !slices.Contains(before, "restore") {
				t.Errorf("started from another %v: before %s = %q; want the restore", from, name, before)
			}
		}
		if after := names(w.order(t, "before-it").After); !slices.Contains(after, "restore") {
			t.Errorf("started from another %v: after before-it = %q; want the restore", from, after)
		}
		doc := w.order(t, "alone")
		i := slices.IndexFunc(doc.Unordered, func(s spot) bool { return s.Step == "restore" })
		if i < 0 || doc.Unordered[i].Says != "[restore]'s paths restored from the Mac it started from, or a backup, where they're not here already" {
			t.Errorf("started from another %v: in no order with alone = %+v; want the restore, saying what it does", from, doc.Unordered)
		}
	}
}

// A step of the user's own placed after the restore, or before it, on a
// Mac that restores nothing is placed without it, and runs all the same,
// never failing; a thing of the restore's, which has none, is no step.
func TestAfterTheRestoreWithoutOne(t *testing.T) {
	w := orderWorld(t)
	w.writeSection(t, "laptop", "steps", `uses-code "uses the code" --after restore
before-it "comes before it" --before restore
typo "names a thing of the restore's" --after restore:code
`)
	fails := func(name string) string {
		out, _, _ := w.run(t, "agent", "order", name, "--json")
		var doc struct{ Fails string }
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatalf("kit agent order %s --json printed\n%s", name, out)
		}
		return doc.Fails
	}
	for _, name := range []string{"uses-code", "before-it"} {
		doc := w.order(t, name)
		if all := names(slices.Concat(doc.Before, doc.After, doc.Unordered, doc.Absent)); slices.Contains(all, "restore") || fails(name) != "" {
			t.Errorf("kit agent order %s placed it among %q, failing %q; want it placed, the restore nowhere", name, all, fails(name))
		}
	}
	if got := fails("typo"); !strings.HasSuffix(got, ": it comes after restore:code, which isn't a step") {
		t.Errorf("kit agent order typo fails %q; want it failing, restore:code no step", got)
	}
}
