// Package jamendo implements the discovery subset of the official Jamendo API
// v3.0. Public reads only need a user-supplied client_id, so the package holds
// no OAuth token lifecycle: callers inject a Credentials reader that is
// consulted per request, which lets a running server pick up a fresh
// `lilt jamendo setup` without a restart.
//
// Jamendo reports most failures inside an HTTP 200 body (`headers.code`), so
// every call maps the transport layer first and the body code second. Raw
// upstream text never reaches a caller: only a stable api.Error code plus a
// sanitized details.providerCode.
package jamendo

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

const DefaultBaseURL = "https://api.jamendo.com/v3.0"

// PlaybackAudioFormat is the transcode lilt asks for when it resolves a track
// for playback: VBR "good quality" instead of the 96kbps API default.
const PlaybackAudioFormat = "mp32"

// ErrNotConfigured is returned by Credentials when the user has not run
// `lilt jamendo setup` yet.
var ErrNotConfigured = errors.New("Jamendo client_id is not configured")

// Credentials reports the configured Jamendo client_id. It is evaluated on
// every request so a running server picks up setup changes without a restart.
type Credentials func() (clientID string, err error)

type Client struct {
	BaseURL     string
	HTTP        *http.Client
	Credentials Credentials
}

// Track is the subset of a Jamendo track lilt projects or plays. IDs are JSON
// strings; duration is a JSON number in the v3.0 response.
type Track struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Duration   int    `json:"duration"`
	ArtistID   string `json:"artist_id"`
	ArtistName string `json:"artist_name"`
	AlbumID    string `json:"album_id"`
	AlbumName  string `json:"album_name"`
	Image      string `json:"image"`
	AlbumImage string `json:"album_image"`
	Audio      string `json:"audio"`
	ShareURL   string `json:"shareurl"`
	LicenseCC  string `json:"license_ccurl"`
}

// DurationSeconds rejects a negative upstream value so malformed metadata
// cannot fake a real duration.
func (t Track) DurationSeconds() int {
	if t.Duration < 0 {
		return 0
	}
	return t.Duration
}

// ArtworkURL returns the cover lilt displays. Singles carry the image on the
// track and leave album_image empty.
func (t Track) ArtworkURL() string {
	if t.Image != "" {
		return t.Image
	}
	return t.AlbumImage
}

// Playlist is the subset of a Jamendo playlist lilt projects. Jamendo playlists
// carry no artwork and no track count.
type Playlist struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	UserID   string `json:"user_id"`
	UserName string `json:"user_name"`
	ShareURL string `json:"shareurl"`
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

func (c Client) clientID() (string, error) {
	if c.Credentials == nil {
		return "", ErrNotConfigured
	}
	id, err := c.Credentials()
	if err != nil {
		return "", err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return "", ErrNotConfigured
	}
	return id, nil
}

// Validate performs the minimal read used by `lilt jamendo setup`. It proves
// the client_id is accepted without persisting any returned media URL.
func (c Client) Validate(ctx context.Context) *api.Error {
	var tracks []Track
	return c.get(ctx, "/tracks", url.Values{"limit": {"1"}}, &tracks)
}

// SearchTracks runs the free-text track search, which Jamendo applies across
// track, album and artist names, tags, and similar artists.
func (c Client) SearchTracks(ctx context.Context, term string, limit int) ([]Track, *api.Error) {
	var tracks []Track
	return tracks, c.get(ctx, "/tracks", url.Values{"search": {term}, "limit": {fmt.Sprint(limit)}}, &tracks)
}

// SearchPlaylists searches playlists by name; Jamendo has no free-text playlist
// search.
func (c Client) SearchPlaylists(ctx context.Context, term string, limit int) ([]Playlist, *api.Error) {
	var playlists []Playlist
	return playlists, c.get(ctx, "/playlists", url.Values{"namesearch": {term}, "limit": {fmt.Sprint(limit)}}, &playlists)
}

// Track loads one track. audioFormat selects the format of the returned `audio`
// URL ("mp31", "mp32", "ogg", "flac"); empty uses the API default. Callers that
// resolve playback pass PlaybackAudioFormat and must treat the URL as a
// short-lived private resource.
func (c Client) Track(ctx context.Context, id, audioFormat string) (Track, *api.Error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Track{}, api.Errorf(api.CodeInvalidReference, "Jamendo track id is empty")
	}
	query := url.Values{"id": {id}}
	if format := strings.TrimSpace(audioFormat); format != "" {
		query.Set("audioformat", format)
	}
	var tracks []Track
	if apiErr := c.get(ctx, "/tracks", query, &tracks); apiErr != nil {
		return Track{}, apiErr
	}
	if len(tracks) == 0 {
		return Track{}, api.Errorf(api.CodeInvalidReference, "Jamendo track was not found")
	}
	return tracks[0], nil
}

