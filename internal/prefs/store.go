package prefs

import (
	"archive/tar"
	"bytes"
	"cmp"
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
	"github.com/leeovery/kit/internal/state"
)

// cloneTimeout is how long cloning the store may take; lookTimeout how long
// asking the remote whether it has the branch may, which can come on every
// look at the store while its clone is empty; cloneRetry how often kit
// tries again for a lock on the store another kit holds.
const (
	cloneTimeout = 10 * time.Minute
	lookTimeout  = 20 * time.Second
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

// branch is the branch of kit-config's remote the store is on; origin, the
// name of that remote in the store's clone, as git clone names it.
const (
	branch = "prefs"
	origin = "origin"
)

// ensureClone clones the store when this Mac has no clone, or has one with
// nothing committed (clone), refuses one that isn't of the branch
// (checkClone), then applies its own git settings. Its caller holds the
// lock on the store (lockCapture), as an empty clone may be replaced.
func (p *Prefs) ensureClone(ctx context.Context) error {
	remote, err := p.remote(ctx)
	if err != nil {
		return err
	}
	if err := p.clone(ctx, remote); err != nil {
		return err
	}
	if err := p.checkClone(ctx, remote.url); err != nil {
		return err
	}
	for _, kv := range gitConfig {
		if _, err := p.git(ctx, "config", kv[0], kv[1]); err != nil {
			return err
		}
	}
	return nil
}

// config is kit-config's clone, as git drives it.
func (p *Prefs) config() gitrepo.Repo {
	return gitrepo.Repo{Dir: p.Config, Run: p.Run}
}

// kitRemote is kit-config's one remote, where the store is: its URL as set,
// which the store's clone is given, so git's insteadOf and pushInsteadOf
// rules rewrite both alike, and as git takes it, url, which the clone's is
// checked against.
type kitRemote struct{ set, url string }

// remote is where the store is: kit-config's one remote.
func (p *Prefs) remote(ctx context.Context) (kitRemote, error) {
	url, err := p.config().RemoteURL(ctx)
	if err == nil {
		var set string
		set, err = p.config().SetURL(ctx)
		return kitRemote{set: set, url: url}, err
	}
	if errors.Is(err, gitrepo.ErrNoRemote) {
		return kitRemote{}, errors.New("kit-config has no remote, so apps' settings have nowhere to be saved: they're saved on its prefs branch")
	}
	return kitRemote{}, fmt.Errorf("find kit-config's remote, where apps' settings are saved: %w", err)
}

// checkClone refuses the store's clone unless it's of remote, kit-config's
// URL as git takes it, pushing where kit-config pushes, on the branch: one
// cloned from where the store was before it moved to kit-config, or put on
// another branch, is never captured into or restored from. Push URLs are
// compared as git push has them, git's pushInsteadOf applied, as it is to
// both alike. One off the branch part way through a rebase, as when one
// was cut short, holds commits not yet pushed: it's refused as git's
// operation under way, never as a clone to move aside.
func (p *Prefs) checkClone(ctx context.Context, remote string) error {
	at, err := p.repo().URL(ctx, origin)
	if err != nil {
		return err
	}
	if at != remote {
		return fmt.Errorf("%s points at %s, not kit-config's remote (%s): apps' settings are saved on its %s branch now, so move the clone aside for kit to clone the branch there",
			p.Clone, cmp.Or(gitrepo.Shown(at), "no remote"), gitrepo.Shown(remote), branch)
	}
	if err := p.checkPushes(ctx); err != nil {
		return err
	}
	res, err := p.git(ctx, "symbolic-ref", "-q", "HEAD")
	if exit, ok := errors.AsType[*runner.ExitError](err); err != nil && (!ok || exit.Code != 1) {
		return err
	}
	on := strings.TrimPrefix(strings.TrimSpace(string(res.Stdout)), "refs/heads/")
	if on == branch {
		return p.checkUpstream(ctx)
	}
	if err := p.repo().UnderWay(ctx); err != nil {
		return fmt.Errorf("the store's clone, %s: %w", p.Clone, err)
	}
	return fmt.Errorf("%s is on %s, not kit-config's %s branch: move the clone aside for kit to clone the branch there", p.Clone, cmp.Or(on, "no branch"), branch)
}

// checkUpstream refuses the store's clone unless its branch is set to track
// origin's branch of the same name, as every clone kit makes is
// (cloneBranch). A clone of kit-config whose main was renamed prefs is
// still set to track main, which a pull would rebase the store onto, and a
// push send it to; one set to track nothing would be pushed whole as
// prefs. The settings are read, not git's upstream, which git shows as
// none when no fetch refspec maps the branch set, and for a branch with no
// commit. Push asks again all the same.
func (p *Prefs) checkUpstream(ctx context.Context) error {
	remote, err := p.setting(ctx, "branch."+branch+".remote")
	if err != nil {
		return err
	}
	merge, err := p.setting(ctx, "branch."+branch+".merge")
	if err != nil {
		return err
	}
	if remote == origin && (merge == "refs/heads/"+branch || merge == branch) {
		return nil
	}
	return fmt.Errorf("%s tracks %s on %s, not kit-config's %s branch: move the clone aside for kit to clone the branch there, or set it to track the branch (git -C %s config branch.%s.remote %s, then git -C %s config branch.%s.merge refs/heads/%s)",
		p.Clone, cmp.Or(strings.TrimPrefix(merge, "refs/heads/"), "no branch"), cmp.Or(remote, "no remote"), branch,
		p.Clone, branch, origin, p.Clone, branch, branch)
}

// setting is git's setting key in the store's clone: "" when it isn't set,
// as git says by exiting 1.
func (p *Prefs) setting(ctx context.Context, key string) (string, error) {
	res, err := p.git(ctx, "config", "--get", key)
	if exit, ok := errors.AsType[*runner.ExitError](err); ok && exit.Code == 1 {
		return "", nil
	}
	return strings.TrimSpace(string(res.Stdout)), err
}

// checkPushes refuses the store's clone unless it pushes where kit-config
// does.
func (p *Prefs) checkPushes(ctx context.Context) error {
	want, err := p.config().PushURLs(ctx)
	if err != nil {
		return fmt.Errorf("find where kit-config pushes: %w", err)
	}
	res, err := p.git(ctx, "remote", "get-url", "--push", "--all", origin)
	if err != nil {
		return err
	}
	pushes := slices.DeleteFunc(strings.Split(string(res.Stdout), "\n"), func(url string) bool { return url == "" })
	if slices.Equal(pushes, want) {
		return nil
	}
	return fmt.Errorf("%s pushes to %s, where kit-config pushes to %s: move the clone aside for kit to clone the branch there", p.Clone, shown(pushes), shown(want))
}

// shown are urls as kit names them (gitrepo.Shown), and'd.
func shown(urls []string) string {
	names := make([]string, len(urls))
	for i, url := range urls {
		names[i] = gitrepo.Shown(url)
	}
	return strings.Join(names, " and ")
}

// checked refuses the store's clone, when this Mac has one, unless it's of
// kit-config's branch, as checkClone does.
func (p *Prefs) checked(ctx context.Context) error {
	if _, err := os.Stat(filepath.Join(p.Clone, ".git")); err != nil {
		return nil
	}
	remote, err := p.remote(ctx)
	if err != nil {
		return err
	}
	return p.checkClone(ctx, remote.url)
}

// cloneState is how this Mac's clone of the store stands.
type cloneState int

const (
	noClone cloneState = iota
	// emptyClone is a clone with no commit on any branch, as one made
	// before the branch was: replaced once the branch is there, losing no
	// commit.
	emptyClone
	madeClone
)

// clone clones the store, aside, then puts the clone in its place whole,
// so that one cut short, by its time limit or the run stopping, leaves none
// that a later run takes for whole: it clones again, what that one left
// cleared first. An empty clone of kit-config's branch is cloned again once
// the remote has the branch, so a Mac that made one before the branch was,
// as a new Mac whose boot came before any Mac's first capture, takes the
// branch another Mac's capture made; offline, or with the branch still to
// be made, it's kept, and a capture carries on in it. It holds a lock
// beside the clone throughout, so no two kits clone at once: one that
// waited for it finds the clone the other made. A folder in the clone's
// place that isn't one is refused before anything's downloaded.
func (p *Prefs) clone(ctx context.Context, remote kitRemote) error {
	if err := os.MkdirAll(filepath.Dir(p.Clone), 0o700); err != nil {
		return err
	}
	unlock, err := p.lockClone(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	state, err := p.clonedYet(ctx)
	switch {
	case err != nil:
		return err
	case state == madeClone:
		return nil
	case state == emptyClone:
		// An empty clone of anywhere else is refused, never deleted.
		if err := p.checkClone(ctx, remote.url); err != nil {
			return err
		}
	}
	carried, err := p.carried(ctx)
	if err != nil {
		return err
	}
	has, err := p.hasBranch(ctx, remote.set, carried)
	switch {
	case state == emptyClone && (err != nil || !has):
		return nil
	case err != nil:
		return err
	}
	return p.replace(ctx, remote.set, has, carried)
}

// replace puts a clone of remote's branch, as cloneBranch makes it, in the
// place of whatever's there, cloned aside first, so a clone that fails
// leaves what was there.
func (p *Prefs) replace(ctx context.Context, remote string, has bool, carried [][2]string) error {
	aside := p.Clone + ".cloning"
	if err := os.RemoveAll(aside); err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(aside) }()
	if err := p.cloneBranch(ctx, remote, aside, has, carried); err != nil {
		return fmt.Errorf("clone kit-config's %s branch: %w", branch, err)
	}
	if err := os.RemoveAll(p.Clone); err != nil {
		return err
	}
	if err := os.Rename(aside, p.Clone); err != nil {
		return fmt.Errorf("put the store's clone in place: %w", err)
	}
	return nil
}

// clonedYet is how this Mac's clone of the store stands: an error for a
// folder in its place that isn't a clone, and isn't empty. A clone with no
// branch, as git lists them, has no commit: HEAD unborn says only that the
// branch it's on has none.
func (p *Prefs) clonedYet(ctx context.Context) (cloneState, error) {
	_, err := os.Stat(filepath.Join(p.Clone, ".git"))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if in, err := os.ReadDir(p.Clone); err == nil && len(in) > 0 {
			return noClone, fmt.Errorf("%s isn't a clone of kit-config's %s branch, and isn't empty: move it aside for kit to clone the branch there", p.Clone, branch)
		}
		return noClone, nil
	case err != nil:
		return noClone, err
	}
	res, err := p.git(ctx, "for-each-ref", "--count=1", "--format=%(refname)", "refs/heads")
	switch {
	case err != nil:
		return noClone, err
	case strings.TrimSpace(string(res.Stdout)) == "":
		return emptyClone, nil
	}
	return madeClone, nil
}

