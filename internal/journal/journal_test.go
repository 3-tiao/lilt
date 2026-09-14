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
