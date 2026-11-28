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
	"github.com/leeovery/kit/internal/runner"
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

func TestSystemServices(t *testing.T) {
	for _, tc := range []struct {
		title, label, plist string
		step                func(runner.Runner, *steps.Admin) engine.Step
	}{
		{"Remote Login", "com.openssh.sshd", "/System/Library/LaunchDaemons/ssh.plist", steps.RemoteLogin},
		{"File Sharing", "com.apple.smbd", "/System/Library/LaunchDaemons/com.apple.smbd.plist", steps.FileSharing},
		{"Screen Sharing", "com.apple.screensharing", "/System/Library/LaunchDaemons/com.apple.screensharing.plist", steps.ScreenSharing},
	} {
		t.Run(tc.title, func(t *testing.T) {
			held := &steps.Admin{Held: func(context.Context) bool { return true }}
			for _, c := range []struct {
				listed string
				want   check.State
			}{
				{"\t\t\"" + tc.label + "\" => enabled\n", check.OK},
				{"\t\t\"" + tc.label + "\" => false\n", check.OK},
				{"\t\t\"" + tc.label + "\" => disabled\n", check.Attention},
				{"\t\t\"com.example.other\" => enabled\n", check.Attention},
			} {
				fake := runnertest.New(t)
				fake.On("launchctl", "print-disabled", "system").Prints("disabled services = {\n" + c.listed + "}\n")
				step := tc.step(fake, held)
				if res := step.Check(t.Context()); res.State != c.want || step.Title != tc.title || !step.Admin {
					t.Errorf("listing %q = %+v", c.listed, res)
				}
			}

			fake := runnertest.New(t)
			fake.On("launchctl", "print-disabled", "system").Exits(1)
			if res := tc.step(fake, held).Check(t.Context()); res.State != check.Failed {
				t.Errorf("launchctl failing = %+v", res)
			}

			fake.On("launchctl", "print-disabled", "system").Prints("disabled services = {\n}\n")
			waiting := tc.step(fake, &steps.Admin{})
			res := waiting.Check(t.Context())
			if res.State != check.Attention || res.Items[0].Action != steps.ActionInstall {
				t.Fatalf("off = %+v", res)
			}
			if err := waiting.Apply(t.Context(), res); err == nil || err.Error() != steps.AdminWait {
				t.Errorf("Apply() without the password = %v", err)
			}

			fake.On("sudo", "-n", "launchctl", "enable", "system/"+tc.label)
			fake.On("sudo", "-n", "launchctl", "bootstrap", "system", tc.plist).Exits(37)
			if err := tc.step(fake, held).Apply(t.Context(), res); err != nil {
				t.Errorf("Apply() = %v; a service loaded already is no failure", err)
			}
			calls := fake.Calls()
			if want := []string{"sudo -n launchctl enable system/" + tc.label, "sudo -n launchctl bootstrap system " + tc.plist}; !slices.Equal(calls[len(calls)-2:], want) {
				t.Errorf("ran\n%s", strings.Join(calls, "\n"))
			}
		})
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

// localName is the test Mac's local hostname, as scutil says it.
const localName = "Someones-MacBook-Pro"

// named is a runner whose scutil says the Mac's local hostname is
// localName.
func named(t *testing.T) *runnertest.Fake {
	fake := runnertest.New(t)
	fake.On("scutil", "--get", "LocalHostName").Prints(localName + "\n")
	return fake
}

// recording is what the host key's step recorded: the key and local
// hostname of each time.
type recording [][2]string

func (r *recording) record(_ context.Context, key, name string) (check.Result, error) {
	*r = append(*r, [2]string{key, name})
	return check.Result{State: check.OK, Summary: "committed and pushed kit.toml"}, nil
}

// The host key's step is ok while kit-config records this Mac's key, its
// type and key whatever comment either has, and its local hostname, as
// scutil says it; else applying hands both, the key as its file has it but
// the comment, to be recorded, and the check after says how recording
// went. With no key yet, applying has Remote Login answer once, which makes
// it, then records it.
func TestHostKey(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "ssh_host_ed25519_key.pub")
	var recorded recording
	step := func(in, name, address string) engine.Step {
		return steps.HostKey(steps.HostKeying{RemoteLogin: true, Dir: dir, Address: address, Run: named(t), Recorded: in, RecordedName: name, Record: recorded.record})
	}

	woken := step("", "", sshd(t, key, fakeHostKey, "SSH-2.0-OpenSSH_9.9\r\n"))
	res := woken.Check(t.Context())
	if res.State != check.Attention || res.Summary != "none yet" {
		t.Fatalf("no key yet = %+v", res)
	}
	if err := woken.Apply(t.Context(), res); err != nil {
		t.Fatal(err)
	}
	if res := step(fakeHostKey+" a comment", localName, "").Check(t.Context()); res.State != check.OK {
		t.Errorf("recorded = %+v, want ok", res)
	}
	unrecorded := step("", localName, "")
	res = unrecorded.Check(t.Context())
	if res.State != check.Attention || res.Summary != "not in kit-config" {
		t.Fatalf("not recorded = %+v", res)
	}
	if err := unrecorded.Apply(t.Context(), res); err != nil {
		t.Fatal(err)
	}
	if want := (recording{{fakeHostKey, localName}, {fakeHostKey, localName}}); !slices.Equal(recorded, want) {
		t.Errorf("recorded %q, want %q: the key woken, then the one there, each with the local hostname", recorded, want)
	}
	if res := unrecorded.Check(t.Context()); res.State != check.OK || res.Summary != "committed and pushed kit.toml" {
		t.Errorf("after recording = %+v, want what recording said", res)
	}
}

// The local hostname's recorded as macOS says it now: kit-config without
// one, as on a Mac whose key an older kit recorded, or with another, as on
// a Mac renamed since, needs it recorded, and applying records it, the key
// with it; one scutil can't say, or one another Mac can't reach this one
// by, as one set by hand, fails the check, saying why and how to set one,
// and leaves nothing recorded.
func TestHostKeyRecordsTheLocalHostname(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ssh_host_ed25519_key.pub"), []byte(fakeHostKey+" root@laptop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, recorded, summary, item string
	}{
		{"none recorded", "", "local hostname not in kit-config", "local hostname not in kit-config (kit apply records it)"},
		{"renamed since", "Someones-Old-Name", "local hostname not kit-config's", "local hostname not the one kit-config records (kit apply records this Mac's: it's been renamed)"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var recorded recording
			step := steps.HostKey(steps.HostKeying{RemoteLogin: true, Dir: dir, Run: named(t), Recorded: fakeHostKey, RecordedName: tt.recorded, Record: recorded.record})
			res := step.Check(t.Context())
			if res.State != check.Attention || res.Summary != tt.summary || len(res.Items) != 1 || res.Items[0].Name+" ("+res.Items[0].Detail+")" != tt.item {
				t.Fatalf("Check() = %+v; want %q, %q", res, tt.summary, tt.item)
			}
			if err := step.Apply(t.Context(), res); err != nil {
				t.Fatal(err)
			}
			if want := (recording{{fakeHostKey, localName}}); !slices.Equal(recorded, want) {
				t.Errorf("recorded %q, want %q", recorded, want)
			}
		})
	}
	for _, tt := range []struct{ name, says, want string }{
		{"scutil says none", "\n", "this Mac has no local hostname: set one in System Settings › General › Sharing"},
		{"scutil says one set by hand", "Someones.Mac\n", `this Mac's local hostname, "Someones.Mac", isn't one another Mac can reach it by: set one of letters, digits and - in System Settings › General › Sharing`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake := runnertest.New(t)
			fake.On("scutil", "--get", "LocalHostName").Prints(tt.says)
			step := steps.HostKey(steps.HostKeying{RemoteLogin: true, Dir: dir, Run: fake, Recorded: fakeHostKey, Record: func(context.Context, string, string) (check.Result, error) {
				t.Error("recorded a key")
				return check.Result{}, nil
			}})
			res := step.Check(t.Context())
			if res.State != check.Failed || res.Reason != tt.want {
				t.Errorf("Check() = %+v; want failed, %q", res, tt.want)
			}
			if err := step.Apply(t.Context(), res); err == nil || err.Error() != tt.want {
				t.Errorf("Apply() = %v; want %q", err, tt.want)
			}
		})
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
			step := steps.HostKey(steps.HostKeying{RemoteLogin: true, Dir: dir, Address: address, Run: named(t), Record: func(context.Context, string, string) (check.Result, error) {
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
