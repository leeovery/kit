package steps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/plist"
	"github.com/leeovery/kit/internal/runner"
)

// Scratch checks the Scratch volume at volume is mounted, kept out of
// Spotlight and Time Machine, with a tmp folder the user, uid, can write,
// and Claude Code's temporary files sent there (from home's settings).
// Applying makes and sets up what it can, through sudo: the volume (with
// its change log off), Spotlight off, out of Time Machine, the tmp folder;
// Claude Code's settings are Claude Code's setup's.
func Scratch(run runner.Runner, admin *Admin, volume, home string, uid int) engine.Step {
	tmp := filepath.Join(volume, "tmp")
	fix := func(it check.Item) check.Item {
		it.Action = ActionFix
		return it
	}
	return engine.Step{
		Name: FeatureScratch, Title: "Scratch volume", Area: AreaMac, Admin: true,
		Check: func(ctx context.Context) check.Result {
			mounts, err := output(ctx, run, "mount")
			if err != nil {
				return failed(err)
			}
			if !strings.Contains(mounts, " on "+volume+" ") {
				res := problem(FeatureScratch, "not mounted", [3]string{"missing", volume + " isn't mounted, so agents' temporary files have nowhere to go", "kit apply makes it: an APFS volume, Spotlight off, out of Time Machine"})
				res.Items[0] = fix(res.Items[0])
				return res
			}
			var problems [][3]string
			if out, err := output(ctx, run, "mdutil", "-s", volume); err != nil || !strings.Contains(out, "Indexing disabled") {
				problems = append(problems, [3]string{"indexed", "Spotlight indexes it", "kit apply turns it off"})
			}
			if out, err := output(ctx, run, "tmutil", "isexcluded", volume); err != nil || !strings.HasPrefix(strings.TrimSpace(out), "[Excluded]") {
				problems = append(problems, [3]string{"backed-up", "Time Machine backs it up", "kit apply excludes it"})
			}
			if err := syscall.Access(tmp, writable); err != nil {
				problems = append(problems, [3]string{"tmp", tmp + " isn't writable", "kit apply makes it, yours alone"})
			}
			if missing := claudeTmp(home, volume); len(missing) > 0 {
				problems = append(problems, [3]string{"claude", "Claude Code's settings don't send " + strings.Join(missing, " and ") + " to it", "set them in ~/.claude/settings.json's env: \"TMPDIR\": \"" + tmp + "/\", \"CLAUDE_CODE_TMPDIR\": \"" + tmp + "\""})
			}
			if len(problems) > 0 {
				res := problem(FeatureScratch, "mounted, not set up as it should be", problems...)
				for i, it := range res.Items {
					if it.ID != FeatureScratch+":claude" {
						res.Items[i] = fix(it)
					}
				}
				return res
			}
			return check.Result{State: check.OK, Summary: "mounted; not indexed; not backed up"}
		},
		Apply: func(ctx context.Context, found check.Result) error {
			if !admin.ok(ctx) {
				return errors.New(AdminWait)
			}
			sudo := func(args ...string) error {
				_, err := run.Run(ctx, runner.Command{Name: "sudo", Args: append([]string{"-n"}, args...)})
				return err
			}
			todo := map[string]bool{}
			for _, it := range found.Items {
				if it.Action == ActionFix {
					todo[strings.TrimPrefix(it.ID, FeatureScratch+":")] = true
				}
			}
			if todo["missing"] {
				container, err := dataContainer(ctx, run)
				if err != nil {
					return err
				}
				if err := sudo("diskutil", "apfs", "addVolume", container, "APFS", filepath.Base(volume)); err != nil {
					return fmt.Errorf("make the volume: %w", err)
				}
				if err := sudo("mkdir", "-p", filepath.Join(volume, ".fseventsd")); err != nil {
					return err
				}
				if err := sudo("touch", filepath.Join(volume, ".fseventsd", "no_log")); err != nil {
					return err
				}
				todo["indexed"], todo["backed-up"], todo["tmp"] = true, true, true
			}
			var errs []error
			if todo["indexed"] {
				errs = append(errs, sudo("mdutil", "-i", "off", volume))
			}
			if todo["backed-up"] {
				errs = append(errs, sudo("tmutil", "addexclusion", "-v", volume))
			}
			if todo["tmp"] {
				errs = append(errs, sudo("install", "-d", "-o", strconv.Itoa(uid), "-g", "staff", "-m", "700", tmp))
			}
			return errors.Join(errs...)
		},
	}
}

// dataContainer is the APFS container the startup disk is on, where the
// Scratch volume goes.
func dataContainer(ctx context.Context, run runner.Runner) (string, error) {
	out, err := output(ctx, run, "diskutil", "info", "-plist", "/")
	if err != nil {
		return "", fmt.Errorf("find the startup disk's container: %w", err)
	}
	v, err := plist.Read(out)
	if err != nil {
		return "", err
	}
	info, _ := v.(map[string]any)
	container, _ := info["APFSContainerReference"].(string)
	if container == "" {
		return "", errors.New("the startup disk isn't in an APFS container")
	}
	return container, nil
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
