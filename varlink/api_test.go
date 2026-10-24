package varlink_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/rossedman/sdvarlink/varlink"
	"github.com/rossedman/sdvarlink/varlinktest"
)

func TestErrorMessages(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "no params", err: &varlink.Error{Name: "a.B"}, want: "varlink: a.B"},
		{name: "empty object", err: &varlink.Error{Name: "a.B", Parameters: json.RawMessage(`{}`)}, want: "varlink: a.B"},
		{name: "null", err: &varlink.Error{Name: "a.B", Parameters: json.RawMessage(`null`)}, want: "varlink: a.B"},
		{name: "params", err: &varlink.Error{Name: "a.B", Parameters: json.RawMessage(`{"x":1}`)}, want: `varlink: a.B {"x":1}`},
		{name: "system with name and errno", err: &varlink.SystemError{ErrnoName: "ENOENT", Errno: int(syscall.ENOENT)}, want: "varlink: io.systemd.System: ENOENT: no such file or directory"},
		{name: "system errno only", err: &varlink.SystemError{Errno: int(syscall.EPERM)}, want: "varlink: io.systemd.System: errno 1: operation not permitted"},
		{name: "system name only", err: &varlink.SystemError{ErrnoName: "EFOO"}, want: "varlink: io.systemd.System: EFOO"},
		{name: "system nothing", err: &varlink.SystemError{}, want: "varlink: io.systemd.System: errno 0"},
		{name: "protocol", err: &varlink.ProtocolError{Msg: "bad"}, want: "varlink: protocol error: bad"},
		{name: "protocol wrapped", err: &varlink.ProtocolError{Msg: "bad", Err: errors.New("inner")}, want: "varlink: protocol error: bad: inner"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.err.Error(); got != tc.want {
				t.Errorf("Error() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestErrorUnwrapAndNames(t *testing.T) {
	t.Parallel()
	inner := errors.New("inner")
	tests := []struct {
		name     string
		err      error
		wantName string
		isTarget error
		wantIs   bool
	}{
		{name: "plain error", err: errors.New("x"), wantName: ""},
		{name: "varlink error", err: &varlink.Error{Name: "a.B"}, wantName: "a.B", isTarget: &varlink.Error{Name: "a.B"}, wantIs: true},
		{name: "different name", err: &varlink.Error{Name: "a.B"}, wantName: "a.B", isTarget: &varlink.Error{Name: "a.C"}},
		{name: "is non-varlink target", err: &varlink.Error{Name: "a.B"}, wantName: "a.B", isTarget: inner},
		{name: "wrapped well-known", err: fmt.Errorf("ctx: %w", &varlink.Error{Name: varlink.ErrMethodNotFound.Name}), wantName: varlink.ErrMethodNotFound.Name, isTarget: varlink.ErrMethodNotFound, wantIs: true},
		{name: "system errno", err: &varlink.SystemError{Errno: int(syscall.ENOENT)}, wantName: "io.systemd.System", isTarget: syscall.ENOENT, wantIs: true},
		{name: "system other errno", err: &varlink.SystemError{Errno: int(syscall.ENOENT)}, wantName: "io.systemd.System", isTarget: syscall.EPERM},
		{name: "system no errno", err: &varlink.SystemError{}, wantName: "io.systemd.System", isTarget: syscall.ENOENT},
		{name: "protocol wraps", err: &varlink.ProtocolError{Msg: "m", Err: inner}, wantName: "", isTarget: inner, wantIs: true},
		{name: "protocol no inner", err: &varlink.ProtocolError{Msg: "m"}, wantName: "", isTarget: inner},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := varlink.ErrorName(tc.err); got != tc.wantName {
				t.Errorf("ErrorName = %q, want %q", got, tc.wantName)
			}
			if tc.isTarget != nil {
				if got := errors.Is(tc.err, tc.isTarget); got != tc.wantIs {
					t.Errorf("errors.Is = %v, want %v", got, tc.wantIs)
				}
			}
		})
	}
}

