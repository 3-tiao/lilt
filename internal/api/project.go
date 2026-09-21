package api

import "github.com/caiguo/lilt/core"

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
