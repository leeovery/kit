// Package macapp is kit's own Mac app, kit.app: what posts kit's
// notifications, under kit's name and icon, each replacing or taking away
// the one posted before under its id. kit builds it from the files it
// embeds, the first time a run needs it and again once they've changed;
// until it can, a notification goes through osascript instead, never lost.
package macapp

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/leeovery/kit/internal/runner"
)

// BundleID is the app's bundle identifier.
const BundleID = "com.leeovery.kit"

// Name is the app's bundle, in kit's data directory.
const Name = "kit.app"

var (
	//go:embed kit.swift
	source []byte
	//go:embed Info.plist
	infoPlist []byte
	//go:embed AppIcon.icns
	icon []byte
)

// compiling is how long the app's compile has: a minute or more on a slow
// Mac, as a new one in a virtual machine.
const compiling = 10 * time.Minute

const (
	executable = "Contents/MacOS/kit"
	// builtFrom holds the fingerprint of the files the bundle was built
	// from, inside it, so that codesign seals it with the rest.
	builtFrom = "Contents/Resources/built-from"
)

// files are the bundle's files, by their path in it, but its executable.
var files = map[string][]byte{
	"Contents/Info.plist":             infoPlist,
	"Contents/Resources/AppIcon.icns": icon,
}

// fingerprint is the fingerprint of the files the app is built from.
func fingerprint() []byte {
	h := sha256.New()
	for _, part := range [][]byte{source, infoPlist, icon} {
		h.Write(fmt.Appendf(nil, "%d\n", len(part)))
		h.Write(part)
	}
	return []byte(hex.EncodeToString(h.Sum(nil)) + "\n")
}

// App is kit.app, as a run uses it: built, when it needs to be, once a run
// at most, aside, so that a wait for the build can be given up.
type App struct {
	run runner.Runner
	dir string
	// activate is the bundle identifier of the app a click on a
	// notification brings forward: none when "".
	activate string
	now      func() time.Time
	// life is what a build runs in, done once the app's closed.
	life context.Context
	end  context.CancelFunc

	// mu guards whether the app's ready; whether a build's been tried, or
	// the app closed; built, closed once the build under way has ended, nil
	// while none is; and sent, when the last notification was posted or
	// taken away.
	mu    sync.Mutex
	ready bool
	tried bool
	built chan struct{}
	sent  int64
	// runNo is this run's number among those that send notifications, once
	// it's been taken (numbered): "" when it couldn't be.
	runNo    string
	numbered sync.Once
}

// New returns kit.app in the data directory dir, run through run; a click
// on one of its notifications brings forward the app whose bundle
// identifier is activate, when it's not "". now is the time, which says
// what order the app's notifications were posted and taken away in.
func New(run runner.Runner, dir, activate string, now func() time.Time) *App {
	life, end := context.WithCancel(context.Background())
	return &App{run: run, dir: dir, activate: activate, now: now, life: life, end: end}
}

// Path is where the app's bundle is.
func (a *App) Path() string {
	return filepath.Join(a.dir, Name)
}

// Beside the app, it keeps the order of the notifications it's sent
// (kit.swift): marks, the folder where the newest sent under each id is
// kept, with, in it, the last run numbered (runs) and the runs ended
// (ended), the last keptEnded of them; and the lock that kit and the app's
// instances read and write them under, beside it, so that no folder going
// leaves two holding different locks.
const (
	marks     = "notices"
	runs      = "run"
	ended     = "ended"
	keptEnded = 32
	lock      = "notices.lock"
	lockTries = 50
	lockRetry = 10 * time.Millisecond
)

// Prepare readies the app as a run starts: the run numbered, after the
// last; and building the app aside, when it needs building, so that a
// notification posted later needn't wait as long for it.
func (a *App) Prepare() {
	a.number()
	a.building()
}

// number is this run's number among those that send notifications: one
// after the last, kept beside the app, taken the first time it's asked for.
// The app keeps one run's notifications in order after those of the runs
// before it, whatever time each was sent at, as a new Mac's clock can go
// back as it sets its time; and posts none a run sent once it's ended
// (Close). "" when it can't be kept, the app then keeping their order by
// time alone.
func (a *App) number() string {
	a.numbered.Do(func() {
		if n, err := nextRun(a.dir); err == nil {
			a.runNo = strconv.FormatInt(n, 10)
		}
	})
	return a.runNo
}

