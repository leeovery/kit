package cli_test

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/logs"
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

// restoreRecord is the restore's record, in kit's state directory.
var restoreRecord = filepath.Join(".local", "state", "kit", "restore.json")

// partWay is a restore's record with ~/Code's copy part-way, from Time
// Machine's backup.
const partWay = `{"paths": {"~/Code": {"state": "copying", "from": {"kind": "time-machine", "detail": "com.apple.TimeMachine.2026-11-08-141900.backup"}, "started": "2026-11-20T10:00:00Z"}}}`

// The data restore is a step of a Mac that lists paths in [restore] and
// started from another, as kit.toml says; or of one whose record has a
// listed path's restore part-way, which carries on from where it began,
// kit apply naming no source. On a Mac without either, it's none: a fresh
// Mac, or one in use, its restore done.
func TestRestoreIsThereOnlyForAMacStartedFromAnother(t *testing.T) {
	for _, tt := range []struct {
		name        string
		lines, from bool
		record      string
		want        bool
	}{
		{"listing paths, started from another", true, true, "", true},
		{"listing paths, started afresh", true, false, "", false},
		{"listing nothing, started from another", false, true, "", false},
		{"listing paths, one part-way", true, false, partWay, true},
		{"listing paths, its restore done", true, false, `{"paths": {"~/Code": {"state": "done", "started": "2026-11-20T10:00:00Z"}}}`, false},
		{"listing nothing, one part-way", false, false, partWay, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := laptopWorld(t)
			if tt.lines {
				w.writeSection(t, "laptop", "restore", "~/Code\n")
			}
			if tt.from {
				w.startedFrom(t)
			}
			if tt.record != "" {
				w.write(t, restoreRecord, tt.record)
			}
			if got := slices.Contains(w.statusSteps(t), "restore"); got != tt.want {
				t.Errorf("kit status has the restore: %v, want %v", got, tt.want)
			}
		})
	}
}

// A restore part-way on a Mac in use shows in kit status as it stands,
// saying where it's from.
func TestRestorePartWayShows(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "restore", "~/Code\n")
	w.write(t, restoreRecord, partWay)
	out, _, _ := w.run(t, "status", "restore")
	if want := "restore attention 1 declared, 1 to restore\nrestore missing ~/Code (part-way from Time Machine's backup of 8 Nov 2026 14:19) (to restore)\n"; !strings.Contains(out, want) {
		t.Errorf("kit status restore printed\n%s\nwant it holding\n%s", out, want)
	}
}

// rsync3 has kit's PATH find rsync 3 in w, as the restore waits for.
func (w *world) rsync3() {
	w.fake.On("rsync", "--version").Prints("rsync  version 3.4.1  protocol version 32\nCapabilities:\n    xattrs, crtimes\n")
}

// kit apply --restore-from has a Mac restore its [restore] paths from a
// backup, started from another or not, once rsync 3 is on kit's PATH: from
// Time Machine's, on a disk attached, with none the step failing saying
// so; Arq's, by hand, without a terminal the step failing saying what to
// restore where in Arq, --at picking its backup as Time Machine's.
func TestApplyRestoresFromTheBackupNamed(t *testing.T) {
	for _, tt := range []struct {
		args []string
		says string
	}{
		{[]string{"--restore-from", "time-machine"}, "no Time Machine disk is attached"},
		{[]string{"--restore-from", "arq"}, "restoring through Arq is by hand: in Arq, restore ~/Code into ~/.local/state/kit/restore/arq/1, then run kit apply again"},
		{[]string{"--restore-from", "arq", "--at", "2026-11-08"}, "restoring through Arq is by hand"},
	} {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			w := laptopWorld(t)
			w.writeSection(t, "laptop", "restore", "~/Code\n")
			w.rsync3()
			w.fake.On("diskutil", "apfs", "list", "-plist").Prints(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>Containers</key><array/></dict></plist>`)
			w.fake.On("diskutil", "list", "-plist").Prints(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>AllDisksAndPartitions</key><array/></dict></plist>`)
			out, errOut, code := w.run(t, append([]string{"apply", "restore"}, tt.args...)...)
			if code == 0 || !strings.Contains(out, tt.says) {
				t.Errorf("kit apply restore %s printed\n%s%s\nexit %d; want it failing, saying %q", strings.Join(tt.args, " "), out, errOut, code, tt.says)
			}
		})
	}
}

