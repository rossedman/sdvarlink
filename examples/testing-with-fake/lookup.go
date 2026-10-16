// Package lookup is a small piece of application code built on sdvarlink,
// with tests that run against a varlinktest fake instead of systemd. Run
// them anywhere, no systemd needed:
//
//	go test ./examples/testing-with-fake/
package lookup

import (
	"context"
	"errors"
	"fmt"
	"net/netip"

	"github.com/rossedman/sdvarlink/systemd/v257/resolve"
	"github.com/rossedman/sdvarlink/varlink"
)

// ErrNotFound is returned when a name has no addresses.
var ErrNotFound = errors.New("lookup: not found")

// Resolver resolves names through systemd-resolved.
type Resolver struct {
	Client *resolve.Client
}

// IPv4 returns the IPv4 addresses of name.
func (r *Resolver) IPv4(ctx context.Context, name string) ([]netip.Addr, error) {
	const afInet = 2
	out, err := r.Client.ResolveHostname(ctx, resolve.ResolveHostnameIn{Name: name, Family: varlink.Ptr(int64(afInet))})
	if errors.Is(err, resolve.ErrNoSuchResourceRecord) {
		return nil, fmt.Errorf("%s: %w", name, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	var addrs []netip.Addr
	for _, a := range out.Addresses {
		if len(a.Address) != 4 {
			continue
		}
		addrs = append(addrs, netip.AddrFrom4([4]byte{byte(a.Address[0]), byte(a.Address[1]), byte(a.Address[2]), byte(a.Address[3])}))
	}
	return addrs, nil
}
