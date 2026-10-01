package server

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/3-tiao/lilt/internal/api"
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
	// maxEntries bounds the ledger itself. Bodies are bounded separately; this
	// cap bounds pending, completed and tombstoned entries together, because an
	// unbounded map is the memory growth the design forbids. A full ledger
	// rejects new mutations rather than evicting a tombstone that is still
	// protecting an operation (docs/internals/concurrency.md §5.3).
	maxEntries int
	tombstone  time.Duration
}

type dedupEntry struct {
	fingerprint string
	done        chan struct{}
	result      api.Response
	completedAt time.Time
	evicted     bool
}

func newDedupCache(maxBodies, maxEntries int, tombstone time.Duration) *dedupCache {
	if maxBodies <= 0 {
		maxBodies = 4096
	}
	if maxEntries <= 0 {
		maxEntries = 65536
	}
	if tombstone <= 0 {
		tombstone = 10 * time.Minute
	}
	return &dedupCache{
		entries:    make(map[string]*dedupEntry),
		maxBodies:  maxBodies,
		maxEntries: maxEntries,
		tombstone:  tombstone,
	}
}

// begin registers requestID or reports an existing entry. When isNew is false
// the caller must wait on entry.done and then reuse the stored result.
//
// A full ledger rejects the new request with server_busy before registering it:
// the rejected request owns no entry, so nothing here promises to cache its
// result. It must never evict a tombstone to make room, because a tombstone is
// what stops an already-executed request from running twice.
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
	if len(c.entries) >= c.maxEntries {
		return nil, false, api.Errorf(api.CodeServerBusy,
			"the server is tracking too many requests; this request was not executed and its result is not cached")
	}
	entry = &dedupEntry{fingerprint: fingerprint, done: make(chan struct{})}
	c.entries[requestID] = entry
	return entry, true, nil
}

// beginQuery is begin for a pure query. When the ledger is full it reports
// deduped=false so the caller runs the query without caching anything: a query
// has no side effect to protect, and read-only verification must stay available
// even when the mutation ledger is saturated (docs/internals/concurrency.md §5.3).
func (c *dedupCache) beginQuery(requestID, fingerprint string) (entry *dedupEntry, isNew, deduped bool, err *api.Error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneLocked()
	if existing, ok := c.entries[requestID]; ok {
		if existing.fingerprint != fingerprint {
			return nil, false, true, api.Errorf(api.CodeInvalidRequest, "requestId %q was used with different params", requestID)
		}
		return existing, false, true, nil
	}
	if len(c.entries) >= c.maxEntries {
		return nil, false, false, nil
	}
	entry = &dedupEntry{fingerprint: fingerprint, done: make(chan struct{})}
	c.entries[requestID] = entry
	return entry, true, true, nil
}

// abort ends a pending entry that never executed, so a transient rejection
// (server_busy while queueing) is not cached as if it were an outcome. Only a
// request that has not produced a result may be aborted; a completed entry owns
// the dedup guarantee and must stay.
//
// Duplicate requests already coalesced onto this entry MUST be released with
// the same rejection instead of waiting forever on a result that will never
// arrive, and the entry MUST leave the ledger so a later retry of the same
// requestId is admissible again.
func (c *dedupCache) abort(requestID string, response api.Response) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[requestID]
	if !ok {
		return
	}
	select {
	case <-entry.done:
		return
	default:
	}
	entry.result = response
	entry.completedAt = time.Now()
	close(entry.done)
	delete(c.entries, requestID)
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
		return api.Failure("", api.Errorf(api.CodeDuplicateResultUnavailable,
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
