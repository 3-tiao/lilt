package server

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/3-tiao/lilt/core"
	"github.com/3-tiao/lilt/internal/api"
	"github.com/3-tiao/lilt/internal/fakeengine"
)

// Provider gate (automatic half).
//
// Every public source the server exposes must be fully and consistently
// registered. This test runs under the normal `go test ./...` job, so a
// half-registered provider (a descriptor without auth, a capability that no
// longer matches its availability, an unknown capability name, or a reserved
// source that silently starts leaking) cannot land.
//
// The manual half — real-account verification and fixture coverage for search,
// refs, error mapping, and auth — lives in docs/testing/provider-admission.md.
// This file only enforces the structural contract that can be checked without
// credentials.

// reservedButUnimplemented lists contract-reserved source IDs that MUST NOT yet
// appear in sources.list or authorization.list. Adding a provider is a
// deliberate two-step: register it, then delete its entry here. If a source
// starts being exposed while still listed, this test fails and forces the
// author to remove it from the allowlist, at which point every consistency
// check below applies to it automatically.
var reservedButUnimplemented = map[api.SourceID]bool{}

var knownCapabilities = map[string]bool{
	api.CapSearchSongs:         true,
	api.CapSearchAlbums:        true,
	api.CapSearchPlaylists:     true,
	api.CapSearchStations:      true,
	api.CapSearchRadio:         true,
	api.CapSearchTrending:      true,
	api.CapSearchTrendingSongs: true,
	api.CapLibrary:             true,
	api.CapRecommendations:     true,
	api.CapPlaybackFull:        true,
	api.CapPlaybackPreview:     true,
	api.CapPlaybackStream:      true,
	api.CapQueue:               true,
	api.CapShuffle:             true,
	api.CapRepeat:              true,
}

var capabilitySegment = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

var knownAvailability = map[string]bool{
	api.AvailabilityReady:                 true,
	api.AvailabilityAuthorizationRequired: true,
	api.AvailabilitySubscriptionRequired:  true,
	api.AvailabilityUnavailable:           true,
	api.AvailabilityDegraded:              true,
}

var knownAuthStatus = map[string]bool{
	api.AuthNotRequired:   true,
	api.AuthNotDetermined: true,
	api.AuthPending:       true,
	api.AuthAuthorized:    true,
	api.AuthDenied:        true,
	api.AuthExpired:       true,
	api.AuthError:         true,
}

func TestProviderGateSourcesAndAuthorizationAreConsistent(t *testing.T) {
	server, socket := startTestServer(t)
	assertProviderGate(t, server, socket)
}

