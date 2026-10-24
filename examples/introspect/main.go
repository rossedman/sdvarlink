// Command introspect lists the interfaces and methods a Varlink service
// offers, like `varlinkctl info` plus `varlinkctl introspect`, using the
// core varlink package and the idl parser.
//
//	go run ./examples/introspect /run/systemd/io.systemd.Manager
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/rossedman/sdvarlink/idl"
	"github.com/rossedman/sdvarlink/varlink"
)

// main runs the example; see the package comment for usage.
func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: introspect SOCKET")
	}
	ctx := context.Background()
	conn, err := varlink.Dial(ctx, os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	info, err := varlink.GetInfo(ctx, conn)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s %s (%s)\n\n", info.Product, info.Version, info.Vendor)

	for _, name := range info.Interfaces {
		desc, err := varlink.GetInterfaceDescription(ctx, conn, name)
		if err != nil {
			log.Fatal(err)
		}
		iface, err := idl.Parse(desc)
		if err != nil {
			log.Fatalf("%s: %v", name, err)
		}
		fmt.Printf("%s  (%d methods, %d types, %d errors)\n", iface.Name, len(iface.Methods), len(iface.Types), len(iface.Errors))
		for _, m := range iface.Methods {
			stream := ""
			if m.More != idl.MoreNone {
				stream = "  [" + m.More.String() + "]"
			}
			fmt.Printf("    %s%s -> %s%s\n", m.Name, m.In, m.Out, stream)
		}
		fmt.Println()
	}
}
