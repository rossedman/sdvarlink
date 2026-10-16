package lookup

import (
	"context"
	"errors"
	"testing"

	"github.com/rossedman/sdvarlink/systemd/v257/resolve"
	"github.com/rossedman/sdvarlink/varlinktest"
)

// newResolver starts a fake io.systemd.Resolve with canned answers.
func newResolver(t *testing.T) (*Resolver, *varlinktest.Server) {
	t.Helper()
	srv := varlinktest.NewServer(t)
	srv.AddInterface(resolve.InterfaceDescription) // lets Supports() work

	hosts := map[string][]int64{
		"example.com": {93, 184, 215, 14},
		"localhost":   {127, 0, 0, 1},
	}
	srv.Handle("io.systemd.Resolve.ResolveHostname",
		func(ctx context.Context, in resolve.ResolveHostnameIn) (resolve.ResolveHostnameOut, error) {
			addr, ok := hosts[in.Name]
			if !ok {
				return resolve.ResolveHostnameOut{}, &resolve.NoSuchResourceRecordError{}
			}
			return resolve.ResolveHostnameOut{
				Name:      in.Name,
				Addresses: []resolve.ResolvedAddress{{Family: 2, Address: addr}},
			}, nil
		})

	c, err := resolve.DialPath(context.Background(), srv.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return &Resolver{Client: c}, srv
}

func TestIPv4(t *testing.T) {
	r, srv := newResolver(t)
	addrs, err := r.IPv4(context.Background(), "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(addrs) != 1 || addrs[0].String() != "93.184.215.14" {
		t.Fatalf("addrs = %v", addrs)
	}
	// The fake records every call, so tests can assert on what was sent.
	call := srv.Calls()[0]
	if string(call.Parameters) != `{"name":"example.com","family":2}` {
		t.Fatalf("sent %s", call.Parameters)
	}
}

func TestNotFound(t *testing.T) {
	r, _ := newResolver(t)
	_, err := r.IPv4(context.Background(), "nope.invalid")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestFeatureDetection(t *testing.T) {
	// The fake serves the v257 interface description, so it behaves like a
	// v257 host: BrowseServices arrived in a later release.
	r, _ := newResolver(t)
	for method, want := range map[string]bool{"ResolveHostname": true, "BrowseServices": false} {
		got, err := r.Client.Supports(context.Background(), method)
		if err != nil || got != want {
			t.Errorf("Supports(%s) = %v, %v; want %v", method, got, err, want)
		}
	}
}
