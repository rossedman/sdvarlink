//go:build !linux

package varlink

import "net"

// peerCredentials is not supported on this platform and returns
// ErrUnsupported.
func peerCredentials(*net.UnixConn) (Peer, error) { return Peer{}, ErrUnsupported }
