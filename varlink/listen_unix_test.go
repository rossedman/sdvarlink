//go:build unix

package varlink_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/rossedman/sdvarlink/varlink"
)

// A path that fits the socket path limit but leaves no room for Listen's
// private directory is bound in place, with the requested mode.
func TestListenLongPath(t *testing.T) {
	base, err := os.MkdirTemp("", "vl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	limit := len(unix.RawSockaddrUnix{}.Path) - 1
	// The private directory goes next to the socket, so a long directory
	// with a short name is what leaves no room for it.
	path := filepath.Join(base, strings.Repeat("d", limit-len(base)-3), "s")

	l, err := varlink.Listen(path, varlink.WithSocketMode(0o600))
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSocket == 0 || fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, want socket 0600", fi.Mode())
	}
	srv := varlink.NewServer(varlink.ServerInfo{Product: "long"})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { srv.Serve(ctx, l); close(done) }()
	c, err := varlink.Dial(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	var info struct{ Product string }
	if err := c.Call(ctx, "org.varlink.service.GetInfo", nil, &info); err != nil || info.Product != "long" {
		t.Fatalf("GetInfo: %+v, %v", info, err)
	}
	c.Close()
	cancel()
	<-done
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("socket left behind: %v", err)
	}

	// One byte over the limit is refused up front.
	if _, err := varlink.Listen(path + "x"); !errors.Is(err, syscall.EINVAL) {
		t.Fatalf("Listen over the limit: %v, want EINVAL", err)
	}
}
