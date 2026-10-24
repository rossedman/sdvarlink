package varlinktest

import (
	"bytes"
	"context"
	"encoding/json"
	"iter"
	"os"
	"sync"
	"testing"

	"github.com/rossedman/sdvarlink/varlink"
)

// Exchange is one recorded call and its replies.
type Exchange struct {
	Call    Call              `json:"call"`
	Replies []json.RawMessage `json:"replies,omitempty"`
	Error   *ExchangeError    `json:"error,omitempty"`
}

// ExchangeError is a recorded error reply.
type ExchangeError struct {
	Name       string          `json:"name"`
	Parameters json.RawMessage `json:"parameters,omitempty"`
}

// Record starts a server that forwards every call to the real service at
// upstream and, when the test ends, writes the exchanges to golden as
// indented JSON. Point the code under test at the returned server's Path.
//
// File descriptors are forwarded in both directions but not recorded.
//
//	var update = flag.Bool("update", false, "record golden files against live systemd")
//
//	func newServer(t *testing.T) *varlinktest.Server {
//		if *update {
//			return varlinktest.Record(t, "/run/systemd/resolve/io.systemd.Resolve", "testdata/resolve.json")
//		}
//		return varlinktest.Replay(t, "testdata/resolve.json")
//	}
func Record(tb testing.TB, upstream, golden string) *Server {
	tb.Helper()
	up, err := varlink.Dial(context.Background(), upstream)
	if err != nil {
		tb.Fatalf("varlinktest: Record: %v", err)
	}
	s := NewServer(tb)
	var mu sync.Mutex
	var log []Exchange
	s.setFallback(func(ctx context.Context, call *Call) iter.Seq2[*Response, error] {
		return func(yield func(*Response, error) bool) {
			ex := Exchange{Call: *call}
			ex.Call.Files = nil
			defer func() {
				mu.Lock()
				log = append(log, ex)
				mu.Unlock()
			}()
			params := call.Parameters
			opts := []varlink.CallOption{varlink.WithFiles(call.Files...)}
			if call.Oneway {
				if err := up.Oneway(ctx, call.Method, params, opts...); err != nil {
					yield(nil, err)
				}
				return
			}
			seq := up.Stream(ctx, call.Method, params, opts...)
			if !call.More {
				seq = func(yield func(*varlink.Reply, error) bool) {
					yield(up.CallReply(ctx, call.Method, params, opts...))
				}
			}
			for r, err := range seq {
				if err != nil {
					name, p := errorReply(err)
					ex.Error = &ExchangeError{Name: name, Parameters: p}
					yield(nil, err)
					return
				}
				ex.Replies = append(ex.Replies, r.Parameters)
				resp := &Response{Parameters: r.Parameters, Files: r.Files}
				if !yield(resp, nil) {
					return
				}
			}
		}
	})
	tb.Cleanup(func() {
		up.Close()
		mu.Lock()
		defer mu.Unlock()
		data, err := json.MarshalIndent(log, "", "  ")
		if err != nil {
			tb.Errorf("varlinktest: Record: %v", err)
			return
		}
		if err := os.WriteFile(golden, append(data, '\n'), 0o644); err != nil {
			tb.Errorf("varlinktest: Record: %v", err)
		}
	})
	return s
}

// Replay starts a server that answers calls from a golden file written by
// Record. Calls must arrive in the recorded order per method, with equal
// parameters; a mismatch fails the test.
func Replay(tb testing.TB, golden string) *Server {
	tb.Helper()
	data, err := os.ReadFile(golden)
	if err != nil {
		tb.Fatalf("varlinktest: Replay: %v", err)
	}
	var log []Exchange
	if err := json.Unmarshal(data, &log); err != nil {
		tb.Fatalf("varlinktest: Replay: %s: %v", golden, err)
	}
	queues := map[string][]Exchange{}
	for _, ex := range log {
		queues[ex.Call.Method] = append(queues[ex.Call.Method], ex)
	}
	s := NewServer(tb)
	var mu sync.Mutex
	s.setFallback(func(ctx context.Context, call *Call) iter.Seq2[*Response, error] {
		mu.Lock()
		q := queues[call.Method]
		var ex Exchange
		ok := len(q) > 0
		if ok {
			ex, queues[call.Method] = q[0], q[1:]
		}
		mu.Unlock()
		if !ok {
			tb.Errorf("varlinktest: Replay: unexpected call to %s", call.Method)
			return fail(&varlink.Error{Name: varlink.ErrMethodNotFound.Name})(ctx, call)
		}
		if !jsonEqual(ex.Call.Parameters, call.Parameters) {
			tb.Errorf("varlinktest: Replay: %s called with %s, recorded %s", call.Method, call.Parameters, ex.Call.Parameters)
		}
		return func(yield func(*Response, error) bool) {
			for _, r := range ex.Replies {
				if !yield(&Response{Parameters: r}, nil) {
					return
				}
			}
			if ex.Error != nil {
				yield(nil, &varlink.Error{Name: ex.Error.Name, Parameters: ex.Error.Parameters})
			}
		}
	})
	return s
}

// jsonEqual reports whether a and b hold the same JSON value, ignoring
// formatting and key order. Empty input equals {}; input that is not valid
// JSON is compared as raw text.
func jsonEqual(a, b json.RawMessage) bool {
	norm := func(m json.RawMessage) any {
		if len(bytes.TrimSpace(m)) == 0 {
			return map[string]any{}
		}
		var v any
		if json.Unmarshal(m, &v) != nil {
			return string(m)
		}
		return v
	}
	x, _ := json.Marshal(norm(a))
	y, _ := json.Marshal(norm(b))
	return bytes.Equal(x, y)
}