// assertProviderGate holds for any composition, not only the default one: a
// platform that swaps in its own providers must pass the same structural
// contract, or the swap quietly breaks the capability-is-the-only-truth rule.
func assertProviderGate(t *testing.T, server *Server, socket string) {
	t.Helper()

	var descriptors []api.SourceDescriptor
	if response := call(t, socket, "sources.list", nil); !response.OK {
		t.Fatalf("sources.list failed: %+v", response.Error)
	} else if err := json.Unmarshal(response.Data, &descriptors); err != nil {
		t.Fatalf("decode sources.list: %v", err)
	}

	var auths []api.SourceAuthorization
	if response := call(t, socket, "authorization.list", nil); !response.OK {
		t.Fatalf("authorization.list failed: %+v", response.Error)
	} else if err := json.Unmarshal(response.Data, &auths); err != nil {
		t.Fatalf("decode authorization.list: %v", err)
	}

	bySource := make(map[api.SourceID]api.SourceDescriptor, len(descriptors))
	for _, descriptor := range descriptors {
		if _, dup := bySource[descriptor.ID]; dup {
			t.Fatalf("sources.list exposes %q twice", descriptor.ID)
		}
		if descriptor.ID == "" {
			t.Fatal("sources.list exposes a source with an empty id")
		}
		bySource[descriptor.ID] = descriptor
	}
	authBySource := make(map[api.SourceID]api.SourceAuthorization, len(auths))
	for _, auth := range auths {
		if _, dup := authBySource[auth.Source]; dup {
			t.Fatalf("authorization.list exposes %q twice", auth.Source)
		}
		authBySource[auth.Source] = auth
	}

	// sources.list and authorization.list must describe exactly the same set:
	// a source cannot be listed without an auth state, or authorized without
	// being listed.
	for id := range bySource {
		if _, ok := authBySource[id]; !ok {
			t.Fatalf("source %q is in sources.list but not authorization.list", id)
		}
	}
	for id := range authBySource {
		if _, ok := bySource[id]; !ok {
			t.Fatalf("source %q is in authorization.list but not sources.list", id)
		}
	}

	// Reserved sources must not be exposed before they are implemented.
	for id, reserved := range reservedButUnimplemented {
		if !reserved {
			continue
		}
		if _, exposed := bySource[id]; exposed {
			t.Fatalf("source %q is registered but still listed in reservedButUnimplemented; remove it from the allowlist so the provider gate applies to it", id)
		}
		if _, exposed := authBySource[id]; exposed {
			t.Fatalf("source %q has an auth provider but is still listed in reservedButUnimplemented; remove it from the allowlist", id)
		}
	}

	for id, descriptor := range bySource {
		if descriptor.Label == "" {
			t.Fatalf("source %q has an empty label", id)
		}
		if descriptor.Priority <= 0 {
			t.Fatalf("source %q has priority %d, want > 0", id, descriptor.Priority)
		}
		if !knownAvailability[descriptor.Availability] {
			t.Fatalf("source %q has unknown availability %q", id, descriptor.Availability)
		}
		if len(descriptor.Capabilities) == 0 {
			t.Fatalf("source %q declares no capabilities", id)
		}
		for name, capability := range descriptor.Capabilities {
			if !validCapabilityName(id, name) {
				t.Fatalf("source %q declares invalid capability %q", id, name)
			}
			if !capability.Available && capability.Reason == "" {
				t.Fatalf("source %q capability %q is unavailable without a reason", id, name)
			}
		}
		if _, declaresTrending := descriptor.Capabilities[api.CapSearchTrending]; declaresTrending {
			provider, ok := server.providers[id]
			if !ok {
				t.Fatalf("source %q declares search.trending but has no content provider", id)
			}
			if _, ok := provider.(TrendingProvider); !ok {
				t.Fatalf("source %q declares search.trending but does not implement TrendingProvider", id)
			}
			// A declared capability only has to work when it is available: an
			// unavailable descriptor still lists the key so the key set stays
			// stable across availability changes.
			if descriptor.Capabilities[api.CapSearchTrending].Available {
				if response := call(t, socket, "discovery.trending", map[string]any{"source": id, "type": "song", "limit": 1}); !response.OK {
					t.Fatalf("source %q declares available search.trending but discovery.trending failed: %+v", id, response.Error)
				}
			}
		}
		if _, declaresTrendingSongs := descriptor.Capabilities[api.CapSearchTrendingSongs]; declaresTrendingSongs {
			provider, ok := server.providers[id]
			if !ok {
				t.Fatalf("source %q declares search.trending.songs but has no content provider", id)
			}
			if _, ok := provider.(TrendingProvider); !ok {
				t.Fatalf("source %q declares search.trending.songs but does not implement TrendingProvider", id)
			}
			if descriptor.Capabilities[api.CapSearchTrendingSongs].Available {
				if response := call(t, socket, "discovery.trending", map[string]any{"source": id, "type": "song", "limit": 1}); !response.OK {
					t.Fatalf("source %q declares available search.trending.songs but discovery.trending type=song failed: %+v", id, response.Error)
				}
				// The kind-specific capability must not silently degrade: playlist
				// trending stays an explicit unsupported_command.
				if response := call(t, socket, "discovery.trending", map[string]any{"source": id, "type": "playlist", "limit": 1}); response.OK || response.Error.Code != api.CodeUnsupportedCommand {
					t.Fatalf("source %q declares song-only trending but playlist trending is not unsupported: %+v", id, response.Error)
				}
			}
		}
		if descriptor.Capabilities[api.CapSearchAlbums].Available {
			if response := call(t, socket, "discovery.search", map[string]any{"source": id, "term": "x", "type": "album", "limit": 1}); !response.OK {
				t.Fatalf("source %q declares available search.albums but discovery.search type=album failed: %+v", id, response.Error)
			}
		}
		if _, declaresRecommendations := descriptor.Capabilities[api.CapRecommendations]; declaresRecommendations {
			provider, ok := server.providers[id]
			if !ok {
				t.Fatalf("source %q declares recommendations but has no content provider", id)
			}
			if _, ok := provider.(RecommendationsProvider); !ok {
				t.Fatalf("source %q declares recommendations but does not implement RecommendationsProvider", id)
			}
			if descriptor.Capabilities[api.CapRecommendations].Available {
				if response := call(t, socket, "recommendations.list", map[string]any{"source": id, "limit": 1}); !response.OK {
					t.Fatalf("source %q declares available recommendations but recommendations.list failed: %+v", id, response.Error)
				}
			}
		}
		if got, want := descriptor.Available, capabilitiesAnyAvailable(descriptor.Capabilities); got != want {
			t.Fatalf("source %q available=%v but capabilities imply %v", id, got, want)
		}
		if auth := authBySource[id]; !knownAuthStatus[auth.Status] {
			t.Fatalf("source %q has unknown auth status %q", id, auth.Status)
		}
	}
}

