package relay

import (
	"net"

	"golang.org/x/sys/unix"
)

// peerCred is the socket option naming the credentials of the process at a
// Unix socket's other end: LOCAL_PEERCRED in <sys/un.h>, at level
// SOL_LOCAL.
const peerCred = 0x1

// peerUID is the user the process at the other end of c runs as.
func peerUID(c *net.UnixConn) (int, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, err
	}
	var cred *unix.Xucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, peerCred)
	}); err != nil {
		return 0, err
	}
	if credErr != nil {
		return 0, credErr
	}
	return int(cred.Uid), nil
}
