package varlinktest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/rossedman/sdvarlink/varlink"
)

func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

type greetIn struct {
	Name string `json:"name"`
}

type greetOut struct {
	Greeting string `json:"greeting"`
}

type tick struct {
	N int `json:"n"`
}

// reasonError is a typed error like the ones generated packages define.
type reasonError struct {
	Reason string `json:"reason"`
}

func (e *reasonError) Error() string        { return "reason: " + e.Reason }
func (e *reasonError) VarlinkError() string { return "com.example.Greeter.Refused" }

const (
	helloMethod = "com.example.Greeter.Hello"
	ifaceText   = "interface com.example.Greeter\n\nmethod Hello(name: string) -> (greeting: string)\n"
)

func hello(in greetIn) (greetOut, error) { return greetOut{Greeting: "hello " + in.Name}, nil }

func TestHandleShapes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		fn   any
		in   greetIn
		want greetOut
		err  string // Varlink error name, if any
	}{
		{name: "value", fn: hello, in: greetIn{"a"}, want: greetOut{"hello a"}},
		{name: "context", fn: func(context.Context, greetIn) (greetOut, error) { return greetOut{"ctx"}, nil }, want: greetOut{"ctx"}},
		{name: "pointers", fn: func(in *greetIn) (*greetOut, error) { return &greetOut{"hello " + in.Name}, nil }, in: greetIn{"p"}, want: greetOut{"hello p"}},
		{name: "varlink error", fn: func(greetIn) (greetOut, error) { return greetOut{}, &varlink.Error{Name: "com.example.Greeter.Nope"} }, err: "com.example.Greeter.Nope"},
		{name: "plain error", fn: func(greetIn) (greetOut, error) { return greetOut{}, errors.New("broken") }, err: HandlerError},
		{name: "typed error", fn: func(greetIn) (greetOut, error) { return greetOut{}, &reasonError{"busy"} }, err: "com.example.Greeter.Refused"},
		{name: "errno", fn: func(greetIn) (greetOut, error) { return greetOut{}, syscall.ENOENT }, err: "io.systemd.System"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := NewServer(t)
			srv.Handle(helloMethod, tc.fn)
			var got greetOut
			err := srv.Dial().Call(t.Context(), helloMethod, tc.in, &got)
			if tc.err != "" {
				if name := varlink.ErrorName(err); name != tc.err {
					t.Fatalf("error name = %q (%v), want %q", name, err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestHandlePanicsOnBadShape(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		fn   any
		want string
	}{
		{name: "not a func", fn: 42, want: "not a func"},
		{name: "no input", fn: func() (greetOut, error) { return greetOut{}, nil }, want: "unsupported handler type"},
		{name: "three inputs", fn: func(context.Context, greetIn, int) (greetOut, error) { return greetOut{}, nil }, want: "unsupported handler type"},
		{name: "two inputs without context", fn: func(greetIn, greetIn) (greetOut, error) { return greetOut{}, nil }, want: "unsupported handler type"},
		{name: "no outputs", fn: func(greetIn) {}, want: "unsupported handler type"},
		{name: "one non-func output", fn: func(greetIn) error { return nil }, want: "unsupported handler type"},
		{name: "second output not error", fn: func(greetIn) (greetOut, int) { return greetOut{}, 0 }, want: "unsupported handler type"},
		{name: "three outputs", fn: func(greetIn) (greetOut, int, error) { return greetOut{}, 0, nil }, want: "unsupported handler type"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			defer func() {
				msg := fmt.Sprint(recover())
				if !strings.Contains(msg, tc.want) {
					t.Fatalf("panic = %q, want it to contain %q", msg, tc.want)
				}
			}()
			adapt(tc.fn)
			t.Fatal("adapt did not panic")
		})
	}
}

func streamOf(n int, failAt int) iter.Seq2[tick, error] {
	return func(yield func(tick, error) bool) {
		for i := 1; i <= n; i++ {
			if i == failAt {
				yield(tick{}, &varlink.Error{Name: "com.example.Ticks.Stopped"})
				return
			}
			if !yield(tick{i}, nil) {
				return
			}
		}
	}
}

func TestStreamingHandlers(t *testing.T) {
	t.Parallel()
	const method = "com.example.Ticks.Watch"
	register := map[string]func(*Server, int, int){
		"Handle": func(s *Server, n, failAt int) {
			s.Handle(method, func(context.Context, struct{}) iter.Seq2[tick, error] { return streamOf(n, failAt) })
		},
		"OnStream": func(s *Server, n, failAt int) {
			OnStream(s, method, func(context.Context, struct{}) iter.Seq2[tick, error] { return streamOf(n, failAt) })
		},
	}
	tests := []struct {
		name    string
		n       int
		failAt  int
		want    []int
		wantErr string
	}{
		// A stream that yields nothing is answered with one empty final
		// reply, which the client does not yield.
		{name: "empty", n: 0, want: nil},
		{name: "single", n: 1, want: []int{1}},
		{name: "several", n: 4, want: []int{1, 2, 3, 4}},
		{name: "error after values", n: 5, failAt: 3, want: []int{1, 2}, wantErr: "com.example.Ticks.Stopped"},
		{name: "error first", n: 2, failAt: 1, wantErr: "com.example.Ticks.Stopped"},
	}
	for kind, reg := range register {
		for _, tc := range tests {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				srv := NewServer(t)
				reg(srv, tc.n, tc.failAt)
				var got []int
				var gotErr error
				for v, err := range varlink.StreamAs[tick](srv.Dial().Stream(t.Context(), method, nil)) {
					if err != nil {
						gotErr = err
						break
					}
					got = append(got, v.N)
				}
				if !slices.Equal(got, tc.want) {
					t.Errorf("values = %v, want %v", got, tc.want)
				}
				if name := varlink.ErrorName(gotErr); name != tc.wantErr {
					t.Errorf("error = %v (name %q), want name %q", gotErr, name, tc.wantErr)
				}
			})
		}
	}
}

func TestStreamingHandlerWithoutMore(t *testing.T) {
	t.Parallel()
	// A single-reply call to a stream that yields one value is fine; a
	// second value is a handler bug the server reports to the test.
	tests := []struct {
		name     string
		n        int
		wantErrs int
	}{
		{name: "one value", n: 1},
		{name: "two values", n: 2, wantErrs: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tb, srv := newFake(t)
			OnStream(srv, "com.example.Ticks.Watch", func(context.Context, struct{}) iter.Seq2[tick, error] { return streamOf(tc.n, 0) })
			var got tick
			if err := srv.Dial().Call(t.Context(), "com.example.Ticks.Watch", nil, &got); err != nil {
				t.Fatal(err)
			}
			if got.N != 1 {
				t.Errorf("got %+v, want the first value", got)
			}
			errs := tb.errors()
			if len(errs) != tc.wantErrs {
				t.Fatalf("reported errors = %q, want %d", errs, tc.wantErrs)
			}
			if tc.wantErrs > 0 && !strings.Contains(errs[0], "several replies") {
				t.Errorf("error = %q", errs[0])
			}
		})
	}
}

