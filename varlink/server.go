package varlink

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"reflect"
	"runtime/debug"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/rossedman/sdvarlink/idl"
	"github.com/rossedman/sdvarlink/internal/wire"
)

// ServerInfo describes the service in org.varlink.service.GetInfo replies.
type ServerInfo struct {
	Vendor  string
	Product string
	Version string
	URL     string
}

// Server serves Varlink interfaces on Unix sockets.
//
//	srv := varlink.NewServer(varlink.ServerInfo{Vendor: "Example", Product: "queued", Version: "1.2.0"})
//	iface := srv.Interface("io.example.Queue", queueIDL)
//	varlink.Handle(iface, "Depth", func(ctx context.Context, call *varlink.Call, in DepthIn) (DepthOut, error) {
//		return DepthOut{Jobs: q.Len()}, nil
//	})
//	l, err := varlink.Listen("/run/queued/io.example.Queue")
//	if err != nil {
//		return err
//	}
//	return srv.Serve(ctx, l)
//
// Calls on one connection run one at a time, in order; replies go back in
// the same order, so clients may pipeline calls. Each connection runs in
// its own goroutine. Parameters are validated against the interface
// description before a handler runs, as sd-varlink does.
type Server struct {
	info ServerInfo
	cfg  serverConfig

	mu     sync.RWMutex
	ifaces map[string]*Interface
}

type serverConfig struct {
	maxFrame    int
	maxFiles    int
	maxConns    int
	idleTimeout time.Duration
	grace       time.Duration
	logger      *slog.Logger
}

// ServerOption configures a Server.
type ServerOption func(*serverConfig)

// WithMaxConnections limits concurrent connections (default 256).
// Connections beyond the limit are closed as soon as they are accepted.
func WithMaxConnections(n int) ServerOption { return func(c *serverConfig) { c.maxConns = n } }

// WithIdleTimeout closes connections that have no call in flight for d.
// The default, 0, never closes idle connections.
func WithIdleTimeout(d time.Duration) ServerOption {
	return func(c *serverConfig) { c.idleTimeout = d }
}

// WithShutdownGrace sets how long in-flight calls may run after Serve's
// context ends before their contexts are cancelled (default 5s).
func WithShutdownGrace(d time.Duration) ServerOption { return func(c *serverConfig) { c.grace = d } }

// WithLogger sets the logger for handler errors, panics and protocol
// problems (default slog.Default()). Call parameters are never logged.
func WithLogger(l *slog.Logger) ServerOption { return func(c *serverConfig) { c.logger = l } }

// WithServerMaxFrameSize sets the largest call the server accepts (default
// 16 MiB). A larger call closes the connection.
func WithServerMaxFrameSize(n int) ServerOption { return func(c *serverConfig) { c.maxFrame = n } }

// WithServerMaxFiles sets how many file descriptors a call may carry, and a
// reply may send (default 16).
func WithServerMaxFiles(n int) ServerOption { return func(c *serverConfig) { c.maxFiles = n } }

