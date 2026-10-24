//go:build integration

package integration

import (
	"errors"
	"strings"
	"testing"

	"github.com/rossedman/sdvarlink/systemd/latest/hostname"
	"github.com/rossedman/sdvarlink/systemd/latest/job"
	"github.com/rossedman/sdvarlink/systemd/latest/login"
	"github.com/rossedman/sdvarlink/systemd/latest/unit"
	"github.com/rossedman/sdvarlink/varlink"
)

// These tests decode the interfaces systemd v262 reworked most. They run the
// systemd/latest bindings against every release in the matrix, so each one
// first checks that the host's systemd offers what it calls.

// supports skips the test unless the service at socket implements method.
func supports(t *testing.T, socket, method string) {
	t.Helper()
	need(t, socket)
	conn, err := varlink.Dial(ctx(t), socket)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ok, err := varlink.Supports(ctx(t), conn, method)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Skipf("%s not supported by this systemd", method)
	}
}

// describes reports whether the host's IDL for iface at socket contains
// decl, such as "type ServiceContext", to tell which release's fields it
// sends.
func describes(t *testing.T, socket, iface, decl string) bool {
	t.Helper()
	conn, err := varlink.Dial(ctx(t), socket)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	desc, err := varlink.GetInterfaceDescription(ctx(t), conn, iface)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Contains(desc, decl)
}

func TestUnitList(t *testing.T) {
	supports(t, unit.DefaultSocket, "io.systemd.Unit.List")
	c, err := unit.Dial(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// v262 nests per-type settings (ServiceContext, SocketContext, ...) in
	// UnitContext; on older hosts those stay nil.
	nested := describes(t, unit.DefaultSocket, "io.systemd.Unit", "type ServiceContext")
	units, services, withService := 0, 0, 0
	for u, err := range c.ListMore(ctx(t), unit.ListIn{}) {
		if err != nil {
			t.Fatal(err)
		}
		units++
		if u.Context.ID == "" {
			t.Errorf("unit %d has no ID: %+v", units, u.Context)
		}
		if strings.HasSuffix(u.Context.ID, ".service") {
			services++
			if u.Context.Service != nil {
				withService++
			}
		}
	}
	t.Logf("%d units, %d services, %d with ServiceContext", units, services, withService)
	if units == 0 || services == 0 {
		t.Fatalf("listed %d units and %d services, want some of each", units, services)
	}
	if nested && withService == 0 {
		t.Fatal("this systemd declares ServiceContext but sent it for no service")
	}

	// A single unit by name returns the same unit.
	one, err := c.List(ctx(t), unit.ListIn{Name: ptr("init.scope")})
	if err != nil {
		t.Fatal(err)
	}
	if one.Context.ID != "init.scope" {
		t.Fatalf("List(init.scope) = %q", one.Context.ID)
	}
}

func TestJobList(t *testing.T) {
	supports(t, job.DefaultSocket, "io.systemd.Job.List")
	c, err := job.Dial(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	n := 0
	for j, err := range c.ListMore(ctx(t), job.ListIn{}) {
		if errors.Is(err, &job.NoSuchJobError{}) {
			break // an idle system has no queued jobs
		}
		if err != nil {
			t.Fatal(err)
		}
		n++
		if j.ID <= 0 || j.JobType == "" {
			t.Errorf("job %+v has no ID or type", j)
		}
	}
	t.Logf("%d queued jobs", n)
}

func TestHostnameDescribe(t *testing.T) {
	supports(t, hostname.DefaultSocket, "io.systemd.Hostname.Describe")
	c, err := hostname.Dial(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	out, err := c.Describe(ctx(t), hostname.DescribeIn{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Hostname == "" {
		t.Fatal("empty hostname")
	}
	t.Logf("hostname %q, machine tags %v", out.Hostname, out.MachineTags)
}

func TestLoginListSeatsAndSessions(t *testing.T) {
	supports(t, login.DefaultSocket, "io.systemd.Login.ListSeats")
	c, err := login.Dial(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	seats := 0
	for s, err := range c.ListSeatsMore(ctx(t), login.ListSeatsIn{}) {
		if errors.Is(err, &login.NoSuchSeatError{}) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		seats++
		if s.Context.ID == "" {
			t.Errorf("seat %d has no ID", seats)
		}
	}
	sessions := 0
	for s, err := range c.ListSessionsMore(ctx(t), login.ListSessionsIn{}) {
		if errors.Is(err, &login.NoSuchSessionError{}) {
			break // a container usually has no login sessions
		}
		if err != nil {
			t.Fatal(err)
		}
		sessions++
		if s.Runtime.ID == "" {
			t.Errorf("session %d has no ID", sessions)
		}
	}
	t.Logf("%d seats, %d sessions", seats, sessions)
}

// ptr returns a pointer to v.
func ptr[T any](v T) *T { return &v }