func TestStreamStopsWhenClientLeaves(t *testing.T) {
	t.Parallel()
	// Replies are written one behind, so after the client has read the
	// first and hung up, the next yield finds the connection gone and
	// returns false to the handler.
	const method = "com.example.Ticks.Watch"
	register := map[string]func(*Server, chan struct{}, chan []bool){
		"Handle": func(s *Server, gate chan struct{}, result chan []bool) {
			s.Handle(method, func(context.Context, struct{}) iter.Seq2[tick, error] {
				return func(yield func(tick, error) bool) {
					yield(tick{1}, nil)
					yield(tick{2}, nil)
					<-gate
					third := yield(tick{3}, nil)
					fourth := yield(tick{4}, nil) // after a false the adapter answers false itself
					result <- []bool{third, fourth}
				}
			})
		},
		"OnStream": func(s *Server, gate chan struct{}, result chan []bool) {
			OnStream(s, method, func(context.Context, struct{}) iter.Seq2[tick, error] {
				return func(yield func(tick, error) bool) {
					yield(tick{1}, nil)
					yield(tick{2}, nil)
					<-gate
					result <- []bool{yield(tick{3}, nil)}
				}
			})
		},
	}
	for kind, reg := range register {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			srv := NewServer(t)
			gate, result := make(chan struct{}), make(chan []bool, 1)
			reg(srv, gate, result)
			c := srv.Dial()
			for r, err := range c.Stream(t.Context(), method, nil) {
				if err != nil {
					t.Fatal(err)
				}
				var v tick
				if err := r.Decode(&v); err != nil || v.N != 1 {
					t.Fatalf("first reply = %+v, %v", v, err)
				}
				break
			}
			c.Close()
			close(gate)
			for i, ok := range <-result {
				if ok {
					t.Errorf("yield #%d after the client left returned true", i+3)
				}
			}
		})
	}
}