// NewServer returns a server with no interfaces.
func NewServer(info ServerInfo, opts ...ServerOption) *Server {
	cfg := serverConfig{
		maxFrame: DefaultMaxFrameSize,
		maxFiles: DefaultMaxFiles,
		maxConns: 256,
		grace:    5 * time.Second,
		logger:   slog.Default(),
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return &Server{info: info, cfg: cfg, ifaces: map[string]*Interface{}}
}

// Interface registers an interface by its IDL text and returns it, ready
// for handlers. The text is what GetInterfaceDescription returns, so
// introspection always matches what is served. Interface panics if the
// IDL does not parse, does not declare name, or name is already
// registered: these are programming errors to catch at startup.
func (s *Server) Interface(name, description string) *Interface {
	parsed, err := idl.Parse(description)
	if err != nil {
		panic(fmt.Sprintf("varlink: Interface(%q): %v", name, err))
	}
	if parsed.Name != name {
		panic(fmt.Sprintf("varlink: Interface(%q): IDL declares interface %q", name, parsed.Name))
	}
	if name == serviceInterface {
		panic("varlink: org.varlink.service is built in")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.ifaces[name]; dup {
		panic(fmt.Sprintf("varlink: interface %q registered twice", name))
	}
	iface := &Interface{srv: s, name: name, description: description, def: parsed, methods: map[string]*method{}}
	s.ifaces[name] = iface
	return iface
}

// Interface is a registered interface. Add handlers with Handle and
// HandleStream. Methods declared in the IDL without a handler answer
// org.varlink.service.MethodNotImplemented.
type Interface struct {
	srv         *Server
	name        string
	description string
	def         *idl.Interface
	methods     map[string]*method
}

// Name returns the interface name.
func (i *Interface) Name() string { return i.name }

type method struct {
	def          *idl.Method
	stream       bool
	allowSingle  bool
	allowUnknown bool
	holdLast     bool
	run          func(ctx context.Context, call *Call, params []byte, files []*os.File, w *replyWriter) error
}

// HandlerOption adjusts how a method is served.
type HandlerOption func(*method)

// AllowSingle lets a HandleStream handler answer a call made without the
// "more" flag with a single reply: the first value it sends. Without it,
// such calls get org.varlink.service.ExpectedMore.
func AllowSingle() HandlerOption { return func(m *method) { m.allowSingle = true } }

// LastIsFinal makes a HandleStream handler's last value the final reply,
// instead of following it with an empty one. Use it for methods that
// return a finite list, such as io.systemd.Metrics.List: each reply is
// held back until the next send (or the handler's return) shows whether
// more follow, as sd-varlink does for list replies. Do not use it for live
// streams: a held-back event would wait for the next one.
func LastIsFinal() HandlerOption { return func(m *method) { m.holdLast = true } }

// AllowUnknownFields accepts call parameters the IDL does not define,
// instead of answering InvalidParameter.
func AllowUnknownFields() HandlerOption { return func(m *method) { m.allowUnknown = true } }

func (i *Interface) register(name string, inType, outType any, stream bool, run func(context.Context, *Call, []byte, []*os.File, *replyWriter) error, opts []HandlerOption) {
	def := i.def.Method(name)
	if def == nil {
		panic(fmt.Sprintf("varlink: %s.%s is not in the interface's IDL", i.name, name))
	}
	if !stream && def.More == idl.MoreRequired {
		panic(fmt.Sprintf("varlink: %s.%s requires 'more': register it with HandleStream", i.name, name))
	}
	if err := checkStruct(i.def, reflectType(inType), def.In, false); err != nil {
		panic(fmt.Sprintf("varlink: %s.%s input type does not match the IDL: %v", i.name, name, err))
	}
	if err := checkStruct(i.def, reflectType(outType), def.Out, true); err != nil {
		panic(fmt.Sprintf("varlink: %s.%s output type does not match the IDL: %v", i.name, name, err))
	}
	m := &method{def: def, stream: stream, run: run}
	for _, opt := range opts {
		opt(m)
	}
	i.srv.mu.Lock()
	defer i.srv.mu.Unlock()
	if _, dup := i.methods[name]; dup {
		panic(fmt.Sprintf("varlink: %s.%s registered twice", i.name, name))
	}
	i.methods[name] = m
}

// Handle registers fn as the handler for a method of iface. In and Out
// are the method's parameter and reply structs, for example the
// <Method>In and <Method>Out types of a generated package; struct{} works
// for methods without parameters or reply fields. Handle panics if the
// method is not in the IDL, requires "more", or In or Out disagree with it.
//
// The handler's ctx ends when the client disconnects or the server shuts
// down. Return a *Error (or a generated typed error) to send that Varlink
// error; see Server for how other errors are reported. Files the handler
// receives in fd fields of In are its to close; files in Out are closed
// once the reply is sent.
func Handle[In, Out any](iface *Interface, name string, fn func(ctx context.Context, call *Call, in In) (Out, error), opts ...HandlerOption) {
	var zi In
	var zo Out
	iface.register(name, &zi, &zo, false, func(ctx context.Context, call *Call, params []byte, files []*os.File, w *replyWriter) error {
		var in In
		if err := Unmarshal(params, files, &in); err != nil {
			return invalidParameter(fieldOf(err))
		}
		out, err := fn(ctx, call, in)
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err() // abandoned: the connection closes without a reply
		}
		return w.final(out)
	}, opts)
}

// HandleStream registers fn as a streaming handler. Each send writes one
// reply with "continues" set, immediately, which suits live streams; when
// fn returns, an empty final reply ends the stream. For finite lists use
// LastIsFinal, which makes the last value the final reply. Calls made
// without "more" get org.varlink.service.ExpectedMore unless the method is
// registered with AllowSingle. send returns an error once the client is
// gone; the handler should then return.
func HandleStream[In, Out any](iface *Interface, name string, fn func(ctx context.Context, call *Call, in In, send func(Out) error) error, opts ...HandlerOption) {
	var zi In
	var zo Out
	iface.register(name, &zi, &zo, true, func(ctx context.Context, call *Call, params []byte, files []*os.File, w *replyWriter) error {
		var in In
		if err := Unmarshal(params, files, &in); err != nil {
			return invalidParameter(fieldOf(err))
		}
		err := fn(ctx, call, in, func(out Out) error { return w.send(out) })
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err() // abandoned: the connection closes without a final reply
		}
		return w.finish()
	}, opts)
}

