//go:build unix

package varlink

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Listen binds a Unix socket at path for Serve. It creates missing parent
// directories (mode 0755), replaces a stale socket file left by a previous
// run, refuses a path where another server is listening or that is not a
// socket, and sets the socket's mode (default 0660) and optional owner.
// The socket file is removed when the listener closes.
func Listen(path string, opts ...ListenOption) (*net.UnixListener, error) {
	cfg := listenConfig{mode: 0o660, uid: -1, gid: -1}
	for _, opt := range opts {
		opt(&cfg)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("varlink: %s exists and is not a socket", path)
		}
		c, derr := net.DialTimeout("unix", path, time.Second)
		if derr == nil {
			c.Close()
			return nil, fmt.Errorf("varlink: %s: %w", path, syscall.EADDRINUSE)
		}
		if !errors.Is(derr, syscall.ECONNREFUSED) {
			return nil, fmt.Errorf("varlink: checking %s: %w", path, derr)
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	}
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	l.SetUnlinkOnClose(true)
	if err := os.Chmod(path, cfg.mode); err != nil {
		l.Close()
		return nil, err
	}
	if cfg.uid != -1 || cfg.gid != -1 {
		if err := os.Lchown(path, cfg.uid, cfg.gid); err != nil {
			l.Close()
			return nil, err
		}
	}
	return l, nil
}

const listenFDsStart = 3

// ListenActivated returns a listening socket passed by systemd socket
// activation ($LISTEN_FDS, $LISTEN_FDNAMES). With name "", exactly one
// socket must have been passed. Otherwise it returns the socket whose
// FileDescriptorName= (in the .socket unit) is name, so one daemon can
// serve several sockets. It returns ErrNotActivated when the process was
// not socket-activated. The socket file belongs to systemd and is left in
// place when the listener closes.
func ListenActivated(name string) (*net.UnixListener, error) {
	pid, err := strconv.Atoi(os.Getenv("LISTEN_PID"))
	if err != nil || pid != os.Getpid() {
		return nil, ErrNotActivated
	}
	n, err := strconv.Atoi(os.Getenv("LISTEN_FDS"))
	if err != nil || n <= 0 {
		return nil, ErrNotActivated
	}
	names := strings.Split(os.Getenv("LISTEN_FDNAMES"), ":")
	idx := -1
	switch {
	case name == "" && n == 1:
		idx = 0
	case name == "":
		return nil, fmt.Errorf("varlink: %d sockets were passed; pick one by FileDescriptorName", n)
	default:
		for i := 0; i < n && i < len(names); i++ {
			if names[i] == name {
				idx = i
				break
			}
		}
		if idx < 0 {
			return nil, fmt.Errorf("varlink: no activated socket named %q (have %q)", name, os.Getenv("LISTEN_FDNAMES"))
		}
	}
	fd := listenFDsStart + idx
	unix.CloseOnExec(fd)
	// FileListener duplicates the descriptor; close ours afterwards.
	f := os.NewFile(uintptr(fd), "LISTEN_FD_"+strconv.Itoa(fd))
	l, err := net.FileListener(f)
	f.Close()
	if err != nil {
		return nil, fmt.Errorf("varlink: activated fd %d: %w", fd, err)
	}
	ul, ok := l.(*net.UnixListener)
	if !ok {
		l.Close()
		return nil, fmt.Errorf("varlink: activated fd %d is not a Unix socket", fd)
	}
	ul.SetUnlinkOnClose(false)
	return ul, nil
}
