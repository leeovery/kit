// Package oldmac reaches the Mac a new one started from, over SSH, to
// restore from it. kit's config holds no address for a Mac, and the name a
// Mac answers to on the network isn't kit's, so the Macs offering Remote
// Login are found through Bonjour and each asked, over SSH, its kit name;
// the one with the name wanted is checked can be copied from; and what its
// backups leave out of each listed folder is found, for the restore to
// leave out too.
package oldmac

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/leeovery/kit/internal/apps"
	"github.com/leeovery/kit/internal/restore"
	"github.com/leeovery/kit/internal/runner"
)

// RemoteRsync is the old Mac's rsync 3, Homebrew's, which kit-config's
// shared formulae install on every Mac: by name, SSH would find macOS's own,
// openrsync.
const RemoteRsync = "/opt/homebrew/bin/rsync"

// How long Bonjour's listened to for the Macs offering Remote Login, and
// for where one answers; and how long a Mac's given to answer the probe.
const (
	browseFor    = 3 * time.Second
	resolveFor   = 3 * time.Second
	probeTimeout = 30 * time.Second
)

// ErrNoAnswer is in the error of a search that found no Mac answering over
// SSH as the one wanted: off, asleep, away, refusing this Mac's key,
// presenting another host key than kit-config's, not offering Remote
// Login, or out of reach till macOS lets the terminal find devices on
// local networks.
var ErrNoAnswer = errors.New("doesn't answer over SSH")

// ErrNoHostKey is in the error of a search for a Mac whose host key
// kit-config doesn't record: none is looked for, as a Mac's name is only
// its own say.
var ErrNoHostKey = errors.New("can't be restored from")

// Finder finds Macs on the network, run through Run, the host key looked
// for kept in kit's state, StateDir.
type Finder struct {
	Run      runner.Runner
	StateDir string
	// HostKey is the host key the Mac looked for must present, kit-config's,
	// a public key as a known-hosts line or a .pub file has it: a Mac
	// presenting another isn't it, whatever it says its name is. Without
	// one, none is looked for (ErrNoHostKey).
	HostKey string
	// Terminal is the app kit's running in, as macOS names it asking
	// whether it can find devices on local networks (apps.RunningIn):
	// apps.Unnamed when "".
	Terminal string
	// OverSSH is kit running in an SSH session, where macOS never asks
	// that, so a Mac ssh has no route to is said plainly not to answer.
	OverSSH bool
	// Browse and Resolve are how long Bonjour's listened to for the Macs
	// offering Remote Login, and for where one answers: browseFor and
	// resolveFor when 0.
	Browse, Resolve time.Duration
}

// Mac is a Mac found answering over SSH.
type Mac struct {
	// Name is its name, kit's; Instance, the name it offers Remote Login
	// under; Host and Port, where it answered.
	Name, Instance, Host string
	Port                 int
	// User and Home are who SSH reaches it as, and their home folder there.
	User, Home string
	// Rsync is Homebrew's rsync there.
	Rsync restore.Found
	// HostKey is its SSH host key, its type and key, as it says it: "" when
	// it couldn't read one.
	HostKey string
	// protected is whether SSH may read the first protected folder there,
	// as the probe says it, and the folder.
	protected, folder string

	run  runner.Runner
	opts []string
}

// candidate is a Mac offering Remote Login, and what came of asking it its
// name: the Mac, or why not.
type candidate struct {
	instance string
	mac      *Mac
	why      error
}

// Find finds the Mac whose kit name is from and that presents HostKey, as
// a Mac's name is only its own say. One Mac offering Remote Login under two
// names, as when it's renamed, counts once. None answering as from is an
// error, ErrNoAnswer, saying of each Mac found why it isn't; no HostKey, an
// error, ErrNoHostKey, saying how kit-config comes to record it.
func (f Finder) Find(ctx context.Context, from string) (*Mac, error) {
	if err := checkStateDir(f.StateDir); err != nil {
		return nil, err
	}
	pin, err := f.pin(from)
	if err != nil {
		return nil, err
	}
	names, err := f.browse(ctx)
	if err != nil {
		return nil, err
	}
	found := make([]candidate, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Go(func() { found[i] = f.ask(ctx, name, from, pin) })
	}
	wg.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	var macs []*Mac
	for _, c := range found {
		if c.mac != nil && c.mac.Name == from && !slices.ContainsFunc(macs, c.mac.same) {
			macs = append(macs, c.mac)
		}
	}
	switch len(macs) {
	case 0:
		return nil, f.notFound(from, found)
	case 1:
		return macs[0], nil
	}
	hosts := make([]string, len(macs))
	for i, m := range macs {
		hosts[i] = m.Host
	}
	return nil, fmt.Errorf("more than one Mac answers as %s, %s: turn off Remote Login on all but the old Mac", from, strings.Join(hosts, " and "))
}

