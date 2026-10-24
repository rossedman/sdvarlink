package varlink_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/rossedman/sdvarlink/varlink"
)

const testIDL = `# Test interface.
interface com.example.Test

type Color(red, green)

type Item(
	name: string,
	color: ?Color
)

method Echo(text: string, count: ?int) -> (text: string)

method Fail(kind: string) -> ()

# [Requires 'more' flag]
method Ticks(n: int) -> (n: int)

# [Supports 'more' flag]
method Items(n: int) -> (item: Item)

method Who() -> (uid: int, gid: int, pid: int)

method Slow() -> ()

method Unimplemented() -> ()

error Broken(reason: string)
`

type srvEchoIn struct {
	Text  string `json:"text"`
	Count *int64 `json:"count,omitempty"`
}

type srvEchoOut struct {
	Text string `json:"text"`
}

type failIn struct {
	Kind string `json:"kind"`
}

type nIn struct {
	N int64 `json:"n"`
}

type nOut struct {
	N int64 `json:"n"`
}

type item struct {
	Name  string  `json:"name"`
	Color *string `json:"color,omitempty"`
}

type itemOut struct {
	Item item `json:"item"`
}

type whoOut struct {
	UID int64 `json:"uid"`
	GID int64 `json:"gid"`
	PID int64 `json:"pid"`
}

type brokenError struct {
	Reason string `json:"reason"`
}

func (e *brokenError) Error() string        { return "broken: " + e.Reason }
func (e *brokenError) VarlinkError() string { return "com.example.Test.Broken" }

type testServer struct {
	srv    *varlink.Server
	path   string
	cancel context.CancelFunc
	done   chan struct{}
	err    error // Serve's result, valid after done is closed
	logs   *syncBuffer
	live   chan nOut // feeds the live Ticks stream when n == 0
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func sockPath(t testing.TB) string {
	dir, err := os.MkdirTemp("", "vls")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "sock")
}

func startServer(t *testing.T, opts ...varlink.ServerOption) *testServer {
	t.Helper()
	logs := &syncBuffer{}
	opts = append([]varlink.ServerOption{varlink.WithLogger(slog.New(slog.NewTextHandler(logs, nil)))}, opts...)
	ts := &testServer{
		srv:  varlink.NewServer(varlink.ServerInfo{Vendor: "Test", Product: "testd", Version: "1", URL: "https://example.com"}, opts...),
		logs: logs,
		live: make(chan nOut),
	}
	iface := ts.srv.Interface("com.example.Test", testIDL)
	varlink.Handle(iface, "Echo", func(ctx context.Context, call *varlink.Call, in srvEchoIn) (srvEchoOut, error) {
		n := int64(1)
		if in.Count != nil {
			n = *in.Count
		}
		return srvEchoOut{Text: strings.Repeat(in.Text, int(n))}, nil
	})
	varlink.Handle(iface, "Fail", func(ctx context.Context, call *varlink.Call, in failIn) (struct{}, error) {
		switch in.Kind {
		case "typed":
			return struct{}{}, &brokenError{Reason: "on purpose"}
		case "varlink":
			return struct{}{}, &varlink.Error{Name: "com.example.Test.Broken", Parameters: json.RawMessage(`{"reason":"raw"}`)}
		case "denied":
			return struct{}{}, varlink.ErrPermissionDenied
		case "errno":
			return struct{}{}, fmt.Errorf("open: %w", syscall.ENOENT)
		case "panic":
			panic("boom")
		default:
			return struct{}{}, errors.New("secret internal detail")
		}
	})
	varlink.HandleStream(iface, "Ticks", func(ctx context.Context, call *varlink.Call, in nIn, send func(nOut) error) error {
		if in.N == 0 { // live: forward until the client or server goes away
			for {
				select {
				case v := <-ts.live:
					if err := send(v); err != nil {
						return err
					}
				case <-ctx.Done():
					return nil
				}
			}
		}
		for i := int64(1); i <= in.N; i++ {
			if err := send(nOut{N: i}); err != nil {
				return err
			}
		}
		return nil
	})
	varlink.HandleStream(iface, "Items", func(ctx context.Context, call *varlink.Call, in nIn, send func(itemOut) error) error {
		for i := int64(1); i <= in.N; i++ {
			if err := send(itemOut{Item: item{Name: fmt.Sprint("item", i)}}); err != nil {
				return err
			}
		}
		return nil
	}, varlink.LastIsFinal(), varlink.AllowSingle())
	varlink.Handle(iface, "Who", func(ctx context.Context, call *varlink.Call, _ struct{}) (whoOut, error) {
		p, err := call.Peer()
		return whoOut{UID: int64(p.UID), GID: int64(p.GID), PID: int64(p.PID)}, err
	})
	varlink.Handle(iface, "Slow", func(ctx context.Context, call *varlink.Call, _ struct{}) (struct{}, error) {
		select {
		case <-time.After(300 * time.Millisecond):
		case <-ctx.Done():
			return struct{}{}, ctx.Err()
		}
		return struct{}{}, nil
	})

	ts.path = sockPath(t)
	l, err := varlink.Listen(ts.path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ts.cancel = cancel
	ts.done = make(chan struct{})
	go func() { ts.err = ts.srv.Serve(ctx, l); close(ts.done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-ts.done:
		case <-time.After(10 * time.Second):
			t.Error("Serve did not return")
		}
	})
	return ts
}

