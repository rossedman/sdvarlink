// Command queued serves a small job queue over Varlink with sdvarlink's
// server: a typed call (Depth), a mutating call with a peer-credentials
// check (Enqueue), a stream (Watch), a typed error (QueueFull),
// introspection, socket activation and clean shutdown.
//
//	go run ./examples/queued -socket /tmp/queued.sock
//	varlinkctl call /tmp/queued.sock io.example.Queue.Enqueue '{"command":"make test"}'
//	varlinkctl call --more /tmp/queued.sock io.example.Queue.Watch '{"replay":true}'
//	varlinkctl introspect /tmp/queued.sock io.example.Queue
package main

import (
	"context"
	_ "embed"
	"errors"
	"flag"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/rossedman/sdvarlink/varlink"
)

//go:embed io.example.Queue.varlink
var queueIDL string

type Job struct {
	ID       int64  `json:"id"`
	Command  string `json:"command"`
	Priority string `json:"priority"`
}

type DepthOut struct {
	Jobs int64 `json:"jobs"`
}

type EnqueueIn struct {
	Command  string  `json:"command"`
	Priority *string `json:"priority,omitempty"`
}

type EnqueueOut struct {
	Job Job `json:"job"`
}

type WatchIn struct {
	Replay *bool `json:"replay,omitempty"`
}

type WatchOut struct {
	Job Job `json:"job"`
}

// QueueFullError is the io.example.Queue.QueueFull error.
type QueueFullError struct {
	Capacity int64 `json:"capacity"`
}

// Error implements error.
func (e *QueueFullError) Error() string { return "queue full" }

// VarlinkError returns the Varlink error name sent to the client.
func (e *QueueFullError) VarlinkError() string { return "io.example.Queue.QueueFull" }

// queue is the service's state: jobs plus subscribers for Watch.
type queue struct {
	mu       sync.Mutex
	jobs     []Job
	next     int64
	capacity int
	subs     map[chan Job]struct{}
}

// add queues a job and sends it to every subscriber, or returns a
// *QueueFullError when the queue is at capacity.
func (q *queue) add(command, priority string) (Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.jobs) >= q.capacity {
		return Job{}, &QueueFullError{Capacity: int64(q.capacity)}
	}
	q.next++
	j := Job{ID: q.next, Command: command, Priority: priority}
	q.jobs = append(q.jobs, j)
	for ch := range q.subs {
		select {
		case ch <- j:
		default: // slow watcher: drop rather than block the queue
		}
	}
	return j, nil
}

// subscribe returns a channel of jobs added from now on, a snapshot of the
// jobs already queued, and a function that ends the subscription.
func (q *queue) subscribe() (chan Job, []Job, func()) {
	q.mu.Lock()
	defer q.mu.Unlock()
	ch := make(chan Job, 64)
	q.subs[ch] = struct{}{}
	return ch, append([]Job(nil), q.jobs...), func() {
		q.mu.Lock()
		delete(q.subs, ch)
		q.mu.Unlock()
	}
}

// main runs the example; see the package comment for usage.
func main() {
	socket := flag.String("socket", "/run/queued/io.example.Queue", "socket path when not socket-activated")
	capacity := flag.Int("capacity", 100, "maximum queued jobs")
	flag.Parse()

	q := &queue{capacity: *capacity, subs: map[chan Job]struct{}{}}
	srv := varlink.NewServer(varlink.ServerInfo{
		Vendor: "sdvarlink examples", Product: "queued", Version: "1.0.0",
		URL: "https://github.com/rossedman/sdvarlink/tree/main/examples/queued",
	})
	iface := srv.Interface("io.example.Queue", queueIDL)

	varlink.Handle(iface, "Depth", func(ctx context.Context, call *varlink.Call, _ struct{}) (DepthOut, error) {
		q.mu.Lock()
		defer q.mu.Unlock()
		return DepthOut{Jobs: int64(len(q.jobs))}, nil
	})

	varlink.Handle(iface, "Enqueue", func(ctx context.Context, call *varlink.Call, in EnqueueIn) (EnqueueOut, error) {
		// Authorize on the caller's UID: root or the user running queued.
		peer, err := call.Peer()
		if err != nil {
			return EnqueueOut{}, err
		}
		if peer.UID != 0 && int(peer.UID) != os.Getuid() {
			return EnqueueOut{}, varlink.ErrPermissionDenied
		}
		prio := "normal"
		if in.Priority != nil {
			prio = *in.Priority
		}
		j, err := q.add(in.Command, prio)
		return EnqueueOut{Job: j}, err
	})

	varlink.HandleStream(iface, "Watch", func(ctx context.Context, call *varlink.Call, in WatchIn, send func(WatchOut) error) error {
		ch, existing, unsubscribe := q.subscribe()
		defer unsubscribe()
		if in.Replay != nil && *in.Replay {
			for _, j := range existing {
				if err := send(WatchOut{Job: j}); err != nil {
					return err
				}
			}
		}
		for {
			select {
			case j := <-ch:
				if err := send(WatchOut{Job: j}); err != nil {
					return err
				}
			case <-ctx.Done(): // client gone or server shutting down
				return nil
			}
		}
	})

	// Prefer a socket from systemd socket activation; fall back to binding.
	l, err := varlink.ListenActivated("")
	if errors.Is(err, varlink.ErrNotActivated) {
		l, err = varlink.Listen(*socket)
	}
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Printf("queued: serving io.example.Queue on %s", l.Addr())
	if err := srv.Serve(ctx, l); err != nil {
		log.Fatal(err)
	}
	log.Print("queued: stopped")
}
