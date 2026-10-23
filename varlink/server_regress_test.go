package varlink_test

// Regression tests for findings from the server review.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/rossedman/sdvarlink/varlink"
)

const regressIDL = `interface com.example.R

method BadParams() -> ()
method NoName() -> ()
# [Requires 'more' flag]
method Leaky() -> (n: int)
method Echo(text: string, count: ?int) -> (text: string)
method TooManyFiles() -> ()
`

// serveR starts a server with handlers registered by reg and returns its
// socket path.
func serveR(t *testing.T, reg func(*varlink.Interface)) string {
	t.Helper()
	srv := varlink.NewServer(varlink.ServerInfo{}, varlink.WithLogger(discardLogger()))
	reg(srv.Interface("com.example.R", regressIDL))
	path := sockPath(t)
	l, err := varlink.Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { srv.Serve(ctx, l); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return path
}

func echoHandler(iface *varlink.Interface) {
	varlink.Handle(iface, "Echo", func(ctx context.Context, call *varlink.Call, in srvEchoIn) (srvEchoOut, error) {
		return srvEchoOut{Text: in.Text}, nil
	})
}

// rawExchange writes calls on a raw connection and reads n NUL-terminated
// replies, failing if more arrive within settle.
func rawExchange(t *testing.T, path string, calls string, n int, settle time.Duration, halfClose bool) []string {
	t.Helper()
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Write([]byte(calls)); err != nil {
		t.Fatal(err)
	}
	if halfClose {
		c.(*net.UnixConn).CloseWrite()
	}
	var buf []byte
	tmp := make([]byte, 4096)
	deadline := time.Now().Add(5 * time.Second)
	for strings.Count(string(buf), "\x00") < n {
		c.SetReadDeadline(deadline)
		k, err := c.Read(tmp)
		if err != nil {
			t.Fatalf("after %q: %v", buf, err)
		}
		buf = append(buf, tmp[:k]...)
	}
	// Nothing more may follow.
	c.SetReadDeadline(time.Now().Add(settle))
	if k, _ := c.Read(tmp); k > 0 {
		buf = append(buf, tmp[:k]...)
	}
	parts := strings.Split(strings.TrimSuffix(string(buf), "\x00"), "\x00")
	if len(parts) != n {
		t.Fatalf("got %d replies, want %d: %q", len(parts), n, parts)
	}
	return parts
}

// Bad error parameters must not cost the call its reply, which would
// shift every later reply onto the wrong call.
func TestServerBadErrorParametersStillReply(t *testing.T) {
	path := serveR(t, func(iface *varlink.Interface) {
		echoHandler(iface)
		varlink.Handle(iface, "BadParams", func(context.Context, *varlink.Call, struct{}) (struct{}, error) {
			return struct{}{}, &varlink.Error{Name: "com.example.R.Oops", Parameters: json.RawMessage("not json")}
		})
	})
	replies := rawExchange(t, path,
		`{"method":"com.example.R.BadParams"}`+"\x00"+`{"method":"com.example.R.Echo","parameters":{"text":"after"}}`+"\x00",
		2, 100*time.Millisecond, false)
	if !strings.Contains(replies[0], `"error":"com.example.R.Oops"`) || !strings.Contains(replies[1], `"after"`) {
		t.Fatalf("replies = %q", replies)
	}
}

// An *Error without a name must not be sent as a success.
func TestServerNamelessErrorIsNotSuccess(t *testing.T) {
	path := serveR(t, func(iface *varlink.Interface) {
		varlink.Handle(iface, "NoName", func(context.Context, *varlink.Call, struct{}) (struct{}, error) {
			return struct{}{}, &varlink.Error{}
		})
	})
	c, _ := varlink.Dial(context.Background(), path)
	defer c.Close()
	if err := c.Call(context.Background(), "com.example.R.NoName", nil, nil); !errors.Is(err, syscall.EIO) {
		t.Fatalf("err = %v", err)
	}
}

// A send from a goroutine the handler left behind must not reach the wire.
func TestServerSendAfterReturnIsRejected(t *testing.T) {
	late := make(chan error, 1)
	path := serveR(t, func(iface *varlink.Interface) {
		echoHandler(iface)
		varlink.HandleStream(iface, "Leaky", func(ctx context.Context, call *varlink.Call, _ struct{}, send func(nOut) error) error {
			go func() {
				time.Sleep(50 * time.Millisecond)
				late <- send(nOut{N: 99})
			}()
			return errors.New("give up")
		})
	})
	replies := rawExchange(t, path,
		`{"method":"com.example.R.Leaky","more":true}`+"\x00"+`{"method":"com.example.R.Echo","parameters":{"text":"next"}}`+"\x00",
		2, 200*time.Millisecond, false)
	if !strings.Contains(replies[0], "io.systemd.System") || !strings.Contains(replies[1], `"next"`) {
		t.Fatalf("replies = %q", replies)
	}
	if err := <-late; err == nil {
		t.Fatal("late send succeeded")
	}
}

// A client that half-closes after its last call still gets the replies.
func TestServerHalfCloseStillServesQueuedCalls(t *testing.T) {
	path := serveR(t, echoHandler)
	replies := rawExchange(t, path,
		`{"method":"com.example.R.Echo","parameters":{"text":"a"}}`+"\x00"+`{"method":"com.example.R.Echo","parameters":{"text":"b"}}`+"\x00",
		2, 50*time.Millisecond, true)
	if replies[0] != `{"parameters":{"text":"a"}}` || replies[1] != `{"parameters":{"text":"b"}}` {
		t.Fatalf("replies = %q", replies)
	}
}

// A reply carrying more files than allowed becomes an error reply; the
// connection survives.
func TestServerTooManyReplyFiles(t *testing.T) {
	type out struct {
		A *os.File `json:"-" varlink:"a,fd"`
		B *os.File `json:"-" varlink:"b,fd"`
	}
	const idl = "interface com.example.F\nmethod Two() -> (a: int, b: int)\nmethod Echo(text: string, count: ?int) -> (text: string)\n"
	srv := varlink.NewServer(varlink.ServerInfo{}, varlink.WithServerMaxFiles(1), varlink.WithLogger(discardLogger()))
	iface := srv.Interface("com.example.F", idl)
	varlink.Handle(iface, "Two", func(context.Context, *varlink.Call, struct{}) (out, error) {
		a, _ := os.Open(os.DevNull)
		b, _ := os.Open(os.DevNull)
		return out{A: a, B: b}, nil
	})
	varlink.Handle(iface, "Echo", func(_ context.Context, _ *varlink.Call, in srvEchoIn) (srvEchoOut, error) {
		return srvEchoOut{Text: in.Text}, nil
	})
	path := sockPath(t)
	l, _ := varlink.Listen(path)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx, l)
	c, _ := varlink.Dial(ctx, path)
	defer c.Close()
	if err := c.Call(ctx, "com.example.F.Two", nil, nil); !errors.Is(err, syscall.EIO) {
		t.Fatalf("err = %v", err)
	}
	if err := c.Call(ctx, "com.example.F.Echo", srvEchoIn{Text: "ok"}, nil); err != nil {
		t.Fatalf("connection lost: %v", err)
	}
}

