package varlink

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net"
	"os"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/rossedman/sdvarlink/internal/wire"
)

const (
	// DefaultMaxFrameSize is the largest message a Conn accepts by default.
	DefaultMaxFrameSize = 16 << 20
	// DefaultMaxFiles is the most file descriptors a Conn accepts with one
	// read by default.
	DefaultMaxFiles = 16
)

// Option configures a Conn.
type Option func(*connOptions)

type connOptions struct {
	maxFrame int
	maxFiles int
}

// defaultOptions applies opts on top of the default frame and file limits.
func defaultOptions(opts []Option) connOptions {
	o := connOptions{maxFrame: DefaultMaxFrameSize, maxFiles: DefaultMaxFiles}
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// WithMaxFrameSize sets the largest message, in bytes, the connection will
// accept from its peer. Larger messages fail with ErrFrameTooLarge.
func WithMaxFrameSize(n int) Option { return func(o *connOptions) { o.maxFrame = n } }

// WithMaxFiles sets how many file descriptors the connection accepts with a
// single read and sends with a single message.
func WithMaxFiles(n int) Option { return func(o *connOptions) { o.maxFiles = n } }

// CallOption configures a single call.
type CallOption func(*callOptions)

type callOptions struct {
	files []*os.File
}

// WithFiles sends extra file descriptors with the call. They are appended
// after any descriptors taken from fd-tagged fields of the input, so their
// indexes start at the number of such fields (0 for raw calls). The caller
// keeps ownership of the files.
func WithFiles(files ...*os.File) CallOption {
	return func(o *callOptions) { o.files = append(o.files, files...) }
}

// Caller is the interface shared by *Conn and *Pool. Generated clients are
// built on it, and tests can substitute their own implementation.
type Caller interface {
	Call(ctx context.Context, method string, in, out any, opts ...CallOption) error
	Stream(ctx context.Context, method string, in any, opts ...CallOption) iter.Seq2[*Reply, error]
	Oneway(ctx context.Context, method string, in any, opts ...CallOption) error
}

// Reply is one reply to a call. Streaming calls yield several.
type Reply struct {
	// Parameters holds the raw reply parameters.
	Parameters json.RawMessage
	// Files holds the descriptors sent with this reply. Decode hands them
	// to fd fields; otherwise the caller must close them (see Close).
	Files []*os.File
	// Continues is true if more replies follow.
	Continues bool
}

// carriesNothing reports whether the reply has no parameters (absent, null
// or {}) and no file descriptors.
func (r *Reply) carriesNothing() bool {
	if len(r.Files) > 0 {
		return false
	}
	p := bytes.TrimSpace(r.Parameters)
	if len(p) == 0 || bytes.Equal(p, []byte("null")) {
		return true
	}
	var m map[string]json.RawMessage
	return json.Unmarshal(p, &m) == nil && len(m) == 0
}

// Decode unmarshals the reply into v and attaches received files to its fd
// fields. Files that no field claims are closed. Decode can be called once.
func (r *Reply) Decode(v any) error {
	files := r.Files
	r.Files = nil
	return Unmarshal(r.Parameters, files, v)
}

// Close closes any files that were not handed out by Decode.
func (r *Reply) Close() {
	wire.CloseFiles(r.Files)
	r.Files = nil
}

// Conn is a client connection to one Varlink service. The protocol answers
// calls in order, so a Conn runs one call at a time; concurrent callers
// wait their turn. Use a Pool for concurrency.
//
// If a call's context ends before its reply arrives, or a stream is
// abandoned before its last reply, the connection is closed: the protocol
// has no way to cancel a call, so the stream position would be lost.
//
// A stream holds the connection until it ends, so do not call the same
// Conn from inside a Stream loop: the inner call would wait forever. Use a
// Pool (generated clients do), which hands the inner call another
// connection.
type Conn struct {
	nc   *net.UnixConn
	t    *wire.Transport
	sem  chan struct{}
	done chan struct{}

	mu   sync.Mutex
	err  error // set once the connection is unusable
	once sync.Once

	dlMu sync.Mutex
	gen  uint64 // call generation, so a stale cancellation cannot hit a later call
}

// Dial connects to the Varlink service listening on the Unix socket at path.
func Dial(ctx context.Context, path string, opts ...Option) (*Conn, error) {
	if !wire.Supported {
		return nil, ErrUnsupported
	}
	var d net.Dialer
	nc, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	return NewConn(nc.(*net.UnixConn), opts...), nil
}

// DialService is Dial for systemd services with well-known socket paths. It
// returns an error matching ErrUnsupported when the platform is not Linux or
// nothing listens at path, so callers can fall back to another API.
func DialService(ctx context.Context, path string, opts ...Option) (*Conn, error) {
	if runtime.GOOS != "linux" {
		return nil, ErrUnsupported
	}
	c, err := Dial(ctx, path, opts...)
	if err != nil {
		if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED) {
			return nil, fmt.Errorf("%w: %s: %w", ErrUnsupported, path, err)
		}
		return nil, err
	}
	return c, nil
}