// cloneBranch clones remote's branch alone into dir, a clone that fetches
// only it, so kit-config's own history never comes with it, with carried,
// the git settings it takes from kit-config's, as git clone's -c keeps them
// in the clone. With the remote holding no such branch, has false, as
// before the first capture, git can't clone it: dir is made the clone it
// would be, empty, on the branch and tracking it, for that capture's push
// to make it.
func (p *Prefs) cloneBranch(ctx context.Context, remote, dir string, has bool, carried [][2]string) error {
	if has {
		args := slices.Concat([]string{"clone"}, options(carried), []string{"--quiet", "--single-branch", "--branch", branch, remote, dir})
		_, err := p.Run.Run(ctx, runner.Command{Name: "git", Args: args, Timeout: cloneTimeout})
		return err
	}
	made := [][]string{
		{"init", "--quiet", "--initial-branch=" + branch, dir},
		{"-C", dir, "remote", "add", "-t", branch, origin, remote},
		{"-C", dir, "config", "branch." + branch + ".remote", origin},
		{"-C", dir, "config", "branch." + branch + ".merge", "refs/heads/" + branch},
	}
	for _, kv := range carried {
		made = append(made, []string{"-C", dir, "config", "--add", kv[0], kv[1]})
	}
	for _, args := range made {
		if _, err := p.Run.Run(ctx, runner.Command{Name: "git", Args: args}); err != nil {
			return err
		}
	}
	return nil
}

