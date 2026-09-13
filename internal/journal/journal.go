// Package journal writes lilt's structured operation log as JSON lines.
package journal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const maxBytes = 5 << 20

// Logger appends JSON-lines events. A nil or failed logger is a no-op.
type Logger struct {
	mu   sync.Mutex
	file *os.File
}

func Path() string {
	if path := os.Getenv("LILT_LOG"); path != "" {
		return path
	}
	if base := os.Getenv("XDG_STATE_HOME"); base != "" {
		return filepath.Join(base, "lilt", "log", "lilt.jsonl")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".local", "state", "lilt", "log", "lilt.jsonl")
	}
	return filepath.Join(home, ".local", "state", "lilt", "log", "lilt.jsonl")
}

// Open opens (or creates) the log file, rotating it once when it grows past 5 MB.
func Open() *Logger {
	path := Path()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return &Logger{}
	}
	if info, err := os.Stat(path); err == nil && info.Size() > maxBytes {
		_ = os.Rename(path, path+".1")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return &Logger{}
	}
	return &Logger{file: file}
}

func (l *Logger) Log(kind string, fields map[string]any) {
	if l == nil || l.file == nil {
		return
	}
	entry := make(map[string]any, len(fields)+2)
	entry["ts"] = time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	entry["kind"] = kind
	for key, value := range fields {
		entry[key] = value
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.file.Write(append(data, '\n'))
}

func (l *Logger) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	err := l.file.Close()
	l.file = nil
	return err
}

// Tail returns the last n entries from the default log path.
func Tail(n int) ([]map[string]any, error) {
	data, err := os.ReadFile(Path())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	lines := splitLines(data)
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	entries := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		var entry map[string]any
		if json.Unmarshal(line, &entry) == nil {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

func splitLines(data []byte) [][]byte {
	lines := make([][]byte, 0)
	start := 0
	for i, b := range data {
		if b == '\n' {
			if i > start {
				lines = append(lines, data[start:i])
			}
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}
