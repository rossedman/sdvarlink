// Package varlinktest provides a fake Varlink service for unit tests.
//
// A Server listens on a temporary Unix socket and answers calls from
// handlers you register, so code built on sdvarlink can be tested without
// systemd:
//
//	srv := varlinktest.NewServer(t)
//	srv.Handle("io.systemd.Resolve.ResolveHostname",
//		func(in resolve.ResolveHostnameIn) (resolve.ResolveHostnameOut, error) {
//			return resolve.ResolveHostnameOut{Name: in.Name}, nil
//		})
//	c, err := resolve.DialPath(ctx, srv.Path())
//
// Handlers can return typed errors, stream replies, and send or receive
// file descriptors. Register interface descriptions with AddInterface so
// that introspection and Supports work against the fake, and use Record
// and Replay to turn a session against real systemd into a golden file.
package varlinktest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"syscall"
	"testing"

	"github.com/rossedman/sdvarlink/idl"
	"github.com/rossedman/sdvarlink/internal/wire"
	"github.com/rossedman/sdvarlink/varlink"
)

// Call is a call the server received.
type Call struct {
	Method     string          `json:"method"`
	Parameters json.RawMessage `json:"parameters,omitempty"`
	More       bool            `json:"more,omitempty"`
	Oneway     bool            `json:"oneway,omitempty"`
	// Files holds the descriptors sent with the call. They belong to the
	// handler; the server closes any the handler does not decode.
	Files []*os.File `json:"-"`
}

// Decode unmarshals the call parameters into v, attaching files to fd
// fields. Files not claimed by v are closed.
func (c *Call) Decode(v any) error {
	files := c.Files
	c.Files = nil
	return varlink.Unmarshal(c.Parameters, files, v)
}

// Response is one reply written by a raw handler.
//
// Files referenced by the response, through fd fields of Parameters or in
// Files, are closed after they are sent.
type Response struct {
	// Parameters is encoded with varlink.Marshal, so fd fields are sent as
	// descriptors. nil encodes as {}.
	Parameters any
	// Files are extra descriptors to send, appended after fd fields.
	Files []*os.File
}

// RawHandler handles a call. It returns the replies to send, in order; for
// calls without "more" it must return exactly one. A non-nil error is sent
// as an error reply after any replies.
type RawHandler func(ctx context.Context, call *Call) iter.Seq2[*Response, error]

// HandlerError is the error name sent when a handler returns a plain Go
// error that is not a Varlink or errno error.
const HandlerError = "io.github.rossedman.sdvarlink.varlinktest.HandlerError"

// Server is a fake Varlink service on a temporary Unix socket.
type Server struct {
	tb   testing.TB
	path string
	ln   *net.UnixListener
	ctx  context.Context
	stop context.CancelFunc
	wg   sync.WaitGroup

	mu       sync.Mutex
	handlers map[string]RawHandler
	ifaces   map[string]string // interface name -> IDL description
	calls    []Call
	fallback RawHandler // handles methods without a registered handler
	conns    map[*wire.Transport]struct{}
}

// NewServer starts a server and stops it when the test ends.
func NewServer(tb testing.TB) *Server {
	tb.Helper()
	// Socket paths are limited to 108 bytes, and t.TempDir can be longer.
	dir, err := os.MkdirTemp("", "varlinktest")
	if err != nil {
		tb.Fatalf("varlinktest: %v", err)
	}
	path := filepath.Join(dir, "sock")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		os.RemoveAll(dir)
		tb.Fatalf("varlinktest: listen: %v", err)
	}
	ctx, stop := context.WithCancel(context.Background())
	s := &Server{
		tb:       tb,
		path:     path,
		ln:       ln,
		ctx:      ctx,
		stop:     stop,
		handlers: map[string]RawHandler{},
		ifaces:   map[string]string{},
		conns:    map[*wire.Transport]struct{}{},
	}
	s.wg.Add(1)
	go s.accept()
	tb.Cleanup(func() {
		s.Close()
		os.RemoveAll(dir)
	})
	return s
}

// Path returns the socket path to dial.
func (s *Server) Path() string { return s.path }

