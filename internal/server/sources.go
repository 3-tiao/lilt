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
	return []api.SourceDescriptor{
		s.appleDescriptor(ctx),
		s.radioDescriptor(),
	}
}

func (s *Server) appleDescriptor(ctx context.Context) api.SourceDescriptor {
	descriptor := api.SourceDescriptor{
		ID:           api.SourceAppleMusic,
		Label:        "Apple Music",
		Priority:     100,
		Availability: api.AvailabilityUnavailable,
		Reason:       "no playback engine",
		Capabilities: map[string]api.Capability{},
	}
	engine := s.currentEngine()
	if engine == nil {
		return descriptor
	}
	status, err := engine.Authorization(ctx)
	if err != nil {
		descriptor.Availability = api.AvailabilityDegraded
		descriptor.Reason = err.Error()
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
	available := func(reason string) api.Capability { return api.Capability{Available: true} }
	unavailable := func(reason string) api.Capability { return api.Capability{Available: false, Reason: reason} }
	descriptor.Capabilities[api.CapSearchSongs] = available("")
	descriptor.Capabilities[api.CapSearchPlaylists] = available("")
	descriptor.Capabilities[api.CapSearchStations] = available("")
	descriptor.Capabilities[api.CapLibrary] = available("")
	descriptor.Capabilities[api.CapRecommendations] = available("")
	descriptor.Capabilities[api.CapPlaybackPreview] = available("")
	if full {
		descriptor.Capabilities[api.CapPlaybackFull] = available("")
		descriptor.Capabilities[api.CapQueue] = available("")
		descriptor.Capabilities[api.CapShuffle] = available("")
		descriptor.Capabilities[api.CapRepeat] = available("")
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

func (s *Server) radioDescriptor() api.SourceDescriptor {
	descriptor := api.SourceDescriptor{
		ID:           api.SourceRadio,
		Label:        "Radio",
		Priority:     50,
		Availability: api.AvailabilityReady,
		Capabilities: map[string]api.Capability{},
	}
	if s.radio == nil {
		descriptor.Capabilities[api.CapSearchRadio] = api.Capability{Available: false, Reason: "radio directory unavailable"}
	} else {
		descriptor.Capabilities[api.CapSearchRadio] = api.Capability{Available: true}
	}
	if s.currentEngine() == nil {
		descriptor.Capabilities[api.CapPlaybackStream] = api.Capability{Available: false, Reason: "no stream engine"}
	} else {
		descriptor.Capabilities[api.CapPlaybackStream] = api.Capability{Available: true}
	}
	descriptor.Available = anyAvailable(descriptor.Capabilities)
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
