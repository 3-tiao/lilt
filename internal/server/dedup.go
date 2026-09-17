package server

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/caiguo/lilt/internal/api"
)

// dedupCache implements request idempotency for the Client API. A repeated
// requestId with the same command/params fingerprint reuses the original
// result; a different fingerprint is invalid_request. Completed bodies are
// bounded, but a tombstone is retained so an evicted result yields
// duplicate_result_unavailable instead of re-executing.
type dedupCache struct {
	mu        sync.Mutex
	entries   map[string]*dedupEntry
	completed []string // completion order for body eviction
	maxBodies int
	tombstone time.Duration
}

type dedupEntry struct {
	fingerprint string
	done        chan struct{}
	result      api.Response
	completedAt time.Time
	evicted     bool
}

func newDedupCache(maxBodies int, tombstone time.Duration) *dedupCache {
	if maxBodies <= 0 {
		maxBodies = 4096
	}
	if tombstone <= 0 {
		tombstone = 10 * time.Minute
	}
	return &dedupCache{
		entries:   make(map[string]*dedupEntry),
		maxBodies: maxBodies,
		tombstone: tombstone,
	}
}

// begin registers requestID or reports an existing entry. When isNew is false
// the caller must wait on entry.done and then reuse the stored result.
func (c *dedupCache) begin(requestID, fingerprint string) (entry *dedupEntry, isNew bool, err *api.Error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneLocked()
	if existing, ok := c.entries[requestID]; ok {
		if existing.fingerprint != fingerprint {
			return nil, false, api.Errorf(api.CodeInvalidRequest, "requestId %q was used with different params", requestID)
		}
		return existing, false, nil
	}
	entry = &dedupEntry{fingerprint: fingerprint, done: make(chan struct{})}
	c.entries[requestID] = entry
	return entry, true, nil
}

// finish stores the result and releases waiters.
func (c *dedupCache) finish(requestID string, result api.Response) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[requestID]
	if !ok {
		return
	}
	entry.result = result
	entry.completedAt = time.Now()
	close(entry.done)
	c.completed = append(c.completed, requestID)
	for len(c.completed) > c.maxBodies {
		oldest := c.completed[0]
		c.completed = c.completed[1:]
		if victim, ok := c.entries[oldest]; ok {
			victim.evicted = true
			victim.result = api.Response{}
		}
	}
}

// result returns the reusable outcome for a completed entry.
func (c *dedupCache) result(entry *dedupEntry) api.Response {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry.evicted {
		return api.Failure("", "", api.Errorf(api.CodeDuplicateResultUnavailable,
			"the original result for this requestId was evicted; read state and decide whether to retry with a new requestId"))
	}
	return entry.result
}

func (c *dedupCache) pruneLocked() {
	now := time.Now()
	removed := false
	for id, entry := range c.entries {
		select {
		case <-entry.done:
			if now.Sub(entry.completedAt) > c.tombstone {
				delete(c.entries, id)
				removed = true
			}
		default:
		}
	}
	if removed {
		kept := c.completed[:0]
		for _, id := range c.completed {
			if _, ok := c.entries[id]; ok {
				kept = append(kept, id)
			}
		}
		c.completed = kept
	}
}

// fingerprint canonicalizes {command, params} so key order and omitted defaults
// cannot produce different fingerprints. It is computed AFTER params validation
// in the dispatcher.
func fingerprint(command string, params json.RawMessage) string {
	canonical := map[string]any{"command": command}
	if len(params) > 0 {
		var decoded any
		if err := json.Unmarshal(params, &decoded); err != nil {
			canonical["params"] = string(params)
		} else {
			canonical["params"] = decoded
		}
	}
	// encoding/json sorts map keys, giving a stable encoding.
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return command
	}
	return string(encoded)
}

// normalizeParams unmarshals params into a generic value and re-encodes it so
// equivalent documents share a fingerprint. Object key order is normalized by
// encoding/json.
func normalizeParams(params json.RawMessage) json.RawMessage {
	if len(params) == 0 {
		return nil
	}
	var decoded any
	if err := json.Unmarshal(params, &decoded); err != nil {
		return params
	}
	encoded, err := json.Marshal(decoded)
	if err != nil {
		return params
	}
	return encoded
}
