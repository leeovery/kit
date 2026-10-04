package render

import (
	"encoding/json"
	"io"

	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/status"
)

// JSON is the face for --json: nothing as the run goes, and the status
// document at its end; nothing at all when no run started, as for a command
// that prints its own document.
type JSON struct {
	w       io.Writer
	builder status.Builder
	started bool
}

// NewJSON returns the JSON face, writing to w.
func NewJSON(w io.Writer) *JSON {
	return &JSON{w: w}
}

func (j *JSON) Emit(e event.Event) {
	if _, ok := e.(event.RunStarted); ok {
		j.started = true
	}
	j.builder.Emit(e)
}

// Close writes the document, when a run started.
func (j *JSON) Close() error {
	if !j.started {
		return nil
	}
	return WriteJSON(j.w, j.builder.Document())
}

// WriteJSON writes v to w as indented JSON, as every --json does.
func WriteJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
