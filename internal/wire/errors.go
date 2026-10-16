package wire

import "errors"

var (
	// ErrUnsupported is returned on platforms without Unix sockets.
	ErrUnsupported = errors.New("varlink: service not available on this host")
	// ErrFrameTooLarge is returned when a message exceeds the frame limit.
	ErrFrameTooLarge = errors.New("varlink: message exceeds frame size limit")
	// ErrTooManyFiles is returned when a message carries too many descriptors.
	ErrTooManyFiles = errors.New("varlink: too many file descriptors")
)
