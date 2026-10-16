//go:build !unix

package varlink

import "syscall"

func errnoName(syscall.Errno) string { return "" }
