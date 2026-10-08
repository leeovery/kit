package askpass

import (
	"net"

	"golang.org/x/sys/unix"
)

// peerPID is the socket option naming the process at a Unix socket's other
// end: LOCAL_PEERPID in <sys/un.h>, at level SOL_LOCAL.
const peerPID = 0x2

// peer is the process at the other end of c.
func peer(c *net.UnixConn) (int, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, err
	}
	var pid int
	var peerErr error
	if err := raw.Control(func(fd uintptr) {
		pid, peerErr = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, peerPID)
	}); err != nil {
		return 0, err
	}
	return pid, peerErr
}

// parent is the process that started pid: 0 when it can't be found.
func parent(pid int) int {
	p, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0
	}
	return int(p.Eproc.Ppid)
}

// descends is whether pid was started by ancestor, or by a process it
// started, however far down.
func descends(pid, ancestor int) bool {
	for range 64 {
		switch {
		case pid == ancestor:
			return true
		case pid <= 1:
			return false
		}
		pid = parent(pid)
	}
	return false
}
