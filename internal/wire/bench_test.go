//go:build unix

package wire

import (
	"bytes"
	"testing"
)

// BenchmarkReadMessage measures reading one realistic reply frame from a
// socket; the frame is written through the same transport pair each iteration.
func BenchmarkReadMessage(b *testing.B) {
	a, c := pair(b)
	w := New(a, 1<<20, 4)
	r := New(c, 1<<20, 4)
	msg := []byte(`{"parameters":{"addresses":[{"ifindex":2,"family":2,"address":[192,0,2,1]},` +
		`{"ifindex":2,"family":10,"address":[32,1,13,184,0,0,0,0,0,0,0,0,0,0,0,1]}],` +
		`"name":"example.org","flags":786945},"continues":false}`)
	b.ReportAllocs()
	b.SetBytes(int64(len(msg) + 1))
	for b.Loop() {
		if err := w.WriteMessage(msg, nil); err != nil {
			b.Fatal(err)
		}
		got, _, err := r.ReadMessage()
		if err != nil {
			b.Fatal(err)
		}
		if !bytes.Equal(got, msg) {
			b.Fatal("message mismatch")
		}
	}
}
