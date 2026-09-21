package server

import (
	"context"

	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/player"
)

// appleResourceClient returns the Apple Music resource runtime. It is separate
// from s.engine: the latter is the exclusive MusicKit playback backend and is
// intentionally terminated when AudioEngine takes over.
//
// Tests that only inject one fake Engine retain a narrow fallback while the
// production command wires AppleResourceFactory to a dedicated helper client.
func (s *Server) appleResourceClient(_ context.Context) (AppleResourceClient, *api.Error) {
	s.appleResourceMu.Lock()
	defer s.appleResourceMu.Unlock()
	if s.appleResource != nil {
		return s.appleResource, nil
	}
	if s.appleResourceFactory != nil {
		resource, err := s.appleResourceFactory()
		if err != nil || resource == nil {
			return nil, api.Errorf(api.CodeSourceUnavailable, "Apple Music resources are unavailable")
		}
		s.appleResource = resource
		return resource, nil
	}
	if resource, ok := s.currentEngine().(AppleResourceClient); ok && resource != nil {
		return resource, nil
	}
	return nil, api.Errorf(api.CodeSourceUnavailable, "Apple Music resources are unavailable")
}

// noteAppleResourceFailure drops an unusable helper transport so the next
// resource request creates a fresh client. Provider/API errors keep the current
// client: they describe a request failure, not a broken runtime.
func (s *Server) noteAppleResourceFailure(resource AppleResourceClient, err error) {
	if !player.IsTransportError(err) {
		return
	}
	s.invalidateAppleResource(resource)
}

// mapAppleResourceError preserves stable helper errors while keeping a failed
// read-only helper independent from playback-engine restart supervision.
func (s *Server) mapAppleResourceError(resource AppleResourceClient, err error) *api.Error {
	if err == nil {
		return nil
	}
	if player.IsTransportError(err) {
		s.invalidateAppleResource(resource)
		return api.Errorf(api.CodeSourceUnavailable, "Apple Music resources are unavailable")
	}
	if rpcErr, ok := err.(*player.RPCError); ok {
		return api.Errorf(mapHelperCode(rpcErr.Code), "%s", rpcErr.Error()).WithDetails(map[string]any{"providerCode": rpcErr.Code})
	}
	return api.Errorf(api.CodeSearchFailed, "%v", err)
}

// invalidateAppleResource never changes the active playback backend.
func (s *Server) invalidateAppleResource(resource AppleResourceClient) {
	s.appleResourceMu.Lock()
	if s.appleResource != resource {
		s.appleResourceMu.Unlock()
		return
	}
	s.appleResource = nil
	s.appleResourceMu.Unlock()
	if closer, ok := resource.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
}
