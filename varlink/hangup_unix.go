//go:build unix

package varlink

import (
	"net"

	"golang.org/x/sys/unix"
)

// hungUp reports whether the peer closed the connection entirely, as
// opposed to only shutting down its write side (half-close), after which
// it still reads replies.
func hungUp(c *net.UnixConn) bool {
	rc, err := c.SyscallConn()
	if err != nil {
		return true
	}
	gone := true
	_ = rc.Control(func(fd uintptr) {
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLOUT}}
		if n, err := unix.Poll(fds, 0); err == nil && n >= 0 {
			gone = fds[0].Revents&(unix.POLLHUP|unix.POLLERR) != 0
		}
	})
	return gone
}
