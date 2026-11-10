package steps_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"slices"
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

// sshd stands in for Remote Login at a connection, as launchd starts it:
// sshd-keygen-wrapper writes the host key, key, at path, then sshd says
// banner. It returns where it answers.
func sshd(t *testing.T, path, key, banner string) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		if key != "" {
			_ = os.WriteFile(path, []byte(key+" root@laptop\n"), 0o644)
		}
		_, _ = conn.Write([]byte(banner))
	}()
	return l.Addr().String()
}

// The host key's step is ok while kit-config records this Mac's key, its
// type and key whatever comment either has; else applying hands the key,
// as its file has it but the comment, to be recorded, and the check after
// says how recording went. With no key yet, applying has Remote Login
// answer once, which makes it, then records it.
func TestHostKey(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "ssh_host_ed25519_key.pub")
	var recorded []string
	step := func(in, address string) engine.Step {
		return steps.HostKey(steps.HostKeying{RemoteLogin: true, Dir: dir, Address: address, Recorded: in, Record: func(_ context.Context, key string) (check.Result, error) {
			recorded = append(recorded, key)
			return check.Result{State: check.OK, Summary: "committed and pushed kit.toml"}, nil
		}})
	}

	woken := step("", sshd(t, key, fakeHostKey, "SSH-2.0-OpenSSH_9.9\r\n"))
	res := woken.Check(t.Context())
	if res.State != check.Attention || res.Summary != "none yet" {
		t.Fatalf("no key yet = %+v", res)
	}
	if err := woken.Apply(t.Context(), res); err != nil {
		t.Fatal(err)
	}
	if res := step(fakeHostKey+" a comment", "").Check(t.Context()); res.State != check.OK {
		t.Errorf("recorded = %+v, want ok", res)
	}
	unrecorded := step("", "")
	res = unrecorded.Check(t.Context())
	if res.State != check.Attention || res.Summary != "not in kit-config" {
		t.Fatalf("not recorded = %+v", res)
	}
	if err := unrecorded.Apply(t.Context(), res); err != nil {
		t.Fatal(err)
	}
	if want := []string{fakeHostKey, fakeHostKey}; !slices.Equal(recorded, want) {
		t.Errorf("recorded %q, want %q: the key woken, then the one there", recorded, want)
	}
	if res := unrecorded.Check(t.Context()); res.State != check.OK || res.Summary != "committed and pushed kit.toml" {
		t.Errorf("after recording = %+v, want what recording said", res)
	}
}

// With no key yet, a Remote Login that doesn't answer, answers as
// something else, or answers without making one, leaves the step for the
// next apply, saying so, nothing recorded.
func TestHostKeyNotMadeYet(t *testing.T) {
	for _, tt := range []struct {
		name, key, banner, want string
	}{
		{"not answering", "", "", "Remote Login didn't answer at "},
		{"not SSH", fakeHostKey, "220 ready\r\n", `it answered "220 ready", not as SSH`},
		{"no key made", "", "SSH-2.0-OpenSSH_9.9\r\n", "this Mac has no SSH host key, even once Remote Login answered"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			address := "127.0.0.1:1"
			if tt.banner != "" {
				address = sshd(t, filepath.Join(dir, "ssh_host_ed25519_key.pub"), tt.key, tt.banner)
			}
			step := steps.HostKey(steps.HostKeying{RemoteLogin: true, Dir: dir, Address: address, Record: func(context.Context, string) (check.Result, error) {
				t.Error("recorded a key")
				return check.Result{}, nil
			}})
			if err := step.Apply(t.Context(), step.Check(t.Context())); !engine.IsNotYet(err) || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Apply() = %v; want not yet, saying %q", err, tt.want)
			}
		})
	}
}

// With Remote Login off, there's no host key to record: ok, nothing to do.
func TestHostKeyWithRemoteLoginOff(t *testing.T) {
	step := steps.HostKey(steps.HostKeying{Dir: t.TempDir()})
	if res := step.Check(t.Context()); res.State != check.OK || res.Summary != "Remote Login is off: no host key to record" {
		t.Errorf("Remote Login off = %+v", res)
	}
}