func TestRegistrationAcceptsCustomJSONTypes(t *testing.T) {
	const idl = "interface com.example.T\nmethod M(when: string, n: int, f: float, extra: ?object) -> (big: int)\nmethod Raw(a: string) -> ()\n"
	srv := varlink.NewServer(varlink.ServerInfo{})
	iface := srv.Interface("com.example.T", idl)
	type in struct {
		When  time.Time      `json:"when"`
		N     json.Number    `json:"n"`
		F     json.Number    `json:"f"`
		Extra map[string]any `json:"extra,omitempty"`
	}
	type out struct {
		Big *big.Int `json:"big"`
	}
	varlink.Handle(iface, "M", func(context.Context, *varlink.Call, in) (out, error) { return out{}, nil })
	varlink.Handle(iface, "Raw", func(context.Context, *varlink.Call, map[string]any) (struct{}, error) { return struct{}{}, nil })
}

func TestRegistrationRejectsRepliesThatCanOmitRequiredFields(t *testing.T) {
	const idl = "interface com.example.T\nmethod A() -> (n: int)\nmethod B() -> (s: string)\nmethod C() -> (s: ?string)\n"
	srv := varlink.NewServer(varlink.ServerInfo{})
	iface := srv.Interface("com.example.T", idl)
	mustPanic := func(name string, f func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Errorf("%s: registered", name)
			}
		}()
		f()
	}
	mustPanic("omitempty", func() {
		varlink.Handle(iface, "A", func(context.Context, *varlink.Call, struct{}) (struct {
			N int64 `json:"n,omitempty"`
		}, error) {
			return struct {
				N int64 `json:"n,omitempty"`
			}{}, nil
		})
	})
	mustPanic("pointer", func() {
		varlink.Handle(iface, "B", func(context.Context, *varlink.Call, struct{}) (struct {
			S *string `json:"s"`
		}, error) {
			return struct {
				S *string `json:"s"`
			}{}, nil
		})
	})
	// Nullable fields may be either.
	varlink.Handle(iface, "C", func(context.Context, *varlink.Call, struct{}) (struct {
		S *string `json:"s,omitempty"`
	}, error) {
		return struct {
			S *string `json:"s,omitempty"`
		}{}, nil
	})
}

// Two servers starting at once on the same path: exactly one wins.
func TestListenConcurrentStarts(t *testing.T) {
	path := sockPath(t)
	var wg sync.WaitGroup
	results := make(chan error, 8)
	listeners := make(chan net.Listener, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, err := varlink.Listen(path)
			if err == nil {
				// Keep serving so the socket counts as live.
				go func() {
					for {
						c, err := l.Accept()
						if err != nil {
							return
						}
						c.Close()
					}
				}()
				listeners <- l
			}
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	close(listeners)
	ok := 0
	for err := range results {
		if err == nil {
			ok++
		} else if !errors.Is(err, syscall.EADDRINUSE) {
			t.Errorf("unexpected error: %v", err)
		}
	}
	for l := range listeners {
		l.Close()
	}
	if ok != 1 {
		t.Fatalf("%d servers listened on one path", ok)
	}
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestListenAddrIsTheSocketPath(t *testing.T) {
	path := sockPath(t)
	l, err := varlink.Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if got := l.Addr().String(); got != path {
		t.Fatalf("Addr = %s, want %s", got, path)
	}
}
