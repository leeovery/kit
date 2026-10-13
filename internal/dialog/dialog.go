// Package dialog is macOS asking the person at the Mac something, in a
// window of its own: Gatekeeper, before a program from the internet first
// opens; an administrator's password; a permission, as for one app to
// control another. The command that brought it waits on the answer, which
// only the person at the Mac can give.
package dialog

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/leeovery/kit/internal/runner"
)

// askers are the programs macOS asks in, each with what it asks.
var askers = map[string]string{
	"CoreServicesUIAgent":    "asking whether to open a program from the internet",
	"SecurityAgent":          "asking for an administrator's password",
	"UserNotificationCenter": "asking for a permission",
}

// owners lists the programs whose windows are on screen, a line each, as
// the window server has them: windows' owners need no permission to see, as
// their titles would.
const owners = `ObjC.import("CoreGraphics");
ObjC.deepUnwrap(ObjC.castRefToObject($.CGWindowListCopyWindowInfo($.kCGWindowListOptionOnScreenOnly, 0))).map(function (w) { return w.kCGWindowOwnerName }).join("\n")`

// Asking is what macOS is asking, as "asking for a permission", or nothing
// when no window of its asking is on screen.
func Asking(ctx context.Context, run runner.Runner) string {
	res, err := run.Run(ctx, runner.Command{Name: "osascript", Args: []string{"-l", "JavaScript", "-e", owners}, Timeout: 10 * time.Second})
	if err != nil {
		return ""
	}
	for owner := range strings.Lines(string(res.Stdout)) {
		if what, ok := askers[strings.TrimSpace(owner)]; ok {
			return what
		}
	}
	return ""
}

// every is how often a watch looks.
var every = 2 * time.Second

// stays is how long a window must stay on screen, seen on every look, before
// a watch tells of it: CoreServicesUIAgent also shows a "Verifying…" window
// as a new program first opens, which closes by itself.
var stays = 5 * time.Second

// Watch is what macOS is asking, looked at every few seconds.
type Watch struct {
	mu   sync.Mutex
	seen string
}

// Up is whether a window of macOS's asking was on screen at the last look:
// from the first look that saw it, before it's stayed long enough to be
// told of, so the time limits it holds up wait from the start.
func (w *Watch) Up() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.seen != ""
}

// Run looks till ctx is done, telling changed what macOS is asking each time
// that changes: once its window has stayed on screen, seen on every look,
// for stays; nothing, once it's gone, when it was told of.
func (w *Watch) Run(ctx context.Context, run runner.Runner, changed func(what string)) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	need := int((stays + every - 1) / every)
	told, looks := "", 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		what := Asking(ctx, run)
		w.mu.Lock()
		if what == w.seen {
			looks++
		} else {
			w.seen, looks = what, 0
		}
		w.mu.Unlock()
		if looks < need {
			what = ""
		}
		if what != told && ctx.Err() == nil {
			told = what
			changed(what)
		}
	}
}
