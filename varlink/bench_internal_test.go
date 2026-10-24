package varlink

import (
	"os"
	"testing"

	"github.com/rossedman/sdvarlink/idl"
)

// BenchmarkValidateParams validates a ResolveHostname call against the real
// io.systemd.Resolve interface.
func BenchmarkValidateParams(b *testing.B) {
	src, err := os.ReadFile("../idl/v260/io.systemd.Resolve.varlink")
	if err != nil {
		b.Fatal(err)
	}
	iface, err := idl.Parse(string(src))
	if err != nil {
		b.Fatal(err)
	}
	in := iface.Method("ResolveHostname").In
	params := []byte(`{"ifindex":2,"name":"example.org","family":2,"flags":0}`)
	b.ReportAllocs()
	for b.Loop() {
		if field, err := validateParams(iface, in, params, false); err != nil {
			b.Fatal(field, err)
		}
	}
}
