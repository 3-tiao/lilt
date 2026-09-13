package journal

import (
	"path/filepath"
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
