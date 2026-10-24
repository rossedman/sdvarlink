package varlink

import (
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"
)

type timeoutErr struct{ timeout bool }

func (e timeoutErr) Error() string   { return "net error" }
func (e timeoutErr) Timeout() bool   { return e.timeout }
func (e timeoutErr) Temporary() bool { return false }

var _ net.Error = timeoutErr{}

func TestTemporaryAcceptError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "closed", err: net.ErrClosed, want: false},
		{name: "wrapped closed", err: fmt.Errorf("accept: %w", net.ErrClosed), want: false},
		{name: "EMFILE", err: syscall.EMFILE, want: true},
		{name: "ENFILE", err: syscall.ENFILE, want: true},
		{name: "ENOBUFS", err: syscall.ENOBUFS, want: true},
		{name: "ENOMEM", err: syscall.ENOMEM, want: true},
		{name: "ECONNABORTED", err: syscall.ECONNABORTED, want: true},
		{name: "EINTR", err: syscall.EINTR, want: true},
		{name: "wrapped EMFILE", err: &net.OpError{Op: "accept", Err: syscall.EMFILE}, want: true},
		{name: "EACCES", err: syscall.EACCES, want: false},
		{name: "timeout", err: timeoutErr{timeout: true}, want: true},
		{name: "non-timeout net error", err: timeoutErr{}, want: false},
		{name: "plain", err: errors.New("x"), want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := temporaryAcceptError(tc.err); got != tc.want {
				t.Errorf("temporaryAcceptError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