func validCapabilityName(source api.SourceID, name string) bool {
	if knownCapabilities[name] {
		return true
	}
	prefix := string(source) + "."
	if len(name) <= len(prefix) || name[:len(prefix)] != prefix {
		return false
	}
	for _, segment := range strings.Split(name[len(prefix):], ".") {
		if !capabilitySegment.MatchString(segment) {
			return false
		}
	}
	return true
}

func TestValidCapabilityName(t *testing.T) {
	tests := []struct {
		source api.SourceID
		name   string
		want   bool
	}{
		{api.SourceAudius, api.CapPlaybackFull, true},
		{api.SourceAudius, "audius.reposts", true},
		{api.SourceAudius, "audius.queue.edit", true},
		{api.SourceAudius, "audius.", false},
		{api.SourceAudius, "audius..x", false},
		{api.SourceAudius, "audius.Upper", false},
		{api.SourceAudius, "audius.has space", false},
		{api.SourceAudius, "apple-music.library-extra", false},
		{api.SourceAudius, "unknown", false},
	}
	for _, test := range tests {
		if got := validCapabilityName(test.source, test.name); got != test.want {
			t.Errorf("validCapabilityName(%q, %q) = %v, want %v", test.source, test.name, got, test.want)
		}
	}
}

func TestAppleDescriptorWithoutEngineRetainsCapabilities(t *testing.T) {
	descriptor := (&Server{}).appleDescriptor(t.Context())
	if descriptor.Available {
		t.Fatal("Apple descriptor without an engine is available")
	}
	if len(descriptor.Capabilities) == 0 {
		t.Fatal("Apple descriptor without an engine has no capabilities")
	}
	for name, capability := range descriptor.Capabilities {
		if capability.Available || capability.Reason == "" {
			t.Errorf("capability %q = %+v, want unavailable with a reason", name, capability)
		}
	}
}

type descriptorEngine struct {
	*fakeengine.FakeEngine
	status core.AuthorizationStatus
}

func (e *descriptorEngine) Authorization(context.Context) (core.AuthorizationStatus, error) {
	return e.status, nil
}

