package varlinktest

import (
	"runtime"
	"slices"
	"sync"
	"testing"
)

// fakeTB records failures instead of failing the test, so tests can assert
// on what the server reports to its testing.TB. Cleanups are held until
// runCleanups.
type fakeTB struct {
	testing.TB
	mu       sync.Mutex
	errs     []string
	fatals   []string
	cleanups []func()
}

func (f *fakeTB) Helper() {}

func (f *fakeTB) Errorf(format string, args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errs = append(f.errs, sprintf(format, args...))
}

func (f *fakeTB) Fatalf(format string, args ...any) {
	f.mu.Lock()
	f.fatals = append(f.fatals, sprintf(format, args...))
	f.mu.Unlock()
	runtime.Goexit()
}

func (f *fakeTB) Cleanup(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleanups = append(f.cleanups, fn)
}

// runCleanups runs the registered cleanups in last-in first-out order.
func (f *fakeTB) runCleanups() {
	f.mu.Lock()
	fns := slices.Clone(f.cleanups)
	f.cleanups = nil
	f.mu.Unlock()
	for _, fn := range slices.Backward(fns) {
		fn()
	}
}

func (f *fakeTB) errors() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.errs)
}

func (f *fakeTB) fatalMessages() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.fatals)
}

// runFatal runs fn on its own goroutine, so a Fatalf on tb can end it, and
// waits for it to finish.
func runFatal(tb *fakeTB, fn func()) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	<-done
}

// newFake starts a server on a fakeTB and runs the cleanups when t ends.
func newFake(t *testing.T) (*fakeTB, *Server) {
	t.Helper()
	tb := &fakeTB{TB: t}
	t.Cleanup(tb.runCleanups)
	return tb, NewServer(tb)
}
