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
	"unsafe"

	"golang.org/x/sys/unix"
)

func tempFile(t testing.TB, content string) *os.File {
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

func TestReadMessageBoundaries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		maxFrame int
		want     []string // messages read before the final error
		wantErr  error
	}{
		{name: "empty message", input: "\x00", maxFrame: 8, want: []string{""}, wantErr: io.EOF},
		{name: "exactly max frame", input: "12345678\x00", maxFrame: 8, want: []string{"12345678"}, wantErr: io.EOF},
		{name: "one over max frame", input: "123456789\x00", maxFrame: 8, wantErr: ErrFrameTooLarge},
		{name: "second message too large", input: "ok\x00123456789\x00", maxFrame: 8, want: []string{"ok"}, wantErr: ErrFrameTooLarge},
		{name: "no input", input: "", maxFrame: 8, wantErr: io.EOF},
		{name: "truncated frame", input: "{\"a\":", maxFrame: 64, wantErr: io.EOF},
		{name: "complete then truncated", input: "{}\x00{\"b\"", maxFrame: 64, want: []string{"{}"}, wantErr: io.EOF},
		{name: "unterminated oversized", input: "123456789", maxFrame: 8, wantErr: ErrFrameTooLarge},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a, b := pair(t)
			done := make(chan struct{})
			go func() {
				defer close(done)
				a.Write([]byte(tc.input))
				a.Close()
			}()
			r := New(b, tc.maxFrame, 4)
			for i, want := range tc.want {
				got, files, err := r.ReadMessage()
				if err != nil {
					t.Fatalf("message %d: %v", i, err)
				}
				if string(got) != want || len(files) != 0 {
					t.Fatalf("message %d = %q (%d files), want %q", i, got, len(files), want)
				}
			}
			got, files, err := r.ReadMessage()
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if got != nil || files != nil {
				t.Fatalf("got %q and %v alongside error", got, files)
			}
			<-done
		})
	}
}

func TestMaxFiles(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		max  int
	}{
		{name: "zero", max: 0},
		{name: "one", max: 1},
		{name: "many", max: 16},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a, _ := pair(t)
			if got := New(a, 64, tc.max).MaxFiles(); got != tc.max {
				t.Fatalf("MaxFiles = %d, want %d", got, tc.max)
			}
		})
	}
}

