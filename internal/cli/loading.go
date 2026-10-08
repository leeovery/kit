package cli

import (
	"io"
	"sync"
	"time"

	"github.com/leeovery/kit/internal/look"
)

// loading shows, at a terminal, that kit is busy and what with: a turning
// mark and words, on a line drawn over itself until stop, which can be
// called again, takes it down.
func (a *app) loading(out io.Writer, what string) (stop func()) {
	if !a.pretty(out) {
		return func() {}
	}
	w := a.colors(out)
	// A blank line sets the loader apart from what's above it; what follows
	// takes the loader's place, after it.
	_, _ = io.WriteString(w, "\n")
	done, finished := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		tick := time.NewTicker(120 * time.Millisecond)
		defer tick.Stop()
		for i := 0; ; i++ {
			_, _ = io.WriteString(w, "\r\x1b[2K  "+look.Cyan(string([]rune("◐◓◑◒")[i%4]))+" "+look.Muted(what))
			select {
			case <-done:
				_, _ = io.WriteString(w, "\r\x1b[2K")
				return
			case <-tick.C:
			}
		}
	}()
	return sync.OnceFunc(func() {
		close(done)
		<-finished
	})
}
