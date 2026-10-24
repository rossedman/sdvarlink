// Command hostname prints what systemd-hostnamed knows about this machine,
// like `hostnamectl`.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/rossedman/sdvarlink/systemd/v257/hostname"
	"github.com/rossedman/sdvarlink/varlink"
)

// main runs the example; see the package comment for usage.
func main() {
	ctx := context.Background()
	c, err := hostname.Dial(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	h, err := c.Describe(ctx, hostname.DescribeIn{})
	if err != nil {
		log.Fatal(err)
	}
	row := func(k, v string) {
		if v != "" {
			fmt.Printf("%18s: %s\n", k, v)
		}
	}
	row("Static hostname", varlink.Deref(h.StaticHostname))
	row("Transient hostname", h.Hostname)
	row("Pretty hostname", varlink.Deref(h.PrettyHostname))
	row("Icon name", varlink.Deref(h.IconName))
	row("Chassis", varlink.Deref(h.Chassis))
	row("Operating system", varlink.Deref(h.OperatingSystemPrettyName))
	row("Kernel", h.KernelName+" "+h.KernelRelease)
	row("Hardware vendor", varlink.Deref(h.HardwareVendor))
	row("Hardware model", varlink.Deref(h.HardwareModel))
}
