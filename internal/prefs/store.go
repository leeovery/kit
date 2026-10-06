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
	"time"

	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/runner"
)

// cloneTimeout is how long cloning the store may take.
const cloneTimeout = 10 * time.Minute

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
		if p.Remote == "" {
			return errors.New("kit.toml names no prefs_repo for apps' settings")
		}
		if err := os.MkdirAll(filepath.Dir(p.Clone), 0o700); err != nil {
			return err
		}
		if _, err := p.Run.Run(ctx, runner.Command{Name: "git", Args: []string{"clone", "--quiet", p.Remote, p.Clone}, Timeout: cloneTimeout}); err != nil {
			return fmt.Errorf("clone the prefs repository: %w", err)
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

// pull brings in what other Macs pushed, rebasing this Mac's commits onto
// it; a rebase that stops is undone. Offline, it fails, and capture carries
// on, its commit waiting to be pushed.
func (p *Prefs) pull(ctx context.Context) error {
	if _, err := p.git(ctx, "pull", "--rebase", "--autostash", "--quiet"); err != nil {
		_, _ = p.git(ctx, "rebase", "--abort")
		return err
	}
	return nil
}

// push sends the store's commits to its remote, only when that's private
// on GitHub (or not on GitHub at all): why it didn't, or "" once pushed.
func (p *Prefs) push(ctx context.Context, repo gitrepo.Repo) string {
	res, err := p.git(ctx, "remote", "get-url", "origin")
	if err != nil {
		return "no remote to push to"
	}
	name, visibility, err := gitrepo.GitHubVisibility(ctx, p.Run, string(res.Stdout))
	switch {
	case err != nil:
		return err.Error()
	case name != "" && visibility != "private":
		return fmt.Sprintf("%s is %s on GitHub: kit pushes apps' settings only to a private repository (gh repo edit %s --visibility private --accept-visibility-change-consequences)", name, visibility, name)
	}
	if err := repo.Push(ctx); err != nil {
		return err.Error()
	}
	return ""
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
	format := []string{"log", "-n" + strconv.Itoa(count), "--date=format:%Y-%m-%d %H:%M", "--format=%h  %ad  %s"}
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

// head is the clone's last commit.
func (p *Prefs) head(ctx context.Context) string {
	res, err := p.git(ctx, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(res.Stdout))
}
