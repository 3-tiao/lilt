package server

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/fakeengine"
)

// lockAwareEngine records whether the recent sampler calls the engine while
// holding the command lock. The helper executes requests serially, so a state
// poll that races an in-flight play tears the transport down; the sampler must
// therefore sample under s.mu like every command does.
type lockAwareEngine struct {
	*fakeengine.FakeEngine
	server atomic.Pointer[Server]
	held   atomic.Bool
	calls  atomic.Int32
}

func (e *lockAwareEngine) State(ctx context.Context) (core.PlaybackState, error) {
	e.calls.Add(1)
	if server := e.server.Load(); server != nil {
		if !server.mu.TryLock() {
			e.held.Store(true)
		} else {
			server.mu.Unlock()
		}
	}
	return e.FakeEngine.State(ctx)
}

func TestRecentSamplerSamplesUnderCommandLock(t *testing.T) {
	engine := &lockAwareEngine{FakeEngine: fakeengine.NewFakeEngine()}
	server, _ := startTestServerWithEngine(t, engine)
	engine.server.Store(server)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if engine.held.Load() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("sampler called engine.State without the command lock (calls=%d)", engine.calls.Load())
}
