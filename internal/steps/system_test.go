package steps_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/runner/runnertest"
	"github.com/leeovery/kit/internal/steps"
)

func TestTouchIDSudo(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sudo_local")
	fake := runnertest.New(t)
	admin := &steps.Admin{}
	step := steps.TouchIDSudo(fake, admin, file)
	res := step.Check(t.Context())
	if res.State != check.Attention || res.Items[0].Action != steps.ActionInstall || !step.Admin {
		t.Fatalf("no file = %+v", res)
	}
	if err := step.Apply(t.Context(), res); err == nil || err.Error() != steps.AdminWait {
		t.Errorf("Apply() without the password = %v", err)
	}
	admin.Held = func(context.Context) bool { return true }
	fake.On("sudo", "-n", "tee", file)
	if err := step.Apply(t.Context(), res); err != nil {
		t.Fatal(err)
	}
	if c := fake.Commands()[0]; c.Input != "# sudo_local: local customizations for sudo\nauth       sufficient     pam_tid.so\n" {
		t.Errorf("wrote %q", c.Input)
	}
	if err := os.WriteFile(file, []byte("# auth sufficient pam_tid.so\nauth       sufficient     pam_tid.so\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	if res := step.Check(t.Context()); res.State != check.OK {
		t.Errorf("on = %+v", res)
	}
}

func TestRemoteLoginAndFileSharing(t *testing.T) {
	fake := runnertest.New(t)
	admin := &steps.Admin{Held: func(context.Context) bool { return true }}
	fake.On("launchctl", "print-disabled", "system").Prints("disabled services = {\n\t\t\"com.openssh.sshd\" => enabled\n\t\t\"com.apple.smbd\" => disabled\n}\n")
	if res := steps.RemoteLogin(fake, admin).Check(t.Context()); res.State != check.OK {
		t.Errorf("Remote Login = %+v", res)
	}
	sharing := steps.FileSharing(fake, admin)
	res := sharing.Check(t.Context())
	if res.State != check.Attention || res.Items[0].Action != steps.ActionInstall {
		t.Fatalf("File Sharing = %+v", res)
	}
	fake.On("sudo", "-n", "launchctl", "enable", "system/com.apple.smbd")
	fake.On("sudo", "-n", "launchctl", "bootstrap", "system", "/System/Library/LaunchDaemons/com.apple.smbd.plist").Exits(37)
	if err := sharing.Apply(t.Context(), res); err != nil {
		t.Errorf("Apply() = %v; a service loaded already is no failure", err)
	}
	if calls := strings.Join(fake.Calls(), "\n"); !strings.Contains(calls, "sudo -n launchctl enable system/com.apple.smbd") {
		t.Errorf("ran\n%s", calls)
	}
}

// fakeHostKey is an SSH host key, plainly no real one.
const fakeHostKey = "ssh-ed25519 ZmFrZSBlZDI1NTE5IGhvc3Qga2V5"

// The host key's step is ok while kit-config records this Mac's key, its
// type and key whatever comment either has; else applying hands the key,
// as its file has it but the comment, to be recorded, and the check after
// says how recording went. With no key yet, there's nothing to record.
func TestHostKey(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "ssh_host_ed25519_key.pub")
	var recorded []string
	step := func(in string) engine.Step {
		return steps.HostKey(steps.HostKeying{Dir: dir, Recorded: in, Record: func(_ context.Context, key string) (check.Result, error) {
			recorded = append(recorded, key)
			return check.Result{State: check.OK, Summary: "committed and pushed kit.toml"}, nil
		}})
	}

	res := step(fakeHostKey).Check(t.Context())
	if err := step(fakeHostKey).Apply(t.Context(), res); res.State != check.Attention || !engine.IsNotYet(err) {
		t.Errorf("no key yet: check %+v, apply %v; want attention, not yet", res, err)
	}
	if err := os.WriteFile(key, []byte(fakeHostKey+" root@laptop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res := step(fakeHostKey + " a comment").Check(t.Context()); res.State != check.OK {
		t.Errorf("recorded = %+v, want ok", res)
	}
	unrecorded := step("")
	res = unrecorded.Check(t.Context())
	if res.State != check.Attention || res.Summary != "not in kit-config" {
		t.Fatalf("not recorded = %+v", res)
	}
	if err := unrecorded.Apply(t.Context(), res); err != nil {
		t.Fatal(err)
	}
	if len(recorded) != 1 || recorded[0] != fakeHostKey {
		t.Errorf("recorded %q, want %q", recorded, fakeHostKey)
	}
	if res := unrecorded.Check(t.Context()); res.State != check.OK || res.Summary != "committed and pushed kit.toml" {
		t.Errorf("after recording = %+v, want what recording said", res)
	}
}
