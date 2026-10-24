//go:build unix

package varlink_test

import (
	"bufio"
	"net"
	"os"
	"slices"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/rossedman/sdvarlink/varlink"
)

// scriptedPeer returns a client connected to a peer that, once it has read
// one call, answers with replies verbatim. With fd set, the last reply also
// carries a descriptor.
func scriptedPeer(t *testing.T, replies []string, fd bool) *varlink.Conn {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	conn := func(fd int) *net.UnixConn {
		f := os.NewFile(uintptr(fd), "socketpair")
		defer f.Close()
		nc, err := net.FileConn(f)
		if err != nil {
			t.Fatal(err)
		}
		return nc.(*net.UnixConn)
	}
	client, peer := conn(fds[0]), conn(fds[1])
	t.Cleanup(func() { peer.Close() })
	go func() {
		if _, err := bufio.NewReader(peer).ReadString(0); err != nil {
			return
		}
		for i, r := range replies {
			var oob []byte
			if fd && i == len(replies)-1 {
				f, err := os.Open(os.DevNull)
				if err != nil {
					return
				}
				defer f.Close()
				oob = unix.UnixRights(int(f.Fd()))
			}
			if _, _, err := peer.WriteMsgUnix(append([]byte(r), 0), oob, nil); err != nil {
				return
			}
		}
	}()
	c := varlink.NewConn(client)
	t.Cleanup(func() { c.Close() })
	return c
}

// TestStreamEmptyFinalReply checks that the empty final reply sd-varlink
// sends to end a stream is not yielded, while every reply that carries
// something still is.
func TestStreamEmptyFinalReply(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		replies []string
		fd      bool
		want    []string // parameters of the yielded replies
	}{
		{name: "empty stream", replies: []string{`{}`}, want: nil},
		{name: "empty stream, parameters null", replies: []string{`{"parameters":null}`}, want: nil},
		{name: "empty stream, parameters {}", replies: []string{`{"parameters": { } }`}, want: nil},
		{
			name:    "values then empty final",
			replies: []string{`{"parameters":{"n":1},"continues":true}`, `{"parameters":{"n":2},"continues":true}`, `{}`},
			want:    []string{`{"n":1}`, `{"n":2}`},
		},
		{
			name:    "last value is final",
			replies: []string{`{"parameters":{"n":1},"continues":true}`, `{"parameters":{"n":2}}`},
			want:    []string{`{"n":1}`, `{"n":2}`},
		},
		{
			name:    "empty reply that continues is a value",
			replies: []string{`{"parameters":{},"continues":true}`, `{"parameters":{"n":1}}`},
			want:    []string{`{}`, `{"n":1}`},
		},
		{name: "empty final reply with a descriptor", replies: []string{`{}`}, fd: true, want: []string{``}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := scriptedPeer(t, tc.replies, tc.fd)
			var got []string
			for r, err := range c.Stream(t.Context(), "com.example.List", nil) {
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, string(r.Parameters))
				if len(r.Files) != 0 {
					if !tc.fd {
						t.Errorf("unexpected descriptors: %v", r.Files)
					}
					r.Close()
				}
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("yielded %q, want %q", got, tc.want)
			}
			// The stream ended cleanly, so the connection is still usable.
			if err := c.Err(); err != nil {
				t.Errorf("connection closed: %v", err)
			}
		})
	}
}