func TestAppleDescriptorWhileAccountChecksDoesNotClaimSubscriptionRequired(t *testing.T) {
	// The helper's async subscription read has not settled: authorized with an
	// empty account status. The descriptor must say "checking", not report the
	// machine as subscription-less (batch 2026-09-19-watch-sync M2).
	// Authorized, but the helper's async subscription read has not landed.
	engine := &descriptorEngine{FakeEngine: fakeengine.NewFakeEngine(), status: core.AuthorizationStatus{Status: "authorized"}}
	descriptor := (&Server{engine: engine}).appleDescriptor(t.Context())
	if descriptor.Availability != api.AvailabilityDegraded {
		t.Fatalf("checking descriptor availability = %q, want degraded", descriptor.Availability)
	}
	if descriptor.Capabilities[api.CapPlaybackFull].Available {
		t.Fatal("checking descriptor reports playback.full available")
	}
	reason := descriptor.Capabilities[api.CapPlaybackFull].Reason
	if reason == "" || reason == "no playback engine" {
		t.Fatalf("checking capability reason = %q", reason)
	}
}

func TestAppleDescriptorClearsStaleReasonWhenReady(t *testing.T) {
	// The default descriptor carries Reason "no playback engine"; once the
	// engine reports a settled, subscribed account the stale reason must go.
	engine := &descriptorEngine{FakeEngine: fakeengine.NewFakeEngine(), status: core.AuthorizationStatus{Status: "authorized", AccountStatus: "ready", CanPlayCatalogContent: true, HasCloudLibraryEnabled: true}}
	descriptor := (&Server{engine: engine}).appleDescriptor(t.Context())
	if descriptor.Availability != api.AvailabilityReady {
		t.Fatalf("ready descriptor availability = %q", descriptor.Availability)
	}
	if descriptor.Reason != "" {
		t.Fatalf("ready descriptor Reason = %q, want empty", descriptor.Reason)
	}
	if !descriptor.Capabilities[api.CapPlaybackFull].Available {
		t.Fatal("ready descriptor reports playback.full unavailable")
	}
}

func TestRadioDescriptorWithoutServicesIsUnavailable(t *testing.T) {
	descriptor := (&Server{}).radioDescriptor()
	if descriptor.Available || descriptor.Availability != api.AvailabilityUnavailable {
		t.Errorf("radio descriptor = %+v, want unavailable", descriptor)
	}
}

func TestProviderGateKeepsSourceAndAuthorizationSetsEqualWithoutEngine(t *testing.T) {
	_, socket := startTestServerWithEngine(t, nil)

	var descriptors []api.SourceDescriptor
	if response := call(t, socket, "sources.list", nil); !response.OK {
		t.Fatalf("sources.list failed: %+v", response.Error)
	} else if err := json.Unmarshal(response.Data, &descriptors); err != nil {
		t.Fatalf("decode sources.list: %v", err)
	}
	var authorizations []api.SourceAuthorization
	if response := call(t, socket, "authorization.list", nil); !response.OK {
		t.Fatalf("authorization.list failed: %+v", response.Error)
	} else if err := json.Unmarshal(response.Data, &authorizations); err != nil {
		t.Fatalf("decode authorization.list: %v", err)
	}

	if len(descriptors) != len(authorizations) {
		t.Fatalf("sources=%d, authorizations=%d", len(descriptors), len(authorizations))
	}
	for _, descriptor := range descriptors {
		found := false
		for _, authorization := range authorizations {
			if authorization.Source == descriptor.ID {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("source %q is missing an authorization entry", descriptor.ID)
		}
	}
}

// capabilitiesAnyAvailable is the independent recomputation of the
// descriptor.Available invariant. It intentionally does not call the server's
// own helper so a change to that helper cannot hide an inconsistency.
func capabilitiesAnyAvailable(capabilities map[string]api.Capability) bool {
	for _, capability := range capabilities {
		if capability.Available {
			return true
		}
	}
	return false
}
