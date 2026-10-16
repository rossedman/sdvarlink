package varlinktest_test

import (
	"context"
	"fmt"
	"iter"
	"testing"

	"github.com/rossedman/sdvarlink/systemd/v257/resolve"
	"github.com/rossedman/sdvarlink/varlink"
	"github.com/rossedman/sdvarlink/varlinktest"
)

// In a test, pass the *testing.T; the server stops when the test ends.
func ExampleNewServer() {
	t := &testing.T{} // in real tests, use the test's t
	srv := varlinktest.NewServer(t)
	srv.Handle("io.systemd.Resolve.ResolveHostname", func(in resolve.ResolveHostnameIn) (resolve.ResolveHostnameOut, error) {
		return resolve.ResolveHostnameOut{Name: in.Name, Addresses: []resolve.ResolvedAddress{{Family: 2, Address: []int64{10, 0, 0, 1}}}}, nil
	})
	defer srv.Close()

	c, _ := resolve.DialPath(context.Background(), srv.Path())
	defer c.Close()
	out, _ := c.ResolveHostname(context.Background(), resolve.ResolveHostnameIn{Name: "db.internal"})
	fmt.Println(out.Name, out.Addresses[0].Address)
	// Output: db.internal [10 0 0 1]
}

type tick struct {
	N int `json:"n"`
}

func ExampleOnStream() {
	t := &testing.T{}
	srv := varlinktest.NewServer(t)
	defer srv.Close()
	varlinktest.OnStream(srv, "com.example.Ticks", func(ctx context.Context, _ struct{}) iter.Seq2[tick, error] {
		return func(yield func(tick, error) bool) {
			for i := 1; i <= 3; i++ {
				if !yield(tick{N: i}, nil) {
					return
				}
			}
		}
	})

	conn := srv.Dial()
	for v, err := range varlink.StreamAs[tick](conn.Stream(context.Background(), "com.example.Ticks", nil)) {
		if err != nil {
			panic(err)
		}
		fmt.Print(v.N, " ")
	}
	fmt.Println()
	// Output: 1 2 3
}
