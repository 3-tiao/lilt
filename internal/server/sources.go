package server

import (
	"context"
	"sort"
	"time"

	"github.com/caiguo/lilt/internal/api"
)

// sourceDescriptors reports each public source and its capability availability.
func (s *Server) sourceDescriptors() []api.SourceDescriptor {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return s.sourceDescriptorsCtx(ctx)
}

func (s *Server) sourceDescriptorsCtx(ctx context.Context) []api.SourceDescriptor {
	result := make([]api.SourceDescriptor, 0, len(s.providers)+1)
	for _, provider := range s.providers {
		result = append(result, provider.Descriptor(ctx))
	}
	result = append(result, s.radioDescriptor())
	sort.Slice(result, func(i, j int) bool {
		if result[i].Priority != result[j].Priority {
			return result[i].Priority > result[j].Priority
		}
		return result[i].ID < result[j].ID
	})
	return result
}

func (s *Server) appleDescriptor(ctx context.Context) api.SourceDescriptor {
	descriptor := api.SourceDescriptor{
		ID:           api.SourceAppleMusic,
		Label:        "Apple Music",
		Priority:     100,
		Availability: api.AvailabilityUnavailable,
		Reason:       "no playback engine",
		Description:  "Apple Music through the signed MusicKit helper. Search, library, and recommendations are declared whenever the helper is reachable; full playback, queue, shuffle, and repeat depend on authorization and an active subscription.",
		Capabilities: unavailableAppleCapabilities("no playback engine"),
	}
	engine := s.currentEngine()
	if engine == nil {
		return descriptor
	}
	status, err := engine.Authorization(ctx)
	if err != nil {
		descriptor.Availability = api.AvailabilityDegraded
		descriptor.Reason = err.Error()
		descriptor.Capabilities = unavailableAppleCapabilities(descriptor.Reason)
		return descriptor
	}
	authorized := status.Status == "authorized"
	full := authorized && status.CanPlayCatalogContent
	switch {
	case full:
		descriptor.Availability = api.AvailabilityReady
	case authorized:
		descriptor.Availability = api.AvailabilitySubscriptionRequired
		descriptor.Reason = "an active Apple Music subscription is required for full playback"
	case status.Status == "not_determined":
		descriptor.Availability = api.AvailabilityAuthorizationRequired
		descriptor.Reason = "Apple Music authorization has not been requested"
	default:
		descriptor.Availability = api.AvailabilityAuthorizationRequired
		descriptor.Reason = "Apple Music access is not granted"
	}
	available := func(description string) api.Capability {
		return api.Capability{Available: true, Description: description}
	}
	unavailable := func(reason string) api.Capability { return api.Capability{Available: false, Reason: reason} }
	descriptor.Capabilities[api.CapSearchSongs] = available("Search the Apple Music catalog for songs.")
	descriptor.Capabilities[api.CapSearchPlaylists] = available("Search the Apple Music catalog for playlists.")
	descriptor.Capabilities[api.CapSearchStations] = available("Search MusicKit radio stations.")
	descriptor.Capabilities[api.CapLibrary] = available("Read the user's cloud library playlists.")
	descriptor.Capabilities[api.CapRecommendations] = available("Read Apple Music recommendations.")
	descriptor.Capabilities[api.CapPlaybackPreview] = available("Play a 30-second preview without a subscription.")
	if full {
		descriptor.Capabilities[api.CapPlaybackFull] = available("Play full catalog and library tracks.")
		descriptor.Capabilities[api.CapQueue] = available("Edit the MusicKit playback queue.")
		descriptor.Capabilities[api.CapShuffle] = available("Toggle MusicKit shuffle.")
		descriptor.Capabilities[api.CapRepeat] = available("Set MusicKit repeat mode.")
	} else {
		reason := descriptor.Reason
		descriptor.Capabilities[api.CapPlaybackFull] = unavailable(reason)
		descriptor.Capabilities[api.CapQueue] = unavailable(reason)
		descriptor.Capabilities[api.CapShuffle] = unavailable(reason)
		descriptor.Capabilities[api.CapRepeat] = unavailable(reason)
	}
	descriptor.Available = anyAvailable(descriptor.Capabilities)
	return descriptor
}

func unavailableAppleCapabilities(reason string) map[string]api.Capability {
	capabilities := map[string]api.Capability{}
	for _, name := range []string{
		api.CapSearchSongs,
		api.CapSearchPlaylists,
		api.CapSearchStations,
		api.CapLibrary,
		api.CapRecommendations,
		api.CapPlaybackFull,
		api.CapPlaybackPreview,
		api.CapQueue,
		api.CapShuffle,
		api.CapRepeat,
	} {
		capabilities[name] = api.Capability{Reason: reason}
	}
	return capabilities
}

func (s *Server) radioDescriptor() api.SourceDescriptor {
	descriptor := api.SourceDescriptor{
		ID:           api.SourceRadio,
		Label:        "Radio",
		Priority:     50,
		Availability: api.AvailabilityReady,
		Description:  "Live radio streams from the Radio Browser directory plus the vendored builtin snapshot. Radio has no finite queue; use radio.search for discovery.",
		Capabilities: map[string]api.Capability{},
	}
	if s.radio == nil {
		descriptor.Capabilities[api.CapSearchRadio] = api.Capability{Available: false, Reason: "radio directory unavailable"}
	} else {
		descriptor.Capabilities[api.CapSearchRadio] = api.Capability{Available: true, Description: "Search Radio Browser and the builtin station snapshot."}
	}
	if s.currentEngine() == nil {
		descriptor.Capabilities[api.CapPlaybackStream] = api.Capability{Available: false, Reason: "no stream engine"}
	} else {
		descriptor.Capabilities[api.CapPlaybackStream] = api.Capability{Available: true, Description: "Play a live stream URL."}
	}
	descriptor.Available = anyAvailable(descriptor.Capabilities)
	if !descriptor.Available {
		descriptor.Availability = api.AvailabilityUnavailable
		descriptor.Reason = "radio directory and stream engine are unavailable"
	}
	return descriptor
}

func anyAvailable(capabilities map[string]api.Capability) bool {
	for _, capability := range capabilities {
		if capability.Available {
			return true
		}
	}
	return false
}

// authorizations returns the per-source authorization state.
func (s *Server) authorizations() []api.SourceAuthorization {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return s.authorizationsCtx(ctx)
}

func (s *Server) authorizationsCtx(ctx context.Context) []api.SourceAuthorization {
	sources := make([]api.SourceID, 0, len(s.authProviders))
	for source := range s.authProviders {
		sources = append(sources, source)
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i] < sources[j] })
	result := make([]api.SourceAuthorization, 0, len(sources))
	for _, source := range sources {
		result = append(result, s.authProviders[source].Describe(ctx))
	}
	return result
}

func mapAppleAuthStatus(status string) string {
	switch status {
	case "authorized":
		return api.AuthAuthorized
	case "denied", "restricted":
		return api.AuthDenied
	case "not_determined":
		return api.AuthNotDetermined
	default:
		return api.AuthError
	}
}
