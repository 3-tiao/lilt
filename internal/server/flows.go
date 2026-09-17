package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/caiguo/lilt/internal/api"
)

// flowManager owns server-side authorization flows. Flows outlive the client
// that began them and never carry tokens or secrets.
type flowManager struct {
	mu        sync.Mutex
	flows     map[string]api.AuthorizationFlow
	active    map[api.SourceID]string
	completed map[string]time.Time
	cancels   map[string]context.CancelFunc
	retention time.Duration
}

const flowRetention = 10 * time.Minute

func newFlowManager() *flowManager {
	return &flowManager{
		flows:     make(map[string]api.AuthorizationFlow),
		active:    make(map[api.SourceID]string),
		completed: make(map[string]time.Time),
		cancels:   make(map[string]context.CancelFunc),
		retention: flowRetention,
	}
}

// setCancel records the cancel function for a pending flow's provider work.
func (m *flowManager) setCancel(flowID string, cancel context.CancelFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.flows[flowID]; ok {
		m.cancels[flowID] = cancel
	}
}

// cancelAll cancels every pending flow, used during shutdown.
func (m *flowManager) cancelAll() {
	m.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(m.cancels))
	for id, cancel := range m.cancels {
		cancels = append(cancels, cancel)
		delete(m.cancels, id)
	}
	m.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (m *flowManager) begin(source api.SourceID) (api.AuthorizationFlow, *api.Error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneLocked()
	if existing, ok := m.active[source]; ok {
		flow := m.flows[existing]
		return flow, api.Errorf(api.CodeAuthorizationInProgress, "authorization for %s is already in progress", source)
	}
	flow := api.AuthorizationFlow{
		FlowID:      newFlowID(),
		Source:      source,
		Status:      api.FlowPending,
		Interaction: api.Interaction{Type: api.InteractionSystemDialog},
	}
	m.flows[flow.FlowID] = flow
	m.active[source] = flow.FlowID
	return flow, nil
}

func (m *flowManager) complete(flowID string, flow api.AuthorizationFlow) {
	m.mu.Lock()
	defer m.mu.Unlock()
	flow.FlowID = flowID
	m.flows[flowID] = flow
	delete(m.active, flow.Source)
	delete(m.cancels, flowID)
	if flow.Status != api.FlowPending {
		m.completed[flowID] = time.Now()
	}
}

func (m *flowManager) get(flowID string) (api.AuthorizationFlow, *api.Error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneLocked()
	flow, ok := m.flows[flowID]
	if !ok {
		return api.AuthorizationFlow{}, api.Errorf(api.CodeAuthorizationFlowNotFound, "no authorization flow with id %q", flowID)
	}
	return flow, nil
}

func (m *flowManager) cancel(flowID string) (api.AuthorizationFlow, *api.Error) {
	m.mu.Lock()
	m.pruneLocked()
	flow, ok := m.flows[flowID]
	if !ok {
		m.mu.Unlock()
		return api.AuthorizationFlow{}, api.Errorf(api.CodeAuthorizationFlowNotFound, "no authorization flow with id %q", flowID)
	}
	if flow.Status != api.FlowPending {
		m.mu.Unlock()
		return flow, nil
	}
	flow.Status = api.FlowCancelled
	m.flows[flowID] = flow
	delete(m.active, flow.Source)
	m.completed[flowID] = time.Now()
	cancel := m.cancels[flowID]
	delete(m.cancels, flowID)
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return flow, nil
}

func (m *flowManager) pruneLocked() {
	now := time.Now()
	for id, at := range m.completed {
		if now.Sub(at) > m.retention {
			delete(m.completed, id)
			delete(m.flows, id)
		}
	}
}

func newFlowID() string {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("flow-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw[:])
}
