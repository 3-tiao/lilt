package fakeengine

import (
	"context"
	"testing"

	"github.com/caiguo/lilt/core"
)

// Removing the current entry advances to the next one and clamps the index, the
// same cursor rules the helper applies: without them a remove left queueIndex
// out of range ("2/1") after deleting the current row (batch 2026-09-23-polish,
// rp3 replay).
func TestQueueRemoveKeepsIndexInRange(t *testing.T) {
	engine := NewFakeEngine()
	state, err := engine.PlaySongs(context.Background(), core.PlaySongsRequest{IDs: []string{"a", "b"}})
	if err != nil {
		t.Fatalf("PlaySongs: %v", err)
	}
	if state.QueueIndex != 0 {
		t.Fatalf("start index = %d", state.QueueIndex)
	}
	if _, err := engine.QueueJump(context.Background(), 1); err != nil {
		t.Fatalf("QueueJump: %v", err)
	}
	// Remove the current (last) entry: the index must land on the remaining row.
	state, err = engine.QueueRemove(context.Background(), 1)
	if err != nil {
		t.Fatalf("QueueRemove: %v", err)
	}
	if len(state.Queue) != 1 || state.QueueIndex != 0 {
		t.Fatalf("after removing the current row: queue=%d index=%d, want 1/0", len(state.Queue), state.QueueIndex)
	}

	// Remove an entry before the cursor: the cursor shifts down with it.
	if _, err := engine.PlaySongs(context.Background(), core.PlaySongsRequest{IDs: []string{"a", "b", "c"}}); err != nil {
		t.Fatalf("PlaySongs: %v", err)
	}
	if _, err := engine.QueueJump(context.Background(), 2); err != nil {
		t.Fatalf("QueueJump: %v", err)
	}
	state, err = engine.QueueRemove(context.Background(), 0)
	if err != nil {
		t.Fatalf("QueueRemove: %v", err)
	}
	if len(state.Queue) != 2 || state.QueueIndex != 1 {
		t.Fatalf("after removing a row before the cursor: queue=%d index=%d, want 2/1", len(state.Queue), state.QueueIndex)
	}
}