func TestInvalidParameters(t *testing.T) {
	t.Parallel()
	const method = "com.example.Greeter.Hello"
	bad := map[string]any{"name": 5}
	register := map[string]func(*Server, *bool){
		"Handle": func(s *Server, called *bool) {
			s.Handle(method, func(greetIn) (greetOut, error) { *called = true; return greetOut{}, nil })
		},
		"On": func(s *Server, called *bool) {
			On(s, method, func(context.Context, greetIn) (greetOut, error) { *called = true; return greetOut{}, nil })
		},
		"OnStream": func(s *Server, called *bool) {
			OnStream(s, method, func(context.Context, greetIn) iter.Seq2[greetOut, error] {
				*called = true
				return func(func(greetOut, error) bool) {}
			})
		},
	}
	for kind, reg := range register {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			srv := NewServer(t)
			var called bool
			reg(srv, &called)
			err := srv.Dial().Call(t.Context(), method, bad, nil)
			if !errors.Is(err, varlink.ErrInvalidParameter) {
				t.Fatalf("err = %v, want InvalidParameter", err)
			}
			if called {
				t.Error("handler ran despite undecodable parameters")
			}
		})
	}
}

func TestOn(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		fn      func(context.Context, greetIn) (greetOut, error)
		in      greetIn
		want    greetOut
		wantErr string
	}{
		{name: "reply", fn: func(_ context.Context, in greetIn) (greetOut, error) { return greetOut{in.Name}, nil }, in: greetIn{"x"}, want: greetOut{"x"}},
		{name: "empty input", fn: func(_ context.Context, in greetIn) (greetOut, error) { return greetOut{"[" + in.Name + "]"}, nil }, want: greetOut{"[]"}},
		{name: "error", fn: func(context.Context, greetIn) (greetOut, error) { return greetOut{}, errors.New("boom") }, wantErr: HandlerError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := NewServer(t)
			On(srv, helloMethod, tc.fn)
			var got greetOut
			err := srv.Dial().Call(t.Context(), helloMethod, tc.in, &got)
			if name := varlink.ErrorName(err); name != tc.wantErr {
				t.Fatalf("err = %v (name %q), want name %q", err, name, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestErrorReply(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		err        error
		wantName   string
		wantParams string
	}{
		{name: "varlink error", err: &varlink.Error{Name: "a.b.C", Parameters: json.RawMessage(`{"k":1}`)}, wantName: "a.b.C", wantParams: `{"k":1}`},
		{name: "varlink error without parameters", err: &varlink.Error{Name: "a.b.C"}, wantName: "a.b.C"},
		{name: "wrapped varlink error", err: fmt.Errorf("ctx: %w", &varlink.Error{Name: "a.b.D", Parameters: json.RawMessage(`{}`)}), wantName: "a.b.D", wantParams: `{}`},
		{name: "named error", err: &reasonError{"busy"}, wantName: "com.example.Greeter.Refused", wantParams: `{"reason":"busy"}`},
		{name: "wrapped named error", err: fmt.Errorf("ctx: %w", &reasonError{"x"}), wantName: "com.example.Greeter.Refused", wantParams: `{"reason":"x"}`},
		{name: "errno", err: syscall.EACCES, wantName: "io.systemd.System", wantParams: `{"origin":"linux","errno":13}`},
		{name: "wrapped errno", err: fmt.Errorf("open: %w", syscall.ENOENT), wantName: "io.systemd.System", wantParams: `{"origin":"linux","errno":2}`},
		{name: "plain", err: errors.New(`say "hi"`), wantName: HandlerError, wantParams: `{"message":"say \"hi\""}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			name, params := errorReply(tc.err)
			if name != tc.wantName {
				t.Errorf("name = %q, want %q", name, tc.wantName)
			}
			if tc.wantParams == "" {
				if len(params) != 0 {
					t.Errorf("params = %s, want none", params)
				}
			} else if !jsonEqual(params, json.RawMessage(tc.wantParams)) {
				t.Errorf("params = %s, want %s", params, tc.wantParams)
			}
		})
	}
}

func TestRawReplies(t *testing.T) {
	t.Parallel()
	seq := func(items ...any) RawHandler {
		return func(context.Context, *Call) iter.Seq2[*Response, error] {
			return func(yield func(*Response, error) bool) {
				for _, it := range items {
					var ok bool
					if err, isErr := it.(error); isErr {
						ok = yield(nil, err)
					} else {
						ok = yield(it.(*Response), nil)
					}
					if !ok {
						return
					}
				}
			}
		}
	}
	boom := &varlink.Error{Name: "com.example.Raw.Boom"}
	tests := []struct {
		name       string
		h          RawHandler
		more       bool
		want       []string // reply parameters
		wantErr    string
		wantReport string // substring of what the server reported to the test
	}{
		{name: "no replies answers empty", h: seq(), want: []string{`{}`}},
		{name: "nil parameters encode empty", h: seq(&Response{}), want: []string{`{}`}},
		{name: "one reply", h: seq(&Response{Parameters: tick{7}}), want: []string{`{"n":7}`}},
		{name: "error only", h: seq(boom), wantErr: boom.Name},
		{name: "error after reply without more drops reply", h: seq(&Response{Parameters: tick{1}}, boom), wantErr: boom.Name},
		{name: "several replies with more", h: seq(&Response{Parameters: tick{1}}, &Response{Parameters: tick{2}}), more: true, want: []string{`{"n":1}`, `{"n":2}`}},
		{name: "error after replies with more", h: seq(&Response{Parameters: tick{1}}, boom), more: true, want: []string{`{"n":1}`}, wantErr: boom.Name},
		{name: "several replies without more", h: seq(&Response{Parameters: tick{1}}, &Response{Parameters: tick{2}}), want: []string{`{"n":1}`}, wantReport: "several replies"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tb, srv := newFake(t)
			srv.HandleRaw("com.example.Raw.Do", tc.h)
			c := srv.Dial()
			var got []string
			var gotErr error
			if tc.more {
				for r, err := range c.Stream(t.Context(), "com.example.Raw.Do", nil) {
					if err != nil {
						gotErr = err
						break
					}
					got = append(got, string(r.Parameters))
				}
			} else {
				r, err := c.CallReply(t.Context(), "com.example.Raw.Do", nil)
				if err != nil {
					gotErr = err
				} else {
					got = append(got, string(r.Parameters))
				}
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("replies = %q, want %q", got, tc.want)
			}
			if name := varlink.ErrorName(gotErr); name != tc.wantErr {
				t.Errorf("error = %v (name %q), want name %q", gotErr, name, tc.wantErr)
			}
			errs := tb.errors()
			switch {
			case tc.wantReport == "" && len(errs) != 0:
				t.Errorf("unexpected report: %q", errs)
			case tc.wantReport != "" && (len(errs) != 1 || !strings.Contains(errs[0], tc.wantReport)):
				t.Errorf("reports = %q, want one containing %q", errs, tc.wantReport)
			}
		})
	}
}

func TestUnencodableReplyClosesConnection(t *testing.T) {
	t.Parallel()
	tb, srv := newFake(t)
	srv.HandleRaw("com.example.Raw.Do", func(context.Context, *Call) iter.Seq2[*Response, error] {
		return func(yield func(*Response, error) bool) {
			yield(&Response{Parameters: make(chan int)}, nil)
		}
	})
	c := srv.Dial()
	if err := c.Call(t.Context(), "com.example.Raw.Do", nil, nil); err == nil {
		t.Fatal("call succeeded without a reply")
	}
	errs := tb.errors()
	if len(errs) != 1 || !strings.Contains(errs[0], "encode reply") {
		t.Fatalf("reports = %q, want one encode failure", errs)
	}
}

func TestOnewayCallsGetNoReply(t *testing.T) {
	t.Parallel()
	srv := NewServer(t)
	seen := make(chan struct{})
	srv.HandleRaw("com.example.Raw.Notify", func(context.Context, *Call) iter.Seq2[*Response, error] {
		return func(yield func(*Response, error) bool) {
			defer close(seen)
			yield(&Response{Parameters: tick{1}}, nil)
		}
	})
	srv.HandleRaw("com.example.Raw.Fail", func(context.Context, *Call) iter.Seq2[*Response, error] {
		return fail(errors.New("ignored"))(nil, nil)
	})
	c := srv.Dial()
	if err := c.Oneway(t.Context(), "com.example.Raw.Notify", tick{3}); err != nil {
		t.Fatal(err)
	}
	<-seen
	// A reply to the oneway call would be read as the answer to this one.
	On(srv, helloMethod, func(_ context.Context, in greetIn) (greetOut, error) { return greetOut{in.Name}, nil })
	var got greetOut
	if err := c.Call(t.Context(), helloMethod, greetIn{"after"}, &got); err != nil || got.Greeting != "after" {
		t.Fatalf("call after oneway = %+v, %v", got, err)
	}
	if err := c.Oneway(t.Context(), "com.example.Raw.Fail", nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(t.Context(), helloMethod, greetIn{"again"}, &got); err != nil || got.Greeting != "again" {
		t.Fatalf("call after failed oneway = %+v, %v", got, err)
	}

	calls := srv.Calls()
	if len(calls) != 4 {
		t.Fatalf("recorded %d calls, want 4: %+v", len(calls), calls)
	}
	wantOneway := []bool{true, false, true, false}
	for i, call := range calls {
		if call.Oneway != wantOneway[i] {
			t.Errorf("call %d (%s) oneway = %v, want %v", i, call.Method, call.Oneway, wantOneway[i])
		}
	}
}

func TestCallsRecorded(t *testing.T) {
	t.Parallel()
	srv := NewServer(t)
	srv.Handle(helloMethod, hello)
	OnStream(srv, "com.example.Ticks.Watch", func(context.Context, struct{}) iter.Seq2[tick, error] { return streamOf(2, 0) })
	if got := srv.Calls(); len(got) != 0 {
		t.Fatalf("calls before any request = %+v", got)
	}
	c := srv.Dial()
	var out greetOut
	if err := c.Call(t.Context(), helloMethod, greetIn{"rec"}, &out); err != nil {
		t.Fatal(err)
	}
	for _, err := range c.Stream(t.Context(), "com.example.Ticks.Watch", nil) {
		if err != nil {
			t.Fatal(err)
		}
	}
	calls := srv.Calls()
	if len(calls) != 2 {
		t.Fatalf("got %d calls, want 2", len(calls))
	}
	if calls[0].Method != helloMethod || !jsonEqual(calls[0].Parameters, json.RawMessage(`{"name":"rec"}`)) || calls[0].More {
		t.Errorf("first call = %+v", calls[0])
	}
	if calls[1].Method != "com.example.Ticks.Watch" || !calls[1].More {
		t.Errorf("second call = %+v", calls[1])
	}
	// The returned slice is a copy.
	calls[0].Method = "changed"
	if srv.Calls()[0].Method != helloMethod {
		t.Error("mutating the result changed the server's record")
	}
}

func tempFileWith(t *testing.T, content string) *os.File {
	t.Helper()
	f, err := os.Create(filepath.Join(t.TempDir(), "f"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestFilesBothWays(t *testing.T) {
	t.Parallel()
	srv := NewServer(t)
	received := make(chan string, 1)
	leftover := make(chan int, 1)
	reply := tempFileWith(t, "pong")
	srv.HandleRaw("com.example.Raw.Swap", func(_ context.Context, call *Call) iter.Seq2[*Response, error] {
		return func(yield func(*Response, error) bool) {
			data, err := io.ReadAll(call.Files[0])
			if err != nil {
				data = []byte(err.Error())
			}
			received <- string(data)
			// Decode takes the files; the server must not close them twice.
			var in struct{}
			if err := call.Decode(&in); err != nil {
				yield(nil, err)
				return
			}
			leftover <- len(call.Files)
			yield(&Response{Parameters: tick{1}, Files: []*os.File{reply}}, nil)
		}
	})
	r, err := srv.Dial().CallReply(t.Context(), "com.example.Raw.Swap", nil, varlink.WithFiles(tempFileWith(t, "ping")))
	if err != nil {
		t.Fatal(err)
	}
	if got := <-received; got != "ping" {
		t.Errorf("handler read %q, want ping", got)
	}
	if got := <-leftover; got != 0 {
		t.Errorf("call.Files has %d entries after Decode", got)
	}
	if len(r.Files) != 1 {
		t.Fatalf("reply carried %d files, want 1", len(r.Files))
	}
	defer r.Files[0].Close()
	data, err := io.ReadAll(r.Files[0])
	if err != nil || string(data) != "pong" {
		t.Fatalf("reply file reads %q, %v", data, err)
	}
	// The server closed its copy after sending.
	if _, err := reply.Stat(); err == nil {
		t.Error("server did not close the file it sent")
	}
}

func TestUnknownTargets(t *testing.T) {
	t.Parallel()
	srv := NewServer(t)
	srv.AddInterface(ifaceText)
	srv.HandleRaw("com.example.Other.Ping", fail(errors.New("unused")))
	tests := []struct {
		name   string
		method string
		want   *varlink.Error
		param  string
	}{
		{name: "unknown interface", method: "com.example.Missing.Do", want: varlink.ErrInterfaceNotFound, param: "com.example.Missing"},
		{name: "unqualified method", method: "Do", want: varlink.ErrInterfaceNotFound},
		{name: "method on described interface", method: "com.example.Greeter.Missing", want: varlink.ErrMethodNotFound, param: "com.example.Greeter.Missing"},
		{name: "method beside a handler", method: "com.example.Other.Pong", want: varlink.ErrMethodNotFound, param: "com.example.Other.Pong"},
	}
	c := srv.Dial()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := c.Call(t.Context(), tc.method, nil, nil)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %s", err, tc.want.Name)
			}
			var ve *varlink.Error
			if !errors.As(err, &ve) || !strings.Contains(string(ve.Parameters), tc.param) {
				t.Errorf("parameters = %s, want to mention %q", ve.Parameters, tc.param)
			}
		})
	}
}

func TestIntrospection(t *testing.T) {
	t.Parallel()
	srv := NewServer(t)
	srv.AddInterface(ifaceText)
	srv.Handle("com.example.Zeta.Run", func(struct{}) (struct{}, error) { return struct{}{}, nil })
	srv.HandleRaw("unqualified", fail(errors.New("unused")))
	c := srv.Dial()
	ctx := t.Context()

	t.Run("GetInfo", func(t *testing.T) {
		info, err := varlink.GetInfo(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"com.example.Greeter", "com.example.Zeta", "org.varlink.service"}
		if !slices.Equal(info.Interfaces, want) {
			t.Errorf("interfaces = %v, want %v", info.Interfaces, want)
		}
		if info.Product != "varlinktest" || info.Vendor != "sdvarlink" {
			t.Errorf("info = %+v", info)
		}
	})
	t.Run("GetInterfaceDescription", func(t *testing.T) {
		got, err := varlink.GetInterfaceDescription(ctx, c, "com.example.Greeter")
		if err != nil || got != ifaceText {
			t.Fatalf("got %q, %v", got, err)
		}
	})
	t.Run("unknown interface", func(t *testing.T) {
		_, err := varlink.GetInterfaceDescription(ctx, c, "com.example.Nope")
		if !errors.Is(err, varlink.ErrInterfaceNotFound) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("invalid parameter", func(t *testing.T) {
		err := c.Call(ctx, "org.varlink.service.GetInterfaceDescription", map[string]int{"interface": 5}, nil)
		if !errors.Is(err, varlink.ErrInvalidParameter) {
			t.Fatalf("err = %v", err)
		}
	})
	supports := []struct {
		method string
		want   bool
	}{
		{"com.example.Greeter.Hello", true},
		{"com.example.Greeter.Absent", false},
		{"com.example.Nope.Hello", false},
	}
	for _, tc := range supports {
		t.Run("Supports "+tc.method, func(t *testing.T) {
			got, err := varlink.Supports(ctx, c, tc.method)
			if err != nil || got != tc.want {
				t.Fatalf("Supports = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

func TestAddInterfaceRejectsBadIDL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		text string
	}{
		{name: "empty", text: ""},
		{name: "garbage", text: "this is not an interface"},
		{name: "unterminated method", text: "interface a.b\n\nmethod Do("},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tb, srv := newFake(t)
			runFatal(tb, func() { srv.AddInterface(tc.text) })
			msgs := tb.fatalMessages()
			if len(msgs) != 1 || !strings.Contains(msgs[0], "AddInterface") {
				t.Fatalf("fatal messages = %q", msgs)
			}
			info, err := varlink.GetInfo(t.Context(), srv.Dial())
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(info.Interfaces, []string{"org.varlink.service"}) {
				t.Errorf("rejected interface was registered: %v", info.Interfaces)
			}
		})
	}
}

func TestHandlerOverridesBuiltin(t *testing.T) {
	t.Parallel()
	srv := NewServer(t)
	On(srv, "org.varlink.service.GetInfo", func(context.Context, struct{}) (varlink.Info, error) {
		return varlink.Info{Product: "custom"}, nil
	})
	info, err := varlink.GetInfo(t.Context(), srv.Dial())
	if err != nil || info.Product != "custom" {
		t.Fatalf("info = %+v, %v", info, err)
	}
}

func TestMalformedClientsAreDropped(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
	}{
		{name: "not json", input: "not json\x00"},
		{name: "missing method", input: `{"parameters":{}}` + "\x00"},
		{name: "empty method", input: `{"method":""}` + "\x00"},
		{name: "wrong type", input: `{"method":5}` + "\x00"},
	}
	srv := NewServer(t)
	srv.Handle(helloMethod, hello)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, err := net.Dial("unix", srv.Path())
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if _, err := c.Write([]byte(tc.input)); err != nil {
				t.Fatal(err)
			}
			if n, err := c.Read(make([]byte, 16)); n != 0 || !errors.Is(err, io.EOF) {
				t.Fatalf("read = %d, %v; want the server to hang up", n, err)
			}
		})
	}
	t.Run("server keeps serving", func(t *testing.T) {
		var out greetOut
		if err := srv.Dial().Call(t.Context(), helloMethod, greetIn{"ok"}, &out); err != nil || out.Greeting != "hello ok" {
			t.Fatalf("got %+v, %v", out, err)
		}
		if got := srv.Calls(); len(got) != 1 {
			t.Errorf("malformed messages were recorded as calls: %+v", got)
		}
	})
}

func TestClose(t *testing.T) {
	t.Parallel()
	srv := NewServer(t)
	srv.Handle(helloMethod, hello)
	c := srv.Dial()
	var out greetOut
	if err := c.Call(t.Context(), helloMethod, greetIn{"x"}, &out); err != nil {
		t.Fatal(err)
	}
	srv.Close()
	srv.Close() // closing twice is harmless
	if err := c.Call(t.Context(), helloMethod, greetIn{"x"}, &out); err == nil {
		t.Error("call on a connection of a closed server succeeded")
	}
	if _, err := varlink.Dial(t.Context(), srv.Path()); err == nil {
		t.Error("dialing a closed server succeeded")
	}
}

func TestStartupFailures(t *testing.T) {
	long := strings.Repeat("d", 120) // pushes the socket path past the 108-byte limit
	tests := []struct {
		name string
		tmp  func(t *testing.T) string
		want string
	}{
		{name: "temp dir missing", tmp: func(t *testing.T) string { return filepath.Join(t.TempDir(), "absent") }, want: "varlinktest:"},
		{name: "socket path too long", tmp: func(t *testing.T) string {
			dir := filepath.Join(t.TempDir(), long)
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			return dir
		}, want: "listen"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TMPDIR", tc.tmp(t))
			tb := &fakeTB{TB: t}
			runFatal(tb, func() { NewServer(tb) })
			msgs := tb.fatalMessages()
			if len(msgs) != 1 || !strings.Contains(msgs[0], tc.want) {
				t.Fatalf("fatal messages = %q, want one containing %q", msgs, tc.want)
			}
		})
	}
}

func TestDialFailure(t *testing.T) {
	t.Parallel()
	tb, srv := newFake(t)
	srv.Close()
	runFatal(tb, func() { srv.Dial() })
	msgs := tb.fatalMessages()
	if len(msgs) != 1 || !strings.Contains(msgs[0], "dial") {
		t.Fatalf("fatal messages = %q", msgs)
	}
}

func TestCallDecode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		params  string
		want    greetIn
		wantErr bool
	}{
		{name: "object", params: `{"name":"n"}`, want: greetIn{"n"}},
		{name: "empty object", params: `{}`},
		{name: "wrong type", params: `{"name":1}`, wantErr: true},
		{name: "not json", params: `{`, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			call := &Call{Parameters: json.RawMessage(tc.params)}
			var got greetIn
			err := call.Decode(&got)
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("got %+v, %v; want %+v, error %v", got, err, tc.want, tc.wantErr)
			}
		})
	}
}
