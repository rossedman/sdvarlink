// Command metrics reads the io.systemd.Metrics interface that systemd
// services publish under /run/systemd/report/ (systemd v260+), the
// building block for exporters such as report2otel.
//
//	go run ./examples/metrics
//	go run ./examples/metrics /run/systemd/report/io.systemd.Network
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/rossedman/sdvarlink/systemd/v260/metrics"
	"github.com/rossedman/sdvarlink/varlink"
)

// main runs the example; see the package comment for usage.
func main() {
	sockets := os.Args[1:]
	if len(sockets) == 0 {
		sockets, _ = filepath.Glob("/run/systemd/report/*")
	}
	if len(sockets) == 0 {
		log.Fatal("no metrics sockets under /run/systemd/report (systemd v260+)")
	}
	ctx := context.Background()
	for _, sock := range sockets {
		c, err := metrics.DialPath(ctx, sock)
		if err != nil {
			log.Printf("%s: %v", sock, err)
			continue
		}
		fmt.Printf("# %s\n", filepath.Base(sock))
		// List streams one reply per metric value.
		for m, err := range c.List(ctx) {
			if err != nil {
				log.Printf("%s: %v", sock, err)
				break
			}
			obj := varlink.Deref(m.Object)
			if obj != "" {
				obj = "{" + obj + "}"
			}
			fmt.Printf("%s%s %s\n", m.Name, obj, m.Value)
		}
		c.Close()
	}
}
