package cli_test

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/logs"
	"github.com/leeovery/kit/internal/runner/runnertest"
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

// settingUp has the boot's record say it set the test's Mac up, as the
// boot that named it records for a Mac started from another, or one
// kit-config has, clean-installed under its own name.
func (w *world) settingUp(t *testing.T) {
	t.Helper()
	w.write(t, bootState, `{"set_up": true}`)
}

// settled is a restore's record with ~/Code done and the restore settled.
const settled = `{"paths": {"~/Code": {"state": "done", "started": "2026-11-20T10:00:00Z"}}, "settled": "2026-11-20T12:00:00Z"}`

// The data restore is a step of a Mac in set-up, the boot having named it,
// started from another or not, till the restore settles, listing paths in
// [restore] or not; or of one whose record has a listed path's restore
// part-way, which carries on from where it began, kit apply naming no
// source. On a Mac without either, it's none: a fresh Mac, one in use,
// started from another or not, or one whose restore has settled.
func TestRestoreIsThereInSetUp(t *testing.T) {
	for _, tt := range []struct {
		name                 string
		lines, from, inSetUp bool
		record               string
		want                 bool
	}{
		{"in set-up, started from another", true, true, true, "", true},
		{"in set-up, clean-installed under its own name", true, false, true, "", true},
		{"in use, started from another", true, true, false, "", false},
		{"in use, started from none", true, false, false, "", false},
		{"in set-up, listing nothing", false, true, true, "", true},
		{"in set-up, its restore settled", true, false, true, settled, false},
		{"in set-up from another, its restore settled", true, true, true, settled, false},
		{"in use, one part-way", true, false, false, partWay, true},
		{"in use, its restore done", true, false, false, `{"paths": {"~/Code": {"state": "done", "started": "2026-11-20T10:00:00Z"}}}`, false},
		{"in use, listing nothing, one part-way", false, false, false, partWay, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := laptopWorld(t)
			if tt.lines {
				w.writeSection(t, "laptop", "restore", "~/Code\n")
			}
			if tt.from {
				w.startedFrom(t)
			}
			if tt.inSetUp {
				w.settingUp(t)
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

// A Mac whose boot named it with nothing in [restore] closes set-up at its
// first kit apply, the restore settling with nothing listed: so a line
// added later never finds set-up's window open. The restore is gone from
// the Mac then, kit status showing none; kit apply --restore-from brings
// it back, leaving a path holding what was written since the boot as it
// is, never setting it aside.
func TestSetUpClosesWithNothingListed(t *testing.T) {
	w := laptopWorld(t)
	w.settingUp(t)
	if got := w.statusSteps(t); !slices.Contains(got, "restore") {
		t.Fatalf("in set-up, listing nothing, kit status has %q; want the restore", got)
	}
	if out, errOut, code := w.run(t, "apply", "restore"); code != 0 || !strings.Contains(w.read(t, restoreRecord), `"settled"`) {
		t.Fatalf("kit apply restore printed\n%s%s\nexit %d, the record %s; want it settled", out, errOut, code, w.read(t, restoreRecord))
	}
	w.writeSection(t, "laptop", "restore", "~/Code\n")
	w.write(t, "Code/since-the-boot", "work")
	if slices.Contains(w.statusSteps(t), "restore") {
		t.Error("a line added since, kit status has the restore; want none")
	}
	out, _, _ := w.run(t, "apply", "--plan", "restore", "--restore-from", "time-machine")
	if want := "restore ok 1 declared, all here already\n"; !strings.Contains(out, want) {
		t.Errorf("kit apply --plan restore --restore-from time-machine printed\n%s\nwant it holding %q", out, want)
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
// backup, started from another or not, in set-up or in use, its restore
// settled or never begun, once rsync 3 is on kit's PATH: from Time
// Machine's, on a disk attached, with none the step failing saying so;
// Arq's, by hand, without a terminal the step failing saying what to
// restore where in Arq, --at picking its backup as Time Machine's. On a
// Mac whose restore settled, it restores a line listed since.
func TestApplyRestoresFromTheBackupNamed(t *testing.T) {
	for _, tt := range []struct {
		args    []string
		settled bool
		says    string
	}{
		{[]string{"--restore-from", "time-machine"}, false, "no Time Machine disk is attached"},
		{[]string{"--restore-from", "arq"}, false, "restoring through Arq is by hand: in Arq, restore ~/Code into ~/.local/state/kit/restore/arq/1, then run kit apply again"},
		{[]string{"--restore-from", "arq", "--at", "2026-11-08"}, false, "restoring through Arq is by hand"},
		{[]string{"--restore-from", "time-machine"}, true, "no Time Machine disk is attached: kit apply --restore-from time-machine|arq restores ~/Notes from another source, or kit restore remove ~/Notes drops its line"},
	} {
		t.Run(fmt.Sprintf("%s, settled %v", strings.Join(tt.args, " "), tt.settled), func(t *testing.T) {
			w := laptopWorld(t)
			w.writeSection(t, "laptop", "restore", "~/Code\n")
			if tt.settled {
				w.settingUp(t)
				w.writeSection(t, "laptop", "restore", "~/Code\n~/Notes\n")
				w.write(t, restoreRecord, settled)
			}
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

// studioName is studio's local hostname, as its kit apply records it;
// studioAt, where it answers on the local network.
const (
	studioName = "Someones-Mac-Studio"
	studioAt   = studioName + ".local"
)

// recordingStudio has kit.toml record studio's host key and local hostname,
// as its kit apply does: none for "".
func (w *world) recordingStudio(t *testing.T, key, name string) {
	t.Helper()
	var lines string
	if key != "" {
		lines += "host_key = \"" + key + "\"\n"
	}
	if name != "" {
		lines += "local_hostname = \"" + name + "\"\n"
	}
	w.write(t, configFile, w.read(t, configFile)+lines)
}

// Started from another Mac, the restore copies from it over SSH, at the
// local hostname kit-config records for it, as the Mac presenting the host
// key recorded; without either recorded, it isn't reached. Without a
// terminal, there's no card to ask where else to restore from, so the step
// fails, saying why, the way on, and the flag that answers it. A Mac
// clean-installed under its own name restores from its own backups, never
// reaching a Mac: without a terminal, there's no card to choose them on,
// so it fails likewise.
func TestApplyRestoreWithoutATerminalNamesTheFlag(t *testing.T) {
	const ways = ": kit apply --restore-from time-machine|arq restores ~/Code from another source, or kit restore remove ~/Code drops its line\n"
	for _, tt := range []struct {
		name, key, named string
		why              string
	}{
		{"recorded, not answering", otherKey, studioName, "studio doesn't answer over SSH: nothing answered at " + studioAt + ", its local hostname in kit-config: " +
			"with studio awake on this network, kit apply on studio records the one it has now, then kit apply here"},
		{"no host key recorded", "", studioName, "studio can't be restored from: studio's SSH host key isn't in kit-config: kit apply on studio records it, then kit apply here"},
		{"no local hostname recorded", otherKey, "", "studio can't be restored from: studio's local hostname isn't in kit-config: kit apply on studio records it, then kit apply here"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := laptopWorld(t)
			w.writeSection(t, "laptop", "restore", "~/Code\n")
			w.startedFrom(t)
			w.settingUp(t)
			w.recordingStudio(t, tt.key, tt.named)
			w.rsync3()
			w.fake.OnMatch("ssh", func(args []string) bool { return slices.Contains(args, studioAt) }).
				PrintsToStderr("ssh: connect to host " + studioAt + " port 22: Operation timed out\n").Exits(255)
			out, errOut, code := w.run(t, "apply", "restore")
			if want := "restore failed couldn't restore: " + tt.why + ways; code == 0 || !strings.Contains(out, want) {
				t.Errorf("kit apply restore printed\n%s%s\nexit %d; want it failing, saying\n%s", out, errOut, code, want)
			}
			reached := slices.ContainsFunc(w.fake.Calls(), func(c string) bool { return strings.HasPrefix(c, "ssh ") })
			if want := tt.key != "" && tt.named != ""; reached != want {
				t.Errorf("reached studio: %v, want %v", reached, want)
			}
		})
	}
	t.Run("clean-installed under its own name", func(t *testing.T) {
		w := laptopWorld(t)
		w.writeSection(t, "laptop", "restore", "~/Code\n")
		w.settingUp(t)
		w.rsync3()
		out, errOut, code := w.run(t, "apply", "restore")
		want := "restore failed couldn't restore: laptop restores from its own backups, as it started from no other Mac, and which of them a whole kit apply at a terminal asks" +
			": kit apply --restore-from time-machine|arq restores ~/Code from the backup it names, or kit restore remove ~/Code drops its line\n"
		if code == 0 || !strings.Contains(out, want) {
			t.Errorf("kit apply restore printed\n%s%s\nexit %d; want it failing, saying\n%s", out, errOut, code, want)
		}
		if calls := w.fake.Calls(); slices.ContainsFunc(calls, func(c string) bool { return strings.HasPrefix(c, "ssh") }) {
			t.Errorf("kit apply ran %q; want no Mac reached", calls)
		}
	})
}

// Moving day: a Mac the boot set up as started from studio copies its
// [restore] paths from studio at its first kit apply, reaching it at the
// local hostname kit-config records, then .local, trusting the host key
// recorded alone, under studio's kit name, for the probe, the look and the
// copy; the remote's record unread, as the boot's kit-config is the record,
// brought up to date as the run began.
func TestApplyRestoresFromTheMacItStartedFrom(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "restore", "~/Code\n")
	w.startedFrom(t)
	w.settingUp(t)
	w.recordingStudio(t, otherKey, studioName)
	w.rsync3()
	w.fake.OnMatch("ssh", func(args []string) bool {
		return slices.Contains(args, studioAt) && strings.Contains(args[len(args)-1], "kit/machine")
	}).Prints("user=" + filepath.Base(w.home) + "\nhome=" + w.home + "\nkit=studio\nprotected=read Documents\n" +
		"rsync=rsync  version 3.4.1  protocol version 32\nrsync=Capabilities:\nrsync=    xattrs, crtimes\n")
	w.fake.OnMatch("ssh", func(args []string) bool {
		return slices.Contains(args, studioAt) && strings.Contains(args[len(args)-1], "\nlook ")
	}).Prints("folder\x00")
	const stats = "Number of files: 2 (reg: 1, dir: 1)\nNumber of regular files transferred: 1\nTotal file size: 1000 bytes\nTotal transferred file size: 1000 bytes\n"
	from := studioAt + ":" + filepath.Join(w.home, "Code") + "/"
	rsyncs := func(flag string) *runnertest.Script {
		return w.fake.OnMatch("rsync", func(args []string) bool { return slices.Contains(args, flag) && slices.Contains(args, from) })
	}
	rsyncs("--dry-run").Prints(stats)
	rsyncs("--info=progress2,skip1").Prints("1000 100% 1.00MB/s 0:00:01 (xfr#1, to-chk=0/1)\r\n" + stats)
	rsyncs("--checksum")

	out, errOut, code := w.run(t, "apply", "restore")
	if code != 0 || !strings.Contains(out, "restore restored ~/Code\n") {
		t.Fatalf("kit apply restore printed\n%s%s\nexit %d; want ~/Code restored", out, errOut, code)
	}
	if rec := w.read(t, restoreRecord); !strings.Contains(rec, `"~/Code": {`) || !strings.Contains(rec, `"state": "done"`) || !strings.Contains(rec, `"detail": "studio"`) {
		t.Errorf("the restore's record = %s; want ~/Code done, from studio", rec)
	}
	pinned := filepath.Join(w.stateDir(), "restore", "pinned_host")
	if got := w.read(t, strings.TrimPrefix(pinned, w.home+"/")); got != "studio "+otherKey+"\n" {
		t.Errorf("the pinned key's file = %q; want studio's key", got)
	}
	trusts := []string{"StrictHostKeyChecking=yes", "UserKnownHostsFile=" + pinned, "HostKeyAlias=studio"}
	if fetches := slices.DeleteFunc(w.fake.Calls(), func(c string) bool { return !strings.Contains(c, " fetch ") }); len(fetches) != 1 || !w.pulled(fetches[0]) {
		t.Errorf("fetched %q; want the run's own pull alone", fetches)
	}
	for _, c := range w.fake.Commands() {
		said := strings.Join(c.Args, " ")
		if c.Name == "git" && strings.Contains(said, " show ") {
			t.Errorf("ran git %s; want the remote's record unread at the first try", said)
		}
		copying := c.Name == "rsync" && slices.Contains(c.Args, from)
		if c.Name != "ssh" && !copying {
			continue
		}
		for _, want := range trusts {
			if !strings.Contains(said, want) {
				t.Errorf("%s %s\nwant it holding %s", c.Name, said, want)
			}
		}
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
			w.settingUp(t)
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
// anything runs. One the config decides is read against it as the run
// reads it, once kit-config's remote commits are brought in (pulls); the
// rest are refused before even that.
func TestApplyRestoreFlags(t *testing.T) {
	for _, tt := range []struct {
		args  []string
		lines string
		want  string
		pulls bool
	}{
		{[]string{"--restore-from", "dropbox"}, "", `invalid argument "dropbox" for "--restore-from" flag: "dropbox" isn't a source: mac, time-machine or arq`, false},
		{[]string{"--restore-from", "time-machine", "--at", "yesterday"}, "", `invalid argument "yesterday" for "--at" flag: "yesterday" isn't a date, as 2026-11-08, or a time, as "2026-11-08 14:30"`, false},
		{[]string{"--restore-from", "time-machine", "--at", "2026-11-08T14:30"}, "", `invalid argument "2026-11-08T14:30" for "--at" flag`, false},
		{[]string{"--at", "2026-11-08"}, "", "--at picks a backup: give it with --restore-from time-machine or arq", false},
		{[]string{"--restore-from", "mac", "--at", "2026-11-08"}, "", "--at picks a backup: give it with --restore-from time-machine or arq", false},
		{[]string{"--restore-from", "time-machine"}, "", "--restore-from restores the paths [restore] lists, and none are listed for laptop", true},
		{[]string{"--restore-from", "mac"}, "~/Code\n", "--restore-from mac copies from the Mac laptop started from (from, in kit.toml), and it names none", true},
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
			calls := w.fake.Calls()
			if slices.ContainsFunc(calls, func(c string) bool { return !strings.HasPrefix(c, "sudo -n") && !w.pulled(c) }) {
				t.Errorf("kit apply ran %q", calls)
			}
			if pulled := slices.ContainsFunc(calls, w.pulled); pulled != tt.pulls {
				t.Errorf("kit apply brought in kit-config's remote commits: %v, want %v", pulled, tt.pulls)
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
		i := slices.IndexFunc(doc.Unordered, func(s spot) bool { return s.Name == "restore" })
		if i < 0 || doc.Unordered[i].Says != "[restore]'s paths restored from the Mac it started from, or a backup, where they're not here already" {
			t.Errorf("started from another %v: in no order with alone = %+v; want the restore, saying what it does", from, doc.Unordered)
		}
	}
}

// A step of the user's own placed after the restore, or before it, on a Mac that restores nothing is placed without it, and
// runs all the same, never failing; a thing of the restore's, which has
// none, is no step.
func TestAfterTheRestoreWithoutOne(t *testing.T) {
	w := orderWorld(t)
	w.writeSection(t, "laptop", "steps", `uses-code "uses the code" --after restore
before-it "comes before it" --before restore
typo "names a thing of the restore's" --after restore:code
`)
	for _, name := range []string{"uses-code", "before-it"} {
		doc := w.order(t, name)
		if all := names(slices.Concat(doc.Before, doc.After, doc.Unordered, doc.Absent)); slices.Contains(all, "restore") || doc.Fails != "" {
			t.Errorf("kit agent order %s placed it among %q, failing %q; want it placed, the restore nowhere", name, all, doc.Fails)
		}
	}
	if got := w.order(t, "typo").Fails; !strings.HasSuffix(got, ": it comes after restore:code, which isn't a step") {
		t.Errorf("kit agent order typo fails %q; want it failing, restore:code no step", got)
	}
}
