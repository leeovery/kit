package prefs

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/runner"
)

// cloneTimeout is how long cloning the store may take; cloneRetry how
// often kit tries again for the lock another kit holds while it clones.
const (
	cloneTimeout = 10 * time.Minute
	cloneRetry   = 100 * time.Millisecond
)

// gitConfig are the clone's own git settings, keeping the user's global
// ones out: no commit signing (it would wait on 1Password at night), no
// global ignore file (it hides .claude/settings.local.json), no hooks.
var gitConfig = [][2]string{
	{"user.name", "kit"}, {"user.email", "kit@localhost"},
	{"commit.gpgsign", "false"}, {"tag.gpgsign", "false"},
	{"core.excludesFile", "/dev/null"}, {"core.hooksPath", "/dev/null"}, {"core.autocrlf", "false"},
}

func (p *Prefs) git(ctx context.Context, args ...string) (runner.Result, error) {
	return p.Run.Run(ctx, runner.Command{Name: "git", Args: slices.Concat([]string{"-C", p.Clone}, args)})
}

// ensureClone clones the store when this Mac has no clone, then applies
// its own git settings.
func (p *Prefs) ensureClone(ctx context.Context) error {
	_, err := os.Stat(filepath.Join(p.Clone, ".git"))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := p.clone(ctx); err != nil {
			return err
		}
	case err != nil:
		return err
	}
	for _, kv := range gitConfig {
		if _, err := p.git(ctx, "config", kv[0], kv[1]); err != nil {
			return err
		}
	}
	return nil
}

// clone clones the store, aside, then puts the clone in its place whole,
// so that one cut short, by its time limit or the run stopping, leaves none
// that a later run takes for whole: it clones again, what that one left
// cleared first. It holds a lock beside the clone throughout, so no two
// kits clone at once: one that waited for it finds the clone the other
// made. A folder in the clone's place that isn't one is refused before
// anything's downloaded.
func (p *Prefs) clone(ctx context.Context) error {
	if p.Remote == "" {
		return errors.New("kit.toml names no prefs_repo for apps' settings")
	}
	if err := os.MkdirAll(filepath.Dir(p.Clone), 0o700); err != nil {
		return err
	}
	unlock, err := p.lockClone(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	if _, err := os.Stat(filepath.Join(p.Clone, ".git")); err == nil {
		return nil
	}
	if in, err := os.ReadDir(p.Clone); err == nil && len(in) > 0 {
		return fmt.Errorf("%s isn't a clone of the prefs repository, and isn't empty: move it aside for kit to clone the store there", p.Clone)
	}
	aside := p.Clone + ".cloning"
	if err := os.RemoveAll(aside); err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(aside) }()
	if _, err := p.Run.Run(ctx, runner.Command{Name: "git", Args: []string{"clone", "--quiet", p.Remote, aside}, Timeout: cloneTimeout}); err != nil {
		return fmt.Errorf("clone the prefs repository: %w", err)
	}
	if err := os.Rename(aside, p.Clone); err != nil {
		return fmt.Errorf("put the prefs repository's clone in place: %w", err)
	}
	return nil
}

// lockClone takes the lock on cloning the store, beside the clone, trying
// again while another kit holds it, till ctx is done: unlock lets it go.
// Its file's never removed, as a kit waiting for it would go on to lock a
// file no longer there.
func (p *Prefs) lockClone(ctx context.Context) (unlock func(), err error) {
	f, err := os.OpenFile(p.Clone+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("lock the prefs repository's clone: %w", err)
	}
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		switch {
		case err == nil:
			return func() { _ = f.Close() }, nil
		case !errors.Is(err, syscall.EWOULDBLOCK):
			_ = f.Close()
			return nil, fmt.Errorf("lock the prefs repository's clone: %w", err)
		case ctx.Err() != nil:
			_ = f.Close()
			return nil, ctx.Err()
		}
		p.Sleep(cloneRetry)
	}
}

// repo is the store's clone, as git drives it: pulled, committed and
// pushed as kit-config is.
func (p *Prefs) repo() gitrepo.Repo {
	return gitrepo.Repo{Dir: p.Clone, Run: p.Run, Sleep: p.Sleep, Home: p.Home}
}

