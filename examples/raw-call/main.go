// Command raw-call calls any Varlink method with JSON parameters and prints
// the replies, like `varlinkctl call`. It shows the core package on its
// own: no generated bindings needed.
//
//	go run ./examples/raw-call /run/systemd/io.systemd.Manager io.systemd.Manager.Describe
//	go run ./examples/raw-call -more /run/systemd/userdb/io.systemd.Multiplexer \
//	    io.systemd.UserDatabase.GetUserRecord '{"service":"io.systemd.Multiplexer"}'
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/rossedman/sdvarlink/varlink"
)

func main() {
	more := flag.Bool("more", false, "request a stream of replies")
	oneway := flag.Bool("oneway", false, "send without waiting for a reply")
	flag.Parse()
	if flag.NArg() < 2 || flag.NArg() > 3 {
		log.Fatal("usage: raw-call [-more|-oneway] SOCKET METHOD [JSON]")
	}
	socket, method := flag.Arg(0), flag.Arg(1)
	params := json.RawMessage("{}")
	if flag.NArg() == 3 {
		params = json.RawMessage(flag.Arg(2))
	}

	ctx := context.Background()
	conn, err := varlink.Dial(ctx, socket)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	switch {
	case *oneway:
		err = conn.Oneway(ctx, method, params)
	case *more:
		for r, rerr := range conn.Stream(ctx, method, params) {
			if rerr != nil {
				err = rerr
				break
			}
			print(r.Parameters)
			r.Close()
		}
	default:
		var out json.RawMessage
		if err = conn.Call(ctx, method, params, &out); err == nil {
			print(out)
		}
	}

	var ve *varlink.Error
	if errors.As(err, &ve) {
		fmt.Fprintf(os.Stderr, "error: %s %s\n", ve.Name, ve.Parameters)
		os.Exit(1)
	}
	if err != nil {
		log.Fatal(err)
	}
}

func print(raw json.RawMessage) {
	var buf bytes.Buffer
	if json.Indent(&buf, raw, "", "  ") != nil {
		buf.Write(raw)
	}
	fmt.Println(buf.String())
}
