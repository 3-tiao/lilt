// Package audius implements the anonymous discovery and lazy stream-resolution
// subset of the official Audius REST API. Stream URLs are short-lived and are
// returned only to the playback preparation boundary.
package audius

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/caiguo/lilt/internal/api"
)

const DefaultBaseURL = "https://api.audius.co/v1"

type Client struct {
	BaseURL string
	HTTP    *http.Client
}

type Track struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Duration     int    `json:"duration"`
	Permalink    string `json:"permalink"`
	IsStreamable bool   `json:"is_streamable"`
	User         struct {
		Name string `json:"name"`
	} `json:"user"`
}

type Playlist struct {
	ID           string `json:"id"`
	PlaylistName string `json:"playlist_name"`
	Permalink    string `json:"permalink"`
	User         struct {
		Name string `json:"name"`
	} `json:"user"`
	Tracks []Track `json:"tracks"`
}

func (c Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}
func (c Client) baseURL() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return DefaultBaseURL
}

func (c Client) SearchTracks(ctx context.Context, term string, limit int) ([]Track, *api.Error) {
	var tracks []Track
	return tracks, c.get(ctx, "/tracks/search", url.Values{"query": {term}, "limit": {fmt.Sprint(limit)}}, &tracks)
}
func (c Client) SearchPlaylists(ctx context.Context, term string, limit int) ([]Playlist, *api.Error) {
	var playlists []Playlist
	return playlists, c.get(ctx, "/playlists/search", url.Values{"query": {term}, "limit": {fmt.Sprint(limit)}}, &playlists)
}
func (c Client) Playlist(ctx context.Context, id string) (Playlist, *api.Error) {
	if strings.TrimSpace(id) == "" {
		return Playlist{}, api.Errorf(api.CodeInvalidReference, "Audius playlist id is empty")
	}
	var playlist Playlist
	if err := c.get(ctx, "/playlists/"+url.PathEscape(id), nil, &playlist); err != nil {
		return Playlist{}, err
	}
	if playlist.ID == "" {
		return Playlist{}, api.Errorf(api.CodeInvalidReference, "Audius playlist was not found")
	}
	return playlist, nil
}

// PlaylistTracks lists a playlist's tracks through the dedicated endpoint.
// Audius returns the playlist object itself inside a one-element array, so the
// object-shaped response is normalized by get.
func (c Client) PlaylistTracks(ctx context.Context, id string) ([]Track, *api.Error) {
	if strings.TrimSpace(id) == "" {
		return nil, api.Errorf(api.CodeInvalidReference, "Audius playlist id is empty")
	}
	var tracks []Track
	return tracks, c.get(ctx, "/playlists/"+url.PathEscape(id)+"/tracks", nil, &tracks)
}

func (c Client) Track(ctx context.Context, id string) (Track, *api.Error) {
	if strings.TrimSpace(id) == "" {
		return Track{}, api.Errorf(api.CodeInvalidReference, "Audius track id is empty")
	}
	var track Track
	return track, c.get(ctx, "/tracks/"+url.PathEscape(id), nil, &track)
}

// StreamURL resolves one short-lived media URL. Callers must use it only at
// playback start/switch time and must never persist or publicly project it.
func (c Client) StreamURL(ctx context.Context, trackID string) (string, *api.Error) {
	trackID = strings.TrimSpace(trackID)
	if trackID == "" {
		return "", api.Errorf(api.CodeInvalidReference, "Audius track id is empty")
	}
	var mediaURL string
	if err := c.get(ctx, "/tracks/"+url.PathEscape(trackID)+"/stream", url.Values{"no_redirect": {"true"}}, &mediaURL); err != nil {
		return "", err
	}
	parsed, err := url.Parse(mediaURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", api.Errorf(api.CodeSearchFailed, "Audius returned an invalid stream response")
	}
	return mediaURL, nil
}

// Tracks retrieves provider-native tracks by id. It is kept separate from
// discovery projection so no signed media data can accidentally cross layers.
func (c Client) Tracks(ctx context.Context, ids []string) ([]Track, *api.Error) {
	if len(ids) == 0 {
		return nil, api.Errorf(api.CodeInvalidReference, "Audius track ids are empty")
	}
	var tracks []Track
	return tracks, c.get(ctx, "/tracks", url.Values{"id": {strings.Join(ids, ",")}}, &tracks)
}

func (c Client) get(ctx context.Context, path string, query url.Values, out any) *api.Error {
	endpoint, err := url.Parse(c.baseURL() + path)
	if err != nil {
		return api.Errorf(api.CodeSearchFailed, "Audius discovery is unavailable")
	}
	q := endpoint.Query()
	q.Set("app_name", "lilt")
	for key, values := range query {
		for _, value := range values {
			q.Add(key, value)
		}
	}
	endpoint.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return api.Errorf(api.CodeSearchFailed, "Audius discovery is unavailable")
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return api.Errorf(api.CodeSearchFailed, "Audius discovery timed out")
		}
		return api.Errorf(api.CodeSearchFailed, "Audius discovery is unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return api.Errorf(api.CodeSearchFailed, "Audius discovery failed").WithDetails(map[string]any{"providerCode": fmt.Sprint(resp.StatusCode)})
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil || len(envelope.Data) == 0 {
		return api.Errorf(api.CodeSearchFailed, "Audius returned an invalid discovery response")
	}
	if err := decodeData(envelope.Data, out); err != nil {
		return api.Errorf(api.CodeSearchFailed, "Audius returned an invalid discovery response")
	}
	return nil
}

// decodeData accepts both the object envelope used by list-shaped endpoints and
// the one-element array envelope Audius uses for some single-resource lookups.
func decodeData(data json.RawMessage, out any) error {
	if err := json.Unmarshal(data, out); err == nil {
		return nil
	}
	var list []json.RawMessage
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}
	if len(list) == 0 {
		return errors.New("empty data array")
	}
	return json.Unmarshal(list[0], out)
}
