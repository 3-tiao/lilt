package client

import (
	"context"
	"encoding/json"

	"github.com/3-tiao/lilt/internal/api"
	"github.com/3-tiao/lilt/internal/radio"
)

func (c *Client) Countries(ctx context.Context) ([]radio.Country, error) {
	options, err := c.radioOptions(ctx, "country")
	if err != nil {
		return nil, err
	}
	countries := make([]radio.Country, 0, len(options))
	for _, option := range options {
		countries = append(countries, radio.Country{Code: option.Value, Name: option.Value, StationCount: option.Count})
	}
	return countries, nil
}

func (c *Client) Tags(ctx context.Context) ([]radio.Tag, error) {
	options, err := c.radioOptions(ctx, "tag")
	if err != nil {
		return nil, err
	}
	tags := make([]radio.Tag, 0, len(options))
	for _, option := range options {
		tags = append(tags, radio.Tag{Name: option.Value, StationCount: option.Count})
	}
	return tags, nil
}

func (c *Client) Languages(ctx context.Context) ([]radio.Language, error) {
	options, err := c.radioOptions(ctx, "language")
	if err != nil {
		return nil, err
	}
	languages := make([]radio.Language, 0, len(options))
	for _, option := range options {
		languages = append(languages, radio.Language{Name: option.Value, StationCount: option.Count})
	}
	return languages, nil
}

func (c *Client) radioOptions(ctx context.Context, facet string) ([]api.RadioOption, error) {
	response, err := c.Call(ctx, "radio.options", map[string]any{"facet": facet, "origin": api.OriginDirectory})
	if err != nil {
		return nil, err
	}
	var result api.RadioOptionsResult
	if err := decode(response, &result); err != nil {
		return nil, err
	}
	return result.Options, nil
}

func (c *Client) Popular(ctx context.Context, filter radio.Filter, limit, offset int) ([]radio.Station, error) {
	return c.radioStations(ctx, "", filter, offset, limit)
}

func (c *Client) SearchFiltered(ctx context.Context, term string, filter radio.Filter, offset, limit int) ([]radio.Station, error) {
	return c.radioStations(ctx, term, filter, offset, limit)
}

func (c *Client) radioStations(ctx context.Context, term string, filter radio.Filter, offset, limit int) ([]radio.Station, error) {
	response, err := c.Call(ctx, "radio.search", map[string]any{
		"name": term, "tag": filter.Tag, "language": filter.Language, "countryCode": filter.CountryCode,
		"limit": limit, "offset": offset, "origin": api.OriginDirectory,
	})
	if err != nil {
		return nil, err
	}
	var result api.RadioSearchResult
	if err := decode(response, &result); err != nil {
		return nil, err
	}
	stations := make([]radio.Station, 0, len(result.Items))
	for _, item := range result.Items {
		stations = append(stations, toStation(item))
	}
	return stations, nil
}

// StreamName is resolved by the server during playback; the client uses the
// stream's announced name only when the server has already folded it into state.
func (c *Client) StreamName(_ context.Context, _ string) string {
	return ""
}

// RadioCache fetches the server-owned disposable probe cache so the client can
// display cached health and an offline Browse fallback without persisting it.
func (c *Client) RadioCache(ctx context.Context) (*radio.Cache, error) {
	response, err := c.Call(ctx, "radio.cache", nil)
	if err != nil {
		return nil, err
	}
	cache := radio.NewCache("")
	if len(response.Data) > 0 {
		if err := json.Unmarshal(response.Data, cache); err != nil {
			return nil, err
		}
	}
	return cache, nil
}