// reflectType returns the type a zero-value pointer points to.
func reflectType(p any) reflect.Type { return reflect.TypeOf(p).Elem() }

// Call describes the call a handler is serving.
type Call struct {
	method string
	more   bool
	oneway bool
	conn   *serverConn
}

// Method returns the qualified method name.
func (c *Call) Method() string { return c.method }

// More reports whether the client asked for a stream of replies.
func (c *Call) More() bool { return c.more }

// Oneway reports whether the client asked for no reply.
func (c *Call) Oneway() bool { return c.oneway }

// Peer returns the credentials of the connected process, read once per
// connection with SO_PEERCRED. Authorize on UID and GID; the PID is
// informational, since PIDs are reused.
func (c *Call) Peer() (Peer, error) { return c.conn.peer() }

// Peer holds a client's credentials.
type Peer struct {
	UID uint32
	GID uint32
	PID int32
}

const serviceInterface = "org.varlink.service"

// serviceDescription is the IDL of the built-in interface.
const serviceDescription = `# The Varlink Service Interface is provided by every varlink service. It
# describes the service and the interfaces it implements.
interface org.varlink.service

# Get a list of all the interfaces a service provides and information
# about the implementation.
method GetInfo() -> (
	vendor: string,
	product: string,
	version: string,
	url: string,
	interfaces: []string
)

# Get the description of an interface that is implemented by this service.
method GetInterfaceDescription(interface: string) -> (description: string)

# The requested interface was not found.
error InterfaceNotFound(interface: string)

# The requested method was not found
error MethodNotFound(method: string)

# The interface defines the requested method, but the service does not
# implement it.
error MethodNotImplemented(method: string)

# One of the passed parameters is invalid.
error InvalidParameter(parameter: string)

# Client is denied access
error PermissionDenied()

# Method is expected to be called with 'more' set to true, but wasn't
error ExpectedMore()
`