// Playlist loads one playlist's metadata.
func (c Client) Playlist(ctx context.Context, id string) (Playlist, *api.Error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Playlist{}, api.Errorf(api.CodeInvalidReference, "Jamendo playlist id is empty")
	}
	var playlists []Playlist
	if apiErr := c.get(ctx, "/playlists", url.Values{"id": {id}}, &playlists); apiErr != nil {
		return Playlist{}, apiErr
	}
	if len(playlists) == 0 {
		return Playlist{}, api.Errorf(api.CodeInvalidReference, "Jamendo playlist was not found")
	}
	return playlists[0], nil
}

const playlistPageSize = 200

// PlaylistTracks lists the complete playlist through the dedicated paginated
// sub-resource. The public playlist.tracks contract has no pagination field, so
// silently returning Jamendo's first 200 rows would create an incomplete queue.
func (c Client) PlaylistTracks(ctx context.Context, id string) ([]Track, *api.Error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, api.Errorf(api.CodeInvalidReference, "Jamendo playlist id is empty")
	}
	var (
		tracks       []Track
		previousPage []Track
	)
	for offset := 0; ; offset += playlistPageSize {
		var page []Track
		if apiErr := c.get(ctx, "/playlists/tracks", url.Values{
			"id":     {id},
			"limit":  {fmt.Sprint(playlistPageSize)},
			"offset": {fmt.Sprint(offset)},
		}, &page); apiErr != nil {
			return nil, apiErr
		}
		if len(page) == 0 {
			return tracks, nil
		}
		if sameTrackPage(previousPage, page) {
			return nil, api.Errorf(api.CodeSearchFailed, "Jamendo playlist pagination did not advance")
		}
		tracks = append(tracks, page...)
		if len(page) < playlistPageSize {
			return tracks, nil
		}
		previousPage = append(previousPage[:0], page...)
	}
}

func sameTrackPage(left, right []Track) bool {
	if len(left) == 0 || len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].ID != right[i].ID {
			return false
		}
	}
	return true
}

func (c Client) get(ctx context.Context, path string, query url.Values, out any) *api.Error {
	clientID, credentialErr := c.clientID()
	if credentialErr != nil {
		if errors.Is(credentialErr, ErrNotConfigured) {
			return api.Errorf(api.CodeAuthorizationFailed, "Jamendo is not configured; run `lilt jamendo setup`")
		}
		return api.Errorf(api.CodeAuthorizationFailed, "Jamendo credentials could not be read")
	}
	endpoint, err := url.Parse(c.baseURL() + path)
	if err != nil {
		return api.Errorf(api.CodeSearchFailed, "Jamendo discovery is unavailable")
	}
	values := endpoint.Query()
	values.Set("client_id", clientID)
	values.Set("format", "json")
	// Jamendo wants caller filters to win over the fixed client_id/format pair,
	// but the two key sets never overlap.
	for key, entries := range query {
		for _, entry := range entries {
			values.Set(key, entry)
		}
	}
	endpoint.RawQuery = values.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return api.Errorf(api.CodeSearchFailed, "Jamendo discovery is unavailable")
	}
	response, err := c.httpClient().Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return api.Errorf(api.CodeSearchFailed, "Jamendo discovery timed out")
		}
		return api.Errorf(api.CodeSearchFailed, "Jamendo discovery is unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return api.Errorf(api.CodeSearchFailed, "Jamendo discovery failed").
			WithDetails(map[string]any{"providerCode": fmt.Sprint(response.StatusCode)})
	}

	var envelope struct {
		Headers struct {
			Code int `json:"code"`
		} `json:"headers"`
		Results json.RawMessage `json:"results"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		return api.Errorf(api.CodeSearchFailed, "Jamendo returned an invalid discovery response")
	}
	if envelope.Headers.Code != 0 {
		return mapBodyCode(envelope.Headers.Code)
	}
	if len(envelope.Results) == 0 {
		return api.Errorf(api.CodeSearchFailed, "Jamendo returned an invalid discovery response")
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(envelope.Results, out); err != nil {
		return api.Errorf(api.CodeSearchFailed, "Jamendo returned an invalid discovery response")
	}
	return nil
}

// mapBodyCode turns Jamendo's in-body error ids into stable api.Error codes.
// The upstream message is dropped on purpose: it can echo request parameters.
func mapBodyCode(code int) *api.Error {
	providerCode := map[string]any{"providerCode": fmt.Sprint(code)}
	switch code {
	case 5, 11:
		// Invalid client id, or the application was suspended. Both mean the
		// stored credential cannot be used as-is.
		return api.Errorf(api.CodeAuthorizationFailed, "Jamendo rejected the stored client_id; run `lilt jamendo setup`").WithDetails(providerCode)
	case 6:
		return api.Errorf(api.CodeSearchFailed, "Jamendo rate limit exceeded").WithDetails(providerCode)
	default:
		// 1 (generic), 3 (type), 4/8 (missing parameter), 7 (unknown method),
		// and anything new: a request-shape failure, not a credential problem.
		return api.Errorf(api.CodeSearchFailed, "Jamendo discovery failed").WithDetails(providerCode)
	}
}
