package idl

import (
	"os"
	"testing"
)

// BenchmarkParse parses a real systemd interface snapshot.
func BenchmarkParse(b *testing.B) {
	src, err := os.ReadFile("v260/io.systemd.Resolve.varlink")
	if err != nil {
		b.Fatal(err)
	}
	s := string(src)
	b.ReportAllocs()
	b.SetBytes(int64(len(s)))
	for b.Loop() {
		if _, err := Parse(s); err != nil {
			b.Fatal(err)
		}
	}
}
