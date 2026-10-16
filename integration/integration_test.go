//go:build integration

// Package integration runs sdvarlink against real systemd. Run it on a host
// or VM with systemd v257 or newer (CI boots one per supported release):
//
//	go test -tags integration ./integration/
//
// Tests skip when the socket they need does not exist. Only read-only or
// idempotent methods are called; tests that mutate the host are gated
// behind SDVARLINK_MUTATING=1 and meant for disposable VMs.
package integration

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/rossedman/sdvarlink/systemd/latest/journal"
	"github.com/rossedman/sdvarlink/systemd/latest/manager"
	"github.com/rossedman/sdvarlink/systemd/latest/resolve"
	"github.com/rossedman/sdvarlink/systemd/latest/userdatabase"
	"github.com/rossedman/sdvarlink/varlink"
	"github.com/rossedman/sdvarlink/varlinktest"
)

func need(t *testing.T, socket string) {
	t.Helper()
	if _, err := os.Stat(socket); err != nil {
		t.Skipf("%s not present", socket)
	}
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return c
}

func TestIntrospectRealService(t *testing.T) {
	need(t, journal.DefaultSocket)
	conn, err := varlink.Dial(ctx(t), journal.DefaultSocket)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	info, err := varlink.GetInfo(ctx(t), conn)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s %s: %v", info.Product, info.Version, info.Interfaces)
	if !strings.Contains(strings.Join(info.Interfaces, " "), "io.systemd.Journal") {
		t.Fatalf("interfaces = %v", info.Interfaces)
	}
	desc, err := varlink.GetInterfaceDescription(ctx(t), conn, "io.systemd.Journal")
	if err != nil || !strings.Contains(desc, "method Synchronize") {
		t.Fatalf("description = %q, %v", desc, err)
	}
	if ok, err := varlink.Supports(ctx(t), conn, "io.systemd.Journal.Synchronize"); err != nil || !ok {
		t.Fatalf("Supports = %v, %v", ok, err)
	}
	err = conn.Call(ctx(t), "io.systemd.Journal.NoSuchMethod", nil, nil)
	if !errors.Is(err, varlink.ErrMethodNotFound) {
		t.Fatalf("unknown method: %v", err)
	}
	// The connection survives an error reply.
	if err := conn.Call(ctx(t), "org.varlink.service.GetInfo", nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestJournalSynchronize(t *testing.T) {
	need(t, journal.DefaultSocket)
	c, err := journal.Dial(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Synchronize(ctx(t), journal.SynchronizeIn{}); err != nil {
		t.Fatal(err)
	}
}

func TestResolveLocalhost(t *testing.T) {
	need(t, resolve.DefaultSocket)
	c, err := resolve.Dial(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	out, err := c.ResolveHostname(ctx(t), resolve.ResolveHostnameIn{Name: "localhost"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Addresses) == 0 {
		t.Fatal("no addresses for localhost")
	}
}

func TestManagerDescribe(t *testing.T) {
	need(t, manager.DefaultSocket)
	c, err := manager.Dial(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	out, err := c.Describe(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("manager: %+v", out.Runtime)
}

func TestUserDatabaseStream(t *testing.T) {
	need(t, userdatabase.DefaultSocket)
	c, err := userdatabase.Dial(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	n := 0
	for _, err := range c.GetUserRecordMore(ctx(t), userdatabase.GetUserRecordIn{Service: "io.systemd.Multiplexer"}) {
		if err != nil {
			t.Fatal(err)
		}
		n++
	}
	if n == 0 {
		t.Fatal("no user records")
	}
}

// TestVarlinkctlAgainstFake checks the fake server, and therefore the shared
// wire code, against systemd's own client.
func TestVarlinkctlAgainstFake(t *testing.T) {
	bin, err := exec.LookPath("varlinkctl")
	if err != nil {
		t.Skip("varlinkctl not installed")
	}
	srv := varlinktest.NewServer(t)
	srv.AddInterface(resolve.InterfaceDescription)
	srv.Handle("io.systemd.Resolve.ResolveHostname", func(in resolve.ResolveHostnameIn) (resolve.ResolveHostnameOut, error) {
		return resolve.ResolveHostnameOut{Name: in.Name, Addresses: []resolve.ResolvedAddress{{Family: 2, Address: []int64{127, 0, 0, 1}}}}, nil
	})
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.Command(bin, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("varlinkctl %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	if out := run("info", srv.Path()); !strings.Contains(out, "io.systemd.Resolve") {
		t.Fatalf("info:\n%s", out)
	}
	if out := run("call", srv.Path(), "io.systemd.Resolve.ResolveHostname", `{"name":"fake.test"}`); !strings.Contains(out, "fake.test") {
		t.Fatalf("call:\n%s", out)
	}
	if out := run("introspect", srv.Path(), "io.systemd.Resolve"); !strings.Contains(out, "method ResolveHostname") {
		t.Fatalf("introspect:\n%s", out)
	}
}
