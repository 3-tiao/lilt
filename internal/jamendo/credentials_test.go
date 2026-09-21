package jamendo

import (
	"errors"
	"testing"

	"github.com/caiguo/lilt/internal/securestore"
)

func TestClientIDSecureStoreLifecycle(t *testing.T) {
	store := securestore.NewMemory()
	if _, err := LoadClientID(store); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("missing client_id error=%v", err)
	}
	if err := SaveClientID(store, " client-1 "); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadClientID(store); err != nil || got != "client-1" {
		t.Fatalf("client_id=%q err=%v", got, err)
	}
	if err := DeleteClientID(store); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadClientID(store); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("deleted client_id error=%v", err)
	}
}
