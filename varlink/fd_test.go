//go:build linux

package varlink_test

import (
	"context"
	"errors"
	"io"
	"iter"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rossedman/sdvarlink/varlink"
	"github.com/rossedman/sdvarlink/varlinktest"
)

type mountIn struct {
	Image *os.File `json:"-" varlink:"imageFileDescriptor,fd"`
	Where string   `json:"where"`
}

type partition struct {
	Designator string   `json:"designator"`
	Mount      *os.File `json:"-" varlink:"mountFileDescriptor,fd"`
}

type mountOut struct {
	Partitions []partition `json:"partitions"`
}

func tempFile(t *testing.T, content string) *os.File {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "fd")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func openFDs(t *testing.T) int {
	t.Helper()
	ents, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skip("no /proc/self/fd")
	}
	return len(ents)
}

// fdServer echoes the image back as two "partitions", each a fresh
// descriptor for the same file, to exercise both directions.
func fdServer(t *testing.T) *varlinktest.Server {
	srv := varlinktest.NewServer(t)
	srv.Handle("io.systemd.MountFileSystem.MountImage", func(in mountIn) (mountOut, error) {
		defer in.Image.Close()
		// Reopen the received descriptor twice; the server closes the
		// returned files after sending them.
		a, err := os.Open("/proc/self/fd/" + itoa(int(in.Image.Fd())))
		if err != nil {
			return mountOut{}, err
		}
		b, err := os.Open("/proc/self/fd/" + itoa(int(in.Image.Fd())))
		if err != nil {
			a.Close()
			return mountOut{}, err
		}
		return mountOut{Partitions: []partition{{"root", a}, {"usr", b}}}, nil
	})
	return srv
}

func itoa(i int) string {
	var b [20]byte
	n := len(b)
	for {
		n--
		b[n] = byte('0' + i%10)
		i /= 10
		if i == 0 {
			return string(b[n:])
		}
	}
}

func TestFileDescriptorRoundTrip(t *testing.T) {
	srv := fdServer(t)
	c := srv.Dial()
	img := tempFile(t, "disk image bytes")

	var out mountOut
	if err := c.Call(context.Background(), "io.systemd.MountFileSystem.MountImage", mountIn{Image: img, Where: "/mnt"}, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Partitions) != 2 {
		t.Fatalf("out = %+v", out)
	}
	for _, p := range out.Partitions {
		if p.Mount == nil {
			t.Fatalf("partition %s has no fd", p.Designator)
		}
		if got := readAll(t, p.Mount); got != "disk image bytes" {
			t.Fatalf("partition %s reads %q", p.Designator, got)
		}
		p.Mount.Close()
	}
	// The JSON carried indexes, not files.
	if got := string(srv.Calls()[0].Parameters); !strings.Contains(got, `"imageFileDescriptor":0`) {
		t.Fatalf("parameters = %s", got)
	}
	// The caller still owns the input file.
	if _, err := img.Stat(); err != nil {
		t.Fatalf("input file was closed: %v", err)
	}
}

func TestFileDescriptorsDoNotLeak(t *testing.T) {
	srv := fdServer(t)
	c := srv.Dial()
	img := tempFile(t, "x")
	ctx := context.Background()

	call := func() {
		var out mountOut
		if err := c.Call(ctx, "io.systemd.MountFileSystem.MountImage", mountIn{Image: img}, &out); err != nil {
			t.Fatal(err)
		}
		for _, p := range out.Partitions {
			p.Mount.Close()
		}
	}
	call() // warm up
	before := openFDs(t)
	n := 2000
	if testing.Short() {
		n = 200
	}
	for i := 0; i < n; i++ {
		call()
	}
	// Discarding the reply closes the unclaimed descriptors.
	for i := 0; i < n; i++ {
		if err := c.Call(ctx, "io.systemd.MountFileSystem.MountImage", mountIn{Image: img}, nil); err != nil {
			t.Fatal(err)
		}
	}
	after := openFDs(t)
	// The server closes its copies asynchronously; allow a little slack.
	if after > before+4 {
		t.Fatalf("open fds grew from %d to %d over %d calls", before, after, 2*n)
	}
}

