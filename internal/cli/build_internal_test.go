package cli

import (
	"errors"
	"testing"

	"github.com/leeovery/kit/internal/event"
)

// closing is a face that notes it was closed.
type closing struct{ closed bool }

func (*closing) Emit(event.Event) {}
func (c *closing) Close() error   { c.closed = true; return nil }

// A run that can't be readied closes the face it was to be shown on, as
// soon as it fails.
func TestBuildFailingClosesItsFace(t *testing.T) {
	face := &closing{}
	a := &app{HomeDir: func() (string, error) { return "", errors.New("no home") }}
	if _, err := a.build("status", "", face, false); err == nil || !face.closed {
		t.Errorf("build() = %v, the face closed %v; want an error, the face closed", err, face.closed)
	}
}