// Serve accepts connections on l until ctx ends, then shuts down: it
// stops accepting, lets in-flight calls finish for the grace period
// (streaming handlers see their context cancelled at once), closes the
// remaining connections and returns nil. It returns early with an error
// only if accepting fails. l must yield Unix connections; use Listen or
// ListenActivated, or any *net.UnixListener.
func (s *Server) Serve(ctx context.Context, l net.Listener) error {
	// hard ends every call: at the end of the grace period after shutdown.
	hard, cancelHard := context.WithCancel(context.Background())
	defer cancelHard()
	// streams ends streaming handlers as soon as shutdown starts.
	streams, cancelStreams := context.WithCancel(hard)
	defer cancelStreams()
	shutdown := make(chan struct{})

	var wg sync.WaitGroup
	var connMu sync.Mutex
	conns := map[*serverConn]struct{}{}
	acceptErr := make(chan error, 1)
	acceptDone := make(chan struct{})

	go func() {
		defer close(acceptDone)
		sem := make(chan struct{}, max(s.cfg.maxConns, 1))
		var backoff time.Duration
		for {
			nc, err := l.Accept()
			if err != nil {
				if temporaryAcceptError(err) {
					// Out of descriptors or memory, or an aborted
					// connection: wait and retry, as net/http does.
					backoff = min(max(2*backoff, 5*time.Millisecond), time.Second)
					s.cfg.logger.Warn("varlink: accept failed, retrying", "err", err, "delay", backoff)
					time.Sleep(backoff)
					continue
				}
				acceptErr <- err
				return
			}
			backoff = 0
			uc, ok := nc.(*net.UnixConn)
			if !ok {
				nc.Close()
				s.cfg.logger.Warn("varlink: refusing non-Unix connection")
				continue
			}
			select {
			case sem <- struct{}{}:
			default:
				s.cfg.logger.Warn("varlink: connection limit reached, closing new connection", "limit", s.cfg.maxConns)
				uc.Close()
				continue
			}
			sc := s.newConn(uc, hard, streams, shutdown)
			connMu.Lock()
			conns[sc] = struct{}{}
			connMu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				sc.serve()
				connMu.Lock()
				delete(conns, sc)
				connMu.Unlock()
			}()
		}
	}()

	var err error
	select {
	case <-ctx.Done():
	case err = <-acceptErr:
	}
	l.Close()
	<-acceptDone // no wg.Add can race the Wait below
	close(shutdown)
	cancelStreams()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(s.cfg.grace):
		cancelHard()
		connMu.Lock()
		for sc := range conns {
			sc.t.Close()
		}
		connMu.Unlock()
		// Handlers that ignore their context cannot be stopped; give them
		// a moment, then return without them.
		select {
		case <-done:
		case <-time.After(time.Second):
			s.cfg.logger.Warn("varlink: handlers still running after shutdown grace period")
		}
	}
	if err != nil && ctx.Err() == nil && !errors.Is(err, net.ErrClosed) {
		return err
	}
	return nil
}

