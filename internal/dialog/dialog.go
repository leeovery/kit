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

// Watch is what macOS is asking, looked at every few seconds.
type Watch struct {
	mu   sync.Mutex
	what string
}

// Up is whether macOS is asking something.
func (w *Watch) Up() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.what != ""
}

// Run looks till ctx is done, telling changed what macOS is asking each time
// that changes: nothing, once it's been answered.
func (w *Watch) Run(ctx context.Context, run runner.Runner, changed func(what string)) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		what := Asking(ctx, run)
		w.mu.Lock()
		was := w.what
		w.what = what
		w.mu.Unlock()
		if what != was && ctx.Err() == nil {
			changed(what)
		}
	}
}