// nextRun keeps, in dir, the number of the next run to send notifications,
// one after the last kept there, and returns it: none, should the lock not
// be had (lockMarks).
func nextRun(dir string) (int64, error) {
	if err := os.MkdirAll(filepath.Join(dir, marks), 0o755); err != nil {
		return 0, fmt.Errorf("make %s: %w", marks, err)
	}
	unlock, err := lockMarks(dir)
	if err != nil {
		return 0, err
	}
	defer unlock()
	path := filepath.Join(dir, marks, runs)
	last, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return 0, fmt.Errorf("read the last run: %w", err)
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(string(last)), 10, 64)
	n++
	if err := replace(path, []byte(strconv.FormatInt(n, 10)+"\n")); err != nil {
		return 0, err
	}
	return n, nil
}

// endRun keeps, in dir, that the run numbered run has ended, with the
// others last ended, so that the app posts nothing it sent: under the lock,
// unless it can't be had, as an instance of the app holds it long, which
// risks no more than a run's end going unkept.
func endRun(dir, run string) error {
	if unlock, err := lockMarks(dir); err == nil {
		defer unlock()
	}
	path := filepath.Join(dir, marks, ended)
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read the runs ended: %w", err)
	}
	all := append(strings.Fields(string(data)), run)
	all = all[max(0, len(all)-keptEnded):]
	return replace(path, []byte(strings.Join(all, "\n")+"\n"))
}

// lockMarks takes the lock on the marks in dir, trying a while without
// waiting on it, as an instance of the app can hold it while macOS posts a
// notification: unlock lets it go, and an error says it couldn't be had.
func lockMarks(dir string) (unlock func(), err error) {
	f, err := os.OpenFile(filepath.Join(dir, lock), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", lock, err)
	}
	for try := 1; ; try++ {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				_ = f.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) || try == lockTries {
			_ = f.Close()
			return nil, fmt.Errorf("lock %s: %w", lock, err)
		}
		time.Sleep(lockRetry)
	}
}

// replace writes data to path whole, as a file renamed into its place, so
// that it's never read half written.
func replace(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// Ready reports whether the app's there to post with, building it first
// when it's missing or was built from other files, or waiting for the
// build under way; false once ctx is done, the build going on. A build is
// tried once a run at most; without Swift's compiler and the command line
// tools, as on a new Mac before they're installed, it isn't tried, and
// Ready looks again next time.
func (a *App) Ready(ctx context.Context) bool {
	ready, built := a.building()
	if built == nil {
		return ready
	}
	select {
	case <-built:
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.ready
	case <-ctx.Done():
		return false
	}
}

// Close stops a build under way, waiting till it has, and starts no other:
// what's posted after goes through osascript, unless the app was ready.
// The run, numbered, is kept as ended, and none of its notifications that
// the app has yet to post is posted; nor is it numbered once closed.
func (a *App) Close() {
	a.mu.Lock()
	a.tried = true
	built := a.built
	a.mu.Unlock()
	a.end()
	if built != nil {
		<-built
	}
	a.numbered.Do(func() {})
	if a.runNo != "" {
		_ = endRun(a.dir, a.runNo)
	}
}

// building reports whether the app's ready, else the build under way, if
// there is one, started now when the app needs building and a build can
// be tried.
func (a *App) building() (ready bool, built <-chan struct{}) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch {
	case a.ready || a.current():
		a.ready = true
		return true, nil
	case a.built != nil:
		return false, a.built
	case a.tried || !a.canBuild(a.life):
		return false, nil
	}
	a.tried = true
	done := make(chan struct{})
	a.built = done
	go func() {
		err := a.build(a.life)
		a.mu.Lock()
		a.ready, a.built = err == nil, nil
		a.mu.Unlock()
		close(done)
	}()
	return false, done
}

// current reports whether the bundle's there, built from the files
// embedded.
func (a *App) current() bool {
	built, err := os.ReadFile(filepath.Join(a.Path(), builtFrom))
	if err != nil || !bytes.Equal(built, fingerprint()) {
		return false
	}
	_, err = os.Stat(filepath.Join(a.Path(), executable))
	return err == nil
}

// canBuild reports whether Swift's compiler is there to build with: the one
// macOS keeps in /usr/bin asks to install the command line tools when
// they're not, so xcode-select must find them first.
func (a *App) canBuild(ctx context.Context) bool {
	if !runner.Has(a.run, "swiftc") {
		return false
	}
	_, err := a.run.Run(ctx, runner.Command{Name: "xcode-select", Args: []string{"-p"}})
	return err == nil
}

