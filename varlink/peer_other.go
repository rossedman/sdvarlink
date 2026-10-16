//go:build !linux

package varlink

import (
	"net"
	"syscall"
)

func peerCredentials(*net.UnixConn) (Peer, error) { return Peer{}, ErrUnsupported }

func errnoName(syscall.Errno) string { return "" }
