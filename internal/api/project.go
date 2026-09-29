package api

import "github.com/3-tiao/lilt/core"

// ProjectCoreItem converts a domain item into its public shape for a source.
// It is the only core → wire item projection: every layer (server handlers,
// client library reads, TUI mirrors) goes through it, so one item can never be
// named two ways depending on where it was read.
//
// The identity comes from NewIdentity. Radio reports its stable normalized
// stream URL; the catalog sources keep their non-canonical display fields.
func ProjectCoreItem(item core.Item, source SourceID) Item {
	// Display kind comes from the item itself: synthetic rows (section headers)
	// carry a kind that is not part of the playable enum and must survive
	// projection untouched.
	displayKind := item.Kind
	if displayKind == "" {
		displayKind = defaultKind(source)
	}
	identity := NewIdentity(source, item.Kind, item.ID, item.URL)
	projected := Item{
		Source:     source,
		Kind:       displayKind,
		Ref:        item.Ref,
		URL:        item.URL,
		Title:      item.Title,
		Artist:     item.Artist,
		Album:      item.Album,
		DurationMs: item.DurationMs,
		PreviewURL: item.PreviewURL,
	}
	if identity.StableID != "" {
		projected.Source = identity.Source
		projected.Kind = identity.Kind
		projected.ID = identity.StableID
		projected.ProviderID = identity.ProviderID
		if projected.Ref == "" {
			projected.Ref = identity.Ref
		}
	}
	if identity.Source == SourceRadio {
		projected.URL = identity.StreamURL
		projected.Radio = ProjectRadioMetadata(item.Radio)
	}
	return projected
}

// ProjectRadioMetadata converts directory metadata into its public shape.
func ProjectRadioMetadata(metadata *core.RadioMetadata) *RadioMetadata {
	if metadata == nil {
		return nil
	}
	return &RadioMetadata{
		Origin:        metadata.Origin,
		StationUUID:   metadata.StationUUID,
		Tags:          metadata.Tags,
		Languages:     metadata.Languages,
		Country:       metadata.Country,
		CountryCode:   metadata.CountryCode,
		Codec:         metadata.Codec,
		Bitrate:       metadata.Bitrate,
		HLS:           metadata.HLS,
		Votes:         metadata.Votes,
		ClickCount:    metadata.ClickCount,
		ClickTrend:    metadata.ClickTrend,
		LastCheckOK:   metadata.LastCheckOK,
		LastCheckTime: metadata.LastCheckTime,
	}
}

// appleAccountStatusChecking is the Apple provider's wire marker for an
// authorized session whose async subscription read has not settled yet
// (internal/server/auth.go, docs/client-api/models.md §7). The core field
// keeps that state as the empty string — "no conclusion yet" — which is how
// the helper's own unsettled reads arrive too.
const appleAccountStatusChecking = "checking"

// ProjectAuthorization projects a wire authorization into the core account
// model. It is the only wire → core authorization projection: the client
// library's startup read and the TUI's watch path both go through it, so a
// live snapshot can never drop the account conclusion. Details is namespaced
// source-specific data, so only the Apple Music keys documented in
// docs/client-api/models.md are read (accountStatus / canPlayCatalogContent /
// hasCloudLibraryEnabled); every other source's details stay opaque. Reading
// them is what lets the account summary warn from the live snapshot alone:
// without it a live "authorized" event dropped the account conclusion and read
// as "Account: ready" for a subscription-limited session.
func ProjectAuthorization(value SourceAuthorization) core.AuthorizationStatus {
	status := core.AuthorizationStatus{Status: value.Status, AccountLabel: value.AccountLabel}
	if value.Source != SourceAppleMusic {
		return status
	}
	if accountStatus, _ := value.Details["accountStatus"].(string); accountStatus != "" && accountStatus != appleAccountStatusChecking {
		status.AccountStatus = accountStatus
	}
	if canPlay, ok := value.Details["canPlayCatalogContent"].(bool); ok {
		status.CanPlayCatalogContent = canPlay
	}
	if cloudLibrary, ok := value.Details["hasCloudLibraryEnabled"].(bool); ok {
		status.HasCloudLibraryEnabled = cloudLibrary
	}
	return status
}
