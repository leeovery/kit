//go:build !darwin

package askpass

import (
	"errors"
	"net"
)

// peer is the process at the other end of c: unknown off macOS, so nothing
// is answered.
func peer(*net.UnixConn) (int, error) { return 0, errors.New("not on macOS") }

func parent(int) int { return 0 }

func descends(int, int) bool { return false }
