package appleweb

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// The tests drive a protocol-compatible Chromium without one being installed:
// the test binary re-enters itself as a fake DevTools peer when
// LILT_TEST_FAKE_CDP is set. init runs before the testing package parses flags,
// so the chromium-shaped argv never reaches Go's flag parser.
//
// The fake mimics the two behaviours this package exists to handle: a launch
// that restores an extra tab, and a page that refuses play() without user
// activation. It answers a Runtime.evaluate that lacks userGesture with a CDP
// error, which is how the real failure presents: nothing happens, and nothing
// is reported.
func init() {
	if os.Getenv("LILT_TEST_FAKE_CDP") != "1" {
		return
	}
	runFakeCDP()
	os.Exit(0)
}

type fakeCDP struct {
	in      *os.File
	out     *os.File
	writeMu sync.Mutex
	log     *os.File
	// evaluateValue is returned for ordinary evaluations; stateJSON is returned
	// for the state probe.
	evaluateValue string
	stateJSON     string
	// exceptionFor makes one expression fail, to cover error propagation.
	exceptionFor string
}

func runFakeCDP() {
	logPath := os.Getenv("LILT_TEST_FAKE_CDP_LOG")
	var logFile *os.File
	if logPath != "" {
		logFile, _ = os.Create(logPath)
	}
	fake := &fakeCDP{
		in:            os.NewFile(3, "cdp-in"),
		out:           os.NewFile(4, "cdp-out"),
		log:           logFile,
		evaluateValue: "ok",
		stateJSON:     os.Getenv("LILT_TEST_FAKE_CDP_STATE"),
		exceptionFor:  os.Getenv("LILT_TEST_FAKE_CDP_EXCEPTION"),
	}
	if fake.stateJSON == "" {
		fake.stateJSON = `{"ready":true,"authorized":true,"state":2,"isPlaying":true,"position":1.5,"duration":204,"itemID":"111","itemTitle":"Fixture","queueLength":1}`
	}
	if argvPath := os.Getenv("LILT_TEST_FAKE_CDP_ARGV"); argvPath != "" {
		_ = os.WriteFile(argvPath, []byte(strings.Join(os.Args, "\n")), 0o600)
	}
	if launchLog := os.Getenv("LILT_TEST_FAKE_CDP_LAUNCH_LOG"); launchLog != "" {
		file, _ := os.OpenFile(launchLog, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if file != nil {
			_, _ = file.WriteString("launch\n")
			_ = file.Close()
		}
	}
	fake.run()
}

func (f *fakeCDP) run() {
	reader := bufio.NewReader(f.in)
	for {
		payload, err := reader.ReadString(0)
		if err != nil {
			return
		}
		raw := strings.TrimSuffix(payload, "\x00")
		var request struct {
			ID        int             `json:"id"`
			Method    string          `json:"method"`
			Params    json.RawMessage `json:"params"`
			SessionID string          `json:"sessionId"`
		}
		if json.Unmarshal([]byte(raw), &request) != nil {
			continue
		}
		f.record(request.Method, request.Params, request.SessionID)
		f.handle(request.ID, request.Method, request.Params)
	}
}

// record appends one line per request so a test can assert what was sent.
func (f *fakeCDP) record(method string, params json.RawMessage, session string) {
	if f.log == nil {
		return
	}
	var decoded map[string]any
	_ = json.Unmarshal(params, &decoded)
	userGesture, hasGesture := decoded["userGesture"]
	_, _ = fmt.Fprintf(f.log, "%s userGesture=%v present=%v session=%v params=%s\n",
		method, userGesture, hasGesture, session, truncate(string(params)))
}

func truncate(value string) string {
	if len(value) > 200 {
		return value[:200]
	}
	return value
}

func (f *fakeCDP) handle(id int, method string, params json.RawMessage) {
	var decoded map[string]any
	_ = json.Unmarshal(params, &decoded)
	switch method {
	case "Target.getTargets":
		// One page for the requested URL plus a restored tab, which is what
		// --restore-last-session leaves behind.
		url := ""
		for _, arg := range os.Args[1:] {
			if strings.HasPrefix(arg, "http") {
				url = arg
			}
		}
		f.reply(id, map[string]any{"targetInfos": []map[string]any{
			{"targetId": "kept", "type": "page", "url": url},
			{"targetId": "restored", "type": "page", "url": "https://music.apple.com/cn/restored"},
			{"targetId": "worker", "type": "other", "url": ""},
		}})
	case "Target.closeTarget":
		f.reply(id, map[string]any{})
	case "Target.attachToTarget":
		f.reply(id, map[string]any{"sessionId": "session-1"})
	case "Browser.close":
		f.reply(id, map[string]any{})
	case "Runtime.evaluate":
		expression, _ := decoded["expression"].(string)
		if os.Getenv("LILT_TEST_FAKE_CDP_EXIT_ON_PLAY") == "1" && strings.Contains(expression, "setQueue") {
			_ = f.out.Close()
			return
		}
		if f.exceptionFor != "" && strings.Contains(expression, f.exceptionFor) {
			f.replyRaw(id, map[string]any{
				"result": map[string]any{
					"result":           map[string]any{"type": "object"},
					"exceptionDetails": map[string]any{"text": "Uncaught", "exception": map[string]any{"description": "Error: fixture failure\n    at <anonymous>"}},
				},
			})
			return
		}
		// The real page needs user activation; without it play() never settles.
		if gesture, ok := decoded["userGesture"].(bool); !ok || !gesture {
			f.replyError(id, -32602, "userGesture is required")
			return
		}
		if strings.Contains(expression, "playbackState") {
			encoded, _ := json.Marshal(map[string]any{
				"id":     id,
				"result": map[string]any{"result": map[string]any{"type": "string", "value": f.stateJSON}},
			})
			encoded = append(encoded, 0)
			// Written in two chunks so the transport has to reassemble a frame.
			half := len(encoded) / 2
			f.writeRaw(encoded[:half])
			time.Sleep(20 * time.Millisecond)
			f.writeRaw(encoded[half:])
			return
		}
		// The EME probe gets a scripted tri-state answer, so a full composition
		// can exercise supported / unsupported / failed probes hermetically.
		if strings.Contains(expression, "requestMediaKeySystemAccess") {
			switch os.Getenv("LILT_TEST_FAKE_CDP_IME") {
			case "denied":
				f.reply(id, map[string]any{"result": map[string]any{"type": "string", "value": `{"status":"denied","reason":"NotSupportedError"}`}})
			case "error":
				f.replyRaw(id, map[string]any{
					"result": map[string]any{
						"result":           map[string]any{"type": "object"},
						"exceptionDetails": map[string]any{"text": "Uncaught", "exception": map[string]any{"description": "Error: no EME in this fixture\n    at <anonymous>"}},
					},
				})
			default:
				f.reply(id, map[string]any{"result": map[string]any{"type": "string", "value": `{"status":"ok"}`}})
			}
			return
		}
		// Catalog calls get canned fixtures: one song resolvable by id, empty
		// search groups, no albums. Enough for a full server round trip
		// without any network.
		if strings.Contains(expression, "/v1/catalog/") {
			switch {
			case strings.Contains(expression, "/songs/"):
				// The canned payload is already in the mapped shape the page's
				// songMapping returns; the JS mapping itself is exercised by
				// the opt-in real-browser E2E, not here.
				f.reply(id, map[string]any{"result": map[string]any{"type": "string", "value": `{"id":"1111111111","title":"Fixture","artist":"Fixture Artist","album":"Fixture Album","url":"https://music.apple.com/cn/song/fixture/1111111111","durationMs":204000}`}})
			case strings.Contains(expression, "/search"):
				f.reply(id, map[string]any{"result": map[string]any{"type": "string", "value": "[]"}})
			default:
				f.reply(id, map[string]any{"result": map[string]any{"type": "string", "value": "null"}})
			}
			return
		}
		f.reply(id, map[string]any{"result": map[string]any{"type": "string", "value": f.evaluateValue}})
	default:
		f.reply(id, map[string]any{})
	}
}

func (f *fakeCDP) reply(id int, result any) {
	f.replyRaw(id, map[string]any{"result": result})
}

func (f *fakeCDP) replyRaw(id int, fields map[string]any) {
	message := map[string]any{"id": id}
	for key, value := range fields {
		message[key] = value
	}
	encoded, _ := json.Marshal(message)
	f.writeRaw(append(encoded, 0))
}

func (f *fakeCDP) replyError(id int, code int, message string) {
	encoded, _ := json.Marshal(map[string]any{"id": id, "error": map[string]any{"code": code, "message": message}})
	f.writeRaw(append(encoded, 0))
}

func (f *fakeCDP) writeRaw(payload []byte) {
	f.writeMu.Lock()
	defer f.writeMu.Unlock()
	_, _ = f.out.Write(payload)
}
