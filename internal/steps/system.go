package steps

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/restore/oldmac"
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
			return kind.AdminAgain(ctx, func() error {
				_, err := run.Run(ctx, runner.Command{Name: "sudo", Args: []string{"-n", "tee", file}, Input: content + touchIDLine + "\n"})
				return err
			})
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
			err := kind.AdminAgain(ctx, func() error {
				_, err := run.Run(ctx, runner.Command{Name: "sudo", Args: []string{"-n", "launchctl", "enable", "system/" + label}})
				return err
			})
			if err != nil {
				return fmt.Errorf("turn %s on: %w", title, err)
			}
			// Already loaded is no matter: enabled, it's started as needed.
			_ = kind.AdminAgain(ctx, func() error {
				_, err := run.Run(ctx, runner.Command{Name: "sudo", Args: []string{"-n", "launchctl", "bootstrap", "system", plist}})
				return err
			})
			return nil
		},
	}
}

// HostKeyName names the step recording this Mac's SSH host key in
// kit-config.
const HostKeyName = "host-key"

// SSHAddress is where this Mac's Remote Login answers.
const SSHAddress = "127.0.0.1:22"

// HostKeying is the step recording this Mac's SSH host key in kit-config,
// for a Mac restored from it to know it by.
type HostKeying struct {
	// RemoteLogin is whether the Mac's Remote Login feature is on: off,
	// there's no host key to record.
	RemoteLogin bool
	// Dir is where the Mac keeps its host keys: oldmac.SSHDir. Address is
	// where its Remote Login answers: SSHAddress.
	Dir, Address string
	// Recorded is the host key kit-config records for this Mac: "" for none.
	Recorded string
	// Record records key as this Mac's in kit-config, committing and
	// pushing it, and says how that went.
	Record func(ctx context.Context, key string) (check.Result, error)
}

// wakeFor is how long Remote Login has to answer, making the host keys.
const wakeFor = 30 * time.Second

// HostKey checks kit-config records this Mac's SSH host key, the one its
// Remote Login presents, as oldmac reads it, while the Remote Login feature
// is on; applying records it, as a Mac set up again gets a new key, and
// the new one is right. With no key yet, applying has Remote Login answer
// once, which makes them (wake), then records it.
func HostKey(h HostKeying) engine.Step {
	// done is what recording did, which the check after it says.
	var done *check.Result
	step := engine.Step{Name: HostKeyName, Title: "SSH host key", Area: AreaConfig}
	if !h.RemoteLogin {
		step.Check = func(context.Context) check.Result {
			return check.Result{State: check.OK, Summary: "Remote Login is off: no host key to record"}
		}
		return step
	}
	step.Check = func(context.Context) check.Result {
		if done != nil {
			return *done
		}
		own, err := oldmac.HostKey(h.Dir)
		if err != nil {
			return failed(err)
		}
		// One recorded that isn't a key is never this Mac's.
		recorded, _ := oldmac.PublicKey(h.Recorded)
		switch {
		case own == "":
			return problem(HostKeyName, "none yet", [3]string{"none", "no SSH host key yet", "macOS makes it at Remote Login's first connection: kit apply connects once, then records it"})
		case h.Recorded == "":
			return problem(HostKeyName, "not in kit-config", [3]string{"unrecorded", "SSH host key not in kit-config", "kit apply records it"})
		case recorded != own:
			return problem(HostKeyName, "not kit-config's", [3]string{"differs", "SSH host key not the one kit-config records", "kit apply records this Mac's: a Mac set up again has a new one"})
		}
		return check.Result{State: check.OK, Summary: "in kit-config"}
	}
	step.Apply = func(ctx context.Context, _ check.Result) error {
		own, err := h.made(ctx)
		if err != nil {
			return err
		}
		res, err := h.Record(ctx, own)
		if err != nil {
			return err
		}
		done = &res
		return nil
	}
	return step
}

// made is this Mac's host key, Remote Login woken to make it when there's
// none yet.
func (h HostKeying) made(ctx context.Context) (string, error) {
	own, err := oldmac.HostKey(h.Dir)
	if err != nil || own != "" {
		return own, err
	}
	if err := wake(ctx, h.Address); err != nil {
		return "", engine.NotYet(fmt.Sprintf("this Mac has no SSH host key yet, and Remote Login didn't answer at %s to make one: %v; the next kit apply tries again", h.Address, err))
	}
	if own, err = oldmac.HostKey(h.Dir); err == nil && own == "" {
		err = engine.NotYet("this Mac has no SSH host key, even once Remote Login answered; the next kit apply tries again")
	}
	return own, err
}

// wake has Remote Login answer once at addr, reading its banner, logging
// into nothing. ssh.plist is inetd-style: launchd runs sshd-keygen-wrapper
// for each connection, which makes any host key missing before it starts
// sshd, so once sshd says its banner the keys are there.
func wake(ctx context.Context, addr string) error {
	ctx, cancel := context.WithTimeout(ctx, wakeFor)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetReadDeadline(deadline)
	}
	banner, err := bufio.NewReader(conn).ReadString('\n')
	switch {
	case err != nil:
		return fmt.Errorf("no banner: %w", err)
	case !strings.HasPrefix(banner, "SSH-"):
		return fmt.Errorf("it answered %q, not as SSH", strings.TrimSpace(banner))
	}
	return nil
}
