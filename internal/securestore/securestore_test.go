package securestore

import (
	"errors"
	"testing"
)

func TestMemoryStoreRoundTrip(t *testing.T) {
	store := NewMemory()
	if _, err := store.Get("lilt", "audius"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get empty err=%v", err)
	}
	if err := store.Set("lilt", "audius", "secret"); err != nil {
		t.Fatal(err)
	}
	value, err := store.Get("lilt", "audius")
	if err != nil || value != "secret" {
		t.Fatalf("get=%q err=%v", value, err)
	}
	if err := store.Set("lilt", "audius", "rotated"); err != nil {
		t.Fatal(err)
	}
	if value, _ := store.Get("lilt", "audius"); value != "rotated" {
		t.Fatalf("rotated=%q", value)
	}
	if err := store.Delete("lilt", "audius"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("lilt", "audius"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get deleted err=%v", err)
	}
}
