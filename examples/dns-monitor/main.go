// Command dns-monitor streams every DNS query systemd-resolved answers, like
// `resolvectl monitor`. It runs until interrupted. Requires root.
//
//	sudo go run ./examples/dns-monitor
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"

	"github.com/rossedman/sdvarlink/systemd/v257/resolvemonitor"
	"github.com/rossedman/sdvarlink/varlink"
)

// main runs the example; see the package comment for usage.
func main() {
	// Cancelling the context ends the stream and closes the connection.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	c, err := resolvemonitor.Dial(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	for ev, err := range c.SubscribeQueryResults(ctx, resolvemonitor.SubscribeQueryResultsIn{}) {
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Fatal(err)
		}
		if varlink.Deref(ev.Ready) {
			fmt.Println("monitoring… (Ctrl-C to stop)")
			continue
		}
		var q []string
		for _, k := range ev.Question {
			q = append(q, k.Name)
		}
		fmt.Printf("%-10s %-40s answers=%d\n", varlink.Deref(ev.State), strings.Join(q, ","), len(ev.Answer))
	}
}
