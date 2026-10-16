// Command feature-detect reports which Varlink features this host's systemd
// offers. It shows the two checks a program needs when it supports a range
// of systemd releases:
//
//   - Dial fails with varlink.ErrUnsupported when a service's socket does
//     not exist (the service is too old, or not running).
//
//   - Client.Supports reads live introspection to check for a method that
//     newer releases added to an existing interface.
//
//     go run ./examples/feature-detect
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"text/tabwriter"

	"github.com/rossedman/sdvarlink/systemd/latest/hostname"
	"github.com/rossedman/sdvarlink/systemd/latest/machine"
	"github.com/rossedman/sdvarlink/systemd/latest/manager"
	"github.com/rossedman/sdvarlink/systemd/latest/resolve"
	"github.com/rossedman/sdvarlink/systemd/latest/unit"
	"github.com/rossedman/sdvarlink/varlink"
)

type supporter interface {
	Supports(ctx context.Context, method string) (bool, error)
	Close() error
}

type probe struct {
	feature string
	dial    func(context.Context) (supporter, error)
	method  string
}

func wrap[C supporter](f func(context.Context, ...varlink.Option) (C, error)) func(context.Context) (supporter, error) {
	return func(ctx context.Context) (supporter, error) { return f(ctx) }
}

func main() {
	ctx := context.Background()
	probes := []probe{
		{"Describe the service manager", wrap(manager.Dial), "Describe"},
		{"List units over Varlink (v258+)", wrap(unit.Dial), "List"},
		{"Change unit properties (v260+)", wrap(unit.Dial), "SetProperties"},
		{"Resolve host names", wrap(resolve.Dial), "ResolveHostname"},
		{"Browse DNS-SD services", wrap(resolve.Dial), "BrowseServices"},
		{"Dump DNS configuration", wrap(resolve.Dial), "DumpDNSConfiguration"},
		{"Describe the host", wrap(hostname.Dial), "Describe"},
		{"List machines", wrap(machine.Dial), "List"},
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "FEATURE\tMETHOD\tSTATUS")
	for _, p := range probes {
		status := check(ctx, p)
		fmt.Fprintf(w, "%s\t%s\t%s\n", p.feature, p.method, status)
	}
	w.Flush()
}

func check(ctx context.Context, p probe) string {
	c, err := p.dial(ctx)
	if errors.Is(err, varlink.ErrUnsupported) {
		return "✗ service not available (fall back to D-Bus)"
	}
	if err != nil {
		log.Printf("%s: %v", p.feature, err)
		return "✗ " + err.Error()
	}
	defer c.Close()
	ok, err := c.Supports(ctx, p.method)
	switch {
	case err != nil:
		return "? " + err.Error()
	case ok:
		return "✓ supported"
	default:
		return "✗ method missing on this systemd"
	}
}