func (ts *testServer) dial(t *testing.T) *varlink.Conn {
	t.Helper()
	c, err := varlink.Dial(context.Background(), ts.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestServerCall(t *testing.T) {
	ts := startServer(t)
	c := ts.dial(t)
	var out srvEchoOut
	if err := c.Call(context.Background(), "com.example.Test.Echo", srvEchoIn{Text: "ab", Count: varlink.Ptr(int64(3))}, &out); err != nil {
		t.Fatal(err)
	}
	if out.Text != "ababab" {
		t.Fatalf("out = %+v", out)
	}
}

func TestServerPipelining(t *testing.T) {
	// Several calls written before reading any reply come back in order.
	ts := startServer(t)
	nc, err := net.Dial("unix", ts.path)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	var batch bytes.Buffer
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&batch, `{"method":"com.example.Test.Echo","parameters":{"text":"%d"}}`+"\x00", i)
	}
	if _, err := nc.Write(batch.Bytes()); err != nil {
		t.Fatal(err)
	}
	nc.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for strings.Count(string(buf), "\x00") < 20 {
		n, err := nc.Read(tmp)
		if err != nil {
			t.Fatal(err)
		}
		buf = append(buf, tmp[:n]...)
	}
	for i, msg := range strings.Split(strings.TrimSuffix(string(buf), "\x00"), "\x00") {
		want := fmt.Sprintf(`{"parameters":{"text":"%d"}}`, i)
		if msg != want {
			t.Fatalf("reply %d = %s, want %s", i, msg, want)
		}
	}
}

func TestServerBuiltinErrors(t *testing.T) {
	ts := startServer(t)
	c := ts.dial(t)
	ctx := context.Background()
	cases := []struct {
		method string
		in     any
		want   string
		param  string
	}{
		{"com.example.Nope.X", nil, "org.varlink.service.InterfaceNotFound", `{"interface":"com.example.Nope"}`},
		{"com.example.Test.Nope", nil, "org.varlink.service.MethodNotFound", `{"method":"com.example.Test.Nope"}`},
		{"com.example.Test.Unimplemented", nil, "org.varlink.service.MethodNotImplemented", `{"method":"com.example.Test.Unimplemented"}`},
		{"com.example.Test.Echo", map[string]any{}, "org.varlink.service.InvalidParameter", `{"parameter":"text"}`},
		{"com.example.Test.Echo", map[string]any{"text": 5}, "org.varlink.service.InvalidParameter", `{"parameter":"text"}`},
		{"com.example.Test.Echo", map[string]any{"text": "a", "count": 1.5}, "org.varlink.service.InvalidParameter", `{"parameter":"count"}`},
		{"com.example.Test.Echo", map[string]any{"text": "a", "extra": true}, "org.varlink.service.InvalidParameter", `{"parameter":"extra"}`},
		{"com.example.Test.Ticks", map[string]any{"n": 1}, "org.varlink.service.ExpectedMore", ``},
	}
	for _, tc := range cases {
		err := c.Call(ctx, tc.method, tc.in, nil)
		var ve *varlink.Error
		if !errors.As(err, &ve) || ve.Name != tc.want {
			t.Errorf("%s(%v): err = %v, want %s", tc.method, tc.in, err, tc.want)
			continue
		}
		if tc.param != "" && string(ve.Parameters) != tc.param {
			t.Errorf("%s: parameters = %s, want %s", tc.method, ve.Parameters, tc.param)
		}
	}
	if c.Err() != nil {
		t.Fatalf("error replies broke the connection: %v", c.Err())
	}
}

