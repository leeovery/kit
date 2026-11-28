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
	"github.com/leeovery/kit/internal/prefs"
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
// Login, File Sharing and Screen Sharing.
const (
	FeatureTouchIDSudo   = "touch-id-sudo"
	FeatureRemoteLogin   = "remote-login"
	FeatureFileSharing   = "file-sharing"
	FeatureScreenSharing = "screen-sharing"
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

// The Screen Sharing that viewers can take control through: its service,
// the agent macOS grants control to, and where macOS keeps the grants.
const (
	screenSharingLabel = "com.apple.screensharing"
	screenSharingAgent = "com.apple.screensharing.agent"
	privacyDatabase    = "/Library/Application Support/com.apple.TCC/TCC.db"
)

// SharingSettings is System Settings, opened at Sharing.
const SharingSettings = "x-apple.systempreferences:com.apple.Sharing-Settings.extension"

// controlGrants are what macOS grants Screen Sharing's agent for viewers to
// take control: seeing the screen, and sending it keys and clicks.
var controlGrants = []string{"kTCCServicePostEvent", "kTCCServiceScreenCapture"}

// allowed is a grant's auth_value when it's allowed.
const allowed = "2"

// The words of Screen Sharing's to-dos.
const (
	sharingOpen  = "System Settings is open at Sharing"
	sharingWatch = "viewers can take control"
	sharingWhy   = "macOS lets viewers take control only once Screen Sharing is turned on in System Settings, which kit can't do for you."
)

// ScreenSharing checks the Mac shares its screen, viewers able to take
// control: on only when launchd lists its service enabled and macOS grants
// its agent both of controlGrants. Only System Settings grants them, as
// turning Screen Sharing on there does, so the step has no apply: what's
// wrong is the person's to do, and its card opens System Settings at
// Sharing.
func ScreenSharing(run runner.Runner) engine.Step {
	return engine.Step{
		Name: FeatureScreenSharing, Title: "Screen Sharing", Area: AreaDrift,
		Choice: &engine.Choice{Open: func(ctx context.Context, _ check.Item) {
			_, _ = run.Run(ctx, runner.Command{Name: "open", Args: []string{SharingSettings}})
		}},
		Check: func(ctx context.Context) check.Result {
			on, err := serviceOn(ctx, run, screenSharingLabel)
			if err != nil {
				return failed(err)
			}
			if !on {
				return yours(FeatureScreenSharing, "off", "off", "System Settings › General › Sharing: turn on Screen Sharing",
					check.ToDo{Title: "Turn on Screen Sharing", Why: sharingWhy, Steps: []string{sharingOpen, "Turn on Screen Sharing"}, Watch: sharingWatch})
			}
			granted, err := controlGranted(ctx, run)
			switch {
			case err != nil:
				return failed(err)
			case !granted:
				return yours(FeatureScreenSharing, "on, but viewers can only watch", "watch-only", "System Settings › General › Sharing: turn Screen Sharing off, then on again",
					check.ToDo{Title: "Turn Screen Sharing off and on again", Why: "Screen Sharing is on, but viewers can only watch: " + sharingWhy, Steps: []string{sharingOpen, "Turn Screen Sharing off, then on again"}, Watch: sharingWatch})
			}
			return check.Result{State: check.OK, Summary: "on"}
		},
	}
}

// controlGranted reports whether macOS grants Screen Sharing's agent each
// of controlGrants, every row of each allowed: none, or one denied, isn't.
// The database holding them can't be read without Full Disk Access.
func controlGranted(ctx context.Context, run runner.Runner) (bool, error) {
	// The system's sqlite3, by path: Homebrew's can come first on kit's PATH.
	out, err := output(ctx, run, "/usr/bin/sqlite3", "-readonly", privacyDatabase,
		"select service, auth_value from access where client='"+screenSharingAgent+"'")
	if exit := (*runner.ExitError)(nil); errors.As(err, &exit) && strings.Contains(exit.FullStderr, "unable to open database") {
		return false, fmt.Errorf("can't tell whether viewers can take control: %w", prefs.ErrNoAccess)
	}
	if err != nil {
		return false, fmt.Errorf("read what macOS grants Screen Sharing: %w", err)
	}
	granted := map[string]bool{}
	for line := range strings.Lines(out) {
		service, value, _ := strings.Cut(strings.TrimSpace(line), "|")
		ok, seen := granted[service]
		granted[service] = (ok || !seen) && value == allowed
	}
	for _, g := range controlGrants {
		if !granted[g] {
			return false, nil
		}
	}
	return true, nil
}

// service is the step of a system service that's on only when launchd lists
// its job enabled (serviceOn); applying turns it on, through sudo. label is
// its job's, and plist where launchd finds it.
func service(run runner.Runner, admin *Admin, name, title, label, plist string) engine.Step {
	return engine.Step{
		Name: name, Title: title, Area: AreaDrift, Admin: true,
		Check: func(ctx context.Context) check.Result {
			on, err := serviceOn(ctx, run, label)
			switch {
			case err != nil:
				return failed(err)
			case on:
				return check.Result{State: check.OK, Summary: "on"}
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

// serviceOn reports whether launchd lists the job of label enabled: one it
// doesn't list is off, as its plist says Disabled.
func serviceOn(ctx context.Context, run runner.Runner, label string) (bool, error) {
	out, err := output(ctx, run, "launchctl", "print-disabled", "system")
	if err != nil {
		return false, err
	}
	for line := range strings.Lines(out) {
		job, state, ok := strings.Cut(strings.TrimSpace(line), " => ")
		if ok && strings.Trim(job, `"`) == label && (state == "enabled" || state == "false") {
			return true, nil
		}
	}
	return false, nil
}

// HostKeyName names the step recording this Mac's SSH host key and local
// hostname in kit-config.
const HostKeyName = "host-key"

// SSHAddress is where this Mac's Remote Login answers.
const SSHAddress = "127.0.0.1:22"

// HostKeying is the step recording this Mac's SSH host key and local
// hostname in kit-config, for a Mac restored from it to reach it by, and
// know it by.
type HostKeying struct {
	// RemoteLogin is whether the Mac's Remote Login feature is on: off,
	// there's nothing to record.
	RemoteLogin bool
	// Dir is where the Mac keeps its host keys: oldmac.SSHDir. Address is
	// where its Remote Login answers: SSHAddress.
	Dir, Address string
	// Run runs scutil, which says the Mac's local hostname.
	Run runner.Runner
	// Recorded is the host key kit-config records for this Mac, and
	// RecordedName its local hostname: "" for none.
	Recorded, RecordedName string
	// Record records key as this Mac's host key in kit-config, and
	// localHostName as its local hostname, committing and pushing them, and
	// says how that went.
	Record func(ctx context.Context, key, localHostName string) (check.Result, error)
}

// wakeFor is how long Remote Login has to answer, making the host keys.
const wakeFor = 30 * time.Second

// HostKey checks kit-config records this Mac's SSH host key, the one its
// Remote Login presents, as oldmac reads it, and its local hostname, as
// macOS says it, while the Remote Login feature is on; applying records
// them, as a Mac set up again gets a new key, and one renamed a new name,
// and the new ones are right. With no key yet, applying has Remote Login
// answer once, which makes them (wake), then records it.
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
	step.Check = func(ctx context.Context) check.Result {
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
		name, err := localHostName(ctx, h.Run)
		switch {
		case err != nil:
			return failed(err)
		case h.RecordedName == "":
			return problem(HostKeyName, "local hostname not in kit-config", [3]string{"unnamed", "local hostname not in kit-config", "kit apply records it"})
		case h.RecordedName != name:
			return problem(HostKeyName, "local hostname not kit-config's", [3]string{"renamed", "local hostname not the one kit-config records", "kit apply records this Mac's: it's been renamed"})
		}
		return check.Result{State: check.OK, Summary: "in kit-config"}
	}
	step.Apply = func(ctx context.Context, _ check.Result) error {
		own, err := h.made(ctx)
		if err != nil {
			return err
		}
		name, err := localHostName(ctx, h.Run)
		if err != nil {
			return err
		}
		res, err := h.Record(ctx, own, name)
		if err != nil {
			return err
		}
		done = &res
		return nil
	}
	return step
}

// localHostName is the name this Mac answers to on the local network,
// without .local: macOS's LocalHostName, as scutil says it, one a Mac
// restored from this one reaches it by (oldmac.IsLocalHostName).
func localHostName(ctx context.Context, run runner.Runner) (string, error) {
	out, err := output(ctx, run, "scutil", "--get", "LocalHostName")
	if err != nil {
		return "", fmt.Errorf("read this Mac's local hostname: %w", err)
	}
	switch name := strings.TrimSpace(out); {
	case name == "":
		return "", errors.New("this Mac has no local hostname: set one in System Settings › General › Sharing")
	case !oldmac.IsLocalHostName(name):
		return "", fmt.Errorf("this Mac's local hostname, %q, isn't one another Mac can reach it by: set one of letters, digits and - in System Settings › General › Sharing", name)
	default:
		return name, nil
	}
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
