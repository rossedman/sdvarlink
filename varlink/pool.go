package varlink

import (
	"context"
	"iter"
	"sync"
)

// DefaultMaxIdle is the number of idle connections a Pool keeps by default.
const DefaultMaxIdle = 4

// DialFunc opens a new connection for a Pool.
type DialFunc func(ctx context.Context) (*Conn, error)

// Pool hands out connections to one service so that concurrent callers do
// not queue behind each other on a single Conn. It implements Caller.
// Connections that end up in a bad state are dropped rather than reused.
type Pool struct {
	dial    DialFunc
	maxIdle int

	mu     sync.Mutex
	idle   []*Conn
	closed bool
}

// PoolOption configures a Pool.
type PoolOption func(*Pool)

// WithMaxIdle sets how many idle connections the pool keeps open.
func WithMaxIdle(n int) PoolOption { return func(p *Pool) { p.maxIdle = n } }

// NewPool returns a pool that opens connections with dial.
func NewPool(dial DialFunc, opts ...PoolOption) *Pool {
	p := &Pool{dial: dial, maxIdle: DefaultMaxIdle}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// NewPoolPath returns a pool of connections to the socket at path.
func NewPoolPath(path string, opts ...Option) *Pool {
	return NewPool(func(ctx context.Context) (*Conn, error) { return Dial(ctx, path, opts...) })
}

// Get returns an idle connection or dials a new one. Return it with Put.
func (p *Pool) Get(ctx context.Context) (*Conn, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, ErrClosed
	}
	for len(p.idle) > 0 {
		c := p.idle[len(p.idle)-1]
		p.idle = p.idle[:len(p.idle)-1]
		if c.Err() == nil {
			p.mu.Unlock()
			return c, nil
		}
	}
	p.mu.Unlock()
	return p.dial(ctx)
}

// Put returns a connection to the pool. Closed or broken connections, and
// connections beyond the idle limit, are closed instead.
func (p *Pool) Put(c *Conn) {
	if c == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || c.Err() != nil || len(p.idle) >= p.maxIdle {
		c.Close()
		return
	}
	p.idle = append(p.idle, c)
}

// Close closes all idle connections. Connections in use are closed when
// they are returned.
func (p *Pool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	for _, c := range p.idle {
		c.Close()
	}
	p.idle = nil
	return nil
}

// Call runs Conn.Call on a pooled connection.
func (p *Pool) Call(ctx context.Context, method string, in, out any, opts ...CallOption) error {
	c, err := p.Get(ctx)
	if err != nil {
		return err
	}
	defer p.Put(c)
	return c.Call(ctx, method, in, out, opts...)
}

// Oneway runs Conn.Oneway on a pooled connection.
func (p *Pool) Oneway(ctx context.Context, method string, in any, opts ...CallOption) error {
	c, err := p.Get(ctx)
	if err != nil {
		return err
	}
	defer p.Put(c)
	return c.Oneway(ctx, method, in, opts...)
}

// Stream runs Conn.Stream on a pooled connection. The connection returns to
// the pool when the sequence ends.
func (p *Pool) Stream(ctx context.Context, method string, in any, opts ...CallOption) iter.Seq2[*Reply, error] {
	return func(yield func(*Reply, error) bool) {
		c, err := p.Get(ctx)
		if err != nil {
			yield(nil, err)
			return
		}
		defer p.Put(c)
		for r, err := range c.Stream(ctx, method, in, opts...) {
			if !yield(r, err) {
				return
			}
		}
	}
}
