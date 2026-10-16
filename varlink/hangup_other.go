//go:build !unix

package varlink

import "net"

func hungUp(*net.UnixConn) bool { return true }