func TestExtraFilesWithRawCall(t *testing.T) {
	srv := varlinktest.NewServer(t)
	srv.HandleRaw("com.example.Raw", func(ctx context.Context, call *varlinktest.Call) iter.Seq2[*varlinktest.Response, error] {
		return func(yield func(*varlinktest.Response, error) bool) {
			var in struct {
				F *os.File `json:"-" varlink:"fd,fd"`
			}
			if err := call.Decode(&in); err != nil {
				yield(nil, err)
				return
			}
			defer in.F.Close()
			buf := make([]byte, 16)
			n, _ := in.F.ReadAt(buf, 0)
			yield(&varlinktest.Response{Parameters: map[string]string{"content": string(buf[:n])}}, nil)
		}
	})
	c := srv.Dial()
	f := tempFile(t, "hello")
	var out struct{ Content string }
	err := c.Call(context.Background(), "com.example.Raw", map[string]int{"fd": 0}, &out, varlink.WithFiles(f))
	if err != nil {
		t.Fatal(err)
	}
	if out.Content != "hello" {
		t.Fatalf("content = %q", out.Content)
	}
}

// Descriptors that arrived for a reply nobody read must be closed when the
// connection closes.
func TestPendingFilesClosedWithConn(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	srv := varlinktest.NewServer(t)
	srv.HandleRaw("com.example.Two", func(context.Context, *varlinktest.Call) iter.Seq2[*varlinktest.Response, error] {
		return func(yield func(*varlinktest.Response, error) bool) {
			if !yield(&varlinktest.Response{Parameters: map[string]int{"n": 1}}, nil) {
				return
			}
			yield(&varlinktest.Response{Parameters: map[string]int{"fd": 0}, Files: []*os.File{w}}, nil)
		}
	})
	c := srv.Dial()
	for range c.Stream(context.Background(), "com.example.Two", nil) {
		break // abandon after the first reply; the second may already be buffered
	}
	c.Close()
	// The server closed its copy after sending; once the client's copy is
	// closed too, the read end sees EOF.
	r.SetReadDeadline(time.Now().Add(5 * time.Second))
	if n, err := r.Read(make([]byte, 1)); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("a write end is still open: n=%d err=%v", n, err)
	}
}

// Sending a file must not switch it to blocking mode.
func TestSentFileKeepsDeadlines(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	srv := varlinktest.NewServer(t)
	srv.Handle("com.example.Take", func(in struct {
		F *os.File `json:"-" varlink:"f,fd"`
	}) (struct{}, error) {
		in.F.Close()
		return struct{}{}, nil
	})
	c := srv.Dial()
	in := struct {
		F *os.File `json:"-" varlink:"f,fd"`
	}{w}
	if err := c.Call(context.Background(), "com.example.Take", in, nil); err != nil {
		t.Fatal(err)
	}
	if err := w.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("file lost deadline support after sending: %v", err)
	}
}

// The first concurrent use of a type must not race the type cache into
// dropping descriptors.
func TestConcurrentFirstMarshal(t *testing.T) {
	f, _ := os.Open(os.DevNull)
	defer f.Close()
	for round := 0; round < 50; round++ {
		// A fresh type each round, so every round is a first use.
		typ := reflect.StructOf([]reflect.StructField{
			{Name: "F", Type: reflect.TypeOf(f), Tag: `json:"-" varlink:"f,fd"`},
			{Name: "R" + strconv.Itoa(round), Type: reflect.TypeOf(0)},
		})
		v := reflect.New(typ)
		v.Elem().Field(0).Set(reflect.ValueOf(f))
		var wg sync.WaitGroup
		var dropped atomic.Int32
		for i := 0; i < 16; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, files, _ := varlink.Marshal(v.Interface()); len(files) != 1 {
					dropped.Add(1)
				}
			}()
		}
		wg.Wait()
		if dropped.Load() > 0 {
			t.Fatalf("round %d: %d marshals dropped the descriptor", round, dropped.Load())
		}
	}
}
