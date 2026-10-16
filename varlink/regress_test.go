package varlink_test

// Regression tests for bugs found in review. Each names the failure it
// guards against.

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"math"
	"os"
	"testing"
	"time"

	"github.com/rossedman/sdvarlink/varlink"
	"github.com/rossedman/sdvarlink/varlinktest"
)

// A context that is already cancelled must not send the call, and must not
// cost the connection.
func TestCancelledContextSendsNothing(t *testing.T) {
	srv := newEcho(t)
	c := srv.Dial()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for i := 0; i < 200; i++ {
		if err := c.Call(ctx, "com.example.Echo", echoIn{"x"}, nil); !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	}
	if len(srv.Calls()) != 0 {
		t.Fatalf("%d calls reached the server", len(srv.Calls()))
	}
	if c.Err() != nil {
		t.Fatalf("conn closed: %v", c.Err())
	}
}

// Errors found while encoding happen before anything is written, so the
// connection stays usable.
func TestEncodeErrorsKeepConnection(t *testing.T) {
	srv := newEcho(t)
	c := srv.Dial(varlink.WithMaxFiles(1))
	ctx := context.Background()

	if err := c.Call(ctx, "com.example.Echo", map[string]float64{"x": math.NaN()}, nil); err == nil {
		t.Fatal("NaN encoded")
	}
	f1, _ := os.Open(os.DevNull)
	f2, _ := os.Open(os.DevNull)
	defer f1.Close()
	defer f2.Close()
	err := c.Call(ctx, "com.example.Echo", nil, nil, varlink.WithFiles(f1, f2))
	if !errors.Is(err, varlink.ErrTooManyFiles) {
		t.Fatalf("err = %v", err)
	}
	if err := c.Oneway(ctx, "com.example.Echo", map[string]float64{"x": math.Inf(1)}); err == nil {
		t.Fatal("Inf encoded")
	}
	for range c.Stream(ctx, "com.example.Echo", map[string]float64{"x": math.NaN()}) {
	}
	if c.Err() != nil {
		t.Fatalf("conn closed by an encoding error: %v", c.Err())
	}
	if err := c.Call(ctx, "com.example.Echo", echoIn{"still works"}, nil); err != nil {
		t.Fatal(err)
	}
}

// A handler that yields a reply and then an error, for a call without
// "more", must send only the error.
func TestFakeReplyThenErrorWithoutMore(t *testing.T) {
	srv := varlinktest.NewServer(t)
	srv.HandleRaw("com.example.Half", func(context.Context, *varlinktest.Call) iter.Seq2[*varlinktest.Response, error] {
		return func(yield func(*varlinktest.Response, error) bool) {
			if yield(&varlinktest.Response{Parameters: echoOut{Text: "partial"}}, nil) {
				yield(nil, &varlink.Error{Name: "com.example.Broken"})
			}
		}
	})
	c := srv.Dial()
	err := c.Call(context.Background(), "com.example.Half", nil, nil)
	if varlink.ErrorName(err) != "com.example.Broken" {
		t.Fatalf("err = %v", err)
	}
	if c.Err() != nil {
		t.Fatalf("conn closed: %v", c.Err())
	}
}

type recursive struct {
	Next *recursive `json:"next,omitempty"`
	F    *os.File   `json:"-" varlink:"f,fd"`
}

// The type cache must not record "no fd fields" for a recursive type whose
// recursive field comes before its fd field.
func TestRecursiveTypeFiles(t *testing.T) {
	f, _ := os.Open(os.DevNull)
	defer f.Close()
	_, files, err := varlink.Marshal(&recursive{Next: &recursive{F: f}})
	if err != nil || len(files) != 1 {
		t.Fatalf("files = %v, err = %v", files, err)
	}
}

func TestReplyFailsCleanlyOnBadIndex(t *testing.T) {
	// The first field claims file 0, the second has a bad index: the
	// decode fails, and the claimed file must not be left open in v.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var out struct {
		A *os.File `json:"-" varlink:"a,fd"`
		B *os.File `json:"-" varlink:"b,fd"`
	}
	err = varlink.Unmarshal([]byte(`{"a":0,"b":7}`), []*os.File{w}, &out)
	if err == nil {
		t.Fatal("bad index accepted")
	}
	if out.A != nil || out.B != nil {
		t.Fatal("fields left set after a failed decode")
	}
	// All write ends are closed, so the read end sees EOF.
	r.SetReadDeadline(time.Now().Add(5 * time.Second))
	if n, err := r.Read(make([]byte, 1)); n != 0 || err == nil {
		t.Fatalf("write end still open: n=%d err=%v", n, err)
	}
}

func TestErrorKeepsJSONParameters(t *testing.T) {
	e := &varlink.Error{Name: "x.Y", Parameters: json.RawMessage(`{"a":1}`)}
	if e.Error() != `varlink: x.Y {"a":1}` {
		t.Fatal(e.Error())
	}
}