// push sends the store's commits to its remote, only when every URL Push
// pushes to is proven private, as git pushes there (gitrepo.Place): what
// the push did besides (Push), and why it didn't push, or "" once pushed.
func (p *Prefs) push(ctx context.Context, repo gitrepo.Repo) ([]string, string) {
	remote, urls, err := repo.PushURLs(ctx)
	switch {
	case errors.Is(err, gitrepo.ErrNoRemote):
		return nil, "no remote to push to"
	case err != nil:
		return nil, err.Error()
	}
	for _, url := range urls {
		place, err := repo.Place(ctx, remote, url, gitrepo.Pushing)
		switch {
		case err != nil:
			return nil, err.Error()
		case place.Private():
		case place.Repo != "":
			return nil, fmt.Sprintf("%s is %s on GitHub: kit pushes apps' settings only to a private repository (gh repo edit %s --visibility private --accept-visibility-change-consequences)", place.Repo, place.Visibility, place.Repo)
		default:
			return nil, fmt.Sprintf("kit pushes apps' settings only to a repository it can prove private, and can't for %s: %s", gitrepo.Shown(url), place.Unproven)
		}
	}
	notes, err := repo.Push(ctx)
	if err != nil {
		return notes, err.Error()
	}
	return notes, ""
}

// History is the captures that changed a Mac's folder, newest first, count
// of them; or, for target (a domain, or a settings file's path from ~ or
// /), its changes as diffs.
func (p *Prefs) History(ctx context.Context, mac, target string, count int) (string, error) {
	if mac == "" {
		mac = p.Mac
	}
	if _, err := os.Stat(filepath.Join(p.Clone, ".git")); err != nil {
		return "", errors.New("this Mac has no clone of the prefs repository yet: its first capture or restore makes one")
	}
	format := []string{"log", "--no-color", "-n" + strconv.Itoa(count), "--date=format:%Y-%m-%d %H:%M", "--format=%h  %ad  %s"}
	path := mac
	if target != "" {
		path = filepath.Join(mac, "domains", target+".plist")
		if strings.HasPrefix(target, "~") || strings.HasPrefix(target, "/") {
			path = p.storedPath(filepath.Join(mac, "files"), p.expand(target))
		}
		format = append(format, "-p")
	} else {
		format = append(format, "--shortstat")
	}
	res, err := p.git(ctx, slices.Concat(format, []string{"--", path})...)
	if err != nil {
		return "", err
	}
	out := strings.TrimRight(string(res.Stdout), "\n")
	if out == "" {
		return "", fmt.Errorf("no history for %s", path)
	}
	return out, nil
}

// Snapshot is a Mac's folder as it was at when (any date git reads:
// 2026-09-28, "3 days ago"), unpacked into a temporary folder: the folder,
// the commit and its date, and a function removing it.
func (p *Prefs) snapshot(ctx context.Context, mac, when string) (string, string, string, func(), error) {
	res, err := p.git(ctx, "rev-list", "-1", "--before="+when, "HEAD")
	commit := strings.TrimSpace(string(res.Stdout))
	if err != nil || commit == "" {
		return "", "", "", nil, fmt.Errorf("no history in the store from before %s", when)
	}
	res, err = p.git(ctx, "log", "-1", "--date=format:%Y-%m-%d %H:%M", "--format=%ad", commit)
	if err != nil {
		return "", "", "", nil, err
	}
	date := strings.TrimSpace(string(res.Stdout))
	// The archive is binary: kept out of the run's log, as a secret is.
	res, err = p.Run.Run(ctx, runner.Command{Name: "git", Args: []string{"-C", p.Clone, "archive", "--format=tar", commit, mac}, Secret: true})
	if err != nil {
		return "", "", "", nil, fmt.Errorf("couldn't unpack %s as of %s: %w", mac, date, err)
	}
	dir, err := os.MkdirTemp("", "kit-prefs-at-")
	if err != nil {
		return "", "", "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	if err := untar(res.Stdout, dir); err != nil {
		cleanup()
		return "", "", "", nil, fmt.Errorf("couldn't unpack %s as of %s: %w", mac, date, err)
	}
	return filepath.Join(dir, mac), commit, date, cleanup, nil
}

// untar unpacks a tar archive's files and folders into dir.
func untar(data []byte, dir string) error {
	r := tar.NewReader(bytes.NewReader(data))
	for {
		h, err := r.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		path := filepath.Join(dir, filepath.Clean("/"+h.Name))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0o700); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return err
			}
			data, err := io.ReadAll(r)
			if err != nil {
				return err
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				return err
			}
		}
	}
}

// LastSave is when this Mac's folder in the store last changed: its last
// save, as a restore brings it back. The store's cloned first, when this
// Mac has no clone yet.
func (p *Prefs) LastSave(ctx context.Context) (time.Time, error) {
	if err := p.ensureClone(ctx); err != nil {
		return time.Time{}, err
	}
	res, err := p.git(ctx, "log", "-1", "--format=%cI", "--", p.Mac)
	if err != nil {
		return time.Time{}, err
	}
	last := strings.TrimSpace(string(res.Stdout))
	if last == "" {
		return time.Time{}, fmt.Errorf("no settings stored for %s", p.Mac)
	}
	return time.Parse(time.RFC3339, last)
}

// head is the clone's last commit.
func (p *Prefs) head(ctx context.Context) string {
	res, err := p.git(ctx, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(res.Stdout))
}
