package steps

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/runner"
)

// Admin says whether an administrator's password is at hand, for the steps
// whose apply needs one: Held is set once the command has settled it, and
// nil until then.
type Admin struct {
	Held func(ctx context.Context) bool
}

// ok reports whether the password is at hand.
func (a *Admin) ok(ctx context.Context) bool {
	return a != nil && a.Held != nil && a.Held(ctx)
}

// AdminWait says why a step that needs an administrator's password waits.
const AdminWait = "waiting for an administrator's password: kit apply at a terminal asks for it"

// The features that change the Mac's system: Touch ID for sudo, Remote
// Login and File Sharing.
const (
	FeatureTouchIDSudo = "touch-id-sudo"
	FeatureRemoteLogin = "remote-login"
	FeatureFileSharing = "file-sharing"
)

// SudoLocal is sudo's file of local settings, which macOS updates leave as
// it is, unlike /etc/pam.d/sudo.
const SudoLocal = "/etc/pam.d/sudo_local"

// touchIDLine is the line in sudo_local that lets a fingerprint answer sudo.
const touchIDLine = "auth       sufficient     pam_tid.so"

// TouchIDSudo checks sudo takes a fingerprint, as the file at file says;
// applying adds the line, through sudo.
func TouchIDSudo(run runner.Runner, admin *Admin, file string) engine.Step {
	read := func() (string, bool, error) {
		data, err := os.ReadFile(file)
		if errors.Is(err, fs.ErrNotExist) {
			return "", false, nil
		}
		if err != nil {
			return "", false, err
		}
		for line := range strings.Lines(string(data)) {
			if text, _, _ := strings.Cut(line, "#"); strings.Contains(text, "pam_tid.so") {
				return string(data), true, nil
			}
		}
		return string(data), false, nil
	}
	return engine.Step{
		Name: FeatureTouchIDSudo, Title: "Touch ID for sudo", Area: AreaDrift, Admin: true,
		Check: func(context.Context) check.Result {
			_, on, err := read()
			switch {
			case err != nil:
				return failed(err)
			case on:
				return check.Result{State: check.OK, Summary: "on"}
			}
			return check.Result{State: check.Attention, Summary: "off", Items: []check.Item{{ID: FeatureTouchIDSudo + ":" + FeatureTouchIDSudo, Name: "Touch ID for sudo", State: "missing", Detail: "sudo asks for the password, not a fingerprint", Action: ActionInstall}}}
		},
		Apply: func(ctx context.Context, _ check.Result) error {
			if !admin.ok(ctx) {
				return errors.New(AdminWait)
			}
			content, on, err := read()
			if err != nil || on {
				return err
			}
			if content == "" {
				content = "# sudo_local: local customizations for sudo\n"
			}
			if !strings.HasSuffix(content, "\n") {
				content += "\n"
			}
			_, err = run.Run(ctx, runner.Command{Name: "sudo", Args: []string{"-n", "tee", file}, Input: content + touchIDLine + "\n"})
			return err
		},
	}
}

// RemoteLogin checks the Mac takes SSH connections; applying turns its
// service on, through sudo.
func RemoteLogin(run runner.Runner, admin *Admin) engine.Step {
	return service(run, admin, FeatureRemoteLogin, "Remote Login", "com.openssh.sshd", "/System/Library/LaunchDaemons/ssh.plist")
}

// FileSharing checks the Mac shares files over SMB; applying turns its
// service on, through sudo.
func FileSharing(run runner.Runner, admin *Admin) engine.Step {
	return service(run, admin, FeatureFileSharing, "File Sharing", "com.apple.smbd", "/System/Library/LaunchDaemons/com.apple.smbd.plist")
}

// service is the step of a system service that's on unless launchd lists
// it disabled: label is its job's, and plist where launchd finds it.
func service(run runner.Runner, admin *Admin, name, title, label, plist string) engine.Step {
	return engine.Step{
		Name: name, Title: title, Area: AreaDrift, Admin: true,
		Check: func(ctx context.Context) check.Result {
			out, err := output(ctx, run, "launchctl", "print-disabled", "system")
			if err != nil {
				return failed(err)
			}
			for line := range strings.Lines(out) {
				job, state, ok := strings.Cut(strings.TrimSpace(line), " => ")
				if ok && strings.Trim(job, `"`) == label && (state == "enabled" || state == "false") {
					return check.Result{State: check.OK, Summary: "on"}
				}
			}
			return check.Result{State: check.Attention, Summary: "off", Items: []check.Item{{ID: name + ":" + name, Name: title, State: "missing", Detail: "turned off", Action: ActionInstall}}}
		},
		Apply: func(ctx context.Context, _ check.Result) error {
			if !admin.ok(ctx) {
				return errors.New(AdminWait)
			}
			if _, err := run.Run(ctx, runner.Command{Name: "sudo", Args: []string{"-n", "launchctl", "enable", "system/" + label}}); err != nil {
				return fmt.Errorf("turn %s on: %w", title, err)
			}
			// Already loaded is no matter: enabled, it's started as needed.
			_, _ = run.Run(ctx, runner.Command{Name: "sudo", Args: []string{"-n", "launchctl", "bootstrap", "system", plist}})
			return nil
		},
	}
}
