package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/3-tiao/lilt/internal/api"
)

func TestDedupSameRequestReusesResult(t *testing.T) {
	cache := newDedupCache(4, 0, time.Minute)
	first, isNew, err := cache.begin("id-1", "fp")
	if err != nil || !isNew {
		t.Fatalf("begin = %v, isNew=%v", err, isNew)
	}
	second, isNew, err := cache.begin("id-1", "fp")
	if err != nil {
		t.Fatalf("second begin: %v", err)
	}
	if isNew {
		t.Fatal("second identical request reported as new")
	}
	if first != second {
		t.Fatal("second request did not reuse the in-flight entry")
	}
	cache.finish("id-1", api.Success("id-1", map[string]any{"v": 1}))
	select {
	case <-second.done:
	default:
		t.Fatal("entry was not released after finish")
	}
	result := cache.result(second)
	if !result.OK {
		t.Fatalf("cached result = %+v", result)
	}
}

func TestDedupDifferentFingerprintRejected(t *testing.T) {
	cache := newDedupCache(4, 0, time.Minute)
	if _, _, err := cache.begin("id-1", "fp-a"); err != nil {
		t.Fatal(err)
	}
	_, _, err := cache.begin("id-1", "fp-b")
	if err == nil || err.Code != api.CodeInvalidRequest {
		t.Fatalf("err = %v, want invalid_request", err)
	}
}

func TestDedupEvictedBodyYieldsDuplicateResultUnavailable(t *testing.T) {
	cache := newDedupCache(1, 0, time.Minute)
	first, _, err := cache.begin("id-1", "fp")
	if err != nil {
		t.Fatal(err)
	}
	cache.finish("id-1", api.Success("id-1", map[string]any{"v": 1}))
	second, _, err := cache.begin("id-2", "fp")
	if err != nil {
		t.Fatal(err)
	}
	cache.finish("id-2", api.Success("id-2", map[string]any{"v": 2}))

	if result := cache.result(first); result.OK || result.Error.Code != api.CodeDuplicateResultUnavailable {
		t.Fatalf("evicted result = %+v, want duplicate_result_unavailable", result)
	}
	if result := cache.result(second); !result.OK {
		t.Fatalf("retained result = %+v", result)
	}
}

func TestFingerprintIgnoresKeyOrder(t *testing.T) {
	a := json.RawMessage(`{"b":1,"a":2}`)
	b := json.RawMessage(`{"a":2,"b":1}`)
	if fingerprint("cmd", normalizeParams(a)) != fingerprint("cmd", normalizeParams(b)) {
		t.Fatal("fingerprints differ for equivalent params")
	}
	if fingerprint("cmd", normalizeParams(a)) == fingerprint("other", normalizeParams(a)) {
		t.Fatal("fingerprints collide across commands")
	}
}

func TestDedupPruneClearsCompletedOrder(t *testing.T) {
	cache := newDedupCache(10, 0, time.Nanosecond)
	for _, id := range []string{"a", "b"} {
		if _, _, err := cache.begin(id, "fp"); err != nil {
			t.Fatal(err)
		}
		cache.finish(id, api.Success(id, map[string]any{}))
	}
	time.Sleep(2 * time.Millisecond)
	// begin() prunes expired tombstones; the completion-order slice must drop
	// them too or a later reuse can evict a fresh entry prematurely.
	if _, _, err := cache.begin("c", "fp"); err != nil {
		t.Fatal(err)
	}
	if len(cache.completed) != 0 {
		t.Fatalf("completed order retained %d expired ids", len(cache.completed))
	}
}