// NewConn wraps an established Unix stream connection, for example one end
// of a socketpair or a socket received through socket activation.
func NewConn(nc *net.UnixConn, opts ...Option) *Conn {
	o := defaultOptions(opts)
	return &Conn{
		nc:   nc,
		t:    wire.New(nc, o.maxFrame, o.maxFiles),
		sem:  make(chan struct{}, 1),
		done: make(chan struct{}),
	}
}

// Close closes the connection. Calls waiting for their turn fail with
// ErrClosed.
func (c *Conn) Close() error {
	c.fail(ErrClosed)
	return nil
}

// Err returns nil while the connection is usable, and the reason it was
// closed afterwards.
func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// fail marks the connection unusable with err, once, and closes the transport.
// Later calls are no-ops.
func (c *Conn) fail(err error) {
	c.once.Do(func() {
		c.mu.Lock()
		c.err = err
		c.mu.Unlock()
		close(c.done)
		// Closing the transport also closes received descriptors that no
		// reply claimed yet.
		c.t.Close()
	})
}

// acquire waits for this connection's single call slot.
func (c *Conn) acquire(ctx context.Context) error {
	if err := c.Err(); err != nil {
		return err
	}
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return c.Err()
	}
	// select picks randomly among ready cases: do not start a call whose
	// context has already ended, or on a connection closed meanwhile.
	if err := ctx.Err(); err != nil {
		<-c.sem
		return err
	}
	if err := c.Err(); err != nil {
		<-c.sem
		return err
	}
	return nil
}

// release gives up the connection's call slot taken by acquire.
func (c *Conn) release() { <-c.sem }

var longAgo = time.Unix(1, 0)

// begin applies ctx's deadline and cancellation to the socket. The returned
// function undoes that; it closes the connection when the call did not
// finish cleanly, since the reply stream would be out of step.
func (c *Conn) begin(ctx context.Context) func(clean bool) {
	c.dlMu.Lock()
	c.gen++
	gen := c.gen
	dl, _ := ctx.Deadline()
	_ = c.nc.SetDeadline(dl)
	c.dlMu.Unlock()
	stop := context.AfterFunc(ctx, func() {
		c.dlMu.Lock()
		defer c.dlMu.Unlock()
		if c.gen == gen {
			_ = c.nc.SetDeadline(longAgo)
		}
	})
	return func(clean bool) {
		stop()
		c.dlMu.Lock()
		c.gen++
		_ = c.nc.SetDeadline(time.Time{})
		c.dlMu.Unlock()
		if clean {
			return
		}
		if err := ctx.Err(); err != nil {
			c.fail(fmt.Errorf("varlink: call abandoned: %w", err))
		} else {
			c.fail(errors.New("varlink: call abandoned before its last reply"))
		}
	}
}

// ctxErr prefers the context's error over the I/O error it caused.
func (c *Conn) ctxErr(ctx context.Context, err error) error {
	if cerr := ctx.Err(); cerr != nil {
		return cerr
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return context.DeadlineExceeded
	}
	if errors.Is(err, net.ErrClosed) {
		if cerr := c.Err(); cerr != nil {
			return cerr
		}
	}
	return err
}

type callMessage struct {
	Method     string          `json:"method"`
	Parameters json.RawMessage `json:"parameters,omitempty"`
	More       bool            `json:"more,omitempty"`
	Oneway     bool            `json:"oneway,omitempty"`
}

type replyMessage struct {
	Parameters json.RawMessage `json:"parameters"`
	Continues  bool            `json:"continues"`
	Error      string          `json:"error"`
}

// outgoing is an encoded call, ready to write.
type outgoing struct {
	msg   []byte
	files []*os.File
}

// encode builds the call message. It runs before the call takes the
// connection, so encoding errors leave the connection usable.
func (c *Conn) encode(method string, in any, more, oneway bool, opts []CallOption) (*outgoing, error) {
	var co callOptions
	for _, opt := range opts {
		opt(&co)
	}
	params, files, err := Marshal(in)
	if err != nil {
		return nil, fmt.Errorf("varlink: encode %s parameters: %w", method, err)
	}
	files = append(files, co.files...)
	if max := c.t.MaxFiles(); len(files) > max {
		return nil, fmt.Errorf("%w: %s sends %d files, limit is %d (see WithMaxFiles)", ErrTooManyFiles, method, len(files), max)
	}
	msg, err := json.Marshal(callMessage{Method: method, Parameters: params, More: more, Oneway: oneway})
	if err != nil {
		return nil, err
	}
	return &outgoing{msg: msg, files: files}, nil
}

// send writes one prepared message with its files.
func (c *Conn) send(out *outgoing) error { return c.t.WriteMessage(out.msg, out.files) }

