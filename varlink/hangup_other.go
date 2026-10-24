//go:build !unix

package varlink

import "net"

// hungUp reports that the peer is gone; without poll support the connection is
// always treated as hung up.
func hungUp(*net.UnixConn) bool { return true }
