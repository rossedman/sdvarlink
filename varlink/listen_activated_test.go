//go:build unix

package varlink_test

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/rossedman/sdvarlink/varlink"
)

// activatedChildEnv marks the re-executed test binary that plays the
// socket-activated daemon in TestListenActivatedPassedSockets.
const activatedChildEnv = "SDVARLINK_PASSED_SOCKETS_CHILD"

// TestListenActivatedPassedSockets passes two descriptors to a child copy of
// this test binary the way systemd does (fd 3 onwards, $LISTEN_FDS,
// $LISTEN_FDNAMES), without needing systemd: a Unix stream listener named
// "api" and a datagram socket named "dgram".
func TestListenActivatedPassedSockets(t *testing.T) {
	if os.Getenv(activatedChildEnv) == "1" {
		passedSocketsChild(t)
		return
	}
	t.Parallel()
	path := sockPath(t)
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	lf, err := l.File()
	if err != nil {
		t.Fatal(err)
	}
	defer lf.Close()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_DGRAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	dgram := os.NewFile(uintptr(fds[0]), "dgram")
	defer dgram.Close()
	defer syscall.Close(fds[1])

	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestListenActivatedPassedSockets$", "-test.v")
	cmd.ExtraFiles = []*os.File{lf, dgram} // fd 3 and fd 4 in the child
	cmd.Env = append(os.Environ(),
		activatedChildEnv+"=1",
		"LISTEN_FDS=2",
		"LISTEN_FDNAMES=api:dgram",
		"SDVARLINK_TEST_SOCKET="+path,
	)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "--- PASS: TestListenActivatedPassedSockets") {
		t.Fatalf("child: %v\n%s", err, out)
	}
}

// passedSocketsChild runs in the child process. LISTEN_PID can only be set
// here, once the process exists.
func passedSocketsChild(t *testing.T) {
	t.Setenv("LISTEN_PID", strconv.Itoa(os.Getpid()))

	tests := []struct {
		name    string
		fdnames string // "" leaves LISTEN_FDNAMES as the parent set it
		lookup  string
		wantMsg string
	}{
		{name: "unnamed with two sockets", lookup: "", wantMsg: "2 sockets were passed"},
		{name: "unknown name", lookup: "metrics", wantMsg: `no activated socket named "metrics"`},
		{name: "name count mismatch", fdnames: "api", lookup: "api", wantMsg: "LISTEN_FDNAMES has 1 names for 2 sockets"},
		{name: "datagram socket", lookup: "dgram", wantMsg: "is not a stream socket"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.fdnames != "" {
				t.Setenv("LISTEN_FDNAMES", tc.fdnames)
			}
			l, err := varlink.ListenActivated(tc.lookup)
			if err == nil {
				l.Close()
				t.Fatal("ListenActivated succeeded")
			}
			if errors.Is(err, varlink.ErrNotActivated) || !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("err = %v, want containing %q", err, tc.wantMsg)
			}
		})
	}

	l, err := varlink.ListenActivated("api")
	if err != nil {
		t.Fatal(err)
	}
	path := os.Getenv("SDVARLINK_TEST_SOCKET")
	if got := l.Addr().String(); got != path {
		t.Errorf("Addr = %q, want %q", got, path)
	}
	// The adopted listener accepts connections made to the parent's path.
	accepted := make(chan error, 1)
	go func() {
		c, err := l.Accept()
		if err == nil {
			c.Close()
		}
		accepted <- err
	}()
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if err := <-accepted; err != nil {
		t.Fatalf("Accept = %v", err)
	}
	if _, err := varlink.ListenActivated("api"); err == nil || !strings.Contains(err.Error(), "already adopted") {
		t.Errorf("second adoption = %v, want already adopted", err)
	}
	// The socket file belongs to systemd: closing must leave it in place.
	l.Close()
	if _, err := os.Lstat(path); err != nil {
		t.Errorf("socket file after Close: %v", err)
	}
}
