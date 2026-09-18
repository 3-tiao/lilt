package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/builtin"
	"github.com/caiguo/lilt/internal/radio"
)

// handleRadioCache exposes the server-owned disposable probe cache so clients
// (notably the TUI) can display cached station health without own persistence.
func (s *Server) handleRadioCache(_ context.Context, _ json.RawMessage) (any, *api.Error) {
	if s.radioCache == nil {
		return map[string]any{"version": 1, "stations": map[string]any{}, "health": map[string]any{}}, nil
	}
	return s.radioCache, nil
}

// rememberRadioStations caches directory station profiles for offline Browse.
func (s *Server) rememberRadioStations(items []core.Item) {
	if s.radioCache == nil {
		return
	}
	if !s.radioCache.RememberItems(items, time.Now()) {
		return
	}
	if err := s.radioCache.Save(); err != nil {
		s.logf("radio.cache_save_failed", map[string]any{"error": err.Error()})
	}
}

type radioSearchParams struct {
	Name        string `json:"name"`
	Tag         string `json:"tag"`
	Language    string `json:"language"`
	CountryCode string `json:"countryCode"`
	Limit       int    `json:"limit"`
	Offset      int    `json:"offset"`
	Origin      string `json:"origin"`
}

func (s *Server) handleRadioSearch(ctx context.Context, raw json.RawMessage) (any, *api.Error) {
	var params radioSearchParams
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	return s.searchRadio(ctx, params)
}

func (s *Server) searchRadio(ctx context.Context, params radioSearchParams) (any, *api.Error) {
	origin := params.Origin
	if origin == "" {
		origin = "all"
	}
	switch origin {
	case api.OriginBuiltin, api.OriginDirectory, "all":
	default:
		return nil, api.Errorf(api.CodeInvalidRequest, "origin must be builtin, directory, or all")
	}
	limit := params.Limit
	if limit <= 0 {
		limit = 20
	}
	result := api.RadioSearchResult{
		Items: []api.Item{},
		Query: map[string]any{
			"name": params.Name, "tag": params.Tag, "language": params.Language,
			"countryCode": params.CountryCode, "origin": origin,
		},
	}
	attempted := 0
	succeeded := 0

	if origin == api.OriginBuiltin || origin == "all" {
		attempted++
		result.Items = append(result.Items, s.projectItems(filterBuiltin(params), api.SourceRadio)...)
		succeeded++
	}
	if origin == api.OriginDirectory || origin == "all" {
		attempted++
		if s.radio == nil {
			result.DegradedOrigins = append(result.DegradedOrigins, api.DegradedOrigin{
				Origin: api.OriginDirectory, Code: api.CodeSourceUnavailable, Message: "radio directory is unavailable",
			})
		} else {
			stations, err := s.searchDirectory(ctx, params, limit)
			if err != nil {
				result.DegradedOrigins = append(result.DegradedOrigins, api.DegradedOrigin{
					Origin: api.OriginDirectory, Code: api.CodeSearchFailed, Message: err.Error(),
				})
			} else {
				items := radio.ToItems(stations)
				for i := range items {
					if items[i].Radio != nil {
						items[i].Radio.Origin = api.OriginDirectory
					}
				}
				s.rememberRadioStations(items)
				result.Items = append(result.Items, s.projectItems(items, api.SourceRadio)...)
				succeeded++
			}
		}
	}
	if attempted > 0 && succeeded == 0 {
		return nil, api.Errorf(api.CodeSearchFailed, "all requested radio origins failed")
	}
	if len(result.Items) > limit {
		result.Items = result.Items[:limit]
	}
	return result, nil
}

func (s *Server) searchDirectory(ctx context.Context, params radioSearchParams, limit int) ([]radio.Station, error) {
	filter := radio.Filter{Language: params.Language, Tag: params.Tag, CountryCode: params.CountryCode}
	if strings.TrimSpace(params.Name) != "" || params.Tag != "" || params.Language != "" || params.CountryCode != "" {
		return s.radio.SearchFiltered(ctx, params.Name, filter, params.Offset, limit)
	}
	return s.radio.Popular(ctx, filter, limit, params.Offset)
}