// recv reads one reply. A reply carrying an error is returned as an error;
// done reports whether the call is over either way.
func (c *Conn) recv() (r *Reply, done bool, err error) {
	data, files, err := c.t.ReadMessage()
	if err != nil {
		return nil, true, err
	}
	var m replyMessage
	if err := json.Unmarshal(data, &m); err != nil {
		wire.CloseFiles(files)
		perr := &ProtocolError{Msg: "malformed reply", Err: err}
		c.fail(perr)
		return nil, true, perr
	}
	if m.Error != "" {
		wire.CloseFiles(files)
		return nil, true, replyError(m.Error, m.Parameters)
	}
	return &Reply{Parameters: m.Parameters, Files: files, Continues: m.Continues}, !m.Continues, nil
}

// Call invokes method with parameters in and decodes the single reply into
// out. in may be nil or any value that encodes to a JSON object; out may be
// nil to discard the reply. Error replies are returned as *Error (or
// *SystemError); generated clients turn them into typed errors.
func (c *Conn) Call(ctx context.Context, method string, in, out any, opts ...CallOption) error {
	r, err := c.CallReply(ctx, method, in, opts...)
	if err != nil {
		return err
	}
	return r.Decode(out)
}

// CallReply is Call without decoding: it returns the raw reply, including
// any file descriptors, which the caller must Decode or Close.
func (c *Conn) CallReply(ctx context.Context, method string, in any, opts ...CallOption) (*Reply, error) {
	out, err := c.encode(method, in, false, false, opts)
	if err != nil {
		return nil, err
	}
	if err := c.acquire(ctx); err != nil {
		return nil, err
	}
	defer c.release()
	end := c.begin(ctx)
	clean := false
	defer func() { end(clean) }()

	if err := c.send(out); err != nil {
		return nil, c.ctxErr(ctx, err)
	}
	r, done, err := c.recv()
	if err != nil {
		var ve NamedError
		clean = errors.As(err, &ve)
		return nil, c.ctxErr(ctx, err)
	}
	if !done {
		r.Close()
		perr := &ProtocolError{Msg: method + ": reply continues although 'more' was not requested"}
		c.fail(perr)
		return nil, perr
	}
	clean = true
	return r, nil
}

// Oneway invokes method without waiting for, or receiving, a reply.
func (c *Conn) Oneway(ctx context.Context, method string, in any, opts ...CallOption) error {
	out, err := c.encode(method, in, false, true, opts)
	if err != nil {
		return err
	}
	if err := c.acquire(ctx); err != nil {
		return err
	}
	defer c.release()
	end := c.begin(ctx)
	err = c.send(out)
	end(err == nil)
	if err != nil {
		return c.ctxErr(ctx, err)
	}
	return nil
}

// Stream invokes method with the "more" flag and yields each reply as it
// arrives. The sequence ends after the reply that does not continue, or
// with an error. A final reply with no parameters and no file descriptors
// is not yielded: sd-varlink services send one to end an empty stream, so an
// empty list yields nothing. Breaking out of the loop early, or cancelling
// ctx, closes the connection.
//
//	for r, err := range conn.Stream(ctx, "io.systemd.UserDatabase.GetUserRecord", in) {
//		if err != nil {
//			return err
//		}
//		var rec UserRecord
//		if err := r.Decode(&rec); err != nil {
//			return err
//		}
//	}
func (c *Conn) Stream(ctx context.Context, method string, in any, opts ...CallOption) iter.Seq2[*Reply, error] {
	return func(yield func(*Reply, error) bool) {
		out, err := c.encode(method, in, true, false, opts)
		if err != nil {
			yield(nil, err)
			return
		}
		if err := c.acquire(ctx); err != nil {
			yield(nil, err)
			return
		}
		defer c.release()
		end := c.begin(ctx)
		clean := false
		defer func() { end(clean) }()

		if err := c.send(out); err != nil {
			yield(nil, c.ctxErr(ctx, err))
			return
		}
		for {
			r, done, err := c.recv()
			if err != nil {
				var ve NamedError
				clean = errors.As(err, &ve)
				yield(nil, c.ctxErr(ctx, err))
				return
			}
			if done && r.carriesNothing() {
				// sd-varlink ends an empty stream, or a live stream whose
				// producer is done, with an empty final reply; it marks the
				// end and is not a value.
				clean = true
				return
			}
			if !yield(r, nil) {
				clean = done
				return
			}
			if done {
				clean = true
				return
			}
		}
	}
}

// StreamAs decodes each reply of a stream into a new T.
func StreamAs[T any](seq iter.Seq2[*Reply, error]) iter.Seq2[*T, error] {
	return func(yield func(*T, error) bool) {
		for r, err := range seq {
			if err != nil {
				yield(nil, err)
				return
			}
			v := new(T)
			if err := r.Decode(v); err != nil {
				yield(nil, err)
				return
			}
			if !yield(v, nil) {
				return
			}
		}
	}
}