// carried are kit-config's own git settings the store's clone takes, so
// whatever reaches kit-config reaches the store, and it pushes where
// kit-config does: how it signs in to its remote (its credential settings,
// as the boot sets one to sign in through GitHub's CLI), and the push URLs
// its remote has set, as origin's. Each is a key and its value.
func (p *Prefs) carried(ctx context.Context) ([][2]string, error) {
	res, err := p.Run.Run(ctx, runner.Command{Name: "git", Args: []string{"-C", p.Config, "config", "--local", "--get-regexp", "-z", carriedSettings}})
	if exit, ok := errors.AsType[*runner.ExitError](err); ok && exit.Code == 1 {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read how kit-config reaches its remote: %w", err)
	}
	var settings [][2]string
	for setting := range strings.SplitSeq(string(res.Stdout), "\x00") {
		if setting == "" {
			continue
		}
		// A key with no value, as git prints it, is true.
		key, value, ok := strings.Cut(setting, "\n")
		if !ok {
			value = "true"
		}
		if strings.HasPrefix(key, "remote.") {
			key = "remote." + origin + ".pushurl"
		}
		settings = append(settings, [2]string{key, value})
	}
	return settings, nil
}

// carriedSettings matches the settings carried takes, as git config's
// --get-regexp reads it.
const carriedSettings = `^(credential\..*|remote\..*\.pushurl)$`

