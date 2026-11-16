// Package oldmac reaches the Mac a new one started from, over SSH, to
// restore from it: at the local hostname kit-config records for it, as kit
// apply on it records it, presenting the host key kit-config records, as a
// Mac's name is only its own say. It checks the Mac can be copied from, and
// finds what its backups leave out of each listed folder, for the restore
// to leave out too.
package oldmac

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/apps"
	"github.com/leeovery/kit/internal/restore"
	"github.com/leeovery/kit/internal/runner"
)

// RemoteRsync is the old Mac's rsync 3, Homebrew's, which kit-config's
// shared formulae install on every Mac: by name, SSH would find macOS's own,
// openrsync.
const RemoteRsync = "/opt/homebrew/bin/rsync"

// probeTimeout is how long a Mac's given to answer the probe.
const probeTimeout = 30 * time.Second

// ErrNoAnswer is in the error of an old Mac not answering over SSH as
// itself: off, asleep, away, refusing this Mac's key, presenting another
// host key than kit-config's, not offering Remote Login, or out of reach
// till macOS lets the terminal find devices on local networks.
var ErrNoAnswer = errors.New("doesn't answer over SSH")

// ErrUnrecorded is in the error of an old Mac kit-config doesn't record as
// it is now: its host key or local hostname not recorded, or nothing
// answering at its local hostname, as when it's been renamed since. kit
// apply on it records them.
var ErrUnrecorded = errors.New("can't be restored from")

// Finder reaches the old Mac over SSH, run through Run, the host key it
// must present kept in kit's state, StateDir.
type Finder struct {
	Run      runner.Runner
	StateDir string
	// HostKey is the host key the old Mac must present, kit-config's, a
	// public key as a known-hosts line or a .pub file has it: a Mac
	// presenting another isn't it, whatever it says its name is.
	// LocalHostName is the name it answers to on the local network,
	// kit-config's, without .local. Without either, it isn't reached
	// (ErrUnrecorded).
	HostKey, LocalHostName string
	// Terminal is the app kit's running in, as macOS names it asking
	// whether it can find devices on local networks (apps.RunningIn):
	// apps.Unnamed when "".
	Terminal string
	// OverSSH is kit running in an SSH session, where macOS never asks
	// that, so a Mac ssh has no route to is said plainly not to answer.
	OverSSH bool
}

// Mac is the old Mac, answering over SSH.
type Mac struct {
	// Name is its name, kit's; Host, where it answered.
	Name, Host string
	// User and Home are who SSH reaches it as, and their home folder there.
	User, Home string
	// Rsync is Homebrew's rsync there.
	Rsync restore.Found
	// protected is whether SSH may read the first protected folder there,
	// as the probe says it, and the folder.
	protected, folder string

	run  runner.Runner
	opts []string
}

// localHostName is a local hostname as macOS makes one: letters, digits
// and -, not starting with -.
var localHostName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)

// Find reaches the old Mac, whose kit name is from, at LocalHostName, on
// the local network, presenting HostKey, and asks it its kit name. Either
// not recorded is an error, ErrUnrecorded, saying how kit-config comes to
// record it; not answering, presenting another key, or answering as
// another Mac, ErrNoAnswer, saying why.
func (f Finder) Find(ctx context.Context, from string) (*Mac, error) {
	if err := checkStateDir(f.StateDir); err != nil {
		return nil, err
	}
	pin, err := f.pin(from)
	if err != nil {
		return nil, err
	}
	host, err := f.host(from)
	if err != nil {
		return nil, err
	}
	opts := sshOptions(pin, from)
	p, err := f.probe(ctx, opts, host)
	if err != nil {
		return nil, f.unanswered(from, host, err)
	}
	protected, folder, _ := strings.Cut(p.one(keyProtected), " ")
	m := &Mac{
		Name: strings.TrimSpace(p.one(keyKit)), Host: host, User: p.one(keyUser), Home: p.one(keyHome),
		protected: protected, folder: folder, Rsync: rsyncThere(p[keyRsync]), run: f.Run, opts: opts,
	}
	if m.Name != from {
		return nil, fmt.Errorf("%s %w: %s answered, but it's %s", from, ErrNoAnswer, host, printable(cmp.Or(m.Name, "a Mac without kit")))
	}
	return m, nil
}

// pin readies the Mac called from's host key in a known-hosts file of its
// own, and returns its path.
func (f Finder) pin(from string) (string, error) {
	if f.HostKey == "" {
		return "", fmt.Errorf("%s %w: %s's SSH host key isn't in kit-config: kit apply on %s records it", from, ErrUnrecorded, from, from)
	}
	if _, err := PublicKey(f.HostKey); err != nil {
		return "", fmt.Errorf("%s %w: %s's SSH host key in kit-config isn't a valid key: kit apply on %s records it", from, ErrUnrecorded, from, from)
	}
	path := pinned(f.StateDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("make kit's known-hosts folder: %w", err)
	}
	if !plainName.MatchString(from) {
		return "", fmt.Errorf("pin %q's host key: a name to pin a key to is letters, digits, - and ., not starting with -", from)
	}
	line, err := pinLine(from, f.HostKey)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		return "", fmt.Errorf("pin %s's host key: %w", from, err)
	}
	return path, nil
}

// host is where the Mac called from answers on the local network: its
// local hostname, then .local, as macOS answers to it.
func (f Finder) host(from string) (string, error) {
	switch {
	case f.LocalHostName == "":
		return "", fmt.Errorf("%s %w: %s's local hostname isn't in kit-config: kit apply on %s records it", from, ErrUnrecorded, from, from)
	case !localHostName.MatchString(f.LocalHostName):
		return "", fmt.Errorf("%s %w: %s's local hostname in kit-config, %q, isn't one: kit apply on %s records it", from, ErrUnrecorded, from, printable(f.LocalHostName), from)
	}
	return f.LocalHostName + ".local", nil
}