func TestServerHandlerErrors(t *testing.T) {
	ts := startServer(t)
	c := ts.dial(t)
	ctx := context.Background()

	err := c.Call(ctx, "com.example.Test.Fail", failIn{"typed"}, nil)
	var ve *varlink.Error
	if !errors.As(err, &ve) || ve.Name != "com.example.Test.Broken" || string(ve.Parameters) != `{"reason":"on purpose"}` {
		t.Fatalf("typed: %v", err)
	}
	err = c.Call(ctx, "com.example.Test.Fail", failIn{"varlink"}, nil)
	if !errors.As(err, &ve) || string(ve.Parameters) != `{"reason":"raw"}` {
		t.Fatalf("varlink: %v", err)
	}
	if err := c.Call(ctx, "com.example.Test.Fail", failIn{"denied"}, nil); !errors.Is(err, varlink.ErrPermissionDenied) {
		t.Fatalf("denied: %v", err)
	}
	if err := c.Call(ctx, "com.example.Test.Fail", failIn{"errno"}, nil); !errors.Is(err, syscall.ENOENT) {
		t.Fatalf("errno: %v", err)
	}
	// Other errors become EIO without leaking their text, and are logged.
	err = c.Call(ctx, "com.example.Test.Fail", failIn{"other"}, nil)
	var se *varlink.SystemError
	if !errors.As(err, &se) || se.ErrnoName != "EIO" || strings.Contains(err.Error(), "secret") {
		t.Fatalf("other: %v", err)
	}
	if !strings.Contains(ts.logs.String(), "secret internal detail") {
		t.Fatal("handler error was not logged")
	}
	// A panic is recovered: the client gets EIO and the server keeps going.
	if err := c.Call(ctx, "com.example.Test.Fail", failIn{"panic"}, nil); !errors.Is(err, syscall.EIO) {
		t.Fatalf("panic: %v", err)
	}
	if !strings.Contains(ts.logs.String(), "boom") {
		t.Fatal("panic was not logged")
	}
	if err := c.Call(ctx, "com.example.Test.Echo", srvEchoIn{Text: "still up"}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestServerStream(t *testing.T) {
	ts := startServer(t)
	c := ts.dial(t)
	var got []int64
	for r, err := range c.Stream(context.Background(), "com.example.Test.Ticks", nIn{N: 3}) {
		if err != nil {
			t.Fatal(err)
		}
		var v nOut
		r.Decode(&v)
		got = append(got, v.N)
	}
	// Three values; the empty final reply that ends a live stream is not one.
	if fmt.Sprint(got) != "[1 2 3]" {
		t.Fatalf("got %v", got)
	}
}

func TestServerLastIsFinal(t *testing.T) {
	ts := startServer(t)
	c := ts.dial(t)
	var names []string
	var finals int
	for r, err := range c.Stream(context.Background(), "com.example.Test.Items", nIn{N: 3}) {
		if err != nil {
			t.Fatal(err)
		}
		var v itemOut
		r.Decode(&v)
		names = append(names, v.Item.Name)
		if !r.Continues {
			finals++
		}
	}
	if fmt.Sprint(names) != "[item1 item2 item3]" || finals != 1 {
		t.Fatalf("names %v, finals %d", names, finals)
	}
	// AllowSingle: without "more" the first value is the only reply.
	var one itemOut
	if err := c.Call(context.Background(), "com.example.Test.Items", nIn{N: 3}, &one); err != nil || one.Item.Name != "item1" {
		t.Fatalf("single: %+v, %v", one, err)
	}
}

func TestServerLiveStreamDeliversEventsPromptly(t *testing.T) {
	ts := startServer(t)
	c := ts.dial(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		for i := int64(1); i <= 3; i++ {
			select {
			case ts.live <- nOut{N: i}:
			case <-ctx.Done():
				return
			}
		}
	}()
	n := 0
	for r, err := range c.Stream(ctx, "com.example.Test.Ticks", nIn{N: 0}) {
		if err != nil {
			t.Fatal(err)
		}
		var v nOut
		r.Decode(&v)
		n++
		if v.N != int64(n) || !r.Continues {
			t.Fatalf("reply %d = %+v (continues %v)", n, v, r.Continues)
		}
		if n == 3 {
			break // each event arrived without waiting for the next
		}
	}
}

func TestServerClientDisconnectCancelsHandler(t *testing.T) {
	ts := startServer(t)
	c := ts.dial(t)
	go func() { ts.live <- nOut{N: 1} }()
	for range c.Stream(context.Background(), "com.example.Test.Ticks", nIn{N: 0}) {
		break
	}
	// Breaking out closed the connection; the live handler must notice.
	c.Close()
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > 50 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	// A fresh connection still works.
	c2 := ts.dial(t)
	if err := c2.Call(context.Background(), "com.example.Test.Echo", srvEchoIn{Text: "x"}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestServerOneway(t *testing.T) {
	ts := startServer(t)
	c := ts.dial(t)
	if err := c.Oneway(context.Background(), "com.example.Test.Fail", failIn{"other"}); err != nil {
		t.Fatal(err)
	}
	// No reply was written: the next call gets its own reply.
	var out srvEchoOut
	if err := c.Call(context.Background(), "com.example.Test.Echo", srvEchoIn{Text: "after"}, &out); err != nil || out.Text != "after" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if !strings.Contains(ts.logs.String(), "secret internal detail") {
		t.Fatal("oneway error was not logged")
	}
}

func TestServerIntrospection(t *testing.T) {
	ts := startServer(t)
	c := ts.dial(t)
	ctx := context.Background()
	info, err := varlink.GetInfo(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if info.Product != "testd" || fmt.Sprint(info.Interfaces) != "[com.example.Test org.varlink.service]" {
		t.Fatalf("info = %+v", info)
	}
	desc, err := varlink.GetInterfaceDescription(ctx, c, "com.example.Test")
	if err != nil || desc != testIDL {
		t.Fatalf("description differs: %v", err)
	}
	if _, err := varlink.GetInterfaceDescription(ctx, c, "org.varlink.service"); err != nil {
		t.Fatal(err)
	}
	if _, err := varlink.GetInterfaceDescription(ctx, c, "com.example.Nope"); !errors.Is(err, varlink.ErrInterfaceNotFound) {
		t.Fatalf("err = %v", err)
	}
	if ok, _ := varlink.Supports(ctx, c, "com.example.Test.Ticks"); !ok {
		t.Fatal("Supports(Ticks) = false")
	}
}

func TestServerPeerCredentials(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("SO_PEERCRED is Linux-only")
	}
	ts := startServer(t)
	c := ts.dial(t)
	var out whoOut
	if err := c.Call(context.Background(), "com.example.Test.Who", nil, &out); err != nil {
		t.Fatal(err)
	}
	if out.UID != int64(os.Getuid()) || out.GID != int64(os.Getgid()) || out.PID != int64(os.Getpid()) {
		t.Fatalf("peer = %+v", out)
	}
}

func TestServerShutdownDrainsInFlightCalls(t *testing.T) {
	ts := startServer(t)
	c := ts.dial(t)
	errc := make(chan error, 1)
	go func() { errc <- c.Call(context.Background(), "com.example.Test.Slow", nil, nil) }()
	time.Sleep(50 * time.Millisecond) // let the call start
	ts.cancel()
	if err := <-errc; err != nil {
		t.Fatalf("in-flight call failed during shutdown: %v", err)
	}
	select {
	case <-ts.done:
		if ts.err != nil {
			t.Fatalf("Serve = %v", ts.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return")
	}
	if _, err := os.Stat(ts.path); !os.IsNotExist(err) {
		t.Fatal("socket file left behind")
	}
}

func TestServerShutdownEndsStreams(t *testing.T) {
	ts := startServer(t)
	c := ts.dial(t)
	got := make(chan error, 1)
	go func() {
		var last error
		for _, err := range c.Stream(context.Background(), "com.example.Test.Ticks", nIn{N: 0}) {
			last = err
		}
		got <- last
	}()
	time.Sleep(50 * time.Millisecond)
	ts.cancel()
	select {
	case err := <-got:
		// The handler returned because shutdown cancelled it, so the server
		// closes the connection instead of inventing a final reply.
		if err == nil {
			t.Fatal("stream ended with a reply, want the connection closed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not end at shutdown")
	}
}

func TestServerShutdownGraceCutsOffSlowCalls(t *testing.T) {
	ts := startServer(t, varlink.WithShutdownGrace(50*time.Millisecond))
	c := ts.dial(t)
	errc := make(chan error, 1)
	go func() { errc <- c.Call(context.Background(), "com.example.Test.Slow", nil, nil) }()
	time.Sleep(20 * time.Millisecond)
	start := time.Now()
	ts.cancel()
	<-errc // cut off: either an error reply or a closed connection
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("shutdown took %v", d)
	}
}

func TestServerFrameLimit(t *testing.T) {
	ts := startServer(t, varlink.WithServerMaxFrameSize(1024))
	c := ts.dial(t)
	err := c.Call(context.Background(), "com.example.Test.Echo", srvEchoIn{Text: strings.Repeat("x", 4096)}, nil)
	if !errors.Is(err, syscall.ENOBUFS) {
		t.Fatalf("err = %v", err)
	}
}

func TestServerConnectionLimit(t *testing.T) {
	ts := startServer(t, varlink.WithMaxConnections(1))
	c1 := ts.dial(t)
	if err := c1.Call(context.Background(), "com.example.Test.Echo", srvEchoIn{Text: "1"}, nil); err != nil {
		t.Fatal(err)
	}
	c2 := ts.dial(t)
	if err := c2.Call(context.Background(), "com.example.Test.Echo", srvEchoIn{Text: "2"}, nil); err == nil {
		t.Fatal("second connection served past the limit")
	}
}

func TestServerIdleTimeout(t *testing.T) {
	ts := startServer(t, varlink.WithIdleTimeout(50*time.Millisecond))
	c := ts.dial(t)
	if err := c.Call(context.Background(), "com.example.Test.Echo", srvEchoIn{Text: "1"}, nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if err := c.Call(context.Background(), "com.example.Test.Echo", srvEchoIn{Text: "2"}, nil); err == nil {
		t.Fatal("idle connection was not closed")
	}
}

func TestListenReplacesStaleSocket(t *testing.T) {
	path := sockPath(t)
	// A socket file nobody listens on, as a crashed server leaves behind.
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	l.SetUnlinkOnClose(false)
	l.Close()
	l2, err := varlink.Listen(path, varlink.WithSocketMode(0o600))
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, err = %v", fi.Mode(), err)
	}
	// A live socket is refused.
	if _, err := varlink.Listen(path); !errors.Is(err, syscall.EADDRINUSE) {
		t.Fatalf("err = %v", err)
	}
	// A path that is not a socket is refused.
	file := filepath.Join(filepath.Dir(path), "plain")
	os.WriteFile(file, nil, 0o644)
	if _, err := varlink.Listen(file); err == nil {
		t.Fatal("listened over a regular file")
	}
}

func TestListenActivatedNotActivated(t *testing.T) {
	t.Setenv("LISTEN_PID", "")
	if _, err := varlink.ListenActivated(""); !errors.Is(err, varlink.ErrNotActivated) {
		t.Fatalf("err = %v", err)
	}
}

func TestRegistrationPanics(t *testing.T) {
	mustPanic := func(name string, f func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Errorf("%s: no panic", name)
			}
		}()
		f()
	}
	srv := varlink.NewServer(varlink.ServerInfo{})
	iface := srv.Interface("com.example.Test", testIDL)
	mustPanic("bad IDL", func() { srv.Interface("x.y", "interface x.y\nmethod (") })
	mustPanic("name mismatch", func() { srv.Interface("x.z", "interface x.y\n") })
	mustPanic("duplicate interface", func() { srv.Interface("com.example.Test", testIDL) })
	mustPanic("unknown method", func() {
		varlink.Handle(iface, "Nope", func(context.Context, *varlink.Call, struct{}) (struct{}, error) { return struct{}{}, nil })
	})
	mustPanic("requires more", func() {
		varlink.Handle(iface, "Ticks", func(context.Context, *varlink.Call, nIn) (nOut, error) { return nOut{}, nil })
	})
	mustPanic("wrong input type", func() {
		varlink.Handle(iface, "Echo", func(context.Context, *varlink.Call, struct {
			Text int `json:"text"`
		}) (srvEchoOut, error) {
			return srvEchoOut{}, nil
		})
	})
	mustPanic("field not in IDL", func() {
		varlink.Handle(iface, "Echo", func(context.Context, *varlink.Call, struct {
			Txt string `json:"txt"`
		}) (srvEchoOut, error) {
			return srvEchoOut{}, nil
		})
	})
	mustPanic("missing required output", func() {
		varlink.Handle(iface, "Echo", func(context.Context, *varlink.Call, srvEchoIn) (struct{}, error) { return struct{}{}, nil })
	})
	varlink.Handle(iface, "Echo", func(context.Context, *varlink.Call, srvEchoIn) (srvEchoOut, error) { return srvEchoOut{}, nil })
	mustPanic("duplicate handler", func() {
		varlink.Handle(iface, "Echo", func(context.Context, *varlink.Call, srvEchoIn) (srvEchoOut, error) { return srvEchoOut{}, nil })
	})
}
