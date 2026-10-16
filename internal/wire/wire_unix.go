//go:build unix

// Package wire frames NUL-terminated Varlink messages on a Unix stream
// socket and carries file descriptors with SCM_RIGHTS. It is shared by the
// varlink client and the varlinktest fake server.
package wire

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"

	"golang.org/x/sys/unix"
)

// Supported reports whether this platform can carry Varlink connections.
const Supported = true

// Transport frames NUL-terminated messages on a Unix stream socket and
// carries file descriptors with SCM_RIGHTS. ReadMessage and WriteMessage
// may run concurrently with each other, and Close with either.
//
// Which message do received descriptors belong to? Senders attach them to
// a sendmsg that starts with the message referencing them: this package
// sends one message per sendmsg, and systemd's sd-varlink holds back
// further output until a message with descriptors is flushed. Kernels
// differ in where a recvmsg that returns descriptors starts and stops:
//
//   - Linux ends the read right after the chunk that carried descriptors,
//     though it may put earlier, descriptor-less data in front of it. The
//     descriptors belong to the last message that begins in the read.
//   - BSD and macOS start the read at the chunk that carried descriptors
//     and may continue past it. The descriptors belong to the message at
//     the start of the read.
//
// systemd v257 and v258 can also append a descriptor-carrying message to
// unsent earlier replies in one sendmsg. On Linux the rule above still attributes
// those descriptors correctly unless the earlier data exceeds one kernel
// socket buffer chunk (tens of KiB); newer systemd never does this. No
// systemd method that returns descriptors streams replies, so in practice
// the case does not arise.
type Transport struct {
	conn     *net.UnixConn
	maxFrame int
	maxFiles int

	// Owned by the reader.
	buf      []byte // unparsed bytes
	bufStart int64  // stream offset of buf[0]
	readOff  int64  // stream offset of the next byte to read
	rbuf     []byte
	oob      []byte

	mu      sync.Mutex // guards pending and closed
	pending []fdBatch
	closed  bool
}

type fdBatch struct {
	off   int64
	files []*os.File
}

// New wraps c. maxFrame bounds a message in bytes; maxFiles bounds the
// descriptors accepted with one read or sent with one message.
func New(c *net.UnixConn, maxFrame, maxFiles int) *Transport {
	return &Transport{
		conn:     c,
		maxFrame: maxFrame,
		maxFiles: maxFiles,
		rbuf:     make([]byte, 64*1024),
		oob:      make([]byte, unix.CmsgSpace(maxFiles*4)),
	}
}

// ReadMessage returns the next message without its NUL terminator, plus the
// files sent with it. The returned slice is valid until the next call.
func (t *Transport) ReadMessage() ([]byte, []*os.File, error) {
	scanned := 0
	for {
		if i := bytes.IndexByte(t.buf[scanned:], 0); i >= 0 {
			end := scanned + i
			msg := t.buf[:end]
			last := t.bufStart + int64(end)
			var files []*os.File
			t.mu.Lock()
			n := 0
			for _, b := range t.pending {
				if b.off <= last {
					files = append(files, b.files...)
					n++
				}
			}
			t.pending = t.pending[n:]
			t.mu.Unlock()
			// Copy so the caller's slice survives buffer compaction.
			out := append([]byte(nil), msg...)
			t.buf = t.buf[end+1:]
			t.bufStart = last + 1
			return out, files, nil
		}
		scanned = len(t.buf)
		if len(t.buf) > t.maxFrame {
			return nil, nil, ErrFrameTooLarge
		}

		n, oobn, flags, _, err := t.conn.ReadMsgUnix(t.rbuf, t.oob)
		if oobn > 0 {
			files, ferr := parseRights(t.oob[:oobn])
			if flags&unix.MSG_CTRUNC != 0 {
				CloseFiles(files)
				return nil, nil, fmt.Errorf("%w: ancillary data truncated (limit %d)", ErrTooManyFiles, t.maxFiles)
			}
			if ferr != nil {
				CloseFiles(files)
				return nil, nil, ferr
			}
			if len(files) > 0 {
				t.mu.Lock()
				if t.closed {
					// Close already ran: nobody will claim these.
					t.mu.Unlock()
					CloseFiles(files)
					return nil, nil, net.ErrClosed
				}
				t.pending = append(t.pending, fdBatch{off: t.attachOffset(t.rbuf[:n]), files: files})
				t.mu.Unlock()
			}
		}
		if n > 0 {
			t.buf = append(t.buf, t.rbuf[:n]...)
			t.readOff += int64(n)
		}
		if err != nil {
			if n > 0 && !errors.Is(err, io.EOF) {
				continue
			}
			return nil, nil, err
		}
		if n == 0 {
			return nil, nil, io.EOF
		}
	}
}

