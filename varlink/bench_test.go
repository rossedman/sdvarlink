package varlink_test

import (
	"context"
	"iter"
	"testing"

	"github.com/rossedman/sdvarlink/varlink"
	"github.com/rossedman/sdvarlink/varlinktest"
)

func BenchmarkCall(b *testing.B) {
	srv := varlinktest.NewServer(b)
	varlinktest.On(srv, "com.example.Echo", func(_ context.Context, in echoIn) (echoOut, error) {
		return echoOut{Text: in.Text}, nil
	})
	c := srv.Dial()
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var out echoOut
		if err := c.Call(ctx, "com.example.Echo", echoIn{"hello"}, &out); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCallParallelPool(b *testing.B) {
	srv := varlinktest.NewServer(b)
	varlinktest.On(srv, "com.example.Echo", func(_ context.Context, in echoIn) (echoOut, error) {
		return echoOut{Text: in.Text}, nil
	})
	p := varlink.NewPoolPath(srv.Path())
	defer p.Close()
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := p.Call(context.Background(), "com.example.Echo", echoIn{"hello"}, nil); err != nil {
				b.Error(err)
				return
			}
		}
	})
}

func BenchmarkStream(b *testing.B) {
	srv := varlinktest.NewServer(b)
	const per = 100
	varlinktest.OnStream(srv, "com.example.Count", func(_ context.Context, _ echoIn) iter.Seq2[echoOut, error] {
		return func(yield func(echoOut, error) bool) {
			for i := 0; i < per; i++ {
				if !yield(echoOut{Text: "record", Count: i}, nil) {
					return
				}
			}
		}
	})
	c := srv.Dial()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, err := range varlink.StreamAs[echoOut](c.Stream(context.Background(), "com.example.Count", nil)) {
			if err != nil {
				b.Fatal(err)
			}
		}
	}
	b.ReportMetric(float64(b.N*per)/b.Elapsed().Seconds(), "replies/s")
}