// Started from another Mac, the restore copies from it over SSH, finding
// it through Bonjour, as the Mac presenting the host key kit-config records
// for it; with none recorded, it isn't looked for. Without a terminal,
// there's no card to ask where else to restore from, so the step fails,
// saying why and the flag that answers it.
func TestApplyRestoreWithoutATerminalNamesTheFlag(t *testing.T) {
	const ways = ": kit apply --restore-from time-machine|arq restores ~/Code from another source, or kit restore remove ~/Code drops its line\n"
	for _, tt := range []struct {
		name     string
		recorded bool
		why      string
	}{
		{"its host key recorded", true, "studio doesn't answer over SSH: no Mac on the network offers Remote Login"},
		{"no host key recorded", false, "studio can't be restored from: studio's SSH host key isn't in kit-config: kit apply on studio records it"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := laptopWorld(t)
			w.writeSection(t, "laptop", "restore", "~/Code\n")
			w.startedFrom(t)
			if tt.recorded {
				w.write(t, ".config/kit/kit.toml", w.read(t, ".config/kit/kit.toml")+"host_key = \"ssh-rsa ZmFrZSByc2EgaG9zdCBrZXk=\"\n")
			}
			w.rsync3()
			w.fake.On("dns-sd", "-B", "_ssh._tcp", "local.").Prints("Browsing for _ssh._tcp.local.\n")
			out, errOut, code := w.run(t, "apply", "restore")
			if want := "restore failed couldn't restore: " + tt.why + ways; code == 0 || !strings.Contains(out, want) {
				t.Errorf("kit apply restore printed\n%s%s\nexit %d; want it failing, saying\n%s", out, errOut, code, want)
			}
			if browsed := slices.Contains(w.fake.Calls(), "dns-sd -B _ssh._tcp local."); browsed != tt.recorded {
				t.Errorf("looked for Macs on the network: %v, want %v", browsed, tt.recorded)
			}
		})
	}
}

