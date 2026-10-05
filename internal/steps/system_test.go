package steps_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/check"
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
