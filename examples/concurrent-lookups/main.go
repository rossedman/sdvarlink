// Command concurrent-lookups resolves many names in parallel. Generated
// clients keep a pool of connections, so goroutines do not queue behind one
// another on a single socket.
//
//	go run ./examples/concurrent-lookups github.com go.dev systemd.io kernel.org
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/rossedman/sdvarlink/systemd/v257/resolve"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	c, err := resolve.Dial(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	names := os.Args[1:]
	results := make([]string, len(names))
	var wg sync.WaitGroup
	start := time.Now()
	for i, name := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			t := time.Now()
			out, err := c.ResolveHostname(ctx, resolve.ResolveHostnameIn{Name: name})
			if err != nil {
				results[i] = fmt.Sprintf("%-20s error: %v", name, err)
				return
			}
			results[i] = fmt.Sprintf("%-20s %d addresses in %v", name, len(out.Addresses), time.Since(t).Round(time.Millisecond))
		}()
	}
	wg.Wait()
	for _, r := range results {
		fmt.Println(r)
	}
	fmt.Printf("%d lookups in %v\n", len(names), time.Since(start).Round(time.Millisecond))
}