// restoreWaits are the steps the data restore waits for in kit apply's run
// with args, planned, as the run's log has them.
func (w *world) restoreWaits(t *testing.T, args ...string) []string {
	t.Helper()
	out, errOut, _ := w.run(t, append([]string{"apply", "--plan"}, args...)...)
	found, _ := filepath.Glob(filepath.Join(w.home, "Library", "Logs", "kit", "*apply-plan.jsonl"))
	if len(found) != 1 {
		t.Fatalf("kit apply --plan printed\n%s%s\nand left %d logs, want one", out, errOut, len(found))
	}
	records, err := logs.Read(found[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range records {
		for _, s := range r.Steps {
			if s.Name == "restore" {
				return s.Waits
			}
		}
	}
	t.Fatalf("kit apply --plan printed\n%s%s\nwith no restore", out, errOut)
	return nil
}

// The restore comes after the secrets only when it copies from the Mac
// this one started from, over SSH, with the key they write, as kit apply's
// flags can name it: not from a backup they name, nor one a path's
// part-way from.
func TestRestoreAfterTheSecretsOnlyFromTheOldMac(t *testing.T) {
	for _, tt := range []struct {
		name   string
		args   []string
		record string
		want   bool
	}{
		{"from the old Mac", nil, "", true},
		{"from a backup named", []string{"--restore-from", "time-machine"}, "", false},
		{"from the old Mac named, after a backup", []string{"--restore-from", "mac"}, `{"from": {"kind": "time-machine", "detail": "com.apple.TimeMachine.2026-11-08-141900.backup"}}`, true},
		{"after a backup", nil, `{"from": {"kind": "time-machine", "detail": "com.apple.TimeMachine.2026-11-08-141900.backup"}}`, false},
		{"part-way from a backup", nil, partWay, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := laptopWorld(t)
			w.writeSection(t, "laptop", "restore", "~/Code\n")
			w.writeSection(t, "laptop", "secrets", "TOKEN op://vault/item/token\n")
			w.startedFrom(t)
			if tt.record != "" {
				w.write(t, restoreRecord, tt.record)
			}
			if got := slices.Contains(w.restoreWaits(t, tt.args...), "secret"); got != tt.want {
				t.Errorf("the restore waits for the secrets: %v, want %v", got, tt.want)
			}
		})
	}
}

// --restore-from names a source kit knows, for the paths [restore] lists,
// the old Mac only for a Mac started from one; and --at a date or a time,
// with a backup's alone: anything else is an error naming the flag, before
// anything runs.
func TestApplyRestoreFlags(t *testing.T) {
	for _, tt := range []struct {
		args  []string
		lines string
		want  string
	}{
		{[]string{"--restore-from", "dropbox"}, "", `invalid argument "dropbox" for "--restore-from" flag: "dropbox" isn't a source: mac, time-machine or arq`},
		{[]string{"--restore-from", "time-machine", "--at", "yesterday"}, "", `invalid argument "yesterday" for "--at" flag: "yesterday" isn't a date, as 2026-11-08, or a time, as "2026-11-08 14:30"`},
		{[]string{"--restore-from", "time-machine", "--at", "2026-11-08T14:30"}, "", `invalid argument "2026-11-08T14:30" for "--at" flag`},
		{[]string{"--at", "2026-11-08"}, "", "--at picks a backup: give it with --restore-from time-machine or arq"},
		{[]string{"--restore-from", "mac", "--at", "2026-11-08"}, "", "--at picks a backup: give it with --restore-from time-machine or arq"},
		{[]string{"--restore-from", "time-machine"}, "", "--restore-from restores the paths [restore] lists, and none are listed for laptop"},
		{[]string{"--restore-from", "mac"}, "~/Code\n", "--restore-from mac copies from the Mac laptop started from (from, in kit.toml), and it names none"},
	} {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			w := laptopWorld(t)
			if tt.lines != "" {
				w.writeSection(t, "laptop", "restore", tt.lines)
			}
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
// from this one would have it, whether or not this one did: copying from
// this Mac over SSH, after the secrets.
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
		if before := names(w.order(t, "restore").Before); !slices.Contains(before, "secret") {
			t.Errorf("started from another %v: before the restore = %q; want the secrets", from, before)
		}
		doc := w.order(t, "alone")
		i := slices.IndexFunc(doc.Unordered, func(s spot) bool { return s.Step == "restore" })
		if i < 0 || doc.Unordered[i].Says != "[restore]'s paths restored from the Mac it started from, or a backup, where they're not here already" {
			t.Errorf("started from another %v: in no order with alone = %+v; want the restore, saying what it does", from, doc.Unordered)
		}
	}
}

// A step of the user's own placed after the restore or the reinstall, or
// before either, on a Mac that restores nothing is placed without it, and
// runs all the same, never failing; a thing of the restore's, which has
// none, is no step.
func TestAfterTheRestoreWithoutOne(t *testing.T) {
	w := orderWorld(t)
	w.writeSection(t, "laptop", "steps", `uses-code "uses the code" --after restore
before-it "comes before it" --before restore
uses-deps "uses the projects" --after reinstall
tool-first "puts a tool in place" --before reinstall
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
	for _, name := range []string{"uses-code", "before-it", "uses-deps", "tool-first"} {
		doc := w.order(t, name)
		if all := names(slices.Concat(doc.Before, doc.After, doc.Unordered, doc.Absent)); slices.Contains(all, "restore") || slices.Contains(all, "reinstall") || fails(name) != "" {
			t.Errorf("kit agent order %s placed it among %q, failing %q; want it placed, the restore and the reinstall nowhere", name, all, fails(name))
		}
	}
	if got := fails("typo"); !strings.HasSuffix(got, ": it comes after restore:code, which isn't a step") {
		t.Errorf("kit agent order typo fails %q; want it failing, restore:code no step", got)
	}
}
