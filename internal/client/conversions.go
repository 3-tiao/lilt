package client

import (
	"strings"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/radio"
)

func providerIDOf(item api.Item) string {
	if item.ProviderID != "" {
		return item.ProviderID
	}
	id := item.ID
	id = strings.TrimPrefix(id, string(item.Source)+":")
	if colon := strings.Index(id, ":"); colon >= 0 {
		id = id[colon+1:]
	}
	return id
}

func toCoreItems(items []api.Item) []core.Item {
	converted := make([]core.Item, 0, len(items))
	for _, item := range items {
		converted = append(converted, toCoreItem(item))
	}
	return converted
}

func toCoreItem(item api.Item) core.Item {
	coreItem := core.Item{
		Source:     string(item.Source),
		Kind:       item.Kind,
		Ref:        item.Ref,
		Title:      item.Title,
		Artist:     item.Artist,
		URL:        item.URL,
		PreviewURL: item.PreviewURL,
		Radio:      toCoreRadio(item.Radio),
	}
	if item.Source == api.SourceRadio {
		coreItem.ID = item.ID
		if coreItem.URL == "" {
			coreItem.URL = strings.TrimPrefix(item.ID, string(api.SourceRadio)+":")
		}
	} else {
		coreItem.ID = providerIDOf(item)
	}
	return coreItem
}

func toCoreRadio(metadata *api.RadioMetadata) *core.RadioMetadata {
	if metadata == nil {
		return nil
	}
	return &core.RadioMetadata{
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

func toCoreState(state api.PlaybackState) core.PlaybackState {
	converted := core.PlaybackState{
		Source:        string(state.Source),
		Position:      state.Position,
		Duration:      state.Duration,
		Status:        state.Status,
		AudioVariant:  state.AudioVariant,
		Format:        state.Format,
		Available:     state.Available,
		Shuffle:       state.Shuffle,
		Repeat:        state.Repeat,
		IsLive:        state.IsLive,
		Mode:          state.Mode,
		QueueIndex:    state.QueueIndex,
		QueueRevision: state.QueueRevision,
		Queue:         toCoreItems(state.Queue),
	}
	if state.Track != nil {
		track := toCoreItem(*state.Track)
		converted.Track = &track
	}
	if state.PlaybackError != nil {
		converted.Error = *state.PlaybackError
	}
	if state.StreamTitle != nil {
		converted.StreamTitle = *state.StreamTitle
	}
	if state.StreamArtist != nil {
		converted.StreamArtist = *state.StreamArtist
	}
	return converted
}

func toAPIItem(item core.Item, source api.SourceID) api.Item {
	kind := item.Kind
	if kind == "" {
		kind = api.KindSong
	}
	projected := api.Item{
		Source:     source,
		Kind:       kind,
		ProviderID: item.ID,
		URL:        item.URL,
		Title:      item.Title,
		Artist:     item.Artist,
		PreviewURL: item.PreviewURL,
	}
	if source == api.SourceRadio {
		url := item.URL
		if url == "" {
			url = strings.TrimPrefix(item.ID, string(api.SourceRadio)+":")
		}
		projected.ProviderID = ""
		projected.ID = api.RadioRef(url)
		projected.Ref = url
		projected.URL = url
		projected.Radio = toAPIRadio(item.Radio)
		return projected
	}
	providerID := item.ID
	prefix := string(source) + ":" + kind + ":"
	if strings.HasPrefix(providerID, prefix) {
		providerID = strings.TrimPrefix(providerID, prefix)
	}
	projected.ProviderID = providerID
	projected.ID = string(source) + ":" + providerID
	projected.Ref = item.Ref
	if projected.Ref == "" {
		projected.Ref = prefix + providerID
	}
	return projected
}

func toAPIRadio(metadata *core.RadioMetadata) *api.RadioMetadata {
	if metadata == nil {
		return nil
	}
	return &api.RadioMetadata{
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

func toStation(item api.Item) radio.Station {
	station := radio.Station{
		Name: item.Title,
		URL:  item.URL,
	}
	if item.Radio != nil {
		station.StationUUID = item.Radio.StationUUID
		station.Country = item.Radio.Country
		station.CountryCode = item.Radio.CountryCode
		station.Tags = item.Radio.Tags
		station.Languages = item.Radio.Languages
		station.Codec = item.Radio.Codec
		station.Bitrate = item.Radio.Bitrate
		station.HLS = item.Radio.HLS
		station.Votes = item.Radio.Votes
		station.ClickCount = item.Radio.ClickCount
		station.ClickTrend = item.Radio.ClickTrend
		station.LastCheckOK = item.Radio.LastCheckOK
		station.LastCheckTime = item.Radio.LastCheckTime
	}
	return station
}
