// Package macapp is kit's own Mac app, kit.app: what posts kit's
// notifications, under kit's name and icon, each replacing or taking away
// the one posted before under its id; and what the hourly launch opens to
// run kit nightly, so that Full Disk Access is the app's. A release ships
// it beside kit, built once and signed by a Developer ID, and kit copies it
// into its data directory; a kit without one, as one built by hand, builds
// it from the files it embeds. Either is done the first time a run needs it
// and again once what it's made from has changed; until it can be, a
// notification goes through osascript instead, never lost.
package macapp

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/leeovery/kit/internal/plist"
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
	// signedBy holds, in a release's build, the Developer ID team it's
	// signed by, sealed with the rest.
	signedBy = "Contents/Resources/team"
)

// files are the bundle's files, by their path in it, but its executable.
var files = map[string][]byte{
	"Contents/Info.plist":             infoPlist,
	"Contents/Resources/AppIcon.icns": icon,
}

// Fingerprint is the fingerprint of the files kit builds the app from.
func Fingerprint() string {
	h := sha256.New()
	for _, part := range [][]byte{source, infoPlist, icon} {
		h.Write(fmt.Appendf(nil, "%d\n", len(part)))
		h.Write(part)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Identity is what macOS grants Full Disk Access to, for the app in the
// data directory dir: "" when there's none. A release's build keeps its
// grant across kit's updates, as macOS ties it to the team that signed it,
// so that team is its identity; to macOS, one the Mac built itself, signed
// ad hoc, is another app once it's built from other files, so the
// fingerprint of those is.
func Identity(dir string) string {
	if team := read(filepath.Join(dir, Name, signedBy)); team != "" {
		return "team:" + team
	}
	return read(filepath.Join(dir, Name, builtFrom))
}

// read is what the file at path holds, trimmed: "" when there's none.
func read(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// Lay lays the app's bundle out at bundle, all but its executable,
// compiled from kit.swift into the folder made for it: its Info.plist,
// carrying version when it's not "", its icon, and the fingerprint of the
// files it's built from; and team, when it's not "", the Developer ID team
// a release's build is to be signed by. Apple reads a bundle's version as
// numbers and dots alone, so a pre-release's is its release's.
func Lay(bundle, version, team string) error {
	writes := map[string][]byte{builtFrom: []byte(Fingerprint() + "\n")}
	maps.Copy(writes, files)
	if version != "" {
		release, _, _ := strings.Cut(version, "-")
		info, err := versioned(release)
		if err != nil {
			return err
		}
		writes["Contents/Info.plist"] = info
	}
	if team != "" {
		writes[signedBy] = []byte(team + "\n")
	}
	for path, data := range writes {
		if err := write(filepath.Join(bundle, path), data); err != nil {
			return err
		}
	}
	folder := filepath.Dir(filepath.Join(bundle, executable))
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return fmt.Errorf("make %s: %w", folder, err)
	}
	return nil
}

// versioned is the Info.plist kit embeds, carrying version.
func versioned(version string) ([]byte, error) {
	v, err := plist.Decode(infoPlist)
	if err != nil {
		return nil, fmt.Errorf("read %s's Info.plist: %w", Name, err)
	}
	info, _ := v.(map[string]any)
	info["CFBundleVersion"], info["CFBundleShortVersionString"] = version, version
	data, err := plist.Encode(info)
	if err != nil {
		return nil, fmt.Errorf("write %s's Info.plist: %w", Name, err)
	}
	return data, nil
}

// Shipped is the kit.app a release ships with the kit at self, its links
// followed: in the libexec folder beside the kit's own, where Homebrew and
// install.sh put it. "" when there's none, as beside a kit built by hand.
func Shipped(self string) string {
	resolved, err := filepath.EvalSymlinks(self)
	if err != nil {
		return ""
	}
	app := filepath.Join(filepath.Dir(filepath.Dir(resolved)), "libexec", Name)
	if _, err := os.Stat(filepath.Join(app, executable)); err != nil {
		return ""
	}
	return app
}

// App is kit.app, as a run uses it: made, when it needs to be, once a run
// at most, aside, so that a wait for it can be given up.
type App struct {
	run runner.Runner
	dir string
	// from is what the app is made from.
	from origin
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
}

// New returns kit.app in the data directory dir, run through run: copied
// from shipped, the app a release ships beside kit (Shipped), else, when
// that's "", built from the files kit embeds. A click on one of its
// notifications brings forward the app whose bundle identifier is
// activate, when it's not "". now is the time, which says what order the
// app's notifications were posted and taken away in.
func New(run runner.Runner, dir, shipped, activate string, now func() time.Time) *App {
	var from origin = compiled{run: run}
	if shipped != "" {
		from = copied{run: run, from: shipped}
	}
	life, end := context.WithCancel(context.Background())
	return &App{run: run, dir: dir, from: from, activate: activate, now: now, life: life, end: end}
}

// Path is where the app's bundle is.
func (a *App) Path() string {
	return filepath.Join(a.dir, Name)
}

// marks is the folder, beside the app, where the app keeps the order of the
// notifications it's sent (kit.swift).
const marks = "notices"

// Prepare readies the app as a run starts: the order of the notifications
// the last run sent forgotten, as each was taken away by its end, and the
// app's clock may have gone back since, as a new Mac's can before it sets
// its time; and making it aside, when it needs making, so that a
// notification posted later needn't wait as long for it.
func (a *App) Prepare() {
	_ = os.RemoveAll(filepath.Join(a.dir, marks))
	a.building()
}

// Ready reports whether the app's there to post with, making it first
// when it's missing or not what this kit makes, or waiting for the build
// under way; false once ctx is done, the build going on. A build is tried
// once a run at most. One from the files kit embeds isn't tried without
// Swift's compiler and the command line tools, as on a new Mac before
// they're installed, and Ready looks again next time.
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
func (a *App) Close() {
	a.mu.Lock()
	a.tried = true
	built := a.built
	a.mu.Unlock()
	a.end()
	if built != nil {
		<-built
	}
}

// building reports whether the app's ready, else the build under way, if
// there is one, started now when the app needs making and a build can be
// tried.
func (a *App) building() (ready bool, built <-chan struct{}) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch {
	case a.ready || a.Current():
		a.ready = true
		return true, nil
	case a.built != nil:
		return false, a.built
	case a.tried || !a.from.can(a.life):
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

// Current reports whether the bundle's there, made as this kit makes it:
// a copy of the app shipped beside it, else built from the files it
// embeds.
func (a *App) Current() bool {
	return a.from.made(a.Path())
}

// build makes the bundle aside, in the data directory, and puts it in place
// once it's whole.
func (a *App) build(ctx context.Context) error {
	// Kept to one name, so a build cut short is cleared by the next.
	work := filepath.Join(a.dir, "."+Name+"-build")
	if err := os.RemoveAll(work); err != nil {
		return fmt.Errorf("clear %s: %w", work, err)
	}
	defer func() { _ = os.RemoveAll(work) }()
	if err := os.MkdirAll(work, 0o755); err != nil {
		return fmt.Errorf("make %s: %w", work, err)
	}
	bundle := filepath.Join(work, Name)
	if err := a.from.make(ctx, work, bundle); err != nil {
		return err
	}
	if err := os.RemoveAll(a.Path()); err != nil {
		return fmt.Errorf("remove the old %s: %w", Name, err)
	}
	if err := os.Rename(bundle, a.Path()); err != nil {
		return fmt.Errorf("put %s in place: %w", Name, err)
	}
	return nil
}

// origin is what the app's made from.
type origin interface {
	// made reports whether the bundle at path was made from it, as it is.
	made(path string) bool
	// can reports whether the bundle can be made from it now.
	can(ctx context.Context) bool
	// make makes the bundle at bundle, in the folder work.
	make(ctx context.Context, work, bundle string) error
}

// compiled is the app built on the Mac from the files kit embeds, signed
// ad hoc.
type compiled struct {
	run runner.Runner
}

func (c compiled) made(path string) bool {
	if read(filepath.Join(path, builtFrom)) != Fingerprint() {
		return false
	}
	_, err := os.Stat(filepath.Join(path, executable))
	return err == nil
}

// can reports whether Swift's compiler is there to build with: the one
// macOS keeps in /usr/bin asks to install the command line tools when
// they're not, so xcode-select must find them first.
func (c compiled) can(ctx context.Context) bool {
	if !runner.Has(c.run, "swiftc") {
		return false
	}
	_, err := c.run.Run(ctx, runner.Command{Name: "xcode-select", Args: []string{"-p"}})
	return err == nil
}

func (c compiled) make(ctx context.Context, work, bundle string) error {
	src := filepath.Join(work, "kit.swift")
	if err := write(src, source); err != nil {
		return err
	}
	if err := Lay(bundle, "", ""); err != nil {
		return err
	}
	exe := filepath.Join(bundle, executable)
	if _, err := c.run.Run(ctx, runner.Command{Name: "swiftc", Args: []string{"-O", "-o", exe, src}, Timeout: compiling}); err != nil {
		return fmt.Errorf("compile %s: %w", Name, err)
	}
	if _, err := c.run.Run(ctx, runner.Command{Name: "codesign", Args: []string{"-s", "-", "-f", bundle}}); err != nil {
		return fmt.Errorf("sign %s: %w", Name, err)
	}
	return nil
}

// copied is the app a release ships, built and signed once, copied as it
// is.
type copied struct {
	run  runner.Runner
	from string
}

func (c copied) made(path string) bool {
	have, err := digest(path)
	if err != nil {
		return false
	}
	want, err := digest(c.from)
	return err == nil && have == want
}

func (copied) can(context.Context) bool { return true }

// make copies the app with ditto, which keeps the extended attributes and
// the rest a copy of the files alone could lose of what codesign sealed,
// then checks the copy's signature.
func (c copied) make(ctx context.Context, _, bundle string) error {
	if _, err := c.run.Run(ctx, runner.Command{Name: "ditto", Args: []string{c.from, bundle}}); err != nil {
		return fmt.Errorf("copy %s: %w", c.from, err)
	}
	if _, err := c.run.Run(ctx, runner.Command{Name: "codesign", Args: []string{"--verify", "--strict", bundle}}); err != nil {
		return fmt.Errorf("check the signature of %s, copied from %s: %w", Name, c.from, err)
	}
	return nil
}

// digest is the digest of the bundle at path: each file's path in it, and
// what it holds.
func digest(path string) (string, error) {
	h := sha256.New()
	err := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(path, p)
		if err != nil {
			return err
		}
		h.Write(fmt.Appendf(nil, "%s\n%d\n", rel, len(data)))
		h.Write(data)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
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
// The app's told when it was sent (--at), as clear is, as macOS can run two
// launched one after the other in either order, and the first time holds
// one up while it asks the person to allow them: the app does nothing for
// one older than the last under its id.
func (a *App) Notify(ctx context.Context, id, title, body string) {
	if ctx.Err() != nil {
		return
	}
	args := []string{"notify", "--id", id, "--title", title, "--body", body}
	if a.activate != "" {
		args = append(args, "--activate", a.activate)
	}
	at := a.stamp()
	if a.Ready(ctx) && ctx.Err() == nil && a.open(ctx, append(args, "--at", at)...) == nil {
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
		_ = a.open(ctx, "clear", "--id", id, "--at", a.stamp())
	}
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
