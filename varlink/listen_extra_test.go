//go:build unix

package varlink_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/rossedman/sdvarlink/varlink"
)

func TestListenRefusals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		setup   func(t *testing.T, path string)
		wantIs  error
		wantMsg string
	}{
		{
			name: "regular file in the way",
			setup: func(t *testing.T, path string) {
				if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantMsg: "exists and is not a socket",
		},
		{
			name: "live socket",
			setup: func(t *testing.T, path string) {
				l, err := varlink.Listen(path)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { l.Close() })
			},
			wantIs: syscall.EADDRINUSE,
		},
		{
			name: "parent is a file",
			setup: func(t *testing.T, path string) {
				if err := os.WriteFile(filepath.Dir(path), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantIs: syscall.ENOTDIR,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// sockPath, not t.TempDir: the test name would push the path
			// past the socket path limit on macOS (104 bytes).
			path := sockPath(t)
			if tc.name == "parent is a file" {
				path = filepath.Join(filepath.Dir(path), "blocker", "sock")
			}
			tc.setup(t, path)
			l, err := varlink.Listen(path)
			if err == nil {
				l.Close()
				t.Fatal("Listen succeeded")
			}
			if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
				t.Errorf("err = %v, want %v", err, tc.wantIs)
			}
			if tc.wantMsg != "" && !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("err = %v, want containing %q", err, tc.wantMsg)
			}
		})
	}
}

func TestListenCloseKeepsReplacedSocket(t *testing.T) {
	t.Parallel()
	path := sockPath(t)
	l, err := varlink.Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := l.Addr().String(); got != path {
		t.Errorf("Addr = %q, want %q", got, path)
	}
	// Someone else replaced the file: Close must not remove it.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("other"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("replacement removed on Close: %v", err)
	}
}
