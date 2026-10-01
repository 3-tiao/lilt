package server

import (
	"sync"
	"time"
)

// admissionGate serializes public side-effecting commands in admission order.
//
// The server already serializes mutations on Server.mu, but a mutex does not
// define an order: two clients that become runnable together race for the lock.
// The gate is the explicit FIFO linearization point the design requires: after
// a request is admitted here, its relative order with other admitted requests
// is fixed (docs/internals/concurrency.md §5.1, §5.3).
//
// Waiting is bounded. A request that cannot be admitted inside its budget never
// executes and is reported as server_busy, which is a statement the server can
// actually keep ("certainly did not run"), unlike a client-side timeout.
type admissionGate struct {
	mu      sync.Mutex
	held    bool
	waiters []chan struct{}
}

func newAdmissionGate() *admissionGate { return &admissionGate{} }

// acquire takes the slot, waiting at most wait for it. It returns false when
// the budget or ctx expired first, in which case the caller must not execute
// and must not call release.
func (g *admissionGate) acquire(wait time.Duration, cancel <-chan struct{}) bool {
	if g == nil {
		// A server built without a gate (hand-built test servers) has no
		// admission queue; Server.mu still serializes its handlers.
		return true
	}
	g.mu.Lock()
	if !g.held {
		g.held = true
		g.mu.Unlock()
		return true
	}
	ready := make(chan struct{}, 1)
	g.waiters = append(g.waiters, ready)
	g.mu.Unlock()

	if wait <= 0 {
		return g.cancelWait(ready)
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ready:
		return true
	case <-timer.C:
		return g.cancelWait(ready)
	case <-cancel:
		return g.cancelWait(ready)
	}
}

// cancelWait removes a waiter that gave up. The slot is handed off to the next
// waiter on release, so a waiter that was handed the slot at the same moment
// passes it on instead of losing it.
func (g *admissionGate) cancelWait(ready chan struct{}) bool {
	g.mu.Lock()
	for index, waiter := range g.waiters {
		if waiter == ready {
			g.waiters = append(g.waiters[:index], g.waiters[index+1:]...)
			g.mu.Unlock()
			return false
		}
	}
	g.mu.Unlock()
	// Already handed off: take it and release so the next waiter proceeds. The
	// request itself never executed, so server_busy remains true.
	// Either the handoff happened (the token is in the buffer) or it never will.
	g.release()
	return false
}

// release gives the slot to the longest-waiting admitted request, or frees it.
func (g *admissionGate) release() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.waiters) == 0 {
		g.held = false
		return
	}
	next := g.waiters[0]
	g.waiters = g.waiters[1:]
	// Hand the slot off while still holding the lock. ready is buffered, so the
	// send never blocks, and pop+send stays atomic with respect to a waiter that
	// is giving up at the same moment: cancelWait only finds a waiter it can
	// still remove, otherwise the token is already in its buffer and it drains
	// it before releasing.
	next <- struct{}{}
}

// pendingWaiters reports how many requests are waiting for the slot. Tests use
// it to observe queueing without sleeping.
func (g *admissionGate) pendingWaiters() int {
	if g == nil {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.waiters)
}
