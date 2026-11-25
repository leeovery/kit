//go:build !darwin

package relay

import (
	"errors"
	"net"
)

// peerUID is the user the process at the other end of c runs as: unknown
// off macOS, so nothing is answered.
func peerUID(*net.UnixConn) (int, error) { return 0, errors.New("not on macOS") }
