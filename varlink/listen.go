package varlink

import (
	"errors"
	"os"
)

// ErrNotActivated is returned by ListenActivated when the process was not
// started by systemd socket activation, so the caller can fall back to
// Listen:
//
//	l, err := varlink.ListenActivated("")
//	if errors.Is(err, varlink.ErrNotActivated) {
//		l, err = varlink.Listen("/run/queued/io.example.Queue")
//	}
var ErrNotActivated = errors.New("varlink: not socket-activated")

// ListenOption configures Listen.
type ListenOption func(*listenConfig)

type listenConfig struct {
	mode     os.FileMode
	uid, gid int
}

// WithSocketMode sets the socket file's permissions (default 0660).
func WithSocketMode(mode os.FileMode) ListenOption {
	return func(c *listenConfig) { c.mode = mode }
}

// WithSocketOwner sets the socket file's owner and group; -1 leaves one
// unchanged.
func WithSocketOwner(uid, gid int) ListenOption {
	return func(c *listenConfig) { c.uid, c.gid = uid, gid }
}
