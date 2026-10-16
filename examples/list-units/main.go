// Command list-units lists units through PID 1's io.systemd.Unit interface,
// added in systemd v258. On older hosts it says so and exits, which is
// where a real program would fall back to go-systemd's D-Bus API.
//
//	go run ./examples/list-units
//	go run ./examples/list-units -unit ssh.service
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"text/tabwriter"

	"github.com/rossedman/sdvarlink/systemd/v258/unit"
	"github.com/rossedman/sdvarlink/varlink"
)

func main() {
	name := flag.String("unit", "", "show a single unit")
	flag.Parse()
	ctx := context.Background()

	c, err := unit.Dial(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	// Dial only proves the socket exists (it fails with ErrUnsupported
	// before v258). Checking the method as well keeps this working on hosts
	// whose systemd lacks it, and is how you guard methods added later.
	if ok, err := c.Supports(ctx, "List"); err != nil {
		log.Fatal(err)
	} else if !ok {
		log.Fatal("io.systemd.Unit.List needs systemd v258 or newer; use D-Bus here")
	}

	in := unit.ListIn{}
	if *name != "" {
		in.Name = varlink.Ptr(*name)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "UNIT\tLOAD\tACTIVE\tSUB\tDESCRIPTION")
	for u, err := range c.ListMore(ctx, in) {
		if err != nil {
			log.Fatal(err)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			u.Context.ID,
			u.Runtime.LoadState,
			u.Runtime.ActiveState,
			u.Runtime.SubState,
			varlink.Deref(u.Context.Description))
	}
	w.Flush()
}
