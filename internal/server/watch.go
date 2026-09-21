package server

import (
	"encoding/json"
	"net"
	"sync"

	"github.com/caiguo/lilt/internal/api"
)

var eventTopics = map[string]string{
	"playback.changed":      "playback",
	"state.changed":         "state",
	"sources.changed":       "sources",
	"authorization.changed": "authorization",
	"engine.restarted":      "engine",
	"server.warning":        "server",
	"server.shuttingDown":   "server",
}

var alwaysDelivered = map[string]bool{
	"server.warning":      true,
	"server.shuttingDown": true,
}

type watchClient struct {
	topics    map[string]bool // nil means all topics
	events    chan api.Event
	closeOnce sync.Once
	closed    chan struct{}
}

func (c *watchClient) send(event api.Event) {
	select {
	case c.events <- event:
	case <-c.closed:
	default:
		// A slow client on a semantic event must be disconnected so it can
		// reconnect and resync rather than silently miss changes.
		c.close()
	}
}

// close signals the reader to stop. It never closes the events channel because
// publishers may still hold the client and send to it; a send on a closed
// channel would panic even inside a select.
func (c *watchClient) close() {
	c.closeOnce.Do(func() {
		close(c.closed)
	})
}

func (c *watchClient) accepts(eventName string) bool {
	if alwaysDelivered[eventName] || c.topics == nil {
		return true
	}
	topic, ok := eventTopics[eventName]
	if !ok {
		return false
	}
	return c.topics[topic]
}

type watchHub struct {
	mu      sync.Mutex
	nextID  uint64
	clients map[uint64]*watchClient
}

func newWatchHub() *watchHub {
	return &watchHub{clients: make(map[uint64]*watchClient)}
}

func (h *watchHub) register(topics map[string]bool) *watchClient {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextID++
	client := &watchClient{topics: topics, events: make(chan api.Event, 64), closed: make(chan struct{})}
	h.clients[h.nextID] = client
	return client
}

func (h *watchHub) unregister(client *watchClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, existing := range h.clients {
		if existing == client {
			delete(h.clients, id)
			break
		}
	}
	client.close()
}

func (h *watchHub) publish(event api.Event) {
	h.mu.Lock()
	clients := make([]*watchClient, 0, len(h.clients))
	for _, client := range h.clients {
		clients = append(clients, client)
	}
	h.mu.Unlock()
	for _, client := range clients {
		if client.accepts(event.Event) {
			client.send(event)
		}
	}
}

func (h *watchHub) closeAll() {
	h.mu.Lock()
	clients := make([]*watchClient, 0, len(h.clients))
	for id, client := range h.clients {
		clients = append(clients, client)
		delete(h.clients, id)
	}
	h.mu.Unlock()
	for _, client := range clients {
		client.close()
	}
}

// nextSequence assigns the next server-wide sequence. Callers hold s.mu.
func (s *Server) nextSequenceLocked() uint64 {
	s.sequence++
	return s.sequence
}

// publishLocked broadcasts an event at the current sequence. Callers hold s.mu.
func (s *Server) publishLocked(event string, data any) {
	if s.watchers == nil {
		// Minimal servers (unit tests) have no watch hub; there is nobody to
		// notify and no sequence to advance.
		return
	}
	s.watchers.publish(api.Event{
		Event:    event,
		Sequence: s.sequence,
		Data:     mustRaw(data),
	})
}

func mustRaw(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return encoded
}

// startEngineWatch subscribes to the current engine's notifications. It is a
// no-op for engines without subscriptions.
func (s *Server) startEngineWatch() {
	s.mu.Lock()
	engine := s.engine
	s.mu.Unlock()
	if engine == nil {
		return
	}
	s.watchEngine(engine)
}

// serveWatch handles one long-lived session.watch connection.
func (s *Server) serveWatch(conn *net.UnixConn, request api.Request) {
	var params struct {
		IncludeState bool     `json:"includeState"`
		Topics       []string `json:"topics"`
	}
	if err := api.DecodeParams(request.Params, &params); err != nil {
		_ = json.NewEncoder(conn).Encode(s.fail(request.RequestID, err))
		return
	}
	topicSet := map[string]bool{}
	if len(params.Topics) > 0 {
		topicSet = make(map[string]bool, len(params.Topics))
		for _, topic := range params.Topics {
			if !knownTopic(topic) {
				_ = json.NewEncoder(conn).Encode(s.fail(request.RequestID, api.Errorf(api.CodeInvalidRequest, "unknown watch topic %q", topic)))
				return
			}
			topicSet[topic] = true
		}
	}

	// Sequence capture and watcher registration share one s.mu boundary. Slow
	// source/authorization projections run after registration: any concurrent
	// mutation is already queued as sequence > S, so the stream cannot lose the
	// change and playback controls are not held behind those helper calls.
	s.mu.Lock()
	sequence := s.sequence
	queueRevision := s.queueRevision
	activeSource := s.publicActiveSourceLocked()
	state, _ := s.engineStateLocked()
	var appState *api.AppState
	if params.IncludeState {
		value := s.appState()
		appState = &value
	}
	activityUnavailable := s.activity == nil && s.activityPath != ""
	var client *watchClient
	if len(topicSet) > 0 {
		client = s.watchers.register(topicSet)
	} else {
		client = s.watchers.register(nil)
	}
	s.mu.Unlock()
	defer s.watchers.unregister(client)

	snapshot := api.WatchSnapshot{Sequence: sequence}
	if state != nil {
		snapshot.Playback = s.projectState(*state, activeSource, sequence, queueRevision)
	}
	snapshot.State = appState
	if len(topicSet) == 0 || topicSet["sources"] {
		snapshot.Sources = s.sourceDescriptors()
	}
	if len(topicSet) == 0 || topicSet["authorization"] {
		snapshot.Authorizations = s.authorizations()
	}
	if activityUnavailable {
		snapshot.Warning = &api.WatchWarning{
			Code:    api.CodeStorageUnavailable,
			Message: "the activity store is unavailable; favorites and history cannot be read or changed",
		}
	}

	if err := json.NewEncoder(conn).Encode(api.Success(request.RequestID, snapshot)); err != nil {
		return
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		buffer := make([]byte, 256)
		for {
			if _, err := conn.Read(buffer); err != nil {
				return
			}
		}
	}()

	for {
		select {
		case event := <-client.events:
			if err := json.NewEncoder(conn).Encode(event); err != nil {
				return
			}
		case <-client.closed:
			return
		case <-done:
			return
		case <-s.closed:
			return
		}
	}
}

func knownTopic(topic string) bool {
	switch topic {
	case "playback", "state", "sources", "authorization", "engine", "server":
		return true
	}
	return false
}