// Dial returns a client connection to the server, closed when the test ends.
func (s *Server) Dial(opts ...varlink.Option) *varlink.Conn {
	s.tb.Helper()
	c, err := varlink.Dial(context.Background(), s.path, opts...)
	if err != nil {
		s.tb.Fatalf("varlinktest: dial: %v", err)
	}
	s.tb.Cleanup(func() { c.Close() })
	return c
}

// Close stops the server and closes all connections.
func (s *Server) Close() {
	s.mu.Lock()
	s.stop()
	s.mu.Unlock()
	s.ln.Close()
	s.mu.Lock()
	for t := range s.conns {
		t.Close()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

// Calls returns the calls received so far, in order.
func (s *Server) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Call(nil), s.calls...)
}

// AddInterface registers an interface description (IDL text), served
// through org.varlink.service introspection. Generated packages export
// theirs as InterfaceDescription.
func (s *Server) AddInterface(description string) {
	s.tb.Helper()
	iface, err := idl.Parse(description)
	if err != nil {
		s.tb.Fatalf("varlinktest: AddInterface: %v", err)
	}
	s.mu.Lock()
	s.ifaces[iface.Name] = description
	s.mu.Unlock()
}

// HandleRaw registers a raw handler for a qualified method name.
func (s *Server) HandleRaw(method string, h RawHandler) {
	s.mu.Lock()
	s.handlers[method] = h
	s.mu.Unlock()
}

// Handle registers a typed handler. fn must have one of these shapes, where
// In and Out are structs or pointers to structs:
//
//	func(In) (Out, error)
//	func(context.Context, In) (Out, error)
//	func(context.Context, In) iter.Seq2[Out, error]   // streaming
//
// Handle panics if fn has any other shape. For compile-time checking, use
// the generic On and OnStream functions.
func (s *Server) Handle(method string, fn any) {
	s.HandleRaw(method, adapt(fn))
}

// On registers a typed single-reply handler.
func On[In, Out any](s *Server, method string, fn func(context.Context, In) (Out, error)) {
	s.HandleRaw(method, func(ctx context.Context, call *Call) iter.Seq2[*Response, error] {
		return func(yield func(*Response, error) bool) {
			var in In
			if err := call.Decode(&in); err != nil {
				yield(nil, invalidParameter(err))
				return
			}
			out, err := fn(ctx, in)
			if err != nil {
				yield(nil, err)
				return
			}
			yield(&Response{Parameters: out}, nil)
		}
	})
}

// OnStream registers a typed streaming handler. Each value the sequence
// yields becomes one reply; the last one is sent without "continues".
func OnStream[In, Out any](s *Server, method string, fn func(context.Context, In) iter.Seq2[Out, error]) {
	s.HandleRaw(method, func(ctx context.Context, call *Call) iter.Seq2[*Response, error] {
		return func(yield func(*Response, error) bool) {
			var in In
			if err := call.Decode(&in); err != nil {
				yield(nil, invalidParameter(err))
				return
			}
			for out, err := range fn(ctx, in) {
				if err != nil {
					yield(nil, err)
					return
				}
				if !yield(&Response{Parameters: out}, nil) {
					return
				}
			}
		}
	})
}

var (
	ctxType = reflect.TypeOf((*context.Context)(nil)).Elem()
	errType = reflect.TypeOf((*error)(nil)).Elem()
)

