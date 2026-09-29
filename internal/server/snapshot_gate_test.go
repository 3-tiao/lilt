package server

import (
	"testing"

	"github.com/caiguo/lilt/internal/api"
)

// The snapshot content gate dedups on the exact wire bytes of the event data:
// an identical snapshot must not republish and must not consume a sequence,
// while a changed one publishes exactly once (OQ34).
func TestSnapshotGateSuppressesIdenticalAndKeepsSequence(t *testing.T) {
	s, _ := startTestServer(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	base := s.sequence
	data := sourcesChangedData{Sources: []api.SourceDescriptor{{ID: "gate", Label: "Gate", Availability: api.AvailabilityReady}}}
	if !s.publishSnapshotLocked("sources.changed", data) {
		t.Fatal("first snapshot did not publish")
	}
	afterFirst := s.sequence
	if afterFirst != base+1 {
		t.Fatalf("first publish sequence = %d, want %d", afterFirst, base+1)
	}
	if s.publishSnapshotLocked("sources.changed", data) {
		t.Fatal("identical snapshot republished")
	}
	if s.sequence != afterFirst {
		t.Fatalf("suppressed snapshot consumed a sequence: %d -> %d", afterFirst, s.sequence)
	}
	changed := sourcesChangedData{Sources: []api.SourceDescriptor{{ID: "gate", Label: "Gate", Availability: api.AvailabilityDegraded, Reason: "later"}}}
	if !s.publishSnapshotLocked("sources.changed", changed) {
		t.Fatal("changed snapshot did not publish")
	}
	if s.sequence != afterFirst+1 {
		t.Fatalf("changed publish sequence = %d, want %d", s.sequence, afterFirst+1)
	}
}

// The flow summary is part of the signed content: the same flow must publish
// its pending and terminal steps, a new flow with an unchanged authorization
// status must not be swallowed, and a byte-identical payload is suppressed.
func TestSnapshotGateDistinguishesAuthorizationFlow(t *testing.T) {
	s, _ := startTestServer(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	pending := authorizationChangedData{
		Authorization: api.SourceAuthorization{Source: api.SourceAppleMusic, Status: "pending"},
		Flow:          &authorizationFlowSummary{FlowID: "f1", Source: api.SourceAppleMusic, Status: "pending"},
	}
	if !s.publishSnapshotLocked("authorization.changed", pending) {
		t.Fatal("pending flow did not publish")
	}
	if s.publishSnapshotLocked("authorization.changed", pending) {
		t.Fatal("duplicate pending flow republished")
	}
	terminal := authorizationChangedData{
		Authorization: api.SourceAuthorization{Source: api.SourceAppleMusic, Status: "authorized"},
		Flow:          &authorizationFlowSummary{FlowID: "f1", Source: api.SourceAppleMusic, Status: "authorized"},
	}
	if !s.publishSnapshotLocked("authorization.changed", terminal) {
		t.Fatal("same-flow terminal step was suppressed")
	}
	newFlow := authorizationChangedData{
		Authorization: api.SourceAuthorization{Source: api.SourceAppleMusic, Status: "authorized"},
		Flow:          &authorizationFlowSummary{FlowID: "f2", Source: api.SourceAppleMusic, Status: "authorized"},
	}
	if !s.publishSnapshotLocked("authorization.changed", newFlow) {
		t.Fatal("new flow with an unchanged status was suppressed")
	}
}
