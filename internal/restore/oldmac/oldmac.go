// Package oldmac reaches the Mac a new one started from, over SSH, to
// restore from it. kit's config holds no address for a Mac, and the name a
// Mac answers to on the network isn't kit's, so the Macs offering Remote
// Login are found through Bonjour and each asked, over SSH, its kit name;
// the one with the name wanted is checked can be copied from; and what its
// backups leave out of each listed folder is found, for the restore to
// leave out too.
package oldmac

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

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
// SSH as the one wanted: off, asleep, away, refusing this Mac's key, or not
// offering Remote Login.
var ErrNoAnswer = errors.New("doesn't answer over SSH")

// Finder finds Macs on the network, run through Run, their host keys kept
// in kit's state, StateDir.
type Finder struct {
	Run      runner.Runner
	StateDir string
	// HostKey is the host key the Mac looked for must present, a public key
	// as a known-hosts line or a .pub file has it: when it's given, a Mac
	// presenting another isn't it, whatever it says its name is; else each
	// Mac's key is taken on first sight.
	HostKey string
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

// Find finds the Mac whose kit name is from: with a HostKey, the one
// presenting it, as a Mac's name is only its own say. One Mac offering
// Remote Login under two names counts once. None answering as from is an
// error, ErrNoAnswer, saying of each Mac found why it isn't.
func (f Finder) Find(ctx context.Context, from string) (*Mac, error) {
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
		return nil, notFound(from, found)
	case 1:
		return macs[0], nil
	}
	hosts := make([]string, len(macs))
	for i, m := range macs {
		hosts[i] = m.Host
	}
	return nil, fmt.Errorf("more than one Mac answers as %s, %s: turn off Remote Login on all but the old Mac", from, strings.Join(hosts, " and "))
}

// pin readies the Mac called from's host key, when one's given, in a
// known-hosts file of its own: its path, else "", each Mac's key then
// taken on first sight into kit's own known-hosts file.
func (f Finder) pin(from string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(KnownHosts(f.StateDir)), 0o700); err != nil {
		return "", fmt.Errorf("make kit's known-hosts folder: %w", err)
	}
	if f.HostKey == "" {
		return "", nil
	}
	if !plainName.MatchString(from) {
		return "", fmt.Errorf("pin %q's host key: a name to pin a key to is letters, digits, - and ., not starting with -", from)
	}
	line, err := pinLine(from, f.HostKey)
	if err != nil {
		return "", err
	}
	path := pinned(f.StateDir)
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		return "", fmt.Errorf("pin %s's host key: %w", from, err)
	}
	return path, nil
}

// ask is the Mac offering Remote Login as name, asked its kit name, its
// host key the one pinned, in the file pin, for from, when there is one.
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
	opts := sshOptions(KnownHosts(f.StateDir), pin, from, a.port)
	p, err := f.probe(ctx, opts, a.host)
	if _, key := errors.AsType[*hostKeyError](err); key && pin != "" {
		err = fmt.Errorf("isn't %s: its host key doesn't match the one kit-config records", from)
	}
	if err != nil {
		c.why = err
		return c
	}
	protected, folder, _ := strings.Cut(p.one(keyProtected), " ")
	m := &Mac{
		Name: strings.TrimSpace(p.one(keyKit)), Instance: name, Host: a.host, Port: a.port,
		User: p.one(keyUser), Home: p.one(keyHome), protected: protected, folder: folder,
		Rsync: rsyncThere(p[keyRsync]), run: f.Run, opts: opts,
	}
	if m.Name == "" {
		c.why = errors.New("has no kit")
		return c
	}
	c.mac = m
	return c
}

// same reports whether m and other are the same Mac, at one host: a Mac
// can offer Remote Login under two names, its old one still cached after
// it's renamed.
func (m *Mac) same(other *Mac) bool {
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

// notFound is the error for no Mac answering as from, of those found.
func notFound(from string, found []candidate) error {
	if len(found) == 0 {
		return fmt.Errorf("%s %w: no Mac on the network offers Remote Login", from, ErrNoAnswer)
	}
	whys := make([]string, len(found))
	for i, c := range found {
		if c.mac != nil {
			whys[i] = fmt.Sprintf("%s answered, but it's %s", c.instance, c.mac.Name)
		} else {
			whys[i] = fmt.Sprintf("%s %s", c.instance, c.why)
		}
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
// copied; nothing to close.
func (m *Mac) Opened() *restore.Opened {
	return &restore.Opened{
		Home: m.Home, Host: m.Host, Shell: m.Shell(), Rsync: RemoteRsync, Live: true,
		Origin: restore.Origin{Kind: restore.FromMac, Detail: m.Name},
		Close:  func(context.Context) error { return nil },
	}
}
