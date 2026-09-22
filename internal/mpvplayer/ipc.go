// Package mpvplayer drives a local mpv process over its JSON IPC socket. It is
// the Linux counterpart of the signed lilt-audio helper: the same
// server.AudioEngine boundary, implemented in-process instead of in Swift.
//
// The package is deliberately free of build tags: it is plain Go, so the
// hermetic tests run on every platform, and only the platform composition in
// cmd/lilt decides whether it is wired up.
package mpvplayer

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
)

// errIPCClosed reports a request made after the IPC transport became unusable.
var errIPCClosed = errors.New("mpv IPC transport is closed")

// ipcCommand is one mpv JSON IPC request. mpv answers every request that
// carries a request_id with exactly one reply line.
type ipcCommand struct {
	Command   []any  `json:"command"`
	RequestID uint64 `json:"request_id,omitempty"`
}

// ipcMessage is one line of mpv's JSON IPC protocol. A line is either a reply
// (request_id set) or an event (event set). mpv uses "error" both for a reply's
// status string and for an end-file event's failure description, so the two are
// told apart by event being set.
type ipcMessage struct {
	RequestID *uint64         `json:"request_id"`
	Error     string          `json:"error"`
	Data      json.RawMessage `json:"data"`
	Event     string          `json:"event"`
	Name      string          `json:"name"`
	Reason    string          `json:"reason"`
}

// ipcConn is a request/response client for mpv's JSON IPC socket. Replies are
// matched by request_id; events are handed to the owner's callback. A transport
// failure is terminal: every pending request fails and done closes once.
type ipcConn struct {
	conn    net.Conn
	writeMu sync.Mutex

	pendingMu sync.Mutex
	pending   map[uint64]chan ipcMessage
	next      uint64
	closed    bool
	termErr   error

	events func(ipcMessage)
	done   chan struct{}
	once   sync.Once
}

func newIPCConn(conn net.Conn, events func(ipcMessage)) *ipcConn {
	c := &ipcConn{
		conn:    conn,
		pending: make(map[uint64]chan ipcMessage),
		events:  events,
		done:    make(chan struct{}),
	}
	go c.read()
	return c
}

// call runs one mpv command and returns its data field. A non-success error
// string is reported as a command failure, not as a transport failure: mpv is
// still usable.
func (c *ipcConn) call(ctx context.Context, args ...any) (json.RawMessage, error) {
	c.pendingMu.Lock()
	if c.closed {
		err := c.termErr
		c.pendingMu.Unlock()
		if err == nil {
			err = errIPCClosed
		}
		return nil, err
	}
	c.next++
	id := c.next
	reply := make(chan ipcMessage, 1)
	c.pending[id] = reply
	c.pendingMu.Unlock()

	payload, err := json.Marshal(ipcCommand{Command: args, RequestID: id})
	if err != nil {
		c.drop(id)
		return nil, err
	}
	payload = append(payload, '\n')

	c.writeMu.Lock()
	_, writeErr := c.conn.Write(payload)
	c.writeMu.Unlock()
	if writeErr != nil {
		c.drop(id)
		c.fail(fmt.Errorf("write to mpv IPC socket: %w", writeErr))
		return nil, writeErr
	}

	select {
	case message, ok := <-reply:
		if !ok {
			return nil, c.transportError()
		}
		if message.Error != "" && message.Error != "success" {
			return nil, fmt.Errorf("mpv %s: %s", commandName(args), message.Error)
		}
		return message.Data, nil
	case <-ctx.Done():
		// The reply is discarded by the reader; mpv has no way to cancel an
		// issued command, and the caller must not wait for it.
		c.drop(id)
		return nil, fmt.Errorf("mpv %s: %w", commandName(args), ctx.Err())
	case <-c.done:
		return nil, c.transportError()
	}
}

func (c *ipcConn) read() {
	scanner := bufio.NewScanner(c.conn)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var message ipcMessage
		if json.Unmarshal(scanner.Bytes(), &message) != nil {
			continue
		}
		if message.RequestID != nil {
			c.pendingMu.Lock()
			reply, ok := c.pending[*message.RequestID]
			delete(c.pending, *message.RequestID)
			c.pendingMu.Unlock()
			if ok {
				reply <- message
			}
			continue
		}
		if message.Event != "" && c.events != nil {
			c.events(message)
		}
	}
	c.fail(errors.New("mpv closed its IPC socket"))
}

// fail makes the transport permanently unusable. It is called by the reader on
// connection loss and by close on a deliberate shutdown.
func (c *ipcConn) fail(cause error) {
	c.once.Do(func() {
		c.pendingMu.Lock()
		c.closed = true
		c.termErr = cause
		for id, reply := range c.pending {
			delete(c.pending, id)
			close(reply)
		}
		c.pendingMu.Unlock()
		_ = c.conn.Close()
		close(c.done)
	})
}

func (c *ipcConn) close() { c.fail(errors.New("mpv IPC transport closed")) }

func (c *ipcConn) drop(id uint64) {
	c.pendingMu.Lock()
	delete(c.pending, id)
	c.pendingMu.Unlock()
}

func (c *ipcConn) transportError() error {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	if c.termErr != nil {
		return c.termErr
	}
	return errIPCClosed
}

// Failed reports whether the transport is unusable. The client uses it to stop
// publishing once mpv is gone.
func (c *ipcConn) Failed() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

func commandName(args []any) string {
	if len(args) > 0 {
		if name, ok := args[0].(string); ok {
			return name
		}
	}
	return "command"
}
