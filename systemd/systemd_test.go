// Tests for the generated packages, run against the varlinktest fake. They
// live here because the version directories are regenerated from scratch.
package systemd_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"iter"
	"os"
	"runtime"
	"testing"

	latestresolve "github.com/rossedman/sdvarlink/systemd/latest/resolve"
	"github.com/rossedman/sdvarlink/systemd/v257/userdatabase"
	"github.com/rossedman/sdvarlink/systemd/v260/mountfilesystem"
	"github.com/rossedman/sdvarlink/systemd/v260/resolve"
	"github.com/rossedman/sdvarlink/varlink"
	"github.com/rossedman/sdvarlink/varlinktest"
)

func TestResolveHostname(t *testing.T) {
	srv := varlinktest.NewServer(t)
	srv.Handle("io.systemd.Resolve.ResolveHostname", func(in resolve.ResolveHostnameIn) (resolve.ResolveHostnameOut, error) {
		if in.Name != "example.com" {
			return resolve.ResolveHostnameOut{}, &resolve.NoSuchResourceRecordError{}
		}
		return resolve.ResolveHostnameOut{
			Name:      in.Name,
			Addresses: []resolve.ResolvedAddress{{Family: 2, Address: []int64{93, 184, 215, 14}}},
		}, nil
	})
	ctx := context.Background()
	c, err := resolve.DialPath(ctx, srv.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	out, err := c.ResolveHostname(ctx, resolve.ResolveHostnameIn{Name: "example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Name != "example.com" || len(out.Addresses) != 1 || out.Addresses[0].Address[0] != 93 {
		t.Fatalf("out = %+v", out)
	}
	// Optional inputs are omitted, required ones always sent.
	if got := string(srv.Calls()[0].Parameters); got != `{"name":"example.com"}` {
		t.Fatalf("parameters = %s", got)
	}

	_, err = c.ResolveHostname(ctx, resolve.ResolveHostnameIn{Name: "nope.invalid"})
	var nf *resolve.NoSuchResourceRecordError
	if !errors.As(err, &nf) {
		t.Fatalf("err = %#v", err)
	}
	if !errors.Is(err, resolve.ErrNoSuchResourceRecord) {
		t.Fatal("errors.Is with sentinel failed")
	}
	if errors.Is(err, resolve.ErrNoNameServers) {
		t.Fatal("matched the wrong sentinel")
	}
}

func TestTypedErrorWithParameters(t *testing.T) {
	srv := varlinktest.NewServer(t)
	srv.Handle("io.systemd.Resolve.ResolveHostname", func(resolve.ResolveHostnameIn) (resolve.ResolveHostnameOut, error) {
		return resolve.ResolveHostnameOut{}, &resolve.DNSSECValidationFailedError{Result: "invalid"}
	})
	c, _ := resolve.DialPath(context.Background(), srv.Path())
	defer c.Close()
	_, err := c.ResolveHostname(context.Background(), resolve.ResolveHostnameIn{Name: "x"})
	var dv *resolve.DNSSECValidationFailedError
	if !errors.As(err, &dv) || dv.Result != "invalid" {
		t.Fatalf("err = %v", err)
	}
}

func TestUnknownFieldsKept(t *testing.T) {
	srv := varlinktest.NewServer(t)
	srv.HandleRaw("io.systemd.Resolve.ResolveHostname", func(context.Context, *varlinktest.Call) iter.Seq2[*varlinktest.Response, error] {
		return func(yield func(*varlinktest.Response, error) bool) {
			yield(&varlinktest.Response{Parameters: json.RawMessage(
				`{"name":"a","flags":1,"addresses":[{"family":2,"address":[1,2,3,4],"fromTheFuture":true}],"newField":"x"}`)}, nil)
		}
	})
	c, _ := resolve.DialPath(context.Background(), srv.Path())
	defer c.Close()
	out, err := c.ResolveHostname(context.Background(), resolve.ResolveHostnameIn{Name: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if string(out.Extra["newField"]) != `"x"` || string(out.Addresses[0].Extra["fromTheFuture"]) != "true" {
		t.Fatalf("extra = %v / %v", out.Extra, out.Addresses[0].Extra)
	}
}

func TestStreamingUserRecords(t *testing.T) {
	srv := varlinktest.NewServer(t)
	users := []string{"root", "ross", "nobody"}
	srv.Handle("io.systemd.UserDatabase.GetUserRecord", func(ctx context.Context, in userdatabase.GetUserRecordIn) iter.Seq2[userdatabase.GetUserRecordOut, error] {
		return func(yield func(userdatabase.GetUserRecordOut, error) bool) {
			for _, u := range users {
				rec, _ := json.Marshal(map[string]string{"userName": u})
				if !yield(userdatabase.GetUserRecordOut{Record: rec}, nil) {
					return
				}
			}
		}
	})
	c, err := userdatabase.DialPath(context.Background(), srv.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var got []string
	for out, err := range c.GetUserRecordMore(context.Background(), userdatabase.GetUserRecordIn{Service: "io.systemd.Multiplexer"}) {
		if err != nil {
			t.Fatal(err)
		}
		var rec struct{ UserName string }
		json.Unmarshal(out.Record, &rec)
		got = append(got, rec.UserName)
	}
	if len(got) != 3 || got[1] != "ross" {
		t.Fatalf("got %v", got)
	}
}

func TestMountImagePassesFiles(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("fd passing via /proc is Linux-specific")
	}
	srv := varlinktest.NewServer(t)
	srv.Handle("io.systemd.MountFileSystem.MountImage", func(in mountfilesystem.MountImageIn) (mountfilesystem.MountImageOut, error) {
		defer in.ImageFileDescriptor.Close()
		// Pretend the image has one partition and hand back a descriptor.
		f, err := os.Open("/proc/self/fd/" + itoa(int(in.ImageFileDescriptor.Fd())))
		if err != nil {
			return mountfilesystem.MountImageOut{}, err
		}
		return mountfilesystem.MountImageOut{
			SingleFileSystem: true,
			Partitions: []mountfilesystem.PartitionInfo{{
				Designator:          mountfilesystem.PartitionDesignatorRoot,
				MountFileDescriptor: f,
			}},
		}, nil
	})
	img, err := os.CreateTemp(t.TempDir(), "img")
	if err != nil {
		t.Fatal(err)
	}
	img.WriteString("ext4!")
	defer img.Close()

	c, _ := mountfilesystem.DialPath(context.Background(), srv.Path())
	defer c.Close()
	out, err := c.MountImage(context.Background(), mountfilesystem.MountImageIn{ImageFileDescriptor: img})
	if err != nil {
		t.Fatal(err)
	}
	m := out.Partitions[0].MountFileDescriptor
	if m == nil {
		t.Fatal("no mount fd returned")
	}
	defer m.Close()
	m.Seek(0, io.SeekStart)
	b, _ := io.ReadAll(m)
	if string(b) != "ext4!" {
		t.Fatalf("read %q through returned fd", b)
	}
}

func TestSupportsAgainstFake(t *testing.T) {
	srv := varlinktest.NewServer(t)
	srv.AddInterface(resolve.InterfaceDescription)
	c, _ := resolve.DialPath(context.Background(), srv.Path())
	defer c.Close()
	for method, want := range map[string]bool{
		"ResolveHostname":                         true,
		"io.systemd.Resolve.DumpDNSConfiguration": true,
		"TeleportHost":                            false,
	} {
		got, err := c.Supports(context.Background(), method)
		if err != nil || got != want {
			t.Errorf("Supports(%s) = %v, %v; want %v", method, got, err, want)
		}
	}
}

func TestLatestAliases(t *testing.T) {
	// latest re-exports the newest release, so values are interchangeable.
	in := resolve.ResolveHostnameIn{Name: "x"}
	var _ *latestresolve.ResolveHostnameIn = &in //nolint:staticcheck // asserts the alias is the same type
	if latestresolve.SystemdVersion != resolve.SystemdVersion || latestresolve.DefaultSocket != resolve.DefaultSocket {
		t.Fatal("latest does not match newest release")
	}
	srv := varlinktest.NewServer(t)
	srv.Handle("io.systemd.Resolve.ResolveHostname", func(in resolve.ResolveHostnameIn) (resolve.ResolveHostnameOut, error) {
		return resolve.ResolveHostnameOut{Name: in.Name}, nil
	})
	c, err := latestresolve.DialPath(context.Background(), srv.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	out, err := c.ResolveHostname(context.Background(), latestresolve.ResolveHostnameIn{Name: "latest"})
	if err != nil || out.Name != "latest" {
		t.Fatalf("out=%v err=%v", out, err)
	}
}

func TestDialDefaultSocketMissing(t *testing.T) {
	if _, err := os.Stat(resolve.DefaultSocket); err == nil {
		t.Skip("systemd-resolved is running on this host")
	}
	_, err := resolve.Dial(context.Background())
	if !errors.Is(err, varlink.ErrUnsupported) {
		t.Fatalf("err = %v", err)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for ; i > 0; i /= 10 {
		b = append([]byte{byte('0' + i%10)}, b...)
	}
	return string(b)
}

func TestExtraRoundTrips(t *testing.T) {
	var out resolve.ResolveHostnameOut
	if err := json.Unmarshal([]byte(`{"name":"a","flags":0,"addresses":[],"newField":{"x":1}}`), &out); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]json.RawMessage
	json.Unmarshal(data, &back)
	if string(back["newField"]) != `{"x":1}` || string(back["name"]) != `"a"` {
		t.Fatalf("round trip = %s", data)
	}
	// Extra can carry parameters the bindings do not know yet.
	in := resolve.ResolveHostnameIn{Name: "x", Extra: map[string]json.RawMessage{"name": json.RawMessage(`"ignored"`), "future": json.RawMessage(`true`)}}
	data, _ = json.Marshal(in)
	if string(data) != `{"future":true,"name":"x"}` {
		t.Fatalf("encoded = %s", data)
	}
}
