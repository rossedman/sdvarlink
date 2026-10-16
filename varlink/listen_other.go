//go:build !unix

package varlink

import "net"

// Listen is unavailable on this platform.
func Listen(string, ...ListenOption) (*net.UnixListener, error) { return nil, ErrUnsupported }

// ListenActivated is unavailable on this platform.
func ListenActivated(string) (*net.UnixListener, error) { return nil, ErrNotActivated }
