// Command journal-control asks systemd-journald to flush, sync or rotate
// its files, like `journalctl --sync` / `--rotate` / `--flush`. Requires
// root.
//
//	sudo go run ./examples/journal-control sync
package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/rossedman/sdvarlink/systemd/v257/journal"
)

// main runs the example; see the package comment for usage.
func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: journal-control sync|rotate|flush|relinquish")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c, err := journal.Dial(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	switch os.Args[1] {
	case "sync":
		err = c.Synchronize(ctx)
	case "rotate":
		err = c.Rotate(ctx)
	case "flush":
		err = c.FlushToVar(ctx)
	case "relinquish":
		err = c.RelinquishVar(ctx)
	default:
		log.Fatalf("unknown action %q", os.Args[1])
	}
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("journald: %s done", os.Args[1])
}