// unanswered is the error for the Mac called from not answering at host,
// as ssh's error, err, says.
func (f Finder) unanswered(from, host string, err error) error {
	switch _, key := errors.AsType[*hostKeyError](err); {
	case key:
		return fmt.Errorf("%s %w: %s isn't %s: its host key doesn't match the one kit-config records (kit apply on %s records a new one)", from, ErrNoAnswer, host, from, from)
	case errors.Is(err, errNoRoute) && !f.OverSSH:
		app := cmp.Or(f.Terminal, apps.Unnamed)
		return fmt.Errorf("%s %w: %s didn't answer: macOS may be asking whether %s can find devices on local networks: choose Allow, then try again, or, if Don't Allow was chosen, turn on %s in System Settings › Privacy & Security › Local Network",
			from, ErrNoAnswer, host, app, app)
	case errors.Is(err, errNoAnswer), errors.Is(err, errNoRoute):
		return unrecorded{fmt.Errorf("%s %w: nothing answered at %s, its local hostname in kit-config: with %s awake on this network, kit apply on %s records the one it has now", from, ErrNoAnswer, host, from, from)}
	}
	return fmt.Errorf("%s %w: %s %s", from, ErrNoAnswer, host, printable(err.Error()))
}

// unrecorded is an error, in its own words, that's ErrUnrecorded too: kit
// apply on the old Mac is the way on.
type unrecorded struct{ error }

func (u unrecorded) Unwrap() []error { return []error{u.error, ErrUnrecorded} }

// rsyncThere is what Homebrew's rsync on a Mac said of itself, its
// --version's lines.
func rsyncThere(lines []string) restore.Found {
	if len(lines) == 0 {
		return restore.Found{Why: "it isn't installed"}
	}
	return restore.ReadVersion(strings.Join(lines, "\n"))
}

// Check says what stops m being copied from, to a Mac where the person is
// user, at home: nil when nothing does. Its user and home must be the
// same, as Claude keys its transcripts by their full paths; its rsync, rsync
// 3, as kit's copy needs; and SSH must be able to read the folders only
// full disk access opens, ~/Documents, else ~/Desktop, else ~/Downloads.
// What the Mac said of itself is said without control characters.
func (m *Mac) Check(user, home string) error {
	var errs []error
	name := printable(m.Name)
	if m.User != user {
		errs = append(errs, fmt.Errorf("%s's user is %s, and this Mac's %s: they must be the same, as Claude keys its sessions by their full paths", name, printable(m.User), user))
	} else if m.Home != home {
		errs = append(errs, fmt.Errorf("%s's home folder is %s, and this Mac's %s: they must be the same, as Claude keys its sessions by their full paths", name, printable(m.Home), home))
	}
	if !m.Rsync.Usable {
		errs = append(errs, fmt.Errorf("%s's rsync, %s, can't copy: %s: brew install rsync there", name, RemoteRsync, printable(m.Rsync.Why)))
	}
	if m.protected == protectedDenied {
		errs = append(errs, fmt.Errorf("SSH can't read ~/%s on %s: turn on Allow full disk access for remote users there, in System Settings › General › Sharing › Remote Login › ⓘ", printable(m.folder), name))
	}
	return errors.Join(errs...)
}

// Shell is the command rsync reaches m with, ssh and its options, as rsync
// splits it (--rsh).
func (m *Mac) Shell() string {
	return runner.Command{Name: "ssh", Args: m.opts}.String()
}

// Opened is m opened to restore from: live, as it's in use while it's
// copied; what's at the listed paths there looked at through Look; nothing
// to close.
func (m *Mac) Opened() *restore.Opened {
	return &restore.Opened{
		Home: m.Home, Host: m.Host, Shell: m.Shell(), Rsync: RemoteRsync, Look: m.Look, Live: true,
		Origin: restore.Origin{Kind: restore.FromMac, Detail: m.Name},
		Close:  func(context.Context) error { return nil },
	}
}

// Source is the Mac called From, to restore from to this Mac, where the
// person is User, at Home: found as it's opened (Find), checked (Check), and
// opened (Opened).
type Source struct {
	Finder           Finder
	From, User, Home string
	// Recorded is what kit-config's remote records for From now: its host
	// key and local hostname, looked for each time the Mac's tried again,
	// as kit apply on it may have recorded them since the run began.
	// Without it, the Finder's are kept.
	Recorded func(ctx context.Context) (hostKey, localHostName string, err error)
	tried    bool
}

// Open finds the Mac to restore from, and opens it, once nothing stops it
// being copied from: not answering is an error, ErrNoAnswer, saying why;
// its host key or local hostname not recorded, or not to be had from
// kit-config's remote, ErrUnrecorded; what stops it, Check's, each naming
// its fix.
func (s *Source) Open(ctx context.Context) (*restore.Opened, error) {
	if s.tried && s.Recorded != nil {
		key, name, err := s.Recorded(ctx)
		if err != nil {
			return nil, fmt.Errorf("%s %w: kit couldn't fetch kit-config to read its record of %s: %w", s.From, ErrUnrecorded, s.From, err)
		}
		s.Finder.HostKey, s.Finder.LocalHostName = key, name
	}
	s.tried = true
	m, err := s.Finder.Find(ctx, s.From)
	if err != nil {
		return nil, err
	}
	if err := m.Check(s.User, s.Home); err != nil {
		return nil, err
	}
	return m.Opened(), nil
}
