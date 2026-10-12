package cli

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/event"
)

// lockedBuffer is a buffer a face writes from its goroutines.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Should kit panic with applying's screen up, the terminal's put back
// before the panic shows: its own screen, the cursor shown, the keys no
// longer read, so the terminal's out of its raw mode; and the panic goes
// on as it was.
func TestAPanicPutsTheTerminalBack(t *testing.T) {
	var out lockedBuffer
	var mu sync.Mutex
	raw := false
	isRaw := func() bool {
		mu.Lock()
		defer mu.Unlock()
		return raw
	}
	listen := func() (<-chan ask.Key, func()) {
		mu.Lock()
		defer mu.Unlock()
		raw = true
		keys := make(chan ask.Key)
		return keys, sync.OnceFunc(func() {
			mu.Lock()
			defer mu.Unlock()
			raw = false
			close(keys)
		})
	}
	root := NewRootCommand(Deps{
		Version: "0.1.0", Getenv: func(string) string { return "" }, Environ: func() []string { return []string{"TTY_FORCE=1", "TERM=xterm-256color"} },
		Stdout: &out, Stderr: io.Discard, Terminal: func(io.Writer) bool { return true },
		Width: func(io.Writer) int { return 160 }, Height: func(io.Writer) int { return 64 }, Listen: listen,
	})
	a := root.ErrOrStderr().(*errOut).a
	face := a.face()
	_, halt := a.stoppable(t.Context(), face)
	defer halt()
	face.Emit(event.RunStarted{Command: "apply", Machine: "laptop", Steps: []event.Step{{Name: "brew", Title: "Formulae", Area: "Drift"}}})
	if !strings.Contains(out.String(), "\x1b[?1049h") || !isRaw() {
		t.Fatal("applying's screen isn't up, reading keys")
	}
	root.AddCommand(&cobra.Command{Use: "boom", RunE: func(*cobra.Command, []string) error { panic("boom") }})
	defer func() {
		p := recover()
		if err := face.Close(); err != nil {
			t.Error(err)
		}
		if p != "boom" {
			t.Errorf("kit panicked with %v, want the panic as it was, boom", p)
		}
		if s := out.String(); !strings.HasSuffix(s, "\x1b[?1049l\x1b[?25h") || isRaw() {
			t.Errorf("kit panicked, leaving %q, raw: %v; want the terminal's own screen back, the cursor shown, not raw", s[max(len(s)-40, 0):], isRaw())
		}
	}()
	Execute(t.Context(), root, []string{"boom"})
	t.Error("kit panicked, and Execute returned")
}
