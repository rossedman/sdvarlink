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
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Listen binds a Unix socket at path for Serve. It creates missing parent
// directories (mode 0755), replaces a stale socket file left by a previous
// run, refuses a path where another server is listening or that is not a
// socket, and sets the socket's mode (default 0660) and optional owner.
// The socket file is removed when the listener closes.
//
// The socket is created with its final mode and owner inside a private
// directory and then renamed into place, so it is never connectable with
// looser permissions, and a lock file next to it serializes concurrent
// starts so one server cannot replace another's live socket.
func Listen(path string, opts ...ListenOption) (net.Listener, error) {
	cfg := listenConfig{mode: 0o660, uid: -1, gid: -1}
	for _, opt := range opts {
		opt(&cfg)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	unlock, err := lockSocketPath(path)
	if err != nil {
		return nil, err
	}
	defer unlock()

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
		// Stale: rename below replaces it atomically.
	}

	tmpDir, err := os.MkdirTemp(dir, ".varlink-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)
	tmp := filepath.Join(tmpDir, "s")
	ul, err := net.ListenUnix("unix", &net.UnixAddr{Name: tmp, Net: "unix"})
	if err != nil {
		return nil, err
	}
	ul.SetUnlinkOnClose(false)
	fail := func(err error) (net.Listener, error) {
		ul.Close()
		return nil, err
	}
	if err := os.Chmod(tmp, cfg.mode); err != nil {
		return fail(err)
	}
	if cfg.uid != -1 || cfg.gid != -1 {
		if err := os.Lchown(tmp, cfg.uid, cfg.gid); err != nil {
			return fail(err)
		}
	}
	if err := os.Rename(tmp, path); err != nil {
		return fail(err)
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return fail(err)
	}
	return &pathListener{UnixListener: ul, path: path, file: fi}, nil
}

// pathListener removes its socket file on Close, if the file is still the
// one it created.
type pathListener struct {
	*net.UnixListener
	path string
	file os.FileInfo
	once sync.Once
}

// Addr reports the socket's final path, not the one it was created at.
func (l *pathListener) Addr() net.Addr { return &net.UnixAddr{Name: l.path, Net: "unix"} }

func (l *pathListener) Close() error {
	err := l.UnixListener.Close()
	l.once.Do(func() {
		if fi, serr := os.Lstat(l.path); serr == nil && os.SameFile(fi, l.file) {
			os.Remove(l.path)
		}
	})
	return err
}

// lockSocketPath takes an exclusive lock on a lock file next to path.
func lockSocketPath(path string) (func(), error) {
	lockPath := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".lock")
	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
		f.Close() // closing releases the lock too
	}, nil
}

const listenFDsStart = 3

// ListenActivated returns a listening socket passed by systemd socket
// activation ($LISTEN_FDS, $LISTEN_FDNAMES). With name "", exactly one
// socket must have been passed. Otherwise it returns the socket whose
// FileDescriptorName= (in the .socket unit) is name, so one daemon can
// serve several sockets. It returns ErrNotActivated when the process was
// not socket-activated. The socket file belongs to systemd and is left in
// place when the listener closes. Each socket can be adopted once.
func ListenActivated(name string) (net.Listener, error) {
	pid, err := strconv.Atoi(os.Getenv("LISTEN_PID"))
	if err != nil || pid != os.Getpid() {
		return nil, ErrNotActivated
	}
	n, err := strconv.Atoi(os.Getenv("LISTEN_FDS"))
	if err != nil || n <= 0 {
		return nil, ErrNotActivated
	}
	var names []string
	if v, ok := os.LookupEnv("LISTEN_FDNAMES"); ok {
		names = strings.Split(v, ":")
		if len(names) != n {
			return nil, fmt.Errorf("varlink: LISTEN_FDNAMES has %d names for %d sockets", len(names), n)
		}
	}
	// Passed descriptors must not leak into child processes.
	for i := 0; i < n; i++ {
		unix.CloseOnExec(listenFDsStart + i)
	}
	idx := -1
	switch {
	case name == "" && n == 1:
		idx = 0
	case name == "":
		return nil, fmt.Errorf("varlink: %d sockets were passed; pick one by FileDescriptorName", n)
	default:
		for i, nm := range names {
			if nm == name {
				idx = i
				break
			}
		}
		if idx < 0 {
			return nil, fmt.Errorf("varlink: no activated socket named %q (have %q)", name, os.Getenv("LISTEN_FDNAMES"))
		}
	}
	fd := listenFDsStart + idx

	adoptedMu.Lock()
	defer adoptedMu.Unlock()
	if adopted[fd] {
		return nil, fmt.Errorf("varlink: activated socket %d was already adopted", fd)
	}
	if typ, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_TYPE); err != nil || typ != unix.SOCK_STREAM {
		return nil, fmt.Errorf("varlink: activated fd %d is not a stream socket", fd)
	}
	// FileListener duplicates the descriptor. The original stays open so
	// its number cannot be reused and mistaken for the socket later.
	l, err := net.FileListener(os.NewFile(uintptr(fd), "LISTEN_FD_"+strconv.Itoa(fd)))
	if err != nil {
		return nil, fmt.Errorf("varlink: activated fd %d: %w", fd, err)
	}
	ul, ok := l.(*net.UnixListener)
	if !ok {
		l.Close()
		return nil, fmt.Errorf("varlink: activated fd %d is not a Unix socket", fd)
	}
	ul.SetUnlinkOnClose(false)
	adopted[fd] = true
	return ul, nil
}

var (
	adoptedMu sync.Mutex
	adopted   = map[int]bool{}
)