func filterBuiltin(params radioSearchParams) []core.Item {
	// Builtin stations carry only a name and URL; they cannot satisfy a tag,
	// language, or country filter. Returning them under a structured query made
	// unrelated curated stations look tag-matched.
	if strings.TrimSpace(params.Tag) != "" || strings.TrimSpace(params.Language) != "" || strings.TrimSpace(params.CountryCode) != "" {
		return nil
	}
	term := strings.ToLower(strings.TrimSpace(params.Name))
	items := make([]core.Item, 0)
	for _, station := range builtin.Stations() {
		if term != "" && !strings.Contains(strings.ToLower(station.Name), term) {
			continue
		}
		items = append(items, core.Item{
			Kind:  api.KindStream,
			URL:   station.URL,
			Title: station.Name,
			Radio: &core.RadioMetadata{Origin: api.OriginBuiltin},
		})
	}
	return items
}

func (s *Server) handleRadioOptions(ctx context.Context, raw json.RawMessage) (any, *api.Error) {
	var params struct {
		Facet  string `json:"facet"`
		Origin string `json:"origin"`
	}
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	origin := params.Origin
	if origin == "" {
		origin = api.OriginDirectory
	}
	switch origin {
	case api.OriginBuiltin, api.OriginDirectory, "all":
	default:
		return nil, api.Errorf(api.CodeInvalidRequest, "origin must be builtin, directory, or all")
	}
	result := api.RadioOptionsResult{Options: []api.RadioOption{}}
	// Builtin stations carry no facet metadata, so only directory contributes.
	if origin == api.OriginBuiltin {
		return result, nil
	}
	if s.radio == nil {
		if origin == "all" {
			result.DegradedOrigins = append(result.DegradedOrigins, api.DegradedOrigin{
				Origin: api.OriginDirectory, Code: api.CodeSourceUnavailable, Message: "radio directory is unavailable",
			})
			return result, nil
		}
		return nil, api.Errorf(api.CodeSourceUnavailable, "radio directory is unavailable")
	}
	options, err := s.radioFacet(ctx, params.Facet)
	if err != nil {
		if err == errUnknownFacet {
			return nil, api.Errorf(api.CodeInvalidRequest, "facet must be tag, language, or country")
		}
		if origin == "all" {
			result.DegradedOrigins = append(result.DegradedOrigins, api.DegradedOrigin{
				Origin: api.OriginDirectory, Code: api.CodeSearchFailed, Message: err.Error(),
			})
			return result, nil
		}
		return nil, api.Errorf(api.CodeSearchFailed, "%v", err)
	}
	result.Options = options
	return result, nil
}

var errUnknownFacet = errors.New("unknown facet")

func (s *Server) radioFacet(ctx context.Context, facet string) ([]api.RadioOption, error) {
	options := []api.RadioOption{}
	switch facet {
	case "tag":
		tags, err := s.radio.Tags(ctx)
		if err != nil {
			return nil, err
		}
		for _, tag := range tags {
			options = append(options, api.RadioOption{Value: tag.Name, Count: tag.StationCount})
		}
	case "language":
		languages, err := s.radio.Languages(ctx)
		if err != nil {
			return nil, err
		}
		for _, language := range languages {
			options = append(options, api.RadioOption{Value: language.Name, Count: language.StationCount})
		}
	case "country":
		countries, err := s.radio.Countries(ctx)
		if err != nil {
			return nil, err
		}
		for _, country := range countries {
			options = append(options, api.RadioOption{Value: country.Code, Count: country.StationCount})
		}
	default:
		return nil, errUnknownFacet
	}
	return options, nil
}

func (s *Server) handleRadioProbe(ctx context.Context, raw json.RawMessage) (any, *api.Error) {
	var params struct {
		URL string `json:"url"`
	}
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	if err := s.ensureAudioEngineLocked(); err != nil {
		return nil, err
	}
	result, err := s.audioEngine.Probe(ctx, params.URL, 10000)
	if err != nil {
		return nil, s.mapEngineError(err)
	}
	// Persist both healthy and failed outcomes; the cache is disposable.
	if s.radioCache != nil {
		s.radioCache.RecordHealth(params.URL, result.Status, result.LatencyMs, result.ErrorCode, time.Now())
		if saveErr := s.radioCache.Save(); saveErr != nil {
			s.logf("radio.cache_save_failed", map[string]any{"error": saveErr.Error()})
		}
	}
	return api.RadioProbeResult{
		Status: result.Status, LatencyMs: result.LatencyMs, ErrorCode: result.ErrorCode, Message: result.Message,
	}, nil
}