// build builds the bundle aside, in the data directory, and puts it in
// place once it's compiled and signed.
func (a *App) build(ctx context.Context) error {
	// Kept to one name, so a build cut short is cleared by the next.
	work := filepath.Join(a.dir, "."+Name+"-build")
	if err := os.RemoveAll(work); err != nil {
		return fmt.Errorf("clear %s: %w", work, err)
	}
	defer func() { _ = os.RemoveAll(work) }()
	bundle := filepath.Join(work, Name)
	src := filepath.Join(work, "kit.swift")
	writes := map[string][]byte{src: source, filepath.Join(bundle, builtFrom): fingerprint()}
	for path, data := range files {
		writes[filepath.Join(bundle, path)] = data
	}
	for path, data := range writes {
		if err := write(path, data); err != nil {
			return err
		}
	}
	exe := filepath.Join(bundle, executable)
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		return fmt.Errorf("make %s: %w", filepath.Dir(exe), err)
	}
	if _, err := a.run.Run(ctx, runner.Command{Name: "swiftc", Args: []string{"-O", "-o", exe, src}, Timeout: compiling}); err != nil {
		return fmt.Errorf("compile %s: %w", Name, err)
	}
	if _, err := a.run.Run(ctx, runner.Command{Name: "codesign", Args: []string{"-s", "-", "-f", bundle}}); err != nil {
		return fmt.Errorf("sign %s: %w", Name, err)
	}
	if err := os.RemoveAll(a.Path()); err != nil {
		return fmt.Errorf("remove the old %s: %w", Name, err)
	}
	if err := os.Rename(bundle, a.Path()); err != nil {
		return fmt.Errorf("put %s in place: %w", Name, err)
	}
	return nil
}

// write writes data to path, making its folder.
func write(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("make %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// Notify posts a notification under id, replacing any posted under it
// before: through the app, launched as macOS launches apps, else through
// osascript, which can't replace or take one away. Once ctx is done, as
// when it's been taken away while it waited for the app, it posts nothing;
// done already, it starts no build either.
// The app's told which run posted it, and when (--run and --at, sent), as
// clear is, as macOS can run two launched one after the other in either
// order, and the first time holds one up while it asks the person to allow
// them: the app does nothing for one older than the last under its id, nor
// for one of a run that's ended.
func (a *App) Notify(ctx context.Context, id, title, body string) {
	if ctx.Err() != nil {
		return
	}
	args := []string{"notify", "--id", id, "--title", title, "--body", body}
	if a.activate != "" {
		args = append(args, "--activate", a.activate)
	}
	at := a.stamp()
	if a.Ready(ctx) && ctx.Err() == nil && a.open(ctx, append(args, a.sentAt(at)...)...) == nil {
		return
	}
	quote := func(s string) string { return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"` }
	script := "display notification " + quote(body) + " with title " + quote("kit") + " subtitle " + quote(title)
	if ctx.Err() != nil {
		return
	}
	_, _ = a.run.Run(ctx, runner.Command{Name: "osascript", Args: []string{"-e", script}})
}

// Clear takes away the notification posted under id, when the app posted
// it.
func (a *App) Clear(ctx context.Context, id string) {
	a.mu.Lock()
	ready := a.ready
	a.mu.Unlock()
	if ready {
		_ = a.open(ctx, append([]string{"clear", "--id", id}, a.sentAt(a.stamp())...)...)
	}
}

// sentAt is what the app's told of when a notification was posted or taken
// away: at, and by which run, when it's numbered.
func (a *App) sentAt(at string) []string {
	if run := a.number(); run != "" {
		return []string{"--at", at, "--run", run}
	}
	return []string{"--at", at}
}

// stamp is when a notification is posted or taken away, in nanoseconds,
// each after the last.
func (a *App) stamp() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sent = max(a.now().UnixNano(), a.sent+1)
	return strconv.FormatInt(a.sent, 10)
}

// open launches a new instance of the app, in the background, with args.
func (a *App) open(ctx context.Context, args ...string) error {
	_, err := a.run.Run(ctx, runner.Command{Name: "open", Args: append([]string{"-g", "-n", a.Path(), "--args"}, args...)})
	return err
}
