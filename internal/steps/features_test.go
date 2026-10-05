package steps_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/runner/runnertest"
	"github.com/leeovery/kit/internal/steps"
)

func TestScratch(t *testing.T) {
	volume, home := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(volume, "tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	settings := func(env string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"env": {`+env+`}}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mounted := "/dev/disk3s7 on " + volume + " (apfs, local, nobrowse)\n"
	for _, tt := range []struct {
		name          string
		mount, mdutil string
		excluded, env string
		state         check.State
		ids           []string
	}{
		{"set up", mounted, volume + ":\n\tIndexing disabled.\n", "[Excluded]    " + volume + "\n", `"TMPDIR": "` + volume + `/tmp/", "CLAUDE_CODE_TMPDIR": "` + volume + `/tmp"`, check.OK, nil},
		{"indexed, backed up, Claude elsewhere", mounted, volume + ":\n\tIndexing enabled.\n", "[Included]    " + volume + "\n", `"TMPDIR": "/tmp/"`, check.Attention, []string{"scratch:indexed", "scratch:backed-up", "scratch:claude"}},
		{"not mounted", "/dev/disk3s1 on / (apfs)\n", "", "", "", check.Attention, []string{"scratch:missing"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			settings(tt.env)
			fake := runnertest.New(t)
			fake.On("mount").Prints(tt.mount)
			fake.On("mdutil", "-s", volume).Prints(tt.mdutil)
			fake.On("tmutil", "isexcluded", volume).Prints(tt.excluded)
			state, _, ids := checked(t, steps.Scratch(fake, &steps.Admin{}, volume, home, 501))
			if state != tt.state || !slices.Equal(ids, tt.ids) {
				t.Errorf("= %s %q, want %s %q", state, ids, tt.state, tt.ids)
			}
		})
	}
}

func TestFullDiskAccess(t *testing.T) {
	home := t.TempDir()
	for _, p := range []string{"Library/Safari", "Library/Mail", "Library/Messages"} {
		if err := os.MkdirAll(filepath.Join(home, p), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if state, text, _ := checked(t, steps.FullDiskAccess(home)); state != check.OK || text != "granted" {
		t.Errorf("readable = %s %q", state, text)
	}
	for _, p := range []string{"Library/Safari", "Library/Mail", "Library/Messages"} {
		if err := os.Chmod(filepath.Join(home, p), 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(filepath.Join(home, p), 0o700) })
	}
	if state, _, ids := checked(t, steps.FullDiskAccess(home)); state != check.Attention || !slices.Equal(ids, []string{"full-disk-access:denied"}) {
		t.Errorf("denied = %s %q", state, ids)
	}
}

func TestScratchApplyMakesWhatsMissing(t *testing.T) {
	fake := runnertest.New(t)
	admin := &steps.Admin{Held: func(context.Context) bool { return true }}
	step := steps.Scratch(fake, admin, "/Volumes/Scratch", t.TempDir(), 501)
	fake.On("mount").Prints("/dev/disk3s5 on /System/Volumes/Data (apfs)\n")
	res := step.Check(t.Context())
	if res.Items[0].Action != steps.ActionFix || !step.Admin {
		t.Fatalf("not mounted = %+v", res)
	}
	fake.On("diskutil", "info", "-plist", "/").Prints("<plist><dict><key>APFSContainerReference</key><string>disk3</string></dict></plist>")
	for _, args := range [][]string{
		{"-n", "diskutil", "apfs", "addVolume", "disk3", "APFS", "Scratch"},
		{"-n", "mkdir", "-p", "/Volumes/Scratch/.fseventsd"},
		{"-n", "touch", "/Volumes/Scratch/.fseventsd/no_log"},
		{"-n", "mdutil", "-i", "off", "/Volumes/Scratch"},
		{"-n", "tmutil", "addexclusion", "-v", "/Volumes/Scratch"},
		{"-n", "install", "-d", "-o", "501", "-g", "staff", "-m", "700", "/Volumes/Scratch/tmp"},
	} {
		fake.On("sudo", args...)
	}
	if err := step.Apply(t.Context(), res); err != nil {
		t.Fatalf("Apply() = %v", err)
	}
	if n := len(fake.Calls()); n != 8 {
		t.Errorf("ran %d commands:\n%s", n, strings.Join(fake.Calls(), "\n"))
	}
}
