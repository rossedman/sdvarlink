//go:build !linux

package varlink

import "net"

func peerCredentials(*net.UnixConn) (Peer, error) { return Peer{}, ErrUnsupported }