func TestErrorDecode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		params  string
		want    string
		wantErr bool
	}{
		{name: "empty", params: "", want: ""},
		{name: "value", params: `{"reason":"r"}`, want: "r"},
		{name: "invalid", params: `{`, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := &varlink.Error{Name: "a.B"}
			if tc.params != "" {
				e.Parameters = json.RawMessage(tc.params)
			}
			var out struct {
				Reason string `json:"reason"`
			}
			err := e.Decode(&out)
			if (err != nil) != tc.wantErr || out.Reason != tc.want {
				t.Fatalf("Decode = (%q, %v), want (%q, err=%v)", out.Reason, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestSplitMethod(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in, iface, method string
		wantErr           bool
	}{
		{in: "io.systemd.Resolve.ResolveHostname", iface: "io.systemd.Resolve", method: "ResolveHostname"},
		{in: "a.B", iface: "a", method: "B"},
		{in: "NoDots", wantErr: true},
		{in: ".Leading", wantErr: true},
		{in: "trailing.", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			iface, method, err := varlink.SplitMethod(tc.in)
			if (err != nil) != tc.wantErr || iface != tc.iface || method != tc.method {
				t.Fatalf("SplitMethod = (%q, %q, %v)", iface, method, err)
			}
		})
	}
}

func TestGetInfoAndSupports(t *testing.T) {
	t.Parallel()
	srv := varlinktest.NewServer(t)
	srv.AddInterface("interface com.example.Have\nmethod Present() -> ()\n")
	srv.AddInterface("interface com.example.Bad\nmethod Present() -> ()\n")
	c := srv.Dial()
	ctx := t.Context()

	info, err := varlink.GetInfo(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if info.Product != "varlinktest" || !strings.Contains(strings.Join(info.Interfaces, ","), "com.example.Have") {
		t.Errorf("info = %+v", info)
	}
	desc, err := varlink.GetInterfaceDescription(ctx, c, "com.example.Have")
	if err != nil || !strings.Contains(desc, "Present") {
		t.Errorf("description = %q, %v", desc, err)
	}
	if _, err := varlink.GetInterfaceDescription(ctx, c, "com.example.Nope"); !errors.Is(err, varlink.ErrInterfaceNotFound) {
		t.Errorf("missing description err = %v", err)
	}

	tests := []struct {
		name      string
		method    string
		want      bool
		wantErr   bool
		errSubstr string
	}{
		{name: "present", method: "com.example.Have.Present", want: true},
		{name: "method absent", method: "com.example.Have.Absent"},
		{name: "interface absent", method: "com.example.Nope.Present"},
		{name: "unqualified", method: "Present", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := varlink.Supports(ctx, c, tc.method)
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("Supports = (%v, %v), want (%v, err=%v)", got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestSupportsParseError(t *testing.T) {
	t.Parallel()
	srv := varlinktest.NewServer(t)
	srv.HandleRaw("org.varlink.service.GetInterfaceDescription", func(context.Context, *varlinktest.Call) iter.Seq2[*varlinktest.Response, error] {
		return func(yield func(*varlinktest.Response, error) bool) {
			yield(&varlinktest.Response{Parameters: json.RawMessage(`{"description":"not idl"}`)}, nil)
		}
	})
	ok, err := varlink.Supports(t.Context(), srv.Dial(), "com.example.X.M")
	if ok || err == nil || !strings.Contains(err.Error(), "parse com.example.X description") {
		t.Fatalf("Supports = (%v, %v)", ok, err)
	}
}

func TestGetInfoError(t *testing.T) {
	t.Parallel()
	srv := varlinktest.NewServer(t)
	srv.HandleRaw("org.varlink.service.GetInfo", func(context.Context, *varlinktest.Call) iter.Seq2[*varlinktest.Response, error] {
		return func(yield func(*varlinktest.Response, error) bool) {
			yield(nil, &varlink.Error{Name: "com.example.Broken"})
		}
	})
	info, err := varlink.GetInfo(t.Context(), srv.Dial())
	if info != nil || varlink.ErrorName(err) != "com.example.Broken" {
		t.Fatalf("GetInfo = (%+v, %v)", info, err)
	}
}

func TestUnknownFields(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		data    string
		known   []string
		want    map[string]string
		wantErr bool
	}{
		{name: "all known", data: `{"a":1,"b":2}`, known: []string{"a", "b"}},
		{name: "empty object", data: `{}`},
		{name: "extras", data: `{"a":1,"b":"x"}`, known: []string{"a"}, want: map[string]string{"b": `"x"`}},
		{name: "no known", data: `{"a":1}`, want: map[string]string{"a": "1"}},
		{name: "not object", data: `[1]`, wantErr: true},
		{name: "invalid", data: `{`, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := varlink.UnknownFields([]byte(tc.data), tc.known...)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for k, v := range tc.want {
				if string(got[k]) != v {
					t.Errorf("got[%q] = %s, want %s", k, got[k], v)
				}
			}
		})
	}
}

func TestMarshalWithExtra(t *testing.T) {
	t.Parallel()
	type rec struct {
		A int `json:"a"`
	}
	tests := []struct {
		name    string
		v       any
		extra   map[string]json.RawMessage
		want    string
		wantErr bool
	}{
		{name: "no extra", v: rec{A: 1}, want: `{"a":1}`},
		{name: "extra added", v: rec{A: 1}, extra: map[string]json.RawMessage{"b": json.RawMessage(`2`)}, want: `{"a":1,"b":2}`},
		{name: "extra does not override", v: rec{A: 1}, extra: map[string]json.RawMessage{"a": json.RawMessage(`9`)}, want: `{"a":1}`},
		{name: "unmarshalable", v: make(chan int), wantErr: true},
		{name: "non-object with extra", v: []int{1}, extra: map[string]json.RawMessage{"b": json.RawMessage(`2`)}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := varlink.MarshalWithExtra(tc.v, tc.extra)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v", err)
			}
			if !tc.wantErr && string(got) != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestStreamAsErrors(t *testing.T) {
	t.Parallel()
	srv := varlinktest.NewServer(t)
	varlinktest.OnStream(srv, "com.example.Count", counter(3, 0))
	varlinktest.OnStream(srv, "com.example.CountFail", counter(3, 2))
	c := srv.Dial()
	ctx := t.Context()

	// A stream error stops the iteration after the replies before it.
	var got []int
	var gotErr error
	for v, err := range varlink.StreamAs[echoOut](c.Stream(ctx, "com.example.CountFail", echoIn{"x"})) {
		if err != nil {
			gotErr = err
			continue
		}
		got = append(got, v.Count)
	}
	if len(got) != 1 || got[0] != 1 || varlink.ErrorName(gotErr) != "com.example.Broken" {
		t.Fatalf("got %v, err %v", got, gotErr)
	}

	// A reply that does not decode into T ends the iteration with an error.
	type wrong struct {
		Text int `json:"text"`
	}
	var decErr error
	for v, err := range varlink.StreamAs[wrong](c.Stream(ctx, "com.example.Count", echoIn{"x"})) {
		if v != nil {
			t.Fatalf("got value %+v", v)
		}
		decErr = err
	}
	if decErr == nil {
		t.Fatal("expected decode error")
	}

	// Stopping early ends the iteration.
	n := 0
	for range varlink.StreamAs[echoOut](c.Stream(ctx, "com.example.Count", echoIn{"x"})) {
		n++
		break
	}
	if n != 1 {
		t.Fatalf("n = %d", n)
	}
}

func TestConnClose(t *testing.T) {
	t.Parallel()
	c := newEcho(t).Dial()
	if err := c.Err(); err != nil {
		t.Fatalf("Err before close = %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close = %v", err)
	}
	if err := c.Err(); !errors.Is(err, varlink.ErrClosed) {
		t.Fatalf("Err after close = %v", err)
	}
	if err := c.Call(t.Context(), "com.example.Echo", nil, nil); !errors.Is(err, varlink.ErrClosed) {
		t.Fatalf("Call after close = %v", err)
	}
	if err := c.Oneway(t.Context(), "com.example.Echo", nil); !errors.Is(err, varlink.ErrClosed) {
		t.Fatalf("Oneway after close = %v", err)
	}
	for _, err := range c.Stream(t.Context(), "com.example.Echo", nil) {
		if !errors.Is(err, varlink.ErrClosed) {
			t.Fatalf("Stream after close = %v", err)
		}
	}
}

func TestDialErrors(t *testing.T) {
	t.Parallel()
	missing := t.TempDir() + "/nope"
	if _, err := varlink.Dial(t.Context(), missing); !errors.Is(err, syscall.ENOENT) {
		t.Errorf("Dial missing = %v", err)
	}
	srv := newEcho(t)
	if runtime.GOOS != "linux" {
		// systemd only runs on Linux: DialService reports every path as
		// unsupported, even one with a live server.
		for _, path := range []string{missing, srv.Path()} {
			if _, err := varlink.DialService(t.Context(), path); !errors.Is(err, varlink.ErrUnsupported) {
				t.Errorf("DialService(%s) on %s = %v, want ErrUnsupported", path, runtime.GOOS, err)
			}
		}
		return
	}
	_, err := varlink.DialService(t.Context(), missing)
	if !errors.Is(err, varlink.ErrUnsupported) || !errors.Is(err, syscall.ENOENT) || !strings.Contains(err.Error(), missing) {
		t.Errorf("DialService missing = %v", err)
	}
	// Other failures are not reported as "unsupported".
	_, err = varlink.DialService(t.Context(), "/"+strings.Repeat("a", 300))
	if err == nil || errors.Is(err, varlink.ErrUnsupported) {
		t.Errorf("DialService on an overlong path = %v", err)
	}
	c, err := varlink.DialService(t.Context(), srv.Path())
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}

func TestPoolIdleLimit(t *testing.T) {
	t.Parallel()
	srv := newEcho(t)
	tests := []struct {
		name     string
		maxIdle  int
		wantKept int
	}{
		{name: "zero keeps none", maxIdle: 0, wantKept: 0},
		{name: "one", maxIdle: 1, wantKept: 1},
		{name: "two", maxIdle: 2, wantKept: 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := varlink.NewPool(func(ctx context.Context) (*varlink.Conn, error) {
				return varlink.Dial(ctx, srv.Path())
			}, varlink.WithMaxIdle(tc.maxIdle))
			defer p.Close()
			conns := make([]*varlink.Conn, 3)
			for i := range conns {
				c, err := p.Get(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				conns[i] = c
			}
			for _, c := range conns {
				p.Put(c)
			}
			p.Put(nil) // ignored
			kept := 0
			for _, c := range conns {
				if c.Err() == nil {
					kept++
				}
			}
			if kept != tc.wantKept {
				t.Errorf("kept = %d, want %d", kept, tc.wantKept)
			}
		})
	}
}

func TestPoolReuseAndDrop(t *testing.T) {
	t.Parallel()
	srv := newEcho(t)
	p := varlink.NewPoolPath(srv.Path())
	defer p.Close()
	c1, err := p.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	p.Put(c1)
	c2, err := p.Get(t.Context())
	if err != nil || c2 != c1 {
		t.Fatalf("idle conn not reused: %v", err)
	}
	// A conn that broke while idle is skipped.
	p.Put(c2)
	c2.Close()
	c3, err := p.Get(t.Context())
	if err != nil || c3 == c2 || c3.Err() != nil {
		t.Fatalf("broken conn handed out: %v", err)
	}
	p.Put(c3)
	p.Close()
	if err := c3.Err(); !errors.Is(err, varlink.ErrClosed) {
		t.Errorf("idle conn after pool Close: %v", err)
	}
	if _, err := p.Get(t.Context()); !errors.Is(err, varlink.ErrClosed) {
		t.Errorf("Get on closed pool = %v", err)
	}
	c4 := srv.Dial()
	p.Put(c4)
	if c4.Err() == nil {
		t.Error("Put on closed pool kept the connection")
	}
}

func TestPoolOnewayAndDialErrors(t *testing.T) {
	t.Parallel()
	srv := varlinktest.NewServer(t)
	srv.Handle("com.example.Notify", func(in echoIn) (echoOut, error) { return echoOut{}, nil })
	p := varlink.NewPoolPath(srv.Path())
	defer p.Close()
	if err := p.Oneway(t.Context(), "com.example.Notify", echoIn{"n"}); err != nil {
		t.Fatal(err)
	}

	dialErr := errors.New("dial failed")
	bad := varlink.NewPool(func(context.Context) (*varlink.Conn, error) { return nil, dialErr })
	if err := bad.Oneway(t.Context(), "m.X", nil); !errors.Is(err, dialErr) {
		t.Errorf("Oneway = %v", err)
	}
	if err := bad.Call(t.Context(), "m.X", nil, nil); !errors.Is(err, dialErr) {
		t.Errorf("Call = %v", err)
	}
	n := 0
	for r, err := range bad.Stream(t.Context(), "m.X", nil) {
		n++
		if r != nil || !errors.Is(err, dialErr) {
			t.Errorf("Stream = (%v, %v)", r, err)
		}
	}
	if n != 1 {
		t.Errorf("Stream yielded %d times", n)
	}
}

func TestServerCallAccessors(t *testing.T) {
	t.Parallel()
	type seen struct {
		method       string
		more, oneway bool
		ifaceName    string
	}
	got := make(chan seen, 4)
	path := serveR(t, func(iface *varlink.Interface) {
		varlink.Handle(iface, "Echo", func(_ context.Context, call *varlink.Call, in srvEchoIn) (srvEchoOut, error) {
			got <- seen{method: call.Method(), more: call.More(), oneway: call.Oneway(), ifaceName: iface.Name()}
			return srvEchoOut{Text: in.Text}, nil
		})
	})
	c, err := varlink.Dial(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var out srvEchoOut
	if err := c.Call(t.Context(), "com.example.R.Echo", srvEchoIn{Text: "a"}, &out); err != nil || out.Text != "a" {
		t.Fatalf("Call = %+v, %v", out, err)
	}
	if s := <-got; s != (seen{method: "com.example.R.Echo", ifaceName: "com.example.R"}) {
		t.Errorf("call = %+v", s)
	}
	if err := c.Oneway(t.Context(), "com.example.R.Echo", srvEchoIn{Text: "b"}); err != nil {
		t.Fatal(err)
	}
	if s := <-got; s != (seen{method: "com.example.R.Echo", oneway: true, ifaceName: "com.example.R"}) {
		t.Errorf("oneway call = %+v", s)
	}
}

func TestServerAllowUnknownFields(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		opts    []varlink.HandlerOption
		wantErr bool
	}{
		{name: "rejected by default", wantErr: true},
		{name: "allowed with option", opts: []varlink.HandlerOption{varlink.AllowUnknownFields()}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := serveR(t, func(iface *varlink.Interface) {
				varlink.Handle(iface, "Echo", func(_ context.Context, _ *varlink.Call, in srvEchoIn) (srvEchoOut, error) {
					return srvEchoOut{Text: in.Text}, nil
				}, tc.opts...)
			})
			c, err := varlink.Dial(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			var out srvEchoOut
			err = c.Call(t.Context(), "com.example.R.Echo", map[string]any{"text": "t", "bogus": 1}, &out)
			if tc.wantErr {
				var ve *varlink.Error
				if !errors.Is(err, varlink.ErrInvalidParameter) || !errors.As(err, &ve) || !strings.Contains(string(ve.Parameters), "bogus") {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err != nil || out.Text != "t" {
				t.Fatalf("Call = %+v, %v", out, err)
			}
		})
	}
}

func TestListenActivatedEnvironment(t *testing.T) {
	pid := strconv.Itoa(os.Getpid())
	tests := []struct {
		name         string
		pid, fds     string
		fdNames      *string
		want         string
		notActivated bool
		wantErr      string
	}{
		{name: "no pid", pid: "", fds: "1", notActivated: true},
		{name: "other pid", pid: "1", fds: "1", notActivated: true},
		{name: "bad fds", pid: pid, fds: "x", notActivated: true},
		{name: "zero fds", pid: pid, fds: "0", notActivated: true},
		{name: "names count mismatch", pid: pid, fds: "2", fdNames: ptr("a"), wantErr: "1 names for 2 sockets"},
		{name: "many sockets unnamed", pid: pid, fds: "2", wantErr: "2 sockets were passed"},
		{name: "name not found", pid: pid, fds: "1", fdNames: ptr("a"), want: "b", wantErr: `no activated socket named "b"`},
		{name: "name without names", pid: pid, fds: "1", want: "b", wantErr: `no activated socket named "b"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LISTEN_PID", tc.pid)
			t.Setenv("LISTEN_FDS", tc.fds)
			if tc.fdNames != nil {
				t.Setenv("LISTEN_FDNAMES", *tc.fdNames)
			} else {
				os.Unsetenv("LISTEN_FDNAMES")
			}
			l, err := varlink.ListenActivated(tc.want)
			if l != nil {
				l.Close()
				t.Fatal("got a listener")
			}
			if tc.notActivated {
				if !errors.Is(err, varlink.ErrNotActivated) {
					t.Fatalf("err = %v, want ErrNotActivated", err)
				}
				return
			}
			if err == nil || errors.Is(err, varlink.ErrNotActivated) || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }

func TestWithSocketOwner(t *testing.T) {
	t.Parallel()
	path := sockPath(t)
	l, err := varlink.Listen(path, varlink.WithSocketOwner(os.Getuid(), os.Getgid()))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != os.Getuid() || int(st.Gid) != os.Getgid() {
		t.Fatalf("owner = %+v", fi.Sys())
	}
	// An owner change that is not permitted fails the Listen.
	if os.Getuid() != 0 {
		if _, err := varlink.Listen(sockPath(t), varlink.WithSocketOwner(0, -1)); !errors.Is(err, syscall.EPERM) {
			t.Errorf("Listen with foreign owner = %v", err)
		}
	}
}
