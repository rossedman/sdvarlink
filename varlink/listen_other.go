//go:build !unix

package varlink

import "net"

// Listen is unavailable on this platform.
func Listen(string, ...ListenOption) (net.Listener, error) { return nil, ErrUnsupported }

// ListenActivated is unavailable on this platform.
func ListenActivated(string) (net.Listener, error) { return nil, ErrNotActivated }
