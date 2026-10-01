package server

import (
	"crypto/sha256"
	"encoding/json"
	"net"
	"sync"

	"github.com/3-tiao/lilt/internal/api"
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

// sourcesChangedData is the typed payload of sources.changed: the full
// authoritative descriptor snapshot.
type sourcesChangedData struct {
	Sources []api.SourceDescriptor `json:"sources"`
}

// authorizationFlowSummary is the strict {flowId,source,status} projection of
// a flow; watch.md forbids anything richer (no interaction URL, device code,
// account info, or provider details).
type authorizationFlowSummary struct {
	FlowID string       `json:"flowId"`
	Source api.SourceID `json:"source"`
	Status string       `json:"status"`
}

// authorizationChangedData is the typed payload of authorization.changed: the
// full public SourceAuthorization plus the optional flow summary.
type authorizationChangedData struct {
	Authorization api.SourceAuthorization   `json:"authorization"`
	Flow          *authorizationFlowSummary `json:"flow,omitempty"`
}

// nextSequence assigns the next server-wide sequence. Callers hold s.mu.
func (s *Server) nextSequenceLocked() uint64 {
	s.sequence++
	return s.sequence
}

// publishLocked broadcasts an event at the current sequence. Callers hold s.mu.
func (s *Server) publishLocked(event string, data any) {
	s.publishRawLocked(event, mustRaw(data))
}

// publishRawLocked broadcasts an already-encoded event at the current
// sequence. Callers hold s.mu.
func (s *Server) publishRawLocked(event string, data json.RawMessage) {
	if s.watchers == nil {
		// Minimal servers (unit tests) have no watch hub; there is nobody to
		// notify and no sequence to advance.
		return
	}
	s.watchers.publish(api.Event{
		Event:    event,
		Sequence: s.sequence,
		Data:     data,
	})
	if s.debugf != nil {
		s.debugf("watch.publish", map[string]any{"event": event, "sequence": s.sequence})
	}
}

// publishSnapshotLocked publishes one of the snapshot-shaped events
// (sources.changed, authorization.changed) through the server's content gate.
// The gate signature covers only the event's wire data — never `event` or
// `sequence` — computed from the same encoded bytes that go out, so a snapshot
// identical to the last published one is neither republished nor given a
// sequence number. The first publish for an event always goes out: that is
// what corrects subscribers that read state too early (warm-up). Encoding
// failure is an internal error: the event is not published with empty data and
// the sequence is not advanced. Occurrence-shaped events (server.warning,
// engine.restarted, playback/state changes) carry causal semantics and MUST
// NOT pass through this gate. Callers hold s.mu; it returns whether the event
// was published.
func (s *Server) publishSnapshotLocked(event string, data any) bool {
	encoded, err := json.Marshal(data)
	if err != nil {
		if s.logf != nil {
			s.logf("watch.publish_failed", map[string]any{"event": event, "error": err.Error()})
		}
		return false
	}
	signature := sha256.Sum256(encoded)
	if last, published := s.lastPublished[event]; published && last == signature {
		if s.debugf != nil {
			s.debugf("watch.publish_suppressed", map[string]any{"event": event})
		}
		return false
	}
	if s.lastPublished == nil {
		s.lastPublished = map[string][32]byte{}
	}
	s.lastPublished[event] = signature
	s.sequence++
	s.publishRawLocked(event, encoded)
	return true
}

// publishSourcesChangedLocked publishes a fresh full descriptor snapshot as
// sources.changed through the content gate. Callers hold s.mu; it returns
// whether the event was published.
func (s *Server) publishSourcesChangedLocked() bool {
	return s.publishSnapshotLocked("sources.changed", sourcesChangedData{Sources: s.sourceDescriptors()})
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
		_ = json.NewEncoder(conn).Encode(s.stampInstance(s.fail(request.RequestID, err)))
		return
	}
	topicSet := map[string]bool{}
	if len(params.Topics) > 0 {
		topicSet = make(map[string]bool, len(params.Topics))
		for _, topic := range params.Topics {
			if !knownTopic(topic) {
				_ = json.NewEncoder(conn).Encode(s.stampInstance(s.fail(request.RequestID, api.Errorf(api.CodeInvalidRequest, "unknown watch topic %q", topic))))
				return
			}
			topicSet[topic] = true
		}
	}

	if s.debugf != nil {
		s.debugf("server.request", map[string]any{
			"requestId": request.RequestID,
			"command":   request.Command,
			"ok":        true,
			"stream":    true,
		})
	}
	// Prefetch slow provider projections outside the command lock. A change
	// during the read invalidates them. After three collisions, ask the client
	// to reconnect instead of putting provider I/O under the playback lock.
	var snapshot api.WatchSnapshot
	var client *watchClient
	for attempt := 0; attempt < 3; attempt++ {
		s.mu.Lock()
		before := s.sequence
		s.mu.Unlock()
		if len(topicSet) == 0 || topicSet["sources"] {
			snapshot.Sources = s.sourceDescriptors()
		}
		if len(topicSet) == 0 || topicSet["authorization"] {
			snapshot.Authorizations = s.authorizations()
		}
		s.mu.Lock()
		if s.sequence != before {
			s.mu.Unlock()
			if attempt == 2 {
				_ = json.NewEncoder(conn).Encode(s.stampInstance(s.fail(request.RequestID,
					api.Errorf(api.CodeSessionUnavailable, "watch snapshot changed while connecting; retry"))))
				return
			}
			continue
		}
		snapshot.Sequence = s.sequence
		queueRevision := s.queueRevision
		activeSource := s.publicActiveSourceLocked()
		state, _ := s.engineStateLocked()
		if state != nil {
			snapshot.Playback = s.projectState(*state, activeSource, snapshot.Sequence, queueRevision)
		}
		if params.IncludeState {
			value := s.appState()
			snapshot.State = &value
		}
		if s.activity == nil && s.activityPath != "" {
			snapshot.Warning = &api.WatchWarning{
				Code:    api.CodeStorageUnavailable,
				Message: "the activity store is unavailable; favorites and history cannot be read or changed",
			}
		}
		if len(topicSet) > 0 {
			client = s.watchers.register(topicSet)
		} else {
			client = s.watchers.register(nil)
		}
		s.mu.Unlock()
		break
	}
	defer s.watchers.unregister(client)

	if err := json.NewEncoder(conn).Encode(s.stampInstance(api.Success(request.RequestID, snapshot))); err != nil {
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