func TestWriteMessageErrors(t *testing.T) {
	t.Parallel()
	closedFile := func(t *testing.T) *os.File {
		f := tempFile(t, "")
		f.Close()
		return f
	}
	tests := []struct {
		name    string
		maxFile int
		closeTr bool
		files   func(t *testing.T) []*os.File
		wantErr error // nil means any error
	}{
		{name: "closed socket without files", maxFile: 4, closeTr: true, wantErr: net.ErrClosed},
		{name: "closed socket with files", maxFile: 4, closeTr: true, files: func(t *testing.T) []*os.File { return []*os.File{tempFile(t, "")} }, wantErr: net.ErrClosed},
		{name: "closed file", maxFile: 4, files: func(t *testing.T) []*os.File { return []*os.File{closedFile(t)} }, wantErr: nil},
		{name: "closed file after good one", maxFile: 4, files: func(t *testing.T) []*os.File { return []*os.File{tempFile(t, ""), closedFile(t)} }, wantErr: nil},
		{name: "too many files", maxFile: 1, files: func(t *testing.T) []*os.File { return []*os.File{tempFile(t, ""), tempFile(t, "")} }, wantErr: ErrTooManyFiles},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a, _ := pair(t)
			w := New(a, 1<<20, tc.maxFile)
			if tc.closeTr {
				w.Close()
			}
			var files []*os.File
			if tc.files != nil {
				files = tc.files(t)
			}
			err := w.WriteMessage([]byte(`{}`), files)
			if err == nil || (tc.wantErr != nil && !errors.Is(err, tc.wantErr)) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestWriteMessageLargeWithFile(t *testing.T) {
	t.Parallel()
	// The message is larger than the socket buffer, so the kernel accepts
	// only part of it with the descriptor and WriteMessage sends the rest.
	a, b := pair(t)
	msg := bytes.Repeat([]byte("y"), 8<<20)
	f := tempFile(t, "")
	werr := make(chan error, 1)
	go func() { werr <- New(a, 1<<20, 4).WriteMessage(msg, []*os.File{f}) }()
	r := New(b, 16<<20, 4)
	got, files, err := r.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, msg) {
		t.Fatalf("message differs: got %d bytes, want %d", len(got), len(msg))
	}
	if len(files) != 1 {
		t.Fatalf("got %d files, want 1", len(files))
	}
	CloseFiles(files)
	if err := <-werr; err != nil {
		t.Fatal(err)
	}
}

func TestWriteMessageKeepsCallerFile(t *testing.T) {
	t.Parallel()
	a, b := pair(t)
	f := tempFile(t, "hello")
	if err := New(a, 64, 4).WriteMessage([]byte(`{}`), []*os.File{f}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Stat(); err != nil {
		t.Fatalf("sender's file unusable after write: %v", err)
	}
	_, files, err := New(b, 64, 4).ReadMessage()
	if err != nil || len(files) != 1 {
		t.Fatalf("got %d files, err %v", len(files), err)
	}
	defer CloseFiles(files)
	data, err := io.ReadAll(io.NewSectionReader(files[0], 0, 5))
	if err != nil || string(data) != "hello" {
		t.Fatalf("received file reads %q, %v", data, err)
	}
}

func TestCloseIdempotent(t *testing.T) {
	t.Parallel()
	a, b := pair(t)
	tr := New(a, 64, 4)
	for i := range 3 {
		if err := tr.Close(); err != nil {
			t.Fatalf("Close #%d: %v", i+1, err)
		}
	}
	if _, _, err := tr.ReadMessage(); err == nil {
		t.Fatal("ReadMessage succeeded after Close")
	}
	if _, err := b.Write([]byte("x")); err == nil {
		// The peer's first write may still be buffered; the next must fail.
		if _, err := b.Write([]byte("y")); err == nil {
			t.Fatal("peer can still write to a closed transport")
		}
	}
}

func TestCloseClosesPendingFiles(t *testing.T) {
	t.Parallel()
	a, _ := pair(t)
	tr := New(a, 64, 4)
	pending := tempFile(t, "")
	other := tempFile(t, "")
	tr.mu.Lock()
	tr.pending = []fdBatch{{off: 10, files: []*os.File{pending}}, {off: 20, files: []*os.File{other}}}
	tr.mu.Unlock()
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
	for name, f := range map[string]*os.File{"first": pending, "second": other} {
		if _, err := f.Stat(); !errors.Is(err, os.ErrClosed) {
			t.Errorf("%s pending file: Stat err = %v, want ErrClosed", name, err)
		}
	}
	if len(tr.pending) != 0 {
		t.Errorf("pending not cleared: %d batches", len(tr.pending))
	}
}

func TestReadAfterCloseDropsFiles(t *testing.T) {
	t.Parallel()
	// Simulate Close winning the race against a read that returned
	// descriptors: they must be closed and the read must fail.
	a, b := pair(t)
	tr := New(b, 64, 4)
	tr.mu.Lock()
	tr.closed = true
	tr.mu.Unlock()
	if err := New(a, 64, 4).WriteMessage([]byte(`{}`), []*os.File{tempFile(t, "")}); err != nil {
		t.Fatal(err)
	}
	msg, files, err := tr.ReadMessage()
	if !errors.Is(err, net.ErrClosed) {
		t.Fatalf("err = %v, want net.ErrClosed", err)
	}
	if msg != nil || files != nil {
		t.Fatalf("got %q and %v alongside error", msg, files)
	}
	if len(tr.pending) != 0 {
		t.Fatalf("pending = %d, want 0", len(tr.pending))
	}
}

func TestDupFile(t *testing.T) {
	t.Parallel()
	t.Run("duplicates with cloexec", func(t *testing.T) {
		t.Parallel()
		f := tempFile(t, "data")
		d, err := DupFile(f)
		if err != nil {
			t.Fatal(err)
		}
		defer d.Close()
		if d.Name() != f.Name() {
			t.Errorf("name = %q, want %q", d.Name(), f.Name())
		}
		if d.Fd() == f.Fd() {
			t.Error("duplicate shares the original descriptor number")
		}
		flags, err := unix.FcntlInt(d.Fd(), unix.F_GETFD, 0)
		if err != nil || flags&unix.FD_CLOEXEC == 0 {
			t.Errorf("duplicate lacks FD_CLOEXEC (flags %d, err %v)", flags, err)
		}
		d.Close()
		if _, err := f.Stat(); err != nil {
			t.Errorf("closing the duplicate broke the original: %v", err)
		}
	})
	t.Run("closed file", func(t *testing.T) {
		t.Parallel()
		f := tempFile(t, "")
		f.Close()
		d, err := DupFile(f)
		if err == nil || d != nil {
			t.Fatalf("got %v, %v; want nil and an error", d, err)
		}
	})
}

func TestCloseFiles(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		files []*os.File
	}{
		{name: "nil slice"},
		{name: "only nil entries", files: []*os.File{nil, nil}},
		{name: "mixed", files: []*os.File{nil, tempFile(t, ""), nil, tempFile(t, "")}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			CloseFiles(tc.files)
			for i, f := range tc.files {
				if f == nil {
					continue
				}
				if _, err := f.Stat(); !errors.Is(err, os.ErrClosed) {
					t.Errorf("file %d: Stat err = %v, want ErrClosed", i, err)
				}
			}
		})
	}
}

