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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

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
	// life is what a build runs in, done once the app's closed.
	life context.Context
	end  context.CancelFunc

	// mu guards whether the app's ready; whether a build's been tried, or
	// the app closed; and built, closed once the build under way has
	// ended, nil while none is.
	mu    sync.Mutex
	ready bool
	tried bool
	built chan struct{}
}

// New returns kit.app in the data directory dir, run through run; a click
// on one of its notifications brings forward the app whose bundle
// identifier is activate, when it's not "".
func New(run runner.Runner, dir, activate string) *App {
	life, end := context.WithCancel(context.Background())
	return &App{run: run, dir: dir, activate: activate, life: life, end: end}
}

// Path is where the app's bundle is.
func (a *App) Path() string {
	return filepath.Join(a.dir, Name)
}

// Prepare starts building the app aside, when it needs building, so that
// a notification posted later needn't wait as long for it.
func (a *App) Prepare() { a.building() }

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
	if _, err := a.run.Run(ctx, runner.Command{Name: "swiftc", Args: []string{"-O", "-o", exe, src}}); err != nil {
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
// when it's been taken away while it waited for the app, it posts nothing.
func (a *App) Notify(ctx context.Context, id, title, body string) {
	args := []string{"notify", "--id", id, "--title", title, "--body", body}
	if a.activate != "" {
		args = append(args, "--activate", a.activate)
	}
	posted := a.Ready(ctx) && ctx.Err() == nil && a.open(ctx, args...) == nil
	if posted || ctx.Err() != nil {
		return
	}
	quote := func(s string) string { return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"` }
	script := "display notification " + quote(body) + " with title " + quote("kit") + " subtitle " + quote(title)
	_, _ = a.run.Run(ctx, runner.Command{Name: "osascript", Args: []string{"-e", script}})
}

// Clear takes away the notification posted under id, when the app posted
// it.
func (a *App) Clear(ctx context.Context, id string) {
	a.mu.Lock()
	ready := a.ready
	a.mu.Unlock()
	if ready {
		_ = a.open(ctx, "clear", "--id", id)
	}
}

// open launches a new instance of the app, in the background, with args.
func (a *App) open(ctx context.Context, args ...string) error {
	_, err := a.run.Run(ctx, runner.Command{Name: "open", Args: append([]string{"-g", "-n", a.Path(), "--args"}, args...)})
	return err
}
