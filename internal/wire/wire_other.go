//go:build !unix

package wire

import (
	"net"
	"os"
)

// Supported reports whether this platform can carry Varlink connections.
const Supported = false

// Transport is unavailable on this platform.
type Transport struct{}

// New returns a Transport whose methods fail with ErrUnsupported.
func New(*net.UnixConn, int, int) *Transport { return &Transport{} }

func (*Transport) ReadMessage() ([]byte, []*os.File, error) { return nil, nil, ErrUnsupported }
func (*Transport) WriteMessage([]byte, []*os.File) error    { return ErrUnsupported }
func (*Transport) Close() error                             { return nil }

// CloseFiles closes every non-nil file.
func CloseFiles(files []*os.File) {
	for _, f := range files {
		if f != nil {
			f.Close()
		}
	}
}

// DupFile is unavailable on this platform.
func DupFile(*os.File) (*os.File, error) { return nil, ErrUnsupported }