// setLen rewrites the length of the control message at the start of b.
func setLen(b []byte, n int) []byte {
	(*unix.Cmsghdr)(unsafe.Pointer(&b[0])).SetLen(n)
	return b
}

func TestParseRights(t *testing.T) {
	t.Parallel()
	rights := func() []byte {
		p := make([]int, 2)
		if err := syscall.Pipe(p); err != nil {
			t.Fatal(err)
		}
		// parseRights takes ownership of the descriptors it is given, so
		// hand it duplicates and close the originals right away.
		defer unix.Close(p[0])
		defer unix.Close(p[1])
		d0, err0 := unix.FcntlInt(uintptr(p[0]), unix.F_DUPFD_CLOEXEC, 0)
		d1, err1 := unix.FcntlInt(uintptr(p[1]), unix.F_DUPFD_CLOEXEC, 0)
		if err0 != nil || err1 != nil {
			t.Fatal(err0, err1)
		}
		return unix.UnixRights(d0, d1)
	}
	// A control message of another type, as the kernel could deliver.
	// SCM_RIGHTS+1 is SCM_CREDENTIALS on Linux and SCM_TIMESTAMP on macOS
	// and the BSDs; either way it is not SCM_RIGHTS.
	other := make([]byte, unix.CmsgSpace(4))
	h := (*unix.Cmsghdr)(unsafe.Pointer(&other[0]))
	h.Level, h.Type = unix.SOL_SOCKET, unix.SCM_RIGHTS+1
	h.SetLen(unix.CmsgLen(4))

	tests := []struct {
		name      string
		oob       func() []byte
		wantFiles int
		wantErr   bool
	}{
		{name: "empty", oob: func() []byte { return nil }},
		{name: "two descriptors", oob: rights, wantFiles: 2},
		{name: "other message type ignored", oob: func() []byte { return other }},
		{name: "ignored then rights", oob: func() []byte { return append(append([]byte(nil), other...), rights()...) }, wantFiles: 2},
		{name: "length beyond buffer", oob: func() []byte {
			b := unix.UnixRights(0)
			return setLen(b, len(b)+64)
		}, wantErr: true},
		{name: "trailing bytes shorter than a header", oob: func() []byte { return []byte{1, 2, 3} }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Not parallel: descriptors are created and closed per case and
			// the numbers of other cases must not be reused mid-test.
			files, err := parseRights(tc.oob())
			defer CloseFiles(files)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if len(files) != tc.wantFiles {
				t.Fatalf("got %d files, want %d", len(files), tc.wantFiles)
			}
			for _, f := range files {
				flags, _ := unix.FcntlInt(f.Fd(), unix.F_GETFD, 0)
				if flags&unix.FD_CLOEXEC == 0 {
					t.Error("file lacks FD_CLOEXEC")
				}
			}
		})
	}
}

func TestAttachOffset(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		readOff int64
		data    string
		linux   int64
		bsd     int64
	}{
		{name: "no terminator", readOff: 100, data: "abc", linux: 100, bsd: 100},
		{name: "single byte", readOff: 100, data: "\x00", linux: 100, bsd: 100},
		{name: "terminator only at end", readOff: 100, data: "abc\x00", linux: 100, bsd: 100},
		{name: "second message begins in read", readOff: 100, data: "ab\x00cd", linux: 103, bsd: 100},
		{name: "last of several", readOff: 0, data: "a\x00b\x00cd\x00e", linux: 7, bsd: 0},
		{name: "ends with two terminators", readOff: 10, data: "a\x00\x00", linux: 12, bsd: 10},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tr := &Transport{readOff: tc.readOff}
			want := tc.bsd
			if lastStartRule {
				want = tc.linux
			}
			if got := tr.attachOffset([]byte(tc.data)); got != want {
				t.Fatalf("attachOffset = %d, want %d", got, want)
			}
		})
	}
}
