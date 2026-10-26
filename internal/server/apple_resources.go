package server

import (
	"context"
	"fmt"
	"time"

	"github.com/3-tiao/lilt/core"
	"github.com/3-tiao/lilt/internal/api"
	"github.com/3-tiao/lilt/internal/player"
)

// appleResourceClient returns the Apple Music resource runtime. It is separate
// from s.engine: the latter is the exclusive MusicKit playback backend and is
// intentionally terminated when AudioEngine takes over.
//
// Tests that only inject one fake Engine retain a narrow fallback while the
// production command wires AppleResourceFactory to a dedicated helper client.
func (s *Server) appleResourceClient(ctx context.Context) (AppleResourceClient, *api.Error) {
	s.appleResourceMu.Lock()
	if s.appleResource != nil {
		s.appleResourceMu.Unlock()
		return s.appleResource, nil
	}
	if s.appleResourceFactory != nil {
		resource, err := s.appleResourceFactory()
		if err != nil || resource == nil {
			s.appleResourceMu.Unlock()
			return nil, api.Errorf(api.CodeSourceUnavailable, "Apple Music resources are unavailable")
		}
		s.appleResource = resource
		// The resource runtime becoming reachable is a capability transition:
		// the helper starts lazily, so an earlier sources.list answered with
		// degraded Apple capabilities (usability batch 2026-09-21-r13). Watch
		// clients must receive the fresh descriptor snapshot now. Discovery and
		// watch projections may call this without s.mu, so schedule publication
		// after releasing the resource lock rather than assuming the command lock.
		first := !s.appleResourceReady
		s.appleResourceReady = true
		s.appleResourceMu.Unlock()
		if first {
			// Wait before publishing: the helper answers `authorized` before its
			// asynchronous subscription read lands, and a descriptor derived from
			// that half-read status tells every automation that Apple Music
			// cannot fully play. The wait is bounded, so a helper that never
			// settles still yields a snapshot instead of a hang.
			published := awaitSettledAuthorization(ctx, resource)
			s.publishAppleResourceChange()
			go s.publishAppleAccountSettled(resource, published)
		}
		return resource, nil
	}
	s.appleResourceMu.Unlock()
	if resource, ok := s.currentEngine().(AppleResourceClient); ok && resource != nil {
		return resource, nil
	}
	return nil, api.Errorf(api.CodeSourceUnavailable, "Apple Music resources are unavailable")
}

const (
	// appleAccountSettleWindow bounds how long resource creation waits for the
	// helper's asynchronous subscription read before publishing a descriptor
	// anyway. It stays inside the 5s `sources.list` budget.
	appleAccountSettleWindow = 2 * time.Second
	appleAccountSettlePoll   = 100 * time.Millisecond
)

// awaitSettledAuthorization waits, within appleAccountSettleWindow, for the
// freshly started resource runtime to finish its subscription read. The result
// is also the snapshot publishAppleAccountSettled compares against, so a wait
// that already reached the settled state publishes no redundant event. A failed
// read leaves the zero signature: the poll then publishes on its first
// observation of any settled state.
func awaitSettledAuthorization(ctx context.Context, resource AppleResourceClient) core.AuthorizationStatus {
	ctx, cancel := context.WithTimeout(ctx, appleAccountSettleWindow)
	defer cancel()
	var status core.AuthorizationStatus
	for {
		read, err := resource.Authorization(ctx)
		if err == nil {
			status = read
			if !appleAccountChecking(read) {
				return status
			}
		}
		select {
		case <-ctx.Done():
			return status
		case <-time.After(appleAccountSettlePoll):
		}
	}
}

// appleAccountChecking reports whether the helper has authorized but not yet
// settled its asynchronous subscription read. Empty AccountStatus means the
// answer is not in yet, not that the subscription is missing.
func appleAccountChecking(status core.AuthorizationStatus) bool {
	return status.Status == "authorized" && status.AccountStatus == ""
}

// publishAppleAccountSettled closes the gap the helper's asynchronous
// subscription read creates: right after the resource runtime starts,
// `Authorization` reports authorized with an empty AccountStatus, which the
// descriptor maps to "still being read" and unavailable full-playback
// capabilities. When that read settles (or the status otherwise changes),
// watch clients must receive a fresh descriptor snapshot. Bounded: the helper
// normally settles within a few seconds (usability batch 2026-09-21-r13).
func (s *Server) publishAppleAccountSettled(resource AppleResourceClient, published core.AuthorizationStatus) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	signature := appleAuthSignature(published)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		s.appleResourceMu.Lock()
		current := s.appleResource == resource && s.appleResource != nil
		s.appleResourceMu.Unlock()
		if !current {
			return
		}
		status, err := resource.Authorization(ctx)
		if err != nil {
			continue
		}
		next := appleAuthSignature(status)
		if next == signature {
			if status.Status == "authorized" && status.AccountStatus != "" {
				return // settled; nothing further can change this snapshot
			}
			continue
		}
		signature = next
		s.mu.Lock()
		s.publishSourcesChangedLocked()
		s.mu.Unlock()
		if status.Status == "authorized" && status.AccountStatus != "" {
			return
		}
	}
}

func appleAuthSignature(status core.AuthorizationStatus) string {
	return fmt.Sprintf("%s|%s|%t", status.Status, status.AccountStatus, status.CanPlayCatalogContent)
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

// invalidateAppleResource never changes the active playback backend. It marks
// the resource runtime gone so the next request can create a fresh one, and
// republishes the descriptor snapshot: capabilities that depended on the
// runtime (search/library) are no longer available.
func (s *Server) invalidateAppleResource(resource AppleResourceClient) {
	s.appleResourceMu.Lock()
	if s.appleResource != resource {
		s.appleResourceMu.Unlock()
		return
	}
	s.appleResource = nil
	s.appleResourceReady = false
	s.appleResourceMu.Unlock()
	if closer, ok := resource.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
	// A concurrent discovery query may invalidate the resource without s.mu.
	s.publishAppleResourceChange()
}

// publishAppleResourceChange serializes a capability transition with watch
// registration and allocates its own sequence. It runs asynchronously because
// some callers already hold s.mu while others (discovery/watch) do not.
func (s *Server) publishAppleResourceChange() {
	go func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		select {
		case <-s.closed:
			return
		default:
		}
		s.publishSourcesChangedLocked()
	}()
}
