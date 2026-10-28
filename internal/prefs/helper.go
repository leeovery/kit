package prefs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	return info["LSUIElement"] == true || info["LSBackgroundOnly"] == true
}

// readyHelper readies the windowless helper of bundle id id, at app, for
// its settings, as the person can't: opened out of view, sandboxed with no
// settings folder, till macOS makes it one; then quit, running, opened or
// not. What ensureContainer says of it after: one whose folder didn't come
// waits to be opened, as any app's; one that won't quit is found running
// still, and waits as any app does.
func (p *Prefs) readyHelper(ctx context.Context, id, app string) error {
	err := p.ensureContainer(ctx, id, app)
	if err != nil {
		_, _ = p.Run.Run(ctx, runner.Command{Name: "open", Args: []string{"-g", "-b", id}})
		if p.waitFor(helperFolder, func() bool { return p.hasFolder(id) }) {
			err = nil
		}
	}
	if p.running(ctx, id) {
		_, _ = p.Run.Run(ctx, runner.Command{Name: "osascript", Args: []string{"-e", fmt.Sprintf("quit app id %q", id)}})
		p.waitFor(helperQuits, func() bool { return !p.running(ctx, id) })
	}
	return err
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
