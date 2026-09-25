// Package journal writes lilt's structured operation log as JSON lines.
package journal

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const maxBytes = 5 << 20

// Level selects how much detail the journal records. Operators opt into debug
// through LILT_LOG_LEVEL=debug (LILT_DEV_LOG=1 is an alias) for local
// troubleshooting; the default stays the private, redacted info channel.
type Level int

const (
	LevelInfo Level = iota
	LevelDebug
)

// Logger appends JSON-lines events. A nil or failed logger is a no-op.
type Logger struct {
	mu    sync.Mutex
	file  *os.File
	level Level
}

// resolveLevel reads the diagnostic level from the environment. Anything other
// than an explicit debug opt-in stays at info.
func resolveLevel() Level {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("LILT_LOG_LEVEL")), "debug") {
		return LevelDebug
	}
	if os.Getenv("LILT_DEV_LOG") == "1" {
		return LevelDebug
	}
	return LevelInfo
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
	return &Logger{file: file, level: resolveLevel()}
}

// Log writes one info-level entry. User content is kept private (summaries and
// URL redaction), but credential-shaped fields are never written.
func (l *Logger) Log(kind string, fields map[string]any) {
	l.write(kind, fields, false)
}

// Debug writes one debug-level entry only when the diagnostic level is debug.
// In debug the operator has explicitly opted in, so user content (search terms,
// titles, and short-lived media URLs) is written in full; credential-shaped
// fields remain redacted.
func (l *Logger) Debug(kind string, fields map[string]any) {
	if l == nil || l.level != LevelDebug {
		return
	}
	l.write(kind, fields, true)
}

func (l *Logger) write(kind string, fields map[string]any, debug bool) {
	if l == nil || l.file == nil {
		return
	}
	entry := make(map[string]any, len(fields)+2)
	for key, value := range fields {
		if key == "kind" || key == "ts" {
			continue
		}
		entry[key] = sanitizeField(key, value, debug)
	}
	entry["ts"] = time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	entry["kind"] = kind
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.file.Write(append(data, '\n'))
}

// redactedSecret is the marker written for credential-shaped fields. It is used
// at every level and is never replaced by the real value.
const redactedSecret = "[redacted]"

// secretKeyParts are matched as case-insensitive substrings of field names.
// Only credential/secret-shaped names are listed, so ordinary content keys are
// unaffected.
var secretKeyParts = []string{
	"secret", "token", "password", "passwd", "authorization",
	"keychain", "credential", "api_key", "apikey", "access_key",
}

func isSecretKey(key string) bool {
	lower := strings.ToLower(key)
	for _, part := range secretKeyParts {
		if strings.Contains(lower, part) {
			return true
		}
	}
	return false
}

// sanitizeField applies the credential redaction at every level, then either
// keeps user content private (info) or writes it verbatim (debug).
func sanitizeField(key string, value any, debug bool) any {
	if isSecretKey(key) {
		return redactedSecret
	}
	if debug {
		return sanitizeDebugValue(value)
	}
	return safeField(key, value)
}

// sanitizeDebugValue recursively redacts credential-shaped keys but leaves user
// content untouched. Raw JSON params and string slices (CLI args) are decoded
// and walked too, so a credential can never ride through an unparsed shape.
func sanitizeDebugValue(value any) any {
	switch typed := value.(type) {
	case nil, bool, string, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64, float32, float64:
		return value
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			if isSecretKey(key) {
				out[key] = redactedSecret
				continue
			}
			out[key] = sanitizeDebugValue(item)
		}
		return out
	case map[string]string:
		out := make(map[string]string, len(typed))
		for key, item := range typed {
			if isSecretKey(key) {
				out[key] = redactedSecret
				continue
			}
			out[key] = item
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = sanitizeDebugValue(item)
		}
		return out
	case []string:
		return sanitizeArgs(typed)
	case json.RawMessage:
		var decoded any
		if err := json.Unmarshal(typed, &decoded); err != nil {
			// Unparseable params could still carry a credential; never echo them.
			return redactedSecret
		}
		return sanitizeDebugValue(decoded)
	case []byte:
		return redactedSecret
	}
	// Typed slices/maps/structs: canonicalize through JSON so key-based
	// redaction reaches every level instead of passing the value through.
	raw, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return value
	}
	return sanitizeDebugValue(decoded)
}

// sanitizeArgs redacts the value of a credential-shaped CLI flag, both the
// `--name value` and `--name=value` spellings, so `cliArgs` cannot leak secrets.
func sanitizeArgs(args []string) []any {
	out := make([]any, 0, len(args))
	redactNext := false
	for _, arg := range args {
		if redactNext {
			out = append(out, redactedSecret)
			redactNext = false
			continue
		}
		name, hasValue := arg, false
		if eq := strings.IndexByte(arg, '='); eq >= 0 {
			name, hasValue = arg[:eq], true
		}
		flag := strings.TrimLeft(name, "-")
		if !isSecretKey(flag) {
			out = append(out, arg)
			continue
		}
		if hasValue {
			out = append(out, name+"="+redactedSecret)
			continue
		}
		out = append(out, arg)
		redactNext = true
	}
	return out
}

var urlPattern = regexp.MustCompile(`https?://[^\s"']+`)

func safeField(key string, value any) any {
	switch strings.ToLower(key) {
	case "args", "cliargs":
		if args, ok := value.([]string); ok {
			command := ""
			if len(args) > 0 {
				command = args[0]
			}
			return map[string]any{"count": len(args), "command": command}
		}
		return "[redacted]"
	case "value", "term", "query", "reference", "selected", "title", "line":
		return valueSummary(value)
	}
	// Structured payloads are never echoed at info; if one ever reaches this
	// path, summarise it as redacted rather than writing it verbatim.
	switch value.(type) {
	case json.RawMessage, map[string]any, map[string]string, []any, []string:
		return "[redacted]"
	}
	if text, ok := value.(string); ok {
		return redactURLs(text)
	}
	return value
}

func valueSummary(value any) any {
	text, ok := value.(string)
	if !ok {
		return "[redacted]"
	}
	kind := "text"
	if parsed, err := url.Parse(text); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		kind = "url"
		return map[string]any{"kind": kind, "length": len(text), "destination": safeURL(parsed)}
	}
	return map[string]any{"kind": kind, "length": len(text)}
}

func redactURLs(text string) string {
	return urlPattern.ReplaceAllStringFunc(text, func(raw string) string {
		parsed, err := url.Parse(strings.TrimRight(raw, ".,;:)"))
		if err != nil {
			return "[redacted-url]"
		}
		return safeURL(parsed)
	})
}

func safeURL(parsed *url.URL) string {
	if parsed == nil {
		return "[redacted-url]"
	}
	return fmt.Sprintf("%s://%s%s", strings.ToLower(parsed.Scheme), strings.ToLower(parsed.Hostname()), parsed.EscapedPath())
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
