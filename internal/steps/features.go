package steps

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/runner"
)

// Scratch checks the Scratch volume is set up as it's meant to be: mounted,
// kept out of Spotlight and Time Machine, its tmp writable, and Claude
// Code's settings sending its temporary files there (TMPDIR, and
// CLAUDE_CODE_TMPDIR for its own per-session folder).
func Scratch(run runner.Runner, volume, home string) engine.Step {
	return engine.Step{
		Name: FeatureScratch, Title: "Scratch volume", Area: AreaMac,
		Check: func(ctx context.Context) check.Result {
			mounts, err := output(ctx, run, "mount")
			if err != nil {
				return failed(err)
			}
			if !strings.Contains(mounts, " on "+volume+" ") {
				return problem(FeatureScratch, "not mounted", [3]string{"missing", volume + " isn't mounted, so agents' temporary files have nowhere to go", "make the Scratch volume (an APFS volume named Scratch, Spotlight off, out of Time Machine)"})
			}
			var problems [][3]string
			if out, err := output(ctx, run, "mdutil", "-s", volume); err != nil || !strings.Contains(out, "Indexing disabled") {
				problems = append(problems, [3]string{"indexed", "Spotlight indexes it", "sudo mdutil -i off " + volume})
			}
			if out, err := output(ctx, run, "tmutil", "isexcluded", volume); err != nil || !strings.HasPrefix(strings.TrimSpace(out), "[Excluded]") {
				problems = append(problems, [3]string{"backed-up", "Time Machine backs it up", "sudo tmutil addexclusion -v " + volume})
			}
			tmp := filepath.Join(volume, "tmp")
			if err := syscall.Access(tmp, writable); err != nil {
				problems = append(problems, [3]string{"tmp", tmp + " isn't writable", "mkdir -p " + tmp + " (and chmod 1777 it)"})
			}
			if missing := claudeTmp(home, volume); len(missing) > 0 {
				problems = append(problems, [3]string{"claude", "Claude Code's settings don't send " + strings.Join(missing, " and ") + " to it", "set them in ~/.claude/settings.json's env: \"TMPDIR\": \"" + tmp + "/\", \"CLAUDE_CODE_TMPDIR\": \"" + tmp + "\""})
			}
			if len(problems) > 0 {
				return problem(FeatureScratch, "mounted, not set up as it should be", problems...)
			}
			return check.Result{State: check.OK, Summary: "mounted, not indexed, not backed up; Claude's temporary files there"}
		},
	}
}

// writable is access(2)'s W_OK: whether the caller may write.
const writable = 2

// claudeTmp are the variables Claude Code's settings don't point at the
// volume, of TMPDIR and CLAUDE_CODE_TMPDIR.
func claudeTmp(home, volume string) []string {
	var settings struct {
		Env map[string]string `json:"env"`
	}
	data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err == nil {
		_ = json.Unmarshal(data, &settings)
	}
	var missing []string
	for _, name := range []string{"TMPDIR", "CLAUDE_CODE_TMPDIR"} {
		if !strings.HasPrefix(settings.Env[name], volume+"/") {
			missing = append(missing, name)
		}
	}
	return missing
}

// fullDiskAccessProbes are folders macOS shows only to an app with Full Disk
// Access, relative to the home.
var fullDiskAccessProbes = []string{"Library/Safari", "Library/Mail", "Library/Messages"}

// FullDiskAccess checks kit can read what only Full Disk Access shows, as
// settings capture needs: run by the hourly launch's app, the app's grant
// (lost when it's rebuilt unsigned); run at a terminal, the terminal's.
func FullDiskAccess(home string) engine.Step {
	return engine.Step{
		Name: "full-disk-access", Title: "Full Disk Access", Area: AreaBackups,
		Check: func(context.Context) check.Result {
			denied := 0
			for _, probe := range fullDiskAccessProbes {
				_, err := os.ReadDir(filepath.Join(home, probe))
				switch {
				case err == nil:
					return check.Result{State: check.OK, Summary: "granted"}
				case errors.Is(err, fs.ErrPermission):
					denied++
				}
			}
			if denied == 0 {
				return check.Result{State: check.OK, Summary: "nothing protected to read"}
			}
			return problem("full-disk-access", "not granted", [3]string{"denied", "kit can't read what only Full Disk Access shows, so settings capture will fail", "System Settings › Privacy & Security › Full Disk Access: add the app (or terminal) kit runs in"})
		},
	}
}
