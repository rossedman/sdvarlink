//go:build unix

package wire

import (
	"bytes"
	"errors"
	"io"
	"net"
	"os"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func pair(t testing.TB) (*net.UnixConn, *net.UnixConn) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	conn := func(fd int) *net.UnixConn {
		f := os.NewFile(uintptr(fd), "pair")
		defer f.Close()
		c, err := net.FileConn(f)
		if err != nil {
			t.Fatal(err)
		}
		return c.(*net.UnixConn)
	}
	a, b := conn(fds[0]), conn(fds[1])
	t.Cleanup(func() { a.Close(); b.Close() })
	return a, b
}

func TestFraming(t *testing.T) {
	a, b := pair(t)
	// Several messages in one write, then one split over two writes.
	a.Write([]byte(`{"a":1}` + "\x00" + `{"b":2}` + "\x00" + `{"c":`))
	go func() { a.Write([]byte(`3}` + "\x00")) }()
	r := New(b, 1<<20, 4)
	for _, want := range []string{`{"a":1}`, `{"b":2}`, `{"c":3}`} {
		got, files, err := r.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want || len(files) != 0 {
			t.Fatalf("got %q (%d files), want %q", got, len(files), want)
		}
	}
	a.Close()
	if _, _, err := r.ReadMessage(); !errors.Is(err, io.EOF) {
		t.Fatalf("after close: %v", err)
	}
}

func TestFilesBelongToTheirMessage(t *testing.T) {
	a, b := pair(t)
	w := New(a, 1<<20, 4)
	f, err := os.CreateTemp(t.TempDir(), "fd")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	// The first message has no files; the second carries one. Written
	// back to back, they may arrive in a single read.
	if err := w.WriteMessage([]byte(`{"n":1}`), nil); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteMessage([]byte(`{"n":2}`), []*os.File{f}); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteMessage([]byte(`{"n":3}`), nil); err != nil {
		t.Fatal(err)
	}
	r := New(b, 1<<20, 4)
	for i, wantFiles := range []int{0, 1, 0} {
		msg, files, err := r.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		if len(files) != wantFiles {
			t.Fatalf("message %d (%s) got %d files, want %d", i+1, msg, len(files), wantFiles)
		}
		for _, f := range files {
			flags, _ := unix.FcntlInt(f.Fd(), unix.F_GETFD, 0)
			if flags&unix.FD_CLOEXEC == 0 {
				t.Error("received fd lacks FD_CLOEXEC")
			}
			f.Close()
		}
	}
}

func TestFrameLimit(t *testing.T) {
	a, b := pair(t)
	go a.Write(bytes.Repeat([]byte("x"), 4096))
	r := New(b, 1024, 4)
	if _, _, err := r.ReadMessage(); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("err = %v", err)
	}
}

func TestTooManyFilesToSend(t *testing.T) {
	a, _ := pair(t)
	w := New(a, 1<<20, 1)
	err := w.WriteMessage([]byte(`{}`), []*os.File{os.Stdin, os.Stdout})
	if !errors.Is(err, ErrTooManyFiles) {
		t.Fatalf("err = %v", err)
	}
}

func TestTruncatedControlData(t *testing.T) {
	// The receiver accepts one fd per read; the sender passes three, so the
	// kernel truncates the ancillary data. All received fds must be closed.
	a, b := pair(t)
	w := New(a, 1<<20, 8)
	f, _ := os.CreateTemp(t.TempDir(), "fd")
	defer f.Close()
	if err := w.WriteMessage([]byte(`{}`), []*os.File{f, f, f}); err != nil {
		t.Fatal(err)
	}
	r := New(b, 1<<20, 1)
	if _, _, err := r.ReadMessage(); !errors.Is(err, ErrTooManyFiles) {
		t.Fatalf("err = %v", err)
	}
}

func FuzzReadMessage(f *testing.F) {
	f.Add([]byte(`{"parameters":{}}` + "\x00"))
	f.Add([]byte("\x00\x00{}\x00partial"))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, data []byte) {
		a, b := pair(t)
		go func() {
			a.Write(data)
			a.Close()
		}()
		r := New(b, 4096, 4)
		total := 0
		for {
			msg, _, err := r.ReadMessage()
			if err != nil {
				return
			}
			if bytes.IndexByte(msg, 0) >= 0 {
				t.Fatal("message contains NUL")
			}
			total += len(msg) + 1
			if total > len(data) {
				t.Fatal("read more than was written")
			}
		}
	})
}

func TestFilesAttributionStress(t *testing.T) {
	a, b := pair(t)
	w := New(a, 1<<20, 4)
	f, _ := os.CreateTemp(t.TempDir(), "fd")
	defer f.Close()
	const n = 500
	hasFile := func(i int) bool { return i%3 == 1 || i%7 == 0 }
	done := make(chan struct{})
	defer func() { a.Close(); <-done }() // join the writer before f is closed
	go func() {
		defer close(done)
		for i := 0; i < n; i++ {
			msg := []byte(`{"i":` + itoa(i) + `}`)
			var files []*os.File
			if hasFile(i) {
				files = []*os.File{f}
			}
			if err := w.WriteMessage(msg, files); err != nil {
				return
			}
		}
	}()
	r := New(b, 1<<20, 4)
	for i := 0; i < n; i++ {
		msg, files, err := r.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		want := 0
		if hasFile(i) {
			want = 1
		}
		if len(files) != want || string(msg) != `{"i":`+itoa(i)+`}` {
			t.Fatalf("message %d: %s with %d files, want %d", i, msg, len(files), want)
		}
		CloseFiles(files)
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
