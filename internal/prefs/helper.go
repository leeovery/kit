package prefs

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/plist"
	"github.com/leeovery/kit/internal/runner"
)

// helperQuits is how long a helper kit quits has to stop; helperFolder,
// how long one kit opens has for macOS to make its settings folder; and
// helperPoll, how often kit looks meanwhile.
const (
	helperQuits  = 5 * time.Second
	helperFolder = 15 * time.Second
	helperPoll   = 500 * time.Millisecond
)

// errNoFolderYet is why a windowless helper's settings wait: kit opened it,
// and macOS made it no settings folder in time. The next pass opens it again.
var errNoFolderYet = errors.New("its settings folder hasn't come yet")

// windowless reports whether the app at app is a helper with no window: an
// app inside another's bundle whose Info.plist sets LSUIElement or
// LSBackgroundOnly, as login items do. The person can neither quit one nor
// open it to any end, so kit does both itself. A menu-bar app of its own,
// such as Amphetamine, isn't one: the person quits it from its menu.
func windowless(app string) bool {
	if _, ok := insideOf(app); !ok {
		return false
	}
	data, err := os.ReadFile(filepath.Join(app, "Contents", "Info.plist"))
	if err != nil {
		return false
	}
	v, _ := plist.Decode(data)
	info, _ := v.(map[string]any)
	return set(info["LSUIElement"]) || set(info["LSBackgroundOnly"])
}

// set reports whether an Info.plist's flag is on: true, or as older ones
// write it, 1, "1", "YES" or "true".
func set(v any) bool {
	switch v := v.(type) {
	case bool:
		return v
	case int64:
		return v == 1
	case string:
		return v == "1" || strings.EqualFold(v, "yes") || strings.EqualFold(v, "true")
	}
	return false
}

// readyHelpers readies each windowless helper among owners, of those
// installed, that this restore hasn't yet, recording in how what came of it.
func (p *Prefs) readyHelpers(ctx context.Context, owners []string, installed map[string]string, how restoring) {
	for _, id := range owners {
		if _, made := how.containers[id]; made || !windowless(installed[id]) {
			continue
		}
		restart, err := p.readyHelper(ctx, id, installed[id])
		how.containers[id], how.restart[id] = err, restart
	}
}

// readyHelper readies the windowless helper of bundle id id, at app, for
// its settings, as the person can't: opened out of view, sandboxed with no
// settings folder, till macOS makes it one; then quit, running, opened or
// not. Whether kit quit it, running before, to be started again once its
// settings are back; and errNoFolderYet if its folder didn't come. One that
// won't quit is found running still, and waits as any app does.
func (p *Prefs) readyHelper(ctx context.Context, id, app string) (bool, error) {
	was := p.running(ctx, id)
	err := p.ensureContainer(ctx, id, app)
	if err != nil {
		_, _ = p.Run.Run(ctx, runner.Command{Name: "open", Args: []string{"-g", "-b", id}})
		err = errNoFolderYet
		if p.waitFor(helperFolder, func() bool { return p.hasFolder(id) }) {
			err = nil
		}
	}
	quit := false
	if p.running(ctx, id) {
		_, _ = p.Run.Run(ctx, runner.Command{Name: "osascript", Args: []string{"-e", fmt.Sprintf("quit app id %q", id)}, Timeout: 10 * time.Second})
		quit = p.waitFor(helperQuits, func() bool { return !p.running(ctx, id) })
	}
	return was && quit, err
}

// restartHelpers starts again, out of view, each helper of restart kit
// quit that was running before, as a login item is, its settings back.
func (p *Prefs) restartHelpers(ctx context.Context, restart map[string]bool) {
	for _, id := range slices.Sorted(maps.Keys(restart)) {
		if restart[id] {
			_, _ = p.Run.Run(ctx, runner.Command{Name: "open", Args: []string{"-g", "-b", id}})
		}
	}
}

// waitFor reports whether ready comes true within limit, looked at every
// helperPoll.
func (p *Prefs) waitFor(limit time.Duration, ready func() bool) bool {
	for range limit / helperPoll {
		if ready() {
			return true
		}
		p.Sleep(helperPoll)
	}
	return ready()
}
