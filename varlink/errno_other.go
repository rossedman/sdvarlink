//go:build !unix

package varlink

import "syscall"

// errnoName returns "" on platforms without errno names.
func errnoName(syscall.Errno) string { return "" }