// hasBranch reports whether remote has the branch, as git ls-remote says,
// exiting 2 for no such branch, with carried's settings.
func (p *Prefs) hasBranch(ctx context.Context, remote string, carried [][2]string) (bool, error) {
	args := slices.Concat(options(carried), []string{"ls-remote", "--quiet", "--exit-code", remote, "refs/heads/" + branch})
	_, err := p.Run.Run(ctx, runner.Command{Name: "git", Args: args, Timeout: lookTimeout})
	if exit, ok := errors.AsType[*runner.ExitError](err); ok && exit.Code == 2 {
		return false, nil
	}
	return err == nil, err
}

// options are settings, each a key and its value, as git's -c options.
func options(settings [][2]string) []string {
	var args []string
	for _, kv := range settings {
		args = append(args, "-c", kv[0]+"="+kv[1])
	}
	return args
}

// lockClone takes the lock on cloning the store, beside the clone, trying
// again while another kit holds it, till ctx is done: unlock lets it go.
func (p *Prefs) lockClone(ctx context.Context) (unlock func(), err error) {
	unlock, err = p.lock(ctx, p.Clone+".lock")
	if err != nil && ctx.Err() == nil {
		err = fmt.Errorf("lock the store's clone: %w", err)
	}
	return unlock, err
}

// captureWait is how long a capture, restore or take-over waits for one
// under way to finish.
const captureWait = 30 * time.Minute

// lockCapture takes the lock on the store's clone, beside it, so that no
// capture, restore or take-over, as the hourly launch's and one by hand,
// works on the clone while another does: waiting for one under way, till
// ctx is done or for captureWait at most. unlock lets it go.
func (p *Prefs) lockCapture(ctx context.Context) (unlock func(), err error) {
	if err := os.MkdirAll(filepath.Dir(p.Clone), 0o700); err != nil {
		return nil, err
	}
	waiting, cancel := context.WithTimeout(ctx, captureWait)
	defer cancel()
	unlock, err = p.lock(waiting, p.Clone+".capture.lock")
	switch {
	case err == nil, ctx.Err() != nil:
		return unlock, err
	case waiting.Err() != nil:
		return nil, fmt.Errorf("another capture or restore still held the store after %d minutes: this one didn't run", int(captureWait.Minutes()))
	}
	return nil, fmt.Errorf("lock the store: %w", err)
}

// lock takes the lock in the file at path, as state.Lock does, trying
// again every cloneRetry.
func (p *Prefs) lock(ctx context.Context, path string) (unlock func(), err error) {
	return state.Lock(ctx, path, func() { p.Sleep(cloneRetry) })
}

// repo is the store's clone, as git drives it: pulled, committed and
// pushed as kit-config is, but for its branch, which may be missing from
// the remote till the first capture's push makes it.
func (p *Prefs) repo() gitrepo.Repo {
	return gitrepo.Repo{Dir: p.Clone, Run: p.Run, Sleep: p.Sleep, BranchMayBeMissing: true}
}

// push sends the store's commits to its remote, only when every URL Push
// pushes to is proven private (gitrepo.Place): why it didn't push, or ""
// once pushed.
func (p *Prefs) push(ctx context.Context, repo gitrepo.Repo) string {
	missing, err := p.config().BranchMissing(ctx)
	switch {
	case err != nil:
		return "kit-config: " + err.Error()
	case missing:
		return "kit-config's own branch isn't on its remote yet, and a new repository's first branch pushed is the one a new Mac clones: apps' settings are pushed once kit-config's is"
	}
	err = repo.ProvePrivate(ctx)
	notPrivate, isNot := errors.AsType[gitrepo.NotPrivate](err)
	switch {
	case errors.Is(err, gitrepo.ErrNoRemote):
		return "no remote to push to"
	case isNot && notPrivate.Place.Repo != "":
		place := notPrivate.Place
		return fmt.Sprintf("%s is %s on GitHub: kit pushes apps' settings only to a private repository (gh repo edit %s --visibility private --accept-visibility-change-consequences)", place.Repo, place.Visibility, place.Repo)
	case isNot:
		return fmt.Sprintf("kit pushes apps' settings only to a repository it can prove private, and can't for %s: %s", gitrepo.Shown(notPrivate.URL), notPrivate.Place.Unproven)
	case err != nil:
		return err.Error()
	}
	if err := p.checkUpstream(ctx); err != nil {
		return err.Error()
	}
	if err := repo.Push(ctx); err != nil {
		return err.Error()
	}
	return ""
}

