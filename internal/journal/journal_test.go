package journal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogAndTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lilt.jsonl")
	t.Setenv("LILT_LOG", path)
	logger := Open()
	logger.Log("cli", map[string]any{"args": []string{"status"}})
	logger.Log("rpc", map[string]any{"method": "state"})
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := Tail(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0]["kind"] != "rpc" || entries[0]["method"] != "state" {
		t.Fatalf("entries = %#v", entries)
	}
}

func TestLogRedactsInputsAndSensitiveURLs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lilt.jsonl")
	t.Setenv("LILT_LOG", path)
	logger := Open()
	secret := "private search"
	logger.Log("submit", map[string]any{"value": secret, "url": "https://user:pass@example.com/live?token=abc#frag", "error": "failed https://example.com/a?q=secret#x"})
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) || strings.Contains(string(data), "pass") || strings.Contains(string(data), "token") || strings.Contains(string(data), "secret") {
		t.Fatalf("sensitive value leaked: %s", data)
	}
	var entry map[string]any
	if err := json.Unmarshal(data[:len(data)-1], &entry); err != nil {
		t.Fatal(err)
	}
	if entry["url"] != "https://example.com/live" {
		t.Fatalf("safe URL = %#v", entry["url"])
	}
}

func TestLevelFromEnv(t *testing.T) {
	t.Setenv("LILT_LOG", filepath.Join(t.TempDir(), "lilt.jsonl"))
	t.Setenv("LILT_LOG_LEVEL", "")
	t.Setenv("LILT_DEV_LOG", "")
	if logger := Open(); logger.level != LevelInfo {
		t.Fatalf("default level = %v, want info", logger.level)
	}
	t.Setenv("LILT_LOG_LEVEL", "debug")
	if logger := Open(); logger.level != LevelDebug {
		t.Fatalf("LILT_LOG_LEVEL=debug level = %v, want debug", logger.level)
	}
	t.Setenv("LILT_LOG_LEVEL", "nonsense")
	t.Setenv("LILT_DEV_LOG", "1")
	if logger := Open(); logger.level != LevelDebug {
		t.Fatalf("LILT_DEV_LOG alias level = %v, want debug", logger.level)
	}
}

// Debug events are dropped at the default level and written when the operator
// opts in; user content is only expanded at debug.
func TestDebugChannelGatedByLevel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lilt.jsonl")
	t.Setenv("LILT_LOG", path)
	t.Setenv("LILT_LOG_LEVEL", "")
	t.Setenv("LILT_DEV_LOG", "")
	logger := Open()
	logger.Debug("server.request", map[string]any{"term": "private search"})
	logger.Log("cli", map[string]any{"args": []string{"status"}})
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "server.request") {
		t.Fatalf("debug event leaked at info level: %s", data)
	}

	t.Setenv("LILT_LOG_LEVEL", "debug")
	debugLogger := Open()
	debugLogger.Debug("server.request", map[string]any{"term": "private search", "params": map[string]any{"source": "audius"}})
	if err := debugLogger.Close(); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if !strings.Contains(string(data), "server.request") || !strings.Contains(string(data), "private search") {
		t.Fatalf("debug event missing its full content: %s", data)
	}
}

// Structured payloads the production call sites actually pass — raw JSON
// params and CLI argument slices — must be decoded and redacted, not echoed.
func TestRawParamsAndCLIArgsRedacted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lilt.jsonl")
	t.Setenv("LILT_LOG", path)
	t.Setenv("LILT_LOG_LEVEL", "debug")
	t.Setenv("LILT_DEV_LOG", "")
	logger := Open()
	logger.Debug("server.request", map[string]any{
		"params": json.RawMessage(`{"accessToken":"super-secret","source":"audius","nested":{"client_secret":"shh"},"refs":["a","b"]}`),
	})
	logger.Debug("cli.rpc", map[string]any{
		"command": "jamendo",
		"cliArgs": []string{"setup", "--token", "super-secret", "--password=shh", "--source", "audius"},
	})
	// An info-level structured field must not slip through either.
	logger.Log("server.request", map[string]any{
		"params": json.RawMessage(`{"api_key":"super-secret"}`),
	})
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	for _, secret := range []string{"super-secret", "shh"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("credential leaked: %s", data)
		}
	}
	if !strings.Contains(string(data), "[redacted]") {
		t.Fatalf("expected a redaction marker: %s", data)
	}
	// Non-secret content is still available at debug for reproduction.
	if !strings.Contains(string(data), "audius") || !strings.Contains(string(data), "--source") {
		t.Fatalf("debug content over-redacted: %s", data)
	}
}

// Credential-shaped fields are redacted at every level, including debug.
func TestSecretsRedactedEvenInDebug(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lilt.jsonl")
	t.Setenv("LILT_LOG", path)
	t.Setenv("LILT_LOG_LEVEL", "debug")
	t.Setenv("LILT_DEV_LOG", "")
	logger := Open()
	logger.Debug("server.request", map[string]any{
		"accessToken": "super-secret",
		"params":      map[string]any{"client_secret": "shh", "source": "audius"},
		"headers":     map[string]any{"Authorization": "Bearer abc"},
	})
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	for _, secret := range []string{"super-secret", "shh", "Bearer abc"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("credential leaked in debug: %s", data)
		}
	}
	if !strings.Contains(string(data), "[redacted]") {
		t.Fatalf("expected a redaction marker: %s", data)
	}
}
