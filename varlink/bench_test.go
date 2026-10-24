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

type benchAddress struct {
	Ifindex *int  `json:"ifindex,omitempty"`
	Family  int   `json:"family"`
	Address []int `json:"address"`
}

type benchResolveReply struct {
	Addresses []benchAddress `json:"addresses"`
	Name      string         `json:"name,omitempty"`
	Flags     int            `json:"flags"`
}

func benchReply() benchResolveReply {
	idx := 2
	return benchResolveReply{
		Addresses: []benchAddress{
			{Ifindex: &idx, Family: 2, Address: []int{192, 0, 2, 1}},
			{Ifindex: &idx, Family: 10, Address: []int{32, 1, 13, 184, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}},
		},
		Name:  "example.org",
		Flags: 786945,
	}
}

func BenchmarkMarshal(b *testing.B) {
	v := benchReply()
	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := varlink.Marshal(v); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkUnmarshal(b *testing.B) {
	data, _, err := varlink.Marshal(benchReply())
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		var out benchResolveReply
		if err := varlink.Unmarshal(data, nil, &out); err != nil {
			b.Fatal(err)
		}
	}
}