func adapt(fn any) RawHandler {
	v := reflect.ValueOf(fn)
	t := v.Type()
	if t.Kind() != reflect.Func {
		panic(fmt.Sprintf("varlinktest: handler is %T, not a func", fn))
	}
	withCtx := t.NumIn() == 2 && t.In(0) == ctxType
	if t.NumIn() != 1 && !withCtx {
		panic(fmt.Sprintf("varlinktest: unsupported handler type %s", t))
	}
	inType := t.In(t.NumIn() - 1)
	stream := t.NumOut() == 1 && t.Out(0).Kind() == reflect.Func
	if !stream && (t.NumOut() != 2 || t.Out(1) != errType) {
		panic(fmt.Sprintf("varlinktest: unsupported handler type %s", t))
	}
	return func(ctx context.Context, call *Call) iter.Seq2[*Response, error] {
		return func(yield func(*Response, error) bool) {
			in := reflect.New(inType)
			if err := call.Decode(in.Interface()); err != nil {
				yield(nil, invalidParameter(err))
				return
			}
			args := []reflect.Value{in.Elem()}
			if withCtx {
				args = append([]reflect.Value{reflect.ValueOf(ctx)}, args...)
			}
			res := v.Call(args)
			if !stream {
				if err, _ := res[1].Interface().(error); err != nil {
					yield(nil, err)
					return
				}
				yield(&Response{Parameters: res[0].Interface()}, nil)
				return
			}
			// iter.Seq2[Out, error] is func(func(Out, error) bool).
			seq := res[0]
			stopped := false
			body := reflect.MakeFunc(seq.Type().In(0), func(a []reflect.Value) []reflect.Value {
				if stopped {
					return []reflect.Value{reflect.ValueOf(false)}
				}
				if err, _ := a[1].Interface().(error); err != nil {
					yield(nil, err)
					stopped = true
				} else if !yield(&Response{Parameters: a[0].Interface()}, nil) {
					stopped = true
				}
				return []reflect.Value{reflect.ValueOf(!stopped)}
			})
			seq.Call([]reflect.Value{body})
		}
	}
}

func invalidParameter(err error) error {
	return &varlink.Error{Name: varlink.ErrInvalidParameter.Name, Parameters: mustJSON(map[string]string{"parameter": err.Error()})}
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func (s *Server) accept() {
	defer s.wg.Done()
	for {
		c, err := s.ln.AcceptUnix()
		if err != nil {
			return
		}
		t := wire.New(c, varlink.DefaultMaxFrameSize, varlink.DefaultMaxFiles)
		s.mu.Lock()
		if s.ctx.Err() != nil {
			// Close already ran; it did not see this connection.
			s.mu.Unlock()
			t.Close()
			return
		}
		s.conns[t] = struct{}{}
		s.mu.Unlock()
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.serve(t)
			s.mu.Lock()
			delete(s.conns, t)
			s.mu.Unlock()
			t.Close()
		}()
	}
}

type reply struct {
	Parameters json.RawMessage `json:"parameters,omitempty"`
	Continues  bool            `json:"continues,omitempty"`
	Error      string          `json:"error,omitempty"`
}

func (s *Server) serve(t *wire.Transport) {
	for {
		data, files, err := t.ReadMessage()
		if err != nil {
			return
		}
		var call Call
		if err := json.Unmarshal(data, &call); err != nil || call.Method == "" {
			wire.CloseFiles(files)
			return
		}
		rec := call
		rec.Files = nil
		s.mu.Lock()
		s.calls = append(s.calls, rec)
		s.mu.Unlock()
		call.Files = files
		if !s.dispatch(t, &call) {
			return
		}
	}
}

// dispatch runs one call and writes its replies. It returns false if the
// connection should be closed.
func (s *Server) dispatch(t *wire.Transport, call *Call) bool {
	defer wire.CloseFiles(call.Files)
	h := s.handler(call.Method)

	// A reply is held back until the next one (or the end) shows whether
	// it continues. Like a real service, the server gives up its copies of
	// sent descriptors.
	var pending *reply
	var pendingFiles []*os.File
	drop := func() {
		wire.CloseFiles(pendingFiles)
		pending, pendingFiles = nil, nil
	}
	flush := func(continues bool) bool {
		if pending == nil {
			return true
		}
		pending.Continues = continues
		msg, _ := json.Marshal(pending)
		ok := call.Oneway || t.WriteMessage(msg, pendingFiles) == nil
		drop()
		return ok
	}
	n := 0
	for r, err := range h(s.ctx, call) {
		if err != nil {
			if call.More {
				if !flush(true) {
					return false
				}
			} else {
				// Without "more" the call gets one message: the error wins.
				drop()
			}
			name, params := errorReply(err)
			msg, _ := json.Marshal(reply{Error: name, Parameters: params})
			return call.Oneway || t.WriteMessage(msg, nil) == nil
		}
		n++
		params, files, merr := varlink.Marshal(r.Parameters)
		files = append(files, r.Files...)
		if merr != nil {
			wire.CloseFiles(files)
			s.tb.Errorf("varlinktest: %s: encode reply: %v", call.Method, merr)
			drop()
			return false
		}
		if n > 1 && !call.More {
			wire.CloseFiles(files)
			s.tb.Errorf("varlinktest: %s returned several replies to a call without 'more'", call.Method)
			break
		}
		if !flush(true) {
			wire.CloseFiles(files)
			return false
		}
		pending = &reply{Parameters: params}
		pendingFiles = files
	}
	if pending == nil && n == 0 {
		// A handler that yields nothing answers with empty parameters.
		pending = &reply{Parameters: json.RawMessage("{}")}
	}
	return flush(false)
}