// pin readies the Mac called from's host key in a known-hosts file of its
// own, and returns its path.
func (f Finder) pin(from string) (string, error) {
	if f.HostKey == "" {
		return "", fmt.Errorf("%s %w: %s's SSH host key isn't in kit-config: kit apply on %s records it", from, ErrNoHostKey, from, from)
	}
	if _, err := PublicKey(f.HostKey); err != nil {
		return "", fmt.Errorf("%s %w: %s's SSH host key in kit-config isn't a valid key: kit apply on %s records it", from, ErrNoHostKey, from, from)
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

// ask is the Mac offering Remote Login as name, asked its kit name, its
// host key the one pinned, in the file pin, for from.
func (f Finder) ask(ctx context.Context, name, from, pin string) candidate {
	c := candidate{instance: name}
	a, ok, err := f.resolve(ctx, name)
	switch {
	case err != nil:
		c.why = err
		return c
	case !ok:
		c.why = errNoAnswer
		return c
	case !plainName.MatchString(a.host):
		c.why = fmt.Errorf("names its host %q, which isn't a host name kit reaches: letters, digits, - and ., not starting with -", a.host)
		return c
	}
	opts := sshOptions(pin, from, a.port)
	p, err := f.probe(ctx, opts, a.host)
	if _, key := errors.AsType[*hostKeyError](err); key {
		err = fmt.Errorf("isn't %s: its host key doesn't match the one kit-config records (kit apply on %s records a new one)", from, from)
	}
	if f.OverSSH && errors.Is(err, errNoRoute) {
		err = errNoAnswer
	}
	if err != nil {
		c.why = err
		return c
	}
	protected, folder, _ := strings.Cut(p.one(keyProtected), " ")
	m := &Mac{
		Name: strings.TrimSpace(p.one(keyKit)), Instance: name, Host: a.host, Port: a.port,
		User: p.one(keyUser), Home: p.one(keyHome), protected: protected, folder: folder,
		Rsync: rsyncThere(p[keyRsync]), HostKey: p.one(keyHostKey), run: f.Run, opts: opts,
	}
	if m.Name == "" {
		c.why = errors.New("has no kit")
		return c
	}
	c.mac = m
	return c
}

// localNetwork is why the Macs Bonjour found, called unrouted, that ssh
// has no route to, didn't answer: as when macOS is asking whether the app
// kit's running in can find devices on local networks, or was told it
// can't. It's the app's to be allowed, so it's said once for them all.
func (f Finder) localNetwork(unrouted []string) error {
	app := cmp.Or(f.Terminal, apps.Unnamed)
	return fmt.Errorf("%s %w: macOS may be asking whether %s can find devices on local networks: choose Allow, then try again, or, if Don't Allow was chosen, turn on %s in System Settings › Privacy & Security › Local Network",
		strings.Join(unrouted, " and "), errNoAnswer, app, app)
}

// same reports whether m and other are the same Mac: by their host keys,
// as a Mac renamed for a clash of names offers Remote Login under its new
// name and host while its old ones, still cached, reach it too; else, a
// key unread, by their host.
func (m *Mac) same(other *Mac) bool {
	if m.HostKey != "" && other.HostKey != "" {
		return m.HostKey == other.HostKey
	}
	return strings.EqualFold(m.Host, other.Host) && m.Port == other.Port
}

// rsyncThere is what Homebrew's rsync on a Mac said of itself, its
// --version's lines.
func rsyncThere(lines []string) restore.Found {
	if len(lines) == 0 {
		return restore.Found{Why: "it isn't installed"}
	}
	return restore.ReadVersion(strings.Join(lines, "\n"))
}

// notFound is the error for no Mac answering as from, of those found:
// those ssh has no route to said together, last (localNetwork).
func (f Finder) notFound(from string, found []candidate) error {
	if len(found) == 0 {
		return fmt.Errorf("%s %w: no Mac on the network offers Remote Login", from, ErrNoAnswer)
	}
	var whys, unrouted []string
	for _, c := range found {
		switch {
		case c.mac != nil:
			whys = append(whys, fmt.Sprintf("%s answered, but it's %s", c.instance, c.mac.Name))
		case errors.Is(c.why, errNoRoute):
			unrouted = append(unrouted, c.instance)
		default:
			whys = append(whys, fmt.Sprintf("%s %s", c.instance, c.why))
		}
	}
	if len(unrouted) > 0 {
		whys = append(whys, f.localNetwork(unrouted).Error())
	}
	return fmt.Errorf("%s %w: %s", from, ErrNoAnswer, printable(strings.Join(whys, "; ")))
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
	// HostKey is the host key kit-config's remote records for From now,
	// looked for each time the Mac's tried again, as it may have been
	// recorded there since the run began: kit apply on the old Mac records
	// it. Without it, the Finder's is kept.
	HostKey func(ctx context.Context) (string, error)
	tried   bool
}

// Open finds the Mac to restore from, and opens it, once nothing stops it
// being copied from: none answering is an error, ErrNoAnswer, saying why
// of each found; no host key to look for, or none to be had from
// kit-config's remote, ErrNoHostKey; what stops it, Check's, each naming
// its fix.
func (s *Source) Open(ctx context.Context) (*restore.Opened, error) {
	if s.tried && s.HostKey != nil {
		key, err := s.HostKey(ctx)
		if err != nil {
			return nil, fmt.Errorf("%s %w: kit couldn't fetch its SSH host key from kit-config: %w", s.From, ErrNoHostKey, err)
		}
		s.Finder.HostKey = key
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