// attachOffset returns the stream offset of the message that owns
// descriptors received with data, which was just read at offset t.readOff.
// See the Transport comment for the rules.
func (t *Transport) attachOffset(data []byte) int64 {
	if !lastStartRule {
		// BSD and macOS: a read that returns descriptors starts at the
		// record that carried them, and may run on past it.
		return t.readOff
	}
	// Linux: the read ends right after the chunk that carried descriptors,
	// so they belong to the last message that begins in it.
	if len(data) > 1 {
		if i := bytes.LastIndexByte(data[:len(data)-1], 0); i >= 0 {
			return t.readOff + int64(i) + 1
		}
	}
	return t.readOff
}

// WriteMessage writes msg plus a NUL terminator, attaching files to the
// first byte so the receiver associates them with this message.
func (t *Transport) WriteMessage(msg []byte, files []*os.File) error {
	buf := make([]byte, len(msg)+1)
	copy(buf, msg)
	if len(files) == 0 {
		_, err := t.conn.Write(buf)
		return err
	}
	if len(files) > t.maxFiles {
		return fmt.Errorf("%w: %d files exceeds limit %d", ErrTooManyFiles, len(files), t.maxFiles)
	}
	// Send duplicates rather than calling f.Fd(), which would switch the
	// caller's files to blocking mode.
	fds := make([]int, 0, len(files))
	defer func() {
		for _, fd := range fds {
			unix.Close(fd)
		}
	}()
	for _, f := range files {
		fd, err := dupFD(f)
		if err != nil {
			return err
		}
		fds = append(fds, fd)
	}
	n, _, err := t.conn.WriteMsgUnix(buf, unix.UnixRights(fds...), nil)
	if err != nil {
		return err
	}
	if n < len(buf) {
		_, err = t.conn.Write(buf[n:])
	}
	return err
}

// MaxFiles is the most descriptors WriteMessage sends with one message.
func (t *Transport) MaxFiles() int { return t.maxFiles }

// Close closes the socket and any received descriptors not yet handed out.
// It is safe to call more than once and concurrently with ReadMessage.
func (t *Transport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	pending := t.pending
	t.pending = nil
	t.mu.Unlock()
	for _, b := range pending {
		CloseFiles(b.files)
	}
	return t.conn.Close()
}

func parseRights(oob []byte) ([]*os.File, error) {
	msgs, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return nil, fmt.Errorf("varlink: bad control message: %w", err)
	}
	var files []*os.File
	for _, m := range msgs {
		if m.Header.Level != unix.SOL_SOCKET || m.Header.Type != unix.SCM_RIGHTS {
			continue
		}
		fds, err := unix.ParseUnixRights(&m)
		if err != nil {
			return files, fmt.Errorf("varlink: bad SCM_RIGHTS: %w", err)
		}
		for _, fd := range fds {
			// Received descriptors must not leak into child processes.
			unix.CloseOnExec(fd)
			files = append(files, os.NewFile(uintptr(fd), fmt.Sprintf("varlink-fd-%d", fd)))
		}
	}
	return files, nil
}

// CloseFiles closes every non-nil file.
func CloseFiles(files []*os.File) {
	for _, f := range files {
		if f != nil {
			f.Close()
		}
	}
}

// DupFile duplicates f with close-on-exec set.
func DupFile(f *os.File) (*os.File, error) {
	fd, err := dupFD(f)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), f.Name()), nil
}

// dupFD duplicates f's descriptor with close-on-exec set, without changing
// f's blocking mode.
func dupFD(f *os.File) (int, error) {
	rc, err := f.SyscallConn()
	if err != nil {
		return -1, err
	}
	fd := -1
	var derr error
	if err := rc.Control(func(raw uintptr) {
		fd, derr = unix.FcntlInt(raw, unix.F_DUPFD_CLOEXEC, 0)
	}); err != nil {
		return -1, err
	}
	return fd, derr
}
