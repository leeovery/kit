package relay

import (
	"encoding/json"
	"net"
	"testing"
	"time"
)

// SetOwner has r answer the processes of the user uid alone.
func SetOwner(r *Relay, uid int) { r.owner = uid }

// NewLine is a line to the relay at socket, for the store whose ID is
// store, as Holds gives one.
func NewLine(socket, store string) *Line { return &Line{socket: socket, store: store} }

// SetWaits has clients wait ack for a relay to take a request up, and
// answer for any answer, and relays wait request for a client's request,
// till t ends: set before a relay serves, so it's put back once the relay's
// done.
func SetWaits(t testing.TB, ack, answer, request time.Duration) {
	was, wasRequest := waits, requestWait
	waits.ack, waits.holds, waits.stop, waits.answer = ack, answer, answer, answer
	requestWait = request
	t.Cleanup(func() { waits, requestWait = was, wasRequest })
}

// WriteAck writes, at c, a relay taking a request up.
func WriteAck(c net.Conn) error { return json.NewEncoder(c).Encode(response{Ack: true}) }
