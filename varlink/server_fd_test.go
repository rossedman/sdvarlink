//go:build linux

package varlink_test

import (
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rossedman/sdvarlink/varlink"
)

const fdIDL = `interface com.example.Files

# Returns a new descriptor for the same file.
method Reopen(fileDescriptor: int) -> (fileDescriptor: int, size: int)
`

type fileIn struct {
	File *os.File `json:"-" varlink:"fileDescriptor,fd"`
}

type fileOut struct {
	File *os.File `json:"-" varlink:"fileDescriptor,fd"`
	Size int64    `json:"size"`
}

func TestServerFileDescriptors(t *testing.T) {
	srv := varlink.NewServer(varlink.ServerInfo{})
	iface := srv.Interface("com.example.Files", fdIDL)
	varlink.Handle(iface, "Reopen", func(ctx context.Context, call *varlink.Call, in fileIn) (fileOut, error) {
		defer in.File.Close() // received files belong to the handler
		fi, err := in.File.Stat()
		if err != nil {
			return fileOut{}, err
		}
		f, err := os.Open("/proc/self/fd/" + strconv.Itoa(int(in.File.Fd())))
		if err != nil {
			return fileOut{}, err
		}
		return fileOut{File: f, Size: fi.Size()}, nil // the server closes f after sending
	})
	path := sockPath(t)
	l, err := varlink.Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx, l)

	src, _ := os.CreateTemp(t.TempDir(), "src")
	src.WriteString("hello fds")
	defer src.Close()

	c, err := varlink.Dial(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	before := countFDs(t)
	for i := 0; i < 500; i++ {
		var out fileOut
		if err := c.Call(ctx, "com.example.Files.Reopen", fileIn{File: src}, &out); err != nil {
			t.Fatal(err)
		}
		if out.File == nil || out.Size != 9 {
			t.Fatalf("out = %+v", out)
		}
		if i == 0 {
			out.File.Seek(0, io.SeekStart)
			b, _ := io.ReadAll(out.File)
			if string(b) != "hello fds" {
				t.Fatalf("read %q", b)
			}
		}
		out.File.Close()
	}
	// Server and client share this process: neither side may leak.
	time.Sleep(50 * time.Millisecond)
	if after := countFDs(t); after > before+4 {
		t.Fatalf("open fds grew from %d to %d", before, after)
	}
}

func countFDs(t *testing.T) int {
	ents, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skip("no /proc")
	}
	return len(ents)
}

// TestListenActivated runs this test binary under systemd-socket-activate,
// which passes a listening socket the way a .socket unit does.
func TestListenActivated(t *testing.T) {
	if os.Getenv("SDVARLINK_ACTIVATED_CHILD") == "1" {
		activatedChild()
		return
	}
	activate, err := exec.LookPath("systemd-socket-activate")
	if err != nil {
		t.Skip("systemd-socket-activate not installed")
	}
	path := sockPath(t)
	cmd := exec.Command(activate, "--listen="+path, "--fdname=api",
		"-E", "SDVARLINK_ACTIVATED_CHILD=1", os.Args[0], "-test.run=^TestListenActivated$")
	out := &strings.Builder{}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()

	// systemd-socket-activate starts the child on the first connection.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("socket never appeared\n%s", out)
		}
		time.Sleep(20 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := varlink.Dial(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	info, err := varlink.GetInfo(ctx, c)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if info.Product != "activated" {
		t.Fatalf("info = %+v", info)
	}
}

func activatedChild() {
	l, err := varlink.ListenActivated("api")
	if err != nil {
		os.Stderr.WriteString("child: " + err.Error() + "\n")
		os.Exit(1)
	}
	srv := varlink.NewServer(varlink.ServerInfo{Product: "activated"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Serve(ctx, l)
	os.Exit(0)
}

var _ net.Listener = (*net.UnixListener)(nil)