// errorReply converts a handler error into an error name and parameters.
func errorReply(err error) (string, json.RawMessage) {
	var ve *varlink.Error
	if errors.As(err, &ve) {
		return ve.Name, ve.Parameters
	}
	var named varlink.NamedError
	if errors.As(err, &named) {
		params, _ := json.Marshal(named)
		return named.VarlinkError(), params
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return "io.systemd.System", mustJSON(map[string]any{"origin": "linux", "errno": int(errno)})
	}
	return HandlerError, mustJSON(map[string]string{"message": err.Error()})
}

func (s *Server) handler(method string) RawHandler {
	s.mu.Lock()
	defer s.mu.Unlock()
	if h, ok := s.handlers[method]; ok {
		return h
	}
	if s.fallback != nil {
		return s.fallback
	}
	switch method {
	case "org.varlink.service.GetInfo":
		return s.getInfo
	case "org.varlink.service.GetInterfaceDescription":
		return s.getInterfaceDescription
	}
	iface, _, _ := varlink.SplitMethod(method)
	if !s.knowsInterfaceLocked(iface) {
		return fail(&varlink.Error{Name: varlink.ErrInterfaceNotFound.Name, Parameters: mustJSON(map[string]string{"interface": iface})})
	}
	return fail(&varlink.Error{Name: varlink.ErrMethodNotFound.Name, Parameters: mustJSON(map[string]string{"method": method})})
}

func (s *Server) knowsInterfaceLocked(iface string) bool {
	if _, ok := s.ifaces[iface]; ok {
		return true
	}
	for m := range s.handlers {
		if i, _, _ := varlink.SplitMethod(m); i == iface {
			return true
		}
	}
	return false
}

// setFallback installs the handler for methods without their own, used by
// Record and Replay. The server is already accepting, so take the lock.
func (s *Server) setFallback(h RawHandler) {
	s.mu.Lock()
	s.fallback = h
	s.mu.Unlock()
}

func fail(err error) RawHandler {
	return func(context.Context, *Call) iter.Seq2[*Response, error] {
		return func(yield func(*Response, error) bool) { yield(nil, err) }
	}
}

func (s *Server) getInfo(context.Context, *Call) iter.Seq2[*Response, error] {
	s.mu.Lock()
	set := map[string]bool{"org.varlink.service": true}
	for name := range s.ifaces {
		set[name] = true
	}
	for m := range s.handlers {
		if i, _, err := varlink.SplitMethod(m); err == nil {
			set[i] = true
		}
	}
	s.mu.Unlock()
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	info := varlink.Info{Vendor: "sdvarlink", Product: "varlinktest", Version: "0", URL: "https://github.com/rossedman/sdvarlink", Interfaces: names}
	return single(info)
}

func (s *Server) getInterfaceDescription(_ context.Context, call *Call) iter.Seq2[*Response, error] {
	var in struct {
		Interface string `json:"interface"`
	}
	if err := call.Decode(&in); err != nil {
		return fail(invalidParameter(err))(nil, nil)
	}
	s.mu.Lock()
	desc, ok := s.ifaces[in.Interface]
	s.mu.Unlock()
	if !ok {
		return fail(&varlink.Error{Name: varlink.ErrInterfaceNotFound.Name, Parameters: mustJSON(map[string]string{"interface": in.Interface})})(nil, nil)
	}
	return single(map[string]string{"description": desc})
}

func single(v any) iter.Seq2[*Response, error] {
	return func(yield func(*Response, error) bool) { yield(&Response{Parameters: v}, nil) }
}