func temporaryAcceptError(err error) bool {
	if errors.Is(err, net.ErrClosed) {
		return false
	}
	for _, e := range []syscall.Errno{syscall.EMFILE, syscall.ENFILE, syscall.ENOBUFS, syscall.ENOMEM, syscall.ECONNABORTED, syscall.EINTR} {
		if errors.Is(err, e) {
			return true
		}
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

type incoming struct {
	data  []byte
	files []*os.File
	err   error
}

type serverConn struct {
	s        *Server
	nc       *net.UnixConn
	t        *wire.Transport
	hard     context.Context
	streams  context.Context
	shutdown <-chan struct{}

	writeMu sync.Mutex

	peerOnce sync.Once
	peerVal  Peer
	peerErr  error
}

func (s *Server) newConn(nc *net.UnixConn, hard, streams context.Context, shutdown <-chan struct{}) *serverConn {
	return &serverConn{
		s: s, nc: nc,
		t:    wire.New(nc, s.cfg.maxFrame, s.cfg.maxFiles),
		hard: hard, streams: streams, shutdown: shutdown,
	}
}

func (sc *serverConn) peer() (Peer, error) {
	sc.peerOnce.Do(func() { sc.peerVal, sc.peerErr = peerCredentials(sc.nc) })
	return sc.peerVal, sc.peerErr
}

func (sc *serverConn) serve() {
	defer sc.t.Close()
	// The client's lifetime: ends when it disconnects or calls are cut off.
	connCtx, cancel := context.WithCancel(sc.hard)
	defer cancel()

	// A reader goroutine keeps reading while a handler runs, so pipelined
	// calls queue up and a disconnect cancels the running handler.
	msgs := make(chan incoming, 8)
	go func() {
		defer close(msgs)
		for {
			data, files, err := sc.t.ReadMessage()
			if err != nil {
				if errors.Is(err, io.EOF) && !hungUp(sc.nc) {
					// Half-close: the client sent its last call and still
					// reads. Serve what is queued, then close, as sd-varlink
					// does.
					return
				}
				if errors.Is(err, ErrFrameTooLarge) {
					msgs <- incoming{err: err}
				}
				cancel()
				return
			}
			select {
			case msgs <- incoming{data: data, files: files}:
			case <-connCtx.Done():
				wire.CloseFiles(files)
				return
			}
		}
	}()
	defer func() {
		// Close the socket so the reader's blocked read returns, then
		// drain so queued files are closed.
		cancel()
		sc.t.Close()
		for m := range msgs {
			wire.CloseFiles(m.files)
		}
	}()

	var idle <-chan time.Time
	for {
		// After shutdown begins, take no new calls, even pipelined ones.
		select {
		case <-sc.shutdown:
			return
		default:
		}
		var timer *time.Timer
		if d := sc.s.cfg.idleTimeout; d > 0 {
			timer = time.NewTimer(d)
			idle = timer.C
		}
		var m incoming
		var ok bool
		select {
		case m, ok = <-msgs:
		case <-idle:
			return
		case <-sc.shutdown:
			return
		}
		if timer != nil {
			timer.Stop()
		}
		if !ok {
			return
		}
		if m.err != nil {
			sc.writeError("", errnoError(syscall.ENOBUFS))
			sc.s.cfg.logger.Warn("varlink: call exceeds frame size limit, closing connection", "limit", sc.s.cfg.maxFrame)
			return
		}
		if !sc.dispatch(connCtx, m) {
			return
		}
	}
}

type callMsg struct {
	Method     string          `json:"method"`
	Parameters json.RawMessage `json:"parameters"`
	More       bool            `json:"more"`
	Oneway     bool            `json:"oneway"`
	Upgrade    bool            `json:"upgrade"`
}

// dispatch runs one call. It returns false when the connection must close.
func (sc *serverConn) dispatch(connCtx context.Context, m incoming) bool {
	var cm callMsg
	if err := json.Unmarshal(m.data, &cm); err != nil || cm.Method == "" || !isObjectOrNull(cm.Parameters) {
		// sd-varlink treats these as protocol errors and disconnects.
		wire.CloseFiles(m.files)
		sc.s.cfg.logger.Warn("varlink: malformed call, closing connection")
		return false
	}
	call := &Call{method: cm.Method, more: cm.More, oneway: cm.Oneway, conn: sc}
	w := &replyWriter{sc: sc, call: call, maxFiles: sc.s.cfg.maxFiles}

	files := m.files
	err := sc.route(connCtx, call, cm, &files, w)
	wire.CloseFiles(files) // whatever the handler did not take

	// From here on the handler can no longer write: a send from a goroutine
	// it left behind must not land in another call's replies.
	finished, broken, abandoned := w.close()
	switch {
	case broken || errors.Is(err, errStreamClosed):
		return false
	case abandoned:
		// The handler's context ended (client gone, or shutdown): close the
		// connection rather than invent a final reply, as sd-varlink does.
		return false
	case err == nil:
		return true
	}
	if call.oneway || finished {
		// No reply can carry it: oneway calls get none, and this call
		// already had its final reply.
		if !errors.Is(err, errSingleReply) {
			sc.logHandlerError(call, err)
		}
		return true
	}
	w.drop()
	return sc.writeError(call.method, sc.errorFor(call, err))
}

func isObjectOrNull(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) == 0 || bytes.Equal(t, []byte("null")) || t[0] == '{'
}

// route finds and runs the handler. Files it hands to the handler are
// removed from *files.
func (sc *serverConn) route(connCtx context.Context, call *Call, cm callMsg, files *[]*os.File, w *replyWriter) error {
	if cm.Upgrade {
		return &Error{Name: ErrMethodNotImplemented.Name, Parameters: mustParams(map[string]string{"method": cm.Method})}
	}
	ifaceName, methodName, err := SplitMethod(cm.Method)
	if err != nil {
		return &Error{Name: ErrMethodNotFound.Name, Parameters: mustParams(map[string]string{"method": cm.Method})}
	}
	if ifaceName == serviceInterface {
		return sc.builtin(methodName, cm, w)
	}
	sc.s.mu.RLock()
	iface := sc.s.ifaces[ifaceName]
	var m *method
	var def *idl.Method
	if iface != nil {
		m = iface.methods[methodName]
		def = iface.def.Method(methodName)
	}
	sc.s.mu.RUnlock()
	switch {
	case iface == nil:
		return &Error{Name: ErrInterfaceNotFound.Name, Parameters: mustParams(map[string]string{"interface": ifaceName})}
	case def == nil:
		return &Error{Name: ErrMethodNotFound.Name, Parameters: mustParams(map[string]string{"method": cm.Method})}
	case m == nil:
		return &Error{Name: ErrMethodNotImplemented.Name, Parameters: mustParams(map[string]string{"method": cm.Method})}
	}
	if def.More == idl.MoreRequired && !cm.More {
		return ErrExpectedMore
	}
	if m.stream && !cm.More && !cm.Oneway && !m.allowSingle {
		return ErrExpectedMore
	}
	if field, verr := validateParams(iface.def, def.In, cm.Parameters, m.allowUnknown); verr != nil {
		return invalidParameter(field)
	}

	w.more = cm.More && m.stream
	w.holdLast = m.holdLast
	ctx := connCtx
	if m.stream {
		// Streams end as soon as shutdown begins.
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(connCtx)
		stop := context.AfterFunc(sc.streams, cancel)
		defer stop()
		defer cancel()
	}
	handed := *files
	*files = nil
	herr := sc.runHandler(ctx, call, m, cm.Parameters, handed, w)
	if ctx.Err() != nil {
		w.abandon()
	}
	return herr
}

func (sc *serverConn) runHandler(ctx context.Context, call *Call, m *method, params []byte, files []*os.File, w *replyWriter) (err error) {
	defer func() {
		if r := recover(); r != nil {
			sc.s.cfg.logger.Error("varlink: handler panicked", "method", call.method, "panic", r, "stack", string(debug.Stack()))
			err = errHandlerPanic
		}
	}()
	return m.run(ctx, call, params, files, w)
}

var errHandlerPanic = errors.New("varlink: handler panicked")

func (sc *serverConn) builtin(method string, cm callMsg, w *replyWriter) error {
	switch method {
	case "GetInfo":
		sc.s.mu.RLock()
		names := []string{serviceInterface}
		for n := range sc.s.ifaces {
			names = append(names, n)
		}
		sc.s.mu.RUnlock()
		sort.Strings(names)
		i := sc.s.info
		return w.final(Info{Vendor: i.Vendor, Product: i.Product, Version: i.Version, URL: i.URL, Interfaces: names})
	case "GetInterfaceDescription":
		var in struct {
			Interface string `json:"interface"`
		}
		if json.Unmarshal(orEmpty(cm.Parameters), &in) != nil || in.Interface == "" {
			return invalidParameter("interface")
		}
		if in.Interface == serviceInterface {
			return w.final(map[string]string{"description": serviceDescription})
		}
		sc.s.mu.RLock()
		iface := sc.s.ifaces[in.Interface]
		sc.s.mu.RUnlock()
		if iface == nil {
			return &Error{Name: ErrInterfaceNotFound.Name, Parameters: mustParams(map[string]string{"interface": in.Interface})}
		}
		return w.final(map[string]string{"description": iface.description})
	}
	return &Error{Name: ErrMethodNotFound.Name, Parameters: mustParams(map[string]string{"method": cm.Method})}
}

func orEmpty(b []byte) []byte {
	if len(b) == 0 {
		return []byte("{}")
	}
	return b
}

// errorFor converts a handler error into the Varlink error to send.
//
// *Error and generated typed errors are sent as themselves. An error that
// wraps a syscall.Errno becomes io.systemd.System with that errno; any
// other error becomes io.systemd.System with EIO and is logged, so no
// detail leaks to the client. This matches sd-varlink, which reports
// failing method callbacks as io.systemd.System errors.
func (sc *serverConn) errorFor(call *Call, err error) *Error {
	var ve *Error
	if errors.As(err, &ve) {
		return sc.sanitize(call, ve)
	}
	var named NamedError
	if errors.As(err, &named) {
		params, merr := json.Marshal(named)
		if merr != nil || string(params) == "null" {
			params = nil
		}
		return sc.sanitize(call, &Error{Name: named.VarlinkError(), Parameters: params})
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errnoError(errno)
	}
	if !errors.Is(err, errHandlerPanic) {
		sc.logHandlerError(call, err)
	}
	return errnoError(syscall.EIO)
}

// sanitize makes sure an error reply is well formed: a name, and
// parameters that are a JSON object. A broken one would be sent as a
// success, or not at all.
func (sc *serverConn) sanitize(call *Call, e *Error) *Error {
	if e.Name == "" {
		sc.s.cfg.logger.Error("varlink: handler returned an error without a name", "method", call.method)
		return errnoError(syscall.EIO)
	}
	if len(e.Parameters) > 0 && (!json.Valid(e.Parameters) || bytes.TrimSpace(e.Parameters)[0] != '{') {
		sc.s.cfg.logger.Error("varlink: error parameters are not a JSON object; sending none", "method", call.method, "error", e.Name)
		return &Error{Name: e.Name}
	}
	return e
}

func (sc *serverConn) logHandlerError(call *Call, err error) {
	sc.s.cfg.logger.Error("varlink: handler failed", "method", call.method, "err", err)
}

func errnoError(errno syscall.Errno) *Error {
	return &Error{Name: "io.systemd.System", Parameters: mustParams(map[string]any{
		"origin":    "linux",
		"errno":     int(errno),
		"errnoName": errnoName(errno),
	})}
}

func invalidParameter(field string) *Error {
	if field == "" {
		field = "parameters"
	}
	return &Error{Name: ErrInvalidParameter.Name, Parameters: mustParams(map[string]string{"parameter": field})}
}

// fieldOf extracts the offending field from a JSON decoding error.
func fieldOf(err error) string {
	var te *json.UnmarshalTypeError
	if errors.As(err, &te) && te.Field != "" {
		return te.Field
	}
	return "parameters"
}

func mustParams(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

type serverReply struct {
	Parameters json.RawMessage `json:"parameters,omitempty"`
	Continues  bool            `json:"continues,omitempty"`
	Error      string          `json:"error,omitempty"`
}

func (sc *serverConn) write(r serverReply, files []*os.File) error {
	msg, err := json.Marshal(r)
	if err != nil {
		return err
	}
	sc.writeMu.Lock()
	defer sc.writeMu.Unlock()
	return sc.t.WriteMessage(msg, files)
}

// writeError sends an error reply. It returns false if the connection
// can no longer be used.
func (sc *serverConn) writeError(method string, e *Error) bool {
	params := e.Parameters
	if len(params) == 0 {
		params = json.RawMessage("{}")
	}
	if err := sc.write(serverReply{Error: e.Name, Parameters: params}, nil); err != nil {
		sc.s.cfg.logger.Debug("varlink: could not send error reply", "method", method, "err", err)
		return false
	}
	return true
}

// replyWriter sends a call's replies and enforces that a call gets
// exactly one final reply (none for oneway calls), and nothing after
// dispatch has moved on. Its methods may be called from any goroutine.
type replyWriter struct {
	sc       *serverConn
	call     *Call
	maxFiles int
	more     bool // streaming to a client that asked for "more"
	holdLast bool // LastIsFinal: hold each reply until the next shows it continues

	mu        sync.Mutex
	pending   *serverReply
	pfiles    []*os.File
	sent      int
	finished  bool // the final reply went out
	broken    bool // a write failed: the connection is unusable
	closed    bool // dispatch is done with this call
	abandoned bool // the handler's context ended before it returned
}

var (
	errStreamClosed = errors.New("varlink: client is gone")
	errSingleReply  = errors.New("varlink: only one reply allowed without 'more'")
	errCallOver     = errors.New("varlink: send after the handler returned")
)

func (w *replyWriter) encode(v any) (*serverReply, []*os.File, error) {
	params, files, err := Marshal(v)
	if err != nil {
		return nil, nil, fmt.Errorf("varlink: encode %s reply: %w", w.call.method, err)
	}
	if len(files) > w.maxFiles {
		// Refuse before writing, so the call gets an error reply instead
		// of a dropped connection.
		return nil, nil, fmt.Errorf("varlink: %s reply carries %d files, limit is %d: %w", w.call.method, len(files), w.maxFiles, ErrTooManyFiles)
	}
	if len(params) == 0 || string(params) == "null" {
		params = json.RawMessage("{}")
	}
	return &serverReply{Parameters: params}, files, nil
}

// emitLocked writes one reply and gives up the server's copies of its files.
func (w *replyWriter) emitLocked(r *serverReply, files []*os.File, continues bool) error {
	defer wire.CloseFiles(files)
	if !continues {
		w.finished = true
	}
	if w.call.oneway {
		return nil
	}
	r.Continues = continues
	if err := w.sc.write(*r, files); err != nil {
		w.broken = true
		return errStreamClosed
	}
	return nil
}

func (w *replyWriter) flushPendingLocked(continues bool) error {
	if w.pending == nil {
		return nil
	}
	r, files := w.pending, w.pfiles
	w.pending, w.pfiles = nil, nil
	return w.emitLocked(r, files, continues)
}

func (w *replyWriter) usableLocked() error {
	switch {
	case w.closed:
		return errCallOver
	case w.broken:
		return errStreamClosed
	}
	return nil
}

// final sends a single reply.
func (w *replyWriter) final(v any) error {
	r, files, err := w.encode(v)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.usableLocked(); err != nil {
		wire.CloseFiles(files)
		return err
	}
	return w.emitLocked(r, files, false)
}

// send sends one streamed value.
func (w *replyWriter) send(v any) error {
	r, files, err := w.encode(v)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.usableLocked(); err != nil {
		wire.CloseFiles(files)
		return err
	}
	if w.finished {
		wire.CloseFiles(files)
		return errSingleReply
	}
	w.sent++
	switch {
	case !w.more:
		// AllowSingle (or oneway): the first value is the only reply.
		return w.emitLocked(r, files, false)
	case w.holdLast:
		if err := w.flushPendingLocked(true); err != nil {
			wire.CloseFiles(files)
			return err
		}
		w.pending, w.pfiles = r, files
		return nil
	default:
		return w.emitLocked(r, files, true)
	}
}

// finish ends a stream with its final reply: the held-back value under
// LastIsFinal, else an empty reply.
func (w *replyWriter) finish() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.usableLocked(); err != nil {
		return err
	}
	if w.finished {
		return nil
	}
	if w.pending != nil {
		return w.flushPendingLocked(false)
	}
	return w.emitLocked(&serverReply{Parameters: json.RawMessage("{}")}, nil, false)
}

func (w *replyWriter) abandon() {
	w.mu.Lock()
	w.abandoned = true
	w.mu.Unlock()
}

// close ends the call for the handler: later sends fail. A value still
// held back by an abandoned call is discarded.
func (w *replyWriter) close() (finished, broken, abandoned bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	if w.abandoned {
		wire.CloseFiles(w.pfiles)
		w.pending, w.pfiles = nil, nil
	}
	return w.finished, w.broken, w.abandoned
}

// drop runs before an error reply, after close: under LastIsFinal a held
// value still reaches a "more" client, as a continued reply.
func (w *replyWriter) drop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.pending != nil && w.more && !w.broken {
		_ = w.flushPendingLocked(true)
		return
	}
	wire.CloseFiles(w.pfiles)
	w.pending, w.pfiles = nil, nil
}