// Change is a capture that changed a Mac's folder, as History lists it: its
// commit, when it was made and its message; and what it changed: in all, as
// git's shortstat says it, or, in a domain's or a file's history, as a diff.
type Change struct {
	Commit  string    `json:"commit"`
	Date    time.Time `json:"date"`
	Message string    `json:"message"`
	Changed string    `json:"changed"`
}

// ErrNoHistory is History finding no capture that changed what it was asked
// about.
var ErrNoHistory = errors.New("no history")

// History is the captures that changed a Mac's folder, newest first, count
// of them; or, for target (a domain, or a settings file's path from ~ or
// /), its changes, each with its diff.
func (p *Prefs) History(ctx context.Context, mac, target string, count int) ([]Change, error) {
	if mac == "" {
		mac = p.Mac
	}
	if _, err := os.Stat(filepath.Join(p.Clone, ".git")); err != nil {
		return nil, fmt.Errorf("this Mac has no clone of kit-config's %s branch yet: its first capture or restore makes one", branch)
	}
	if err := p.checked(ctx); err != nil {
		return nil, err
	}
	// Each change starts with a record separator, its fields apart by NULs.
	format := []string{"log", "--no-color", "-n" + strconv.Itoa(count), "--format=%x1e%h%x00%aI%x00%s"}
	path := mac
	if target != "" {
		path = filepath.Join(mac, "domains", target+".plist")
		if _, byKeys := p.keyedBy()[target]; byKeys {
			path = filepath.Join(mac, keysDir, target+".plist")
		}
		if strings.HasPrefix(target, "~") || strings.HasPrefix(target, "/") {
			path = p.storedPath(filepath.Join(mac, "files"), p.expand(target))
		}
		format = append(format, "-p")
	} else {
		format = append(format, "--shortstat")
	}
	switch state, err := p.clonedYet(ctx); {
	case err != nil:
		return nil, err
	case state == emptyClone:
		return nil, fmt.Errorf("%w for %s", ErrNoHistory, path)
	}
	res, err := p.git(ctx, slices.Concat(format, []string{"--", path})...)
	if err != nil {
		return nil, err
	}
	var changes []Change
	for _, logged := range strings.Split(string(res.Stdout), "\x1e")[1:] {
		head, body, _ := strings.Cut(logged, "\n")
		fields := strings.SplitN(head, "\x00", 3)
		if len(fields) != 3 {
			return nil, fmt.Errorf("git log said %q, not a commit, its date and its message", head)
		}
		date, err := time.Parse(time.RFC3339, fields[1])
		if err != nil {
			return nil, fmt.Errorf("git log's date for %s: %w", fields[0], err)
		}
		// A shortstat's line starts with a space; a diff's context lines do too.
		changed := strings.Trim(body, "\n")
		if target == "" {
			changed = strings.TrimSpace(changed)
		}
		changes = append(changes, Change{Commit: fields[0], Date: date, Message: fields[2], Changed: changed})
	}
	if len(changes) == 0 {
		return nil, fmt.Errorf("%w for %s", ErrNoHistory, path)
	}
	return changes, nil
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

// Save is a Mac's folder in the store, by its name, and when it last
// changed.
type Save struct {
	Mac string
	At  time.Time
}

// Saves are the folders in the store with a save, sorted by name, this
// Mac's too. The store's cloned first, holding the lock on it, when this
// Mac has no clone yet, or has an empty one and the remote has the branch,
// so a paused Mac never takes a clone made before another Mac's first
// capture for a store with nothing to restore: an empty clone, offline or
// with the branch still to be made, has none.
func (p *Prefs) Saves(ctx context.Context) ([]Save, error) {
	unlock, err := p.lockCapture(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := p.ensureClone(ctx); err != nil {
		return nil, err
	}
	switch state, err := p.clonedYet(ctx); {
	case err != nil:
		return nil, err
	case state == emptyClone:
		return nil, nil
	}
	entries, err := os.ReadDir(p.Clone)
	if err != nil {
		return nil, err
	}
	var saves []Save
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		at, err := p.changed(ctx, e.Name())
		if err != nil {
			return nil, err
		}
		if !at.IsZero() {
			saves = append(saves, Save{Mac: e.Name(), At: at})
		}
	}
	return saves, nil
}

// changed is when a Mac's folder in the store was last committed: never,
// for one that hasn't been.
func (p *Prefs) changed(ctx context.Context, mac string) (time.Time, error) {
	res, err := p.git(ctx, "log", "-1", "--format=%cI", "--", mac)
	if err != nil {
		return time.Time{}, err
	}
	last := strings.TrimSpace(string(res.Stdout))
	if last == "" {
		return time.Time{}, nil
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
