// Command list-users streams every user record from systemd's userdb
// multiplexer, like `userdbctl user`.
//
//	go run ./examples/list-users
//	go run ./examples/list-users -user root
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"text/tabwriter"

	"github.com/rossedman/sdvarlink/systemd/v257/userdatabase"
	"github.com/rossedman/sdvarlink/varlink"
)

// userRecord picks a few fields from the JSON user record format
// (https://systemd.io/USER_RECORD/).
type userRecord struct {
	UserName      string `json:"userName"`
	UID           int    `json:"uid"`
	RealName      string `json:"realName"`
	HomeDirectory string `json:"homeDirectory"`
	Disposition   string `json:"disposition"`
}

// main runs the example; see the package comment for usage.
func main() {
	user := flag.String("user", "", "look up a single user")
	flag.Parse()
	ctx := context.Background()

	c, err := userdatabase.Dial(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	in := userdatabase.GetUserRecordIn{Service: "io.systemd.Multiplexer"}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tUID\tDISPOSITION\tHOME\tREAL NAME")
	defer w.Flush()

	if *user != "" {
		in.UserName = varlink.Ptr(*user)
		out, err := c.GetUserRecord(ctx, in)
		if err != nil {
			log.Fatal(err)
		}
		print(w, out.Record)
		return
	}

	// With "more", the multiplexer streams one reply per user.
	for out, err := range c.GetUserRecordMore(ctx, in) {
		if err != nil {
			log.Fatal(err)
		}
		print(w, out.Record)
	}
}

// print writes one user record as a row of w.
func print(w *tabwriter.Writer, raw json.RawMessage) {
	var u userRecord
	if err := json.Unmarshal(raw, &u); err != nil {
		log.Fatal(err)
	}
	fmt.Fprintf(w, "%s\t%d\t%s\t%s\t%s\n", u.UserName, u.UID, u.Disposition, u.HomeDirectory, u.RealName)
}
