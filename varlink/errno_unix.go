//go:build unix

package varlink

import (
	"syscall"

	"golang.org/x/sys/unix"
)

func errnoName(e syscall.Errno) string { return unix.ErrnoName(e) }
