// Package securestore stores provider credentials outside lilt state. On macOS
// it uses the Keychain via the system `security` tool; tests use Memory.
package securestore

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

// ErrNotFound is returned by Get when no secret is stored.
var ErrNotFound = errors.New("secret not found")

// Store is a tiny service/account secret store.
type Store interface {
	Get(service, account string) (string, error)
	Set(service, account, secret string) error
	Delete(service, account string) error
}

// Memory is an in-process store for tests.
type Memory struct {
	mu      sync.Mutex
	secrets map[string]string
}

func NewMemory() *Memory { return &Memory{secrets: map[string]string{}} }

func memKey(service, account string) string { return service + "\x00" + account }

func (m *Memory) Get(service, account string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	value, ok := m.secrets[memKey(service, account)]
	if !ok {
		return "", ErrNotFound
	}
	return value, nil
}

func (m *Memory) Set(service, account, secret string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.secrets[memKey(service, account)] = secret
	return nil
}

func (m *Memory) Delete(service, account string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.secrets, memKey(service, account))
	return nil
}

// Keychain stores secrets in the macOS login Keychain.
type Keychain struct{}

// Default returns the platform secure store, or nil when none is available.
func Default() Store {
	if runtime.GOOS == "darwin" {
		return Keychain{}
	}
	return nil
}

func (Keychain) Get(service, account string) (string, error) {
	out, err := exec.Command("security", "find-generic-password", "-s", service, "-a", account, "-w").Output()
	if err != nil {
		if isNotFound(err) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("keychain read failed: %w", err)
	}
	return strings.TrimRight(string(out), "\n"), nil
}

func (Keychain) Set(service, account, secret string) error {
	// `-U` updates an existing entry. The secret is passed on argv, which the
	// OS may expose briefly in the process list; documented as a limitation.
	if err := exec.Command("security", "add-generic-password", "-U", "-s", service, "-a", account, "-w", secret).Run(); err != nil {
		return fmt.Errorf("keychain write failed: %w", err)
	}
	return nil
}

func (Keychain) Delete(service, account string) error {
	err := exec.Command("security", "delete-generic-password", "-s", service, "-a", account).Run()
	if err == nil || isNotFound(err) {
		return nil // deleting a missing secret is a no-op
	}
	return fmt.Errorf("keychain delete failed: %w", err)
}

// isNotFound reports `security`'s "item could not be found" exit status (44).
func isNotFound(err error) bool {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode() == 44
	}
	return false
}
