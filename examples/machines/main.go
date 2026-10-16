// Command machines lists containers and VMs registered with
// systemd-machined, like `machinectl list`.
//
//	go run ./examples/machines
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"text/tabwriter"

	"github.com/rossedman/sdvarlink/systemd/v257/machine"
	"github.com/rossedman/sdvarlink/varlink"
)

func main() {
	ctx := context.Background()
	c, err := machine.Dial(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "MACHINE\tCLASS\tSERVICE\tUNIT\tLEADER")
	n := 0
	for m, err := range c.ListMore(ctx, machine.ListIn{}) {
		if errors.Is(err, machine.ErrNoSuchMachine) {
			break // machined answers NoSuchMachine when nothing is registered
		}
		if err != nil {
			log.Fatal(err)
		}
		leader := int64(0)
		if m.Leader != nil {
			leader = m.Leader.PID
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\n", m.Name, m.Class, varlink.Deref(m.Service), varlink.Deref(m.Unit), leader)
		n++
	}
	w.Flush()
	fmt.Printf("\n%d machines listed.\n", n)
}
