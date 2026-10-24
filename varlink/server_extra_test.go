package varlink_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rossedman/sdvarlink/varlink"
)

const extraIDL = `interface com.example.X

method Small(n: int) -> ()
# [Requires 'more' flag]
method Stream(n: int) -> (n: int)
# [Supports 'more' flag]
method List(n: int) -> (n: int)
`

type smallIn struct {
	N int8 `json:"n"`
}

type extraN struct {
	N int64 `json:"n"`
}

func serveX(t *testing.T) *varlink.Conn {
	t.Helper()
	srv := varlink.NewServer(varlink.ServerInfo{}, varlink.WithLogger(discardLogger()))
	iface := srv.Interface("com.example.X", extraIDL)
	varlink.Handle(iface, "Small", func(context.Context, *varlink.Call, smallIn) (struct{}, error) {
		return struct{}{}, nil
	})
	varlink.HandleStream(iface, "Stream", func(_ context.Context, _ *varlink.Call, in smallIn, _ func(extraN) error) error {
		return nil
	})
	// List sends in.N values and then fails, so a held-back value must
	// still reach the client before the error.
	varlink.HandleStream(iface, "List", func(_ context.Context, _ *varlink.Call, in extraN, send func(extraN) error) error {
		for i := int64(1); i <= in.N; i++ {
			if err := send(extraN{N: i}); err != nil {
				return err
			}
		}
		return &varlink.Error{Name: "com.example.X.Broken"}
	}, varlink.LastIsFinal())

	l, err := varlink.Listen(sockPath(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { srv.Serve(ctx, l); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	c, err := varlink.Dial(t.Context(), l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestServerUndecodableParameters(t *testing.T) {
	t.Parallel()
	c := serveX(t)
	tests := []struct {
		name   string
		method string
		in     any
		more   bool
	}{
		{name: "handle overflow", method: "com.example.X.Small", in: map[string]any{"n": 1000}},
		{name: "stream overflow", method: "com.example.X.Stream", in: map[string]any{"n": 1000}, more: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			if tc.more {
				for _, serr := range c.Stream(t.Context(), tc.method, tc.in) {
					err = serr
				}
			} else {
				err = c.Call(t.Context(), tc.method, tc.in, nil)
			}
			var ve *varlink.Error
			if !errors.Is(err, varlink.ErrInvalidParameter) || !errors.As(err, &ve) || !strings.Contains(string(ve.Parameters), `"parameter":"n"`) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestServerListErrorFlushesHeldValue(t *testing.T) {
	t.Parallel()
	c := serveX(t)
	tests := []struct {
		name      string
		n         int64
		wantItems []int64
	}{
		{name: "no values", n: 0},
		{name: "one value", n: 1, wantItems: []int64{1}},
		{name: "two values", n: 2, wantItems: []int64{1, 2}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got []int64
			var gotErr error
			for r, err := range c.Stream(t.Context(), "com.example.X.List", extraN{N: tc.n}) {
				if err != nil {
					gotErr = err
					break
				}
				var v extraN
				if err := r.Decode(&v); err != nil {
					t.Fatal(err)
				}
				if !r.Continues {
					t.Errorf("value %d was final before the error", v.N)
				}
				got = append(got, v.N)
			}
			if varlink.ErrorName(gotErr) != "com.example.X.Broken" {
				t.Errorf("err = %v", gotErr)
			}
			if len(got) != len(tc.wantItems) {
				t.Fatalf("got %v, want %v", got, tc.wantItems)
			}
			for i := range got {
				if got[i] != tc.wantItems[i] {
					t.Errorf("got %v, want %v", got, tc.wantItems)
				}
			}
		})
	}
}

func TestCallReplyClose(t *testing.T) {
	t.Parallel()
	c := serveX(t)
	r, err := c.CallReply(t.Context(), "com.example.X.Small", map[string]any{"n": 1})
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	r.Close() // idempotent
	if len(r.Files) != 0 {
		t.Errorf("Files = %v", r.Files)
	}
	// The connection is still usable.
	if err := c.Call(t.Context(), "com.example.X.Small", map[string]any{"n": 2}, nil); err != nil {
		t.Fatal(err)
	}
}
