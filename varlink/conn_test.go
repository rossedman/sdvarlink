package varlink_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"iter"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/rossedman/sdvarlink/varlink"
	"github.com/rossedman/sdvarlink/varlinktest"
)

type echoIn struct {
	Text string `json:"text"`
}

type echoOut struct {
	Text  string `json:"text"`
	Count int    `json:"count,omitempty"`
}

func newEcho(t *testing.T) *varlinktest.Server {
	srv := varlinktest.NewServer(t)
	srv.Handle("com.example.Echo", func(in echoIn) (echoOut, error) {
		return echoOut{Text: in.Text}, nil
	})
	return srv
}

func TestCall(t *testing.T) {
	srv := newEcho(t)
	c := srv.Dial()
	var out echoOut
	if err := c.Call(context.Background(), "com.example.Echo", echoIn{"hi"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.Text != "hi" {
		t.Fatalf("out = %+v", out)
	}
	// nil input and output are allowed.
	if err := c.Call(context.Background(), "com.example.Echo", nil, nil); err != nil {
		t.Fatal(err)
	}
	calls := srv.Calls()
	if len(calls) != 2 || string(calls[0].Parameters) != `{"text":"hi"}` {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestRawCall(t *testing.T) {
	srv := newEcho(t)
	c := srv.Dial()
	var out json.RawMessage
	if err := c.Call(context.Background(), "com.example.Echo", json.RawMessage(`{"text":"raw"}`), &out); err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"text":"raw"}` {
		t.Fatalf("out = %s", out)
	}
}

func TestErrors(t *testing.T) {
	srv := newEcho(t)
	srv.Handle("com.example.Fail", func(echoIn) (echoOut, error) {
		return echoOut{}, &varlink.Error{Name: "com.example.NoSuchThing", Parameters: json.RawMessage(`{"name":"x"}`)}
	})
	srv.Handle("com.example.Errno", func(echoIn) (echoOut, error) {
		return echoOut{}, syscall.ENOENT
	})
	c := srv.Dial()
	ctx := context.Background()

	err := c.Call(ctx, "com.example.Missing", nil, nil)
	if !errors.Is(err, varlink.ErrMethodNotFound) {
		t.Fatalf("missing method: %v", err)
	}
	err = c.Call(ctx, "org.example.Nope.Missing", nil, nil)
	if !errors.Is(err, varlink.ErrInterfaceNotFound) {
		t.Fatalf("missing interface: %v", err)
	}

	err = c.Call(ctx, "com.example.Fail", nil, nil)
	var ve *varlink.Error
	if !errors.As(err, &ve) || ve.Name != "com.example.NoSuchThing" {
		t.Fatalf("Fail: %v", err)
	}
	var params struct{ Name string }
	if err := ve.Decode(&params); err != nil || params.Name != "x" {
		t.Fatalf("params = %+v, %v", params, err)
	}
	if varlink.ErrorName(err) != "com.example.NoSuchThing" {
		t.Fatalf("ErrorName = %q", varlink.ErrorName(err))
	}

	err = c.Call(ctx, "com.example.Errno", nil, nil)
	var se *varlink.SystemError
	if !errors.As(err, &se) || !errors.Is(err, syscall.ENOENT) {
		t.Fatalf("Errno: %v", err)
	}

	// Error replies leave the connection usable.
	if c.Err() != nil {
		t.Fatalf("conn broken after error replies: %v", c.Err())
	}
	if err := c.Call(ctx, "com.example.Echo", echoIn{"ok"}, nil); err != nil {
		t.Fatal(err)
	}
}

func counter(n int, failAt int) func(context.Context, echoIn) iter.Seq2[echoOut, error] {
	return func(_ context.Context, in echoIn) iter.Seq2[echoOut, error] {
		return func(yield func(echoOut, error) bool) {
			for i := 1; i <= n; i++ {
				if i == failAt {
					yield(echoOut{}, &varlink.Error{Name: "com.example.Broken"})
					return
				}
				if !yield(echoOut{Text: in.Text, Count: i}, nil) {
					return
				}
			}
		}
	}
}

func TestStream(t *testing.T) {
	srv := varlinktest.NewServer(t)
	varlinktest.OnStream(srv, "com.example.Count", counter(3, 0))
	varlinktest.OnStream(srv, "com.example.CountFail", counter(3, 3))
	c := srv.Dial()
	ctx := context.Background()

	var got []int
	for out, err := range varlink.StreamAs[echoOut](c.Stream(ctx, "com.example.Count", echoIn{"x"})) {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, out.Count)
	}
	if len(got) != 3 || got[2] != 3 {
		t.Fatalf("got %v", got)
	}
	if !srv.Calls()[0].More {
		t.Fatal("stream call did not set more")
	}

	var n int
	var last error
	for _, err := range c.Stream(ctx, "com.example.CountFail", nil) {
		if err != nil {
			last = err
			break
		}
		n++
	}
	if n != 2 || varlink.ErrorName(last) != "com.example.Broken" {
		t.Fatalf("n=%d err=%v", n, last)
	}
	if c.Err() != nil {
		t.Fatalf("conn broken after stream error: %v", c.Err())
	}

	// Breaking out early abandons the stream and closes the connection.
	for range c.Stream(ctx, "com.example.Count", nil) {
		break
	}
	if c.Err() == nil {
		t.Fatal("conn still open after abandoned stream")
	}
	if err := c.Call(ctx, "com.example.Count", nil, nil); err == nil {
		t.Fatal("call on closed conn succeeded")
	}
}

func TestStreamSingleReply(t *testing.T) {
	// A method that only ever returns one reply still works with Stream.
	srv := newEcho(t)
	c := srv.Dial()
	n := 0
	for r, err := range c.Stream(context.Background(), "com.example.Echo", echoIn{"one"}) {
		if err != nil {
			t.Fatal(err)
		}
		if r.Continues {
			t.Fatal("single reply continues")
		}
		n++
	}
	if n != 1 || c.Err() != nil {
		t.Fatalf("n=%d err=%v", n, c.Err())
	}
}

func TestContextCancel(t *testing.T) {
	srv := varlinktest.NewServer(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	srv.Handle("com.example.Block", func(ctx context.Context, _ echoIn) (echoOut, error) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return echoOut{}, nil
	})
	c := srv.Dial()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := c.Call(ctx, "com.example.Block", nil, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("cancellation took too long")
	}
	if c.Err() == nil {
		t.Fatal("conn should be closed after an abandoned call")
	}

	c2 := srv.Dial()
	ctx2, cancel2 := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel2()
	}()
	if err := c2.Call(ctx2, "com.example.Block", nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestCallAfterCancelledCallStillWorks(t *testing.T) {
	// A context cancelled after its call completed must not poison the
	// next call on the same connection.
	srv := newEcho(t)
	c := srv.Dial()
	for i := 0; i < 100; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		if err := c.Call(ctx, "com.example.Echo", echoIn{"a"}, nil); err != nil {
			t.Fatal(err)
		}
		cancel()
	}
	if err := c.Call(context.Background(), "com.example.Echo", echoIn{"b"}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestOneway(t *testing.T) {
	srv := varlinktest.NewServer(t)
	got := make(chan string, 1)
	srv.Handle("com.example.Notify", func(in echoIn) (echoOut, error) {
		got <- in.Text
		return echoOut{}, nil
	})
	c := srv.Dial()
	if err := c.Oneway(context.Background(), "com.example.Notify", echoIn{"ping"}); err != nil {
		t.Fatal(err)
	}
	select {
	case s := <-got:
		if s != "ping" {
			t.Fatalf("got %q", s)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("oneway call not delivered")
	}
	// No reply was sent, so the next call gets its own reply.
	if err := c.Call(context.Background(), "com.example.Notify", echoIn{"x"}, nil); err != nil {
		t.Fatal(err)
	}
	if !srv.Calls()[0].Oneway {
		t.Fatal("oneway flag not sent")
	}
}

func TestConcurrentCallsOnOneConn(t *testing.T) {
	srv := newEcho(t)
	c := srv.Dial()
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			text := string(rune('a' + i))
			var out echoOut
			if err := c.Call(context.Background(), "com.example.Echo", echoIn{text}, &out); err != nil {
				errs <- err
				return
			}
			if out.Text != text {
				errs <- errors.New("reply for another call: " + out.Text)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestPool(t *testing.T) {
	srv := varlinktest.NewServer(t)
	srv.Handle("com.example.Echo", func(in echoIn) (echoOut, error) { return echoOut{Text: in.Text}, nil })
	varlinktest.OnStream(srv, "com.example.Count", counter(5, 0))
	p := varlink.NewPoolPath(srv.Path())
	defer p.Close()

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := p.Call(context.Background(), "com.example.Echo", echoIn{"p"}, nil); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	// An abandoned stream's connection is dropped, not reused.
	for range p.Stream(context.Background(), "com.example.Count", nil) {
		break
	}
	n := 0
	for _, err := range p.Stream(context.Background(), "com.example.Count", nil) {
		if err != nil {
			t.Fatal(err)
		}
		n++
	}
	if n != 5 {
		t.Fatalf("n = %d", n)
	}
	p.Close()
	if err := p.Call(context.Background(), "com.example.Echo", nil, nil); !errors.Is(err, varlink.ErrClosed) {
		t.Fatalf("call on closed pool: %v", err)
	}
}

func TestIntrospection(t *testing.T) {
	srv := newEcho(t)
	srv.AddInterface("interface com.example\n\nmethod Echo(text: string) -> (text: string)\n")
	c := srv.Dial()
	ctx := context.Background()

	info, err := varlink.GetInfo(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Interfaces) != 2 || info.Interfaces[0] != "com.example" {
		t.Fatalf("info = %+v", info)
	}
	ok, err := varlink.Supports(ctx, c, "com.example.Echo")
	if err != nil || !ok {
		t.Fatalf("Supports(Echo) = %v, %v", ok, err)
	}
	ok, err = varlink.Supports(ctx, c, "com.example.Shout")
	if err != nil || ok {
		t.Fatalf("Supports(Shout) = %v, %v", ok, err)
	}
	ok, err = varlink.Supports(ctx, c, "org.example.Other.Method")
	if err != nil || ok {
		t.Fatalf("Supports(other interface) = %v, %v", ok, err)
	}
}

func TestDialMissingSocket(t *testing.T) {
	_, err := varlink.DialService(context.Background(), "/nonexistent/io.systemd.Nothing")
	if !errors.Is(err, varlink.ErrUnsupported) {
		t.Fatalf("err = %v", err)
	}
}

func TestFrameTooLarge(t *testing.T) {
	srv := varlinktest.NewServer(t)
	big := make([]byte, 64<<10)
	for i := range big {
		big[i] = 'a'
	}
	srv.Handle("com.example.Big", func(echoIn) (echoOut, error) { return echoOut{Text: string(big)}, nil })
	c := srv.Dial(varlink.WithMaxFrameSize(1024))
	err := c.Call(context.Background(), "com.example.Big", nil, nil)
	if !errors.Is(err, varlink.ErrFrameTooLarge) {
		t.Fatalf("err = %v", err)
	}
}

func TestPeerHangup(t *testing.T) {
	srv := varlinktest.NewServer(t)
	srv.Handle("com.example.Echo", func(echoIn) (echoOut, error) { return echoOut{}, nil })
	c := srv.Dial()
	srv.Close()
	err := c.Call(context.Background(), "com.example.Echo", nil, nil)
	if err == nil || (!errors.Is(err, io.EOF) && !errors.Is(err, syscall.EPIPE) && !errors.Is(err, syscall.ECONNRESET)) {
		t.Fatalf("err = %v", err)
	}
}

func readAll(t *testing.T, f *os.File) string {
	t.Helper()
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
