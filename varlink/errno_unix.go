//go:build unix

package varlink

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// errnoName returns the symbolic name of e, such as "ENOENT".
func errnoName(e syscall.Errno) string { return unix.ErrnoName(e) }
