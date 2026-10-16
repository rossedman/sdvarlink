package health

import (
	"context"
	"flag"
	"strings"
	"testing"

	"github.com/rossedman/sdvarlink/systemd/v257/journal"
	"github.com/rossedman/sdvarlink/varlinktest"
)

var update = flag.Bool("update", false, "record testdata/journal.json against the live journald")

func TestCheckJournal(t *testing.T) {
	var srv *varlinktest.Server
	if *update {
		srv = varlinktest.Record(t, journal.DefaultSocket, "testdata/journal.json")
	} else {
		srv = varlinktest.Replay(t, "testdata/journal.json")
	}
	c, err := journal.DialPath(context.Background(), srv.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	rep, err := CheckJournal(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rep.Product, "journald") {
		t.Fatalf("report = %+v", rep)
	}
}
