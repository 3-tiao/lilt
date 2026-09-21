package jamendo

import (
	"errors"
	"fmt"
	"strings"

	"github.com/caiguo/lilt/internal/securestore"
)

const (
	CredentialService = "lilt"
	ClientIDAccount   = "jamendo.client_id"
)

// LoadClientID reads the application-level client_id from secure storage.
func LoadClientID(store securestore.Store) (string, error) {
	if store == nil {
		return "", ErrNotConfigured
	}
	value, err := store.Get(CredentialService, ClientIDAccount)
	if errors.Is(err, securestore.ErrNotFound) {
		return "", ErrNotConfigured
	}
	if err != nil {
		return "", fmt.Errorf("read Jamendo client_id: %w", err)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", ErrNotConfigured
	}
	return value, nil
}

// SaveClientID stores a validated client_id. Validation belongs to the caller
// because it requires a network request; this function only owns persistence.
func SaveClientID(store securestore.Store, clientID string) error {
	if store == nil {
		return errors.New("secure storage is unavailable")
	}
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return ErrNotConfigured
	}
	if err := store.Set(CredentialService, ClientIDAccount, clientID); err != nil {
		return fmt.Errorf("store Jamendo client_id: %w", err)
	}
	return nil
}

// DeleteClientID removes the local app credential. securestore deletion is
// idempotent, so disconnecting an unconfigured source is a successful no-op.
func DeleteClientID(store securestore.Store) error {
	if store == nil {
		return nil
	}
	if err := store.Delete(CredentialService, ClientIDAccount); err != nil {
		return fmt.Errorf("delete Jamendo client_id: %w", err)
	}
	return nil
}
