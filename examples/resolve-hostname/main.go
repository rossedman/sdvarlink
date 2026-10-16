// Command resolve-hostname resolves names through systemd-resolved's
// io.systemd.Resolve interface, like `resolvectl query`.
//
//	go run ./examples/resolve-hostname github.com systemd.io
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/netip"
	"os"
	"time"

	"github.com/rossedman/sdvarlink/systemd/v257/resolve"
	"github.com/rossedman/sdvarlink/varlink"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: resolve-hostname NAME...")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c, err := resolve.Dial(ctx)
	if errors.Is(err, varlink.ErrUnsupported) {
		log.Fatal("systemd-resolved is not running (or systemd is older than v257)")
	}
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	for _, name := range os.Args[1:] {
		out, err := c.ResolveHostname(ctx, resolve.ResolveHostnameIn{Name: name})
		var noRR *resolve.NoSuchResourceRecordError
		switch {
		case errors.As(err, &noRR):
			fmt.Printf("%s: no such host\n", name)
			continue
		case err != nil:
			log.Fatalf("%s: %v", name, err)
		}
		for _, a := range out.Addresses {
			fmt.Printf("%-24s %s\n", out.Name, toAddr(a.Address))
		}
	}
}

// toAddr converts resolved's byte arrays (4 or 16 ints) to an address.
func toAddr(b []int64) netip.Addr {
	raw := make([]byte, len(b))
	for i, v := range b {
		raw[i] = byte(v)
	}
	addr, _ := netip.AddrFromSlice(raw)
	return addr
}
