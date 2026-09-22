package mpvplayer

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

// The tests drive a real mpv-shaped process without depending on mpv being
// installed: the test binary re-enters itself as a fake mpv when
// LILT_TEST_FAKE_MPV is set. init runs before the testing package parses flags,
// so the mpv-shaped argv the client passes never reaches Go's flag parser.
func init() {
	if os.Getenv("LILT_TEST_FAKE_MPV") != "1" {
		return
	}
	runFakeMPV()
	os.Exit(0)
}

// fakeMPV implements the subset of mpv's JSON IPC protocol the client uses. The
// URL passed to loadfile selects the scenario:
//
//   - containing "fail"  -> the stream is refused (end-file with an error)
//   - containing "eof"   -> the stream plays to its end
//   - containing "hang"  -> the stream never opens
//   - anything else      -> the stream opens and plays
type fakeMPV struct {
	conn    net.Conn
	encoder *json.Encoder
	writeMu sync.Mutex
	loaded  bool
	paused  bool
}

func runFakeMPV() {
	socket := ""
	for _, arg := range os.Args[1:] {
		if value, ok := strings.CutPrefix(arg, "--input-ipc-server="); ok {
			socket = value
		}
	}
	if argvPath := os.Getenv("LILT_TEST_FAKE_MPV_ARGV"); argvPath != "" {
		_ = os.WriteFile(argvPath, []byte(strings.Join(os.Args, "\n")), 0o600)
	}
	if socket == "" {
		os.Exit(2)
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		os.Exit(3)
	}
	conn, err := listener.Accept()
	if err != nil {
		os.Exit(4)
	}
	defer conn.Close()
	fake := &fakeMPV{conn: conn, encoder: json.NewEncoder(conn)}
	fake.run()
}

func (f *fakeMPV) run() {
	scanner := bufio.NewScanner(f.conn)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var request struct {
			Command   []any  `json:"command"`
			RequestID uint64 `json:"request_id"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil || len(request.Command) == 0 {
			continue
		}
		name, _ := request.Command[0].(string)
		switch name {
		case "quit":
			f.reply(request.RequestID, "success", nil)
			return
		case "observe_property":
			property, _ := request.Command[2].(string)
			f.reply(request.RequestID, "success", nil)
			f.initialProperty(property)
		case "get_property":
			property, _ := request.Command[1].(string)
			f.property(request.RequestID, property)
		case "set_property":
			property, _ := request.Command[1].(string)
			f.reply(request.RequestID, "success", nil)
			if property == "pause" {
				value, _ := request.Command[2].(bool)
				f.paused = value
				f.emit("property-change", map[string]any{"name": "pause", "data": value})
			}
		case "loadfile":
			url, _ := request.Command[1].(string)
			f.reply(request.RequestID, "success", nil)
			f.load(url)
		case "stop":
			f.reply(request.RequestID, "success", nil)
			f.loaded = false
			f.emit("property-change", map[string]any{"name": "idle-active", "data": true})
			f.emit("end-file", map[string]any{"reason": "stop"})
		default:
			f.reply(request.RequestID, "success", nil)
		}
	}
}

func (f *fakeMPV) load(url string) {
	if f.loaded {
		// mpv ends the file a replacement displaces before starting the new one.
		// The gap is real: a start must not be reported as stopped (or as
		// buffering) while the replacement is still opening.
		f.loaded = false
		f.emit("property-change", map[string]any{"name": "idle-active", "data": true})
		f.emit("end-file", map[string]any{"reason": "stop"})
		time.Sleep(200 * time.Millisecond)
	}
	f.emit("start-file", map[string]any{"reason": "load"})
	switch {
	case strings.Contains(url, "hang"):
		return
	case strings.Contains(url, "fail"):
		f.emit("end-file", map[string]any{"reason": "error", "error": "Failed to open " + url})
	case strings.Contains(url, "eof"):
		f.emit("property-change", map[string]any{"name": "idle-active", "data": false})
		f.loaded = true
		f.emit("file-loaded", map[string]any{})
		f.emit("property-change", map[string]any{"name": "eof-reached", "data": true})
		f.emit("end-file", map[string]any{"reason": "eof"})
	default:
		f.emit("property-change", map[string]any{"name": "idle-active", "data": false})
		f.emit("property-change", map[string]any{"name": "duration", "data": 0})
		f.loaded = true
		f.emit("file-loaded", map[string]any{})
	}
}

// initialProperty mirrors mpv's behaviour of reporting an observed property's
// current value immediately after observe_property.
func (f *fakeMPV) initialProperty(property string) {
	switch property {
	case "pause":
		f.emit("property-change", map[string]any{"name": "pause", "data": f.paused})
	case "idle-active":
		f.emit("property-change", map[string]any{"name": "idle-active", "data": !f.loaded})
	case "eof-reached":
		f.emit("property-change", map[string]any{"name": "eof-reached", "data": false})
	case "duration":
		f.emit("property-change", map[string]any{"name": "duration"})
	}
}

func (f *fakeMPV) property(requestID uint64, property string) {
	switch property {
	case "time-pos":
		if !f.loaded {
			// mpv answers "property unavailable" when nothing is loaded.
			f.reply(requestID, "property unavailable", nil)
			return
		}
		f.reply(requestID, "success", 3.5)
	case "pause":
		f.reply(requestID, "success", f.paused)
	default:
		f.reply(requestID, "success", nil)
	}
}

func (f *fakeMPV) reply(requestID uint64, status string, data any) {
	f.write(map[string]any{"request_id": requestID, "error": status, "data": data})
}

func (f *fakeMPV) emit(event string, fields map[string]any) {
	message := map[string]any{"event": event}
	for key, value := range fields {
		message[key] = value
	}
	f.write(message)
}

func (f *fakeMPV) write(message map[string]any) {
	f.writeMu.Lock()
	defer f.writeMu.Unlock()
	_ = f.encoder.Encode(message)
}
