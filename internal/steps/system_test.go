package steps_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
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
	} {
		t.Run(tc.title, func(t *testing.T) {
			held := &steps.Admin{Held: func(context.Context) bool { return true }}
			if step := tc.step(nil, held); step.Title != tc.title || !step.Admin {
				t.Errorf("step = %q, admin %t; want %q, needing an administrator", step.Title, step.Admin, tc.title)
			}
			line := func(label, state string) string { return "\t\t\"" + label + "\" => " + state + "\n" }
			for _, c := range []struct {
				listed string
				want   check.State
			}{
				{line(tc.label, "enabled"), check.OK},
				{line(tc.label, "false"), check.OK},
				{line(tc.label, "disabled"), check.Attention},
				{line("com.example.other", "enabled"), check.Attention},
				{line("com.example.other", "enabled") + line(tc.label, "enabled") + line("com.example.last", "disabled"), check.OK},
				{line("com.example.other", "enabled") + line(tc.label, "disabled") + line("com.example.last", "enabled"), check.Attention},
			} {
				fake := runnertest.New(t)
				fake.On("launchctl", "print-disabled", "system").Prints("disabled services = {\n" + c.listed + "}\n")
				if res := tc.step(fake, held).Check(t.Context()); res.State != c.want {
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

// The command reading what macOS grants Screen Sharing's agent.
var readGrants = []string{"-readonly", "/Library/Application Support/com.apple.TCC/TCC.db", "select service, auth_value from access where client='com.apple.screensharing.agent'"}

// screenSharingRunner is a runner whose launchd lists Screen Sharing's
// service as listed says, and whose privacy database holds grants.
func screenSharingRunner(t *testing.T, listed, grants string) *runnertest.Fake {
	fake := runnertest.New(t)
	fake.On("launchctl", "print-disabled", "system").Prints("disabled services = {\n\t\"com.apple.screensharing\" => " + listed + "\n}\n")
	fake.On("/usr/bin/sqlite3", readGrants...).Prints(grants)
	return fake
}

// Screen Sharing is on only with its service enabled and both of control's
// grants allowed: off, or either grant missing or denied, is the person's
// to do, kit watching for viewers to take control. The step needs no
// password, as it has no apply: only System Settings grants control.
func TestScreenSharing(t *testing.T) {
	const both = "kTCCServicePostEvent|2\nkTCCServiceScreenCapture|2\n"
	for _, tt := range []struct {
		name, listed, grants string
		want                 check.State
		summary, item, title string
	}{
		{"on, control granted", "enabled", both, check.OK, "on", "", ""},
		{"on, listed false", "false", both, check.OK, "on", "", ""},
		{"posting events denied", "enabled", "kTCCServicePostEvent|0\nkTCCServiceScreenCapture|2\n", check.Attention, "on, but viewers can only watch", "screen-sharing:watch-only", "Turn Screen Sharing off and on again"},
		{"screen capture denied", "enabled", "kTCCServicePostEvent|2\nkTCCServiceScreenCapture|0\n", check.Attention, "on, but viewers can only watch", "screen-sharing:watch-only", "Turn Screen Sharing off and on again"},
		{"one of two rows denied", "enabled", both + "kTCCServicePostEvent|0\n", check.Attention, "on, but viewers can only watch", "screen-sharing:watch-only", "Turn Screen Sharing off and on again"},
		{"one grant missing", "enabled", "kTCCServiceScreenCapture|2\n", check.Attention, "on, but viewers can only watch", "screen-sharing:watch-only", "Turn Screen Sharing off and on again"},
		{"no grants", "enabled", "", check.Attention, "on, but viewers can only watch", "screen-sharing:watch-only", "Turn Screen Sharing off and on again"},
		{"service disabled", "disabled", both, check.Attention, "off", "screen-sharing:off", "Turn on Screen Sharing"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			step := steps.ScreenSharing(screenSharingRunner(t, tt.listed, tt.grants))
			if step.Admin || step.Apply != nil {
				t.Errorf("admin %t, apply %t; want neither: only System Settings turns it on", step.Admin, step.Apply != nil)
			}
			res := step.Check(t.Context())
			if res.State != tt.want || res.Summary != tt.summary {
				t.Fatalf("Check() = %+v; want %s, %q", res, tt.want, tt.summary)
			}
			if tt.item == "" {
				return
			}
			if !res.ToDo() || len(res.Items) != 1 || res.Items[0].ID != tt.item || res.Items[0].ToDo == nil {
				t.Fatalf("items = %+v; want the to-do %s", res.Items, tt.item)
			}
			if todo := res.Items[0].ToDo; todo.Title != tt.title || todo.Watch != "viewers can take control" || !strings.Contains(todo.Why, "only once Screen Sharing is turned on in System Settings") {
				t.Errorf("to-do = %+v; want %q, watching for control", todo, tt.title)
			}
		})
	}
}

// A privacy database kit can't read, as without Full Disk Access, leaves
// Screen Sharing never ok: it can't tell, and says why. Disabled, it's off
// without reading it.
func TestScreenSharingUnreadable(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("launchctl", "print-disabled", "system").Prints("disabled services = {\n\t\"com.apple.screensharing\" => enabled\n}\n")
	fake.On("/usr/bin/sqlite3", readGrants...).PrintsToStderr(`Error: unable to open database "/Library/Application Support/com.apple.TCC/TCC.db": authorization denied` + "\n").Exits(1)
	res := steps.ScreenSharing(fake).Check(t.Context())
	if res.State != check.Failed || !strings.HasPrefix(res.Reason, "can't tell whether viewers can take control: no Full Disk Access") {
		t.Errorf("unreadable = %+v; want it failed, saying it can't tell for want of Full Disk Access", res)
	}

	fake.On("/usr/bin/sqlite3", readGrants...).PrintsToStderr("Error: in prepare, no such table: access\n").Exits(1)
	if res := steps.ScreenSharing(fake).Check(t.Context()); res.State != check.Failed || !strings.Contains(res.Reason, "no such table") || strings.Contains(res.Reason, "Full Disk Access") {
		t.Errorf("another failure = %+v; want it failed, saying what sqlite3 said", res)
	}

	off := runnertest.New(t)
	off.On("launchctl", "print-disabled", "system").Prints("disabled services = {\n}\n")
	if res := steps.ScreenSharing(off).Check(t.Context()); res.State != check.Attention || res.Summary != "off" {
		t.Errorf("disabled = %+v; want off", res)
	}
	if calls := off.Calls(); len(calls) != 1 {
		t.Errorf("disabled ran\n%s\nwant launchctl alone", strings.Join(calls, "\n"))
	}
}

// Applied at a terminal, Screen Sharing's to-do goes up at the run's end,
// opening System Settings at Sharing, and is done once kit sees viewers can
// take control: never through launchctl.
func TestScreenSharingToDoOpensSharing(t *testing.T) {
	defer steps.SetManualPoll(5 * time.Millisecond)()
	c := newCard(t)
	var mu sync.Mutex
	grants := "kTCCServicePostEvent|0\nkTCCServiceScreenCapture|2\n"
	c.fake.On("launchctl", "print-disabled", "system").Prints("disabled services = {\n\t\"com.apple.screensharing\" => enabled\n}\n")
	c.fake.On("/usr/bin/sqlite3", readGrants...).Filters(func(string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		return grants, nil
	})
	c.fake.On("open", steps.SharingSettings)
	done := make(chan engine.Report, 1)
	go func() {
		done <- applied(t, c, atTheEnd(steps.Manual(c.fake, "/home/someone", c.state, config.List{}, c.raise), steps.ScreenSharing(c.fake)))
	}()
	up := c.cards(t, 1)[0]
	if up.Title != "Turn Screen Sharing off and on again" || !up.Tells || !slices.Equal(up.Steps, []string{"System Settings is open at Sharing", "Turn Screen Sharing off, then on again"}) {
		t.Fatalf("card = %+v; want the toggle, watched", up)
	}
	mu.Lock()
	grants = "kTCCServicePostEvent|2\nkTCCServiceScreenCapture|2\n"
	mu.Unlock()
	if report := <-done; report.Results[steps.FeatureScreenSharing].State != check.OK {
		t.Errorf("screen-sharing stands %+v; want it ok once control's granted", report.Results[steps.FeatureScreenSharing])
	}
	var opened int
	for _, call := range c.fake.Calls() {
		switch {
		case call == "open "+steps.SharingSettings:
			opened++
		case strings.HasPrefix(call, "sudo"), strings.HasPrefix(call, "launchctl") && call != "launchctl print-disabled system":
			t.Errorf("ran %s; want no change through launchctl", call)
		}
	}
	if opened != 1 {
		t.Errorf("opened System Settings at Sharing %d times; want once", opened)
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
