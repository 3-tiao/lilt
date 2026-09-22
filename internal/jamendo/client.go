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
	"time"

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

// readAttempts bounds the read retries described on getList. Jamendo answers a
// valid query with an empty result set for 30-50% of requests (measured 8-10
// empty replies per 30 identical valid lookups, in repeated runs; curling over
// HTTP/1.1 and HTTP/2 measures the same, so it is neither a protocol nor a
// connection-reuse artifact). Five attempts leave under 1% spurious empties at
// 30% per request. A query that is genuinely empty therefore costs five
// requests, which the 35,000/month quota absorbs.
const readAttempts = 5

// retryDelay is the pause before attempt N+1. It is short on purpose: the
// upstream failure is a routing artifact, not congestion. The whole ladder adds
// ~1.5s, and only when a reply came back empty.
var retryDelay = []time.Duration{
	120 * time.Millisecond,
	250 * time.Millisecond,
	450 * time.Millisecond,
	700 * time.Millisecond,
}

// DeveloperPortalURL is where users create the free read-only developer app
// whose client_id lilt stores (docs/internals/jamendo.md §4).
const DeveloperPortalURL = "https://devportal.jamendo.com/"

// Validate performs the minimal read used by `lilt jamendo setup`. It proves
// the client_id is accepted without persisting any returned media URL.
func (c Client) Validate(ctx context.Context) *api.Error {
	_, apiErr := getList[Track](c, ctx, "/tracks", url.Values{"limit": {"1"}}, readAttempts)
	return apiErr
}

// TrendingTracks returns Jamendo's featured tracks ordered by monthly
// popularity. Jamendo has no playlist popularity ordering, so song is the
// only trending kind (docs/internals/jamendo.md §5).
func (c Client) TrendingTracks(ctx context.Context, limit int) ([]Track, *api.Error) {
	return getList[Track](c, ctx, "/tracks", url.Values{
		"featured": {"1"},
		"order":    {"popularity_month"},
		"limit":    {fmt.Sprint(limit)},
	}, readAttempts)
}

// SearchTracks runs the free-text track search, which Jamendo applies across
// track, album and artist names, tags, and similar artists.
func (c Client) SearchTracks(ctx context.Context, term string, limit int) ([]Track, *api.Error) {
	return getList[Track](c, ctx, "/tracks", url.Values{"search": {term}, "limit": {fmt.Sprint(limit)}}, readAttempts)
}

// SearchPlaylists searches playlists by name; Jamendo has no free-text playlist
// search.
func (c Client) SearchPlaylists(ctx context.Context, term string, limit int) ([]Playlist, *api.Error) {
	return getList[Playlist](c, ctx, "/playlists", url.Values{"namesearch": {term}, "limit": {fmt.Sprint(limit)}}, readAttempts)
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
	tracks, apiErr := getList[Track](c, ctx, "/tracks", query, readAttempts)
	if apiErr != nil {
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
	playlists, apiErr := getList[Playlist](c, ctx, "/playlists", url.Values{"id": {id}}, readAttempts)
	if apiErr != nil {
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
		// An empty page ends the playlist, but getList already repeated it once:
		// a page that is empty twice is the real end, not the flaky empty reply.
		page, apiErr := getList[Track](c, ctx, "/playlists/tracks", url.Values{
			"id":     {id},
			"limit":  {fmt.Sprint(playlistPageSize)},
			"offset": {fmt.Sprint(offset)},
		}, readAttempts)
		if apiErr != nil {
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

// getList reads a Jamendo list endpoint and returns its rows.
//
// Jamendo's read API is unreliable in two ways that carry no distinguishing
// signal: it answers HTTP 200 with an empty result set for a query that
// succeeds moments later (measured ~25-35% of requests, for both free-text
// search and single-id lookups), and it occasionally resets the TLS connection
// mid-response. Neither is rate limiting: the empty rate is unchanged at 0.1s
// and 3s request spacing. Because `headers.status` stays "success" with
// `results_count: 0`, an empty reply is indistinguishable from "no matches",
// so the only lever is a bounded repeat of the same request: an empty first
// attempt is retried, and an empty last attempt is accepted as the real
// answer. Transport and 5xx failures are retried the same way; a body error
// code (bad client_id, rate limit, bad request) is returned immediately.
func getList[T any](c Client, ctx context.Context, path string, query url.Values, attempts int) ([]T, *api.Error) {
	var lastErr *api.Error
	for attempt := range attempts {
		if attempt > 0 {
			if err := waitBeforeRetry(ctx, attempt); err != nil {
				break
			}
		}
		items, apiErr, retryable := getListOnce[T](c, ctx, path, query)
		switch {
		case apiErr == nil && (len(items) > 0 || attempt == attempts-1):
			return items, nil
		case apiErr != nil && (!retryable || attempt == attempts-1):
			return nil, apiErr
		case apiErr != nil:
			lastErr = apiErr
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, api.Errorf(api.CodeSearchFailed, "Jamendo discovery timed out")
}

// waitBeforeRetry pauses between attempts and reports a cancelled context.
func waitBeforeRetry(ctx context.Context, attempt int) error {
	delay := retryDelay[len(retryDelay)-1]
	if attempt-1 < len(retryDelay) {
		delay = retryDelay[attempt-1]
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// getListOnce performs one read and reports whether the failure is worth
// repeating.
func getListOnce[T any](c Client, ctx context.Context, path string, query url.Values) ([]T, *api.Error, bool) {
	clientID, credentialErr := c.clientID()
	if credentialErr != nil {
		if errors.Is(credentialErr, ErrNotConfigured) {
			return nil, api.Errorf(api.CodeAuthorizationFailed, "Jamendo is not configured; run `lilt jamendo setup`"), false
		}
		return nil, api.Errorf(api.CodeAuthorizationFailed, "Jamendo credentials could not be read"), false
	}
	endpoint, err := url.Parse(c.baseURL() + path)
	if err != nil {
		return nil, api.Errorf(api.CodeSearchFailed, "Jamendo discovery is unavailable"), false
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
		return nil, api.Errorf(api.CodeSearchFailed, "Jamendo discovery is unavailable"), false
	}
	response, err := c.httpClient().Do(request)
	if err != nil {
		// A cancelled caller is not retried; everything else on the transport
		// (reset connection, TLS EOF, timeout) is the flakiness this layer exists
		// for.
		if ctx.Err() != nil {
			return nil, api.Errorf(api.CodeSearchFailed, "Jamendo discovery timed out"), false
		}
		return nil, api.Errorf(api.CodeSearchFailed, "Jamendo discovery is unavailable"), true
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, api.Errorf(api.CodeSearchFailed, "Jamendo discovery failed").
			WithDetails(map[string]any{"providerCode": fmt.Sprint(response.StatusCode)}), response.StatusCode >= 500
	}

	var envelope struct {
		Headers struct {
			Code int `json:"code"`
		} `json:"headers"`
		Results json.RawMessage `json:"results"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		return nil, api.Errorf(api.CodeSearchFailed, "Jamendo returned an invalid discovery response"), true
	}
	if envelope.Headers.Code != 0 {
		return nil, mapBodyCode(envelope.Headers.Code), false
	}
	if len(envelope.Results) == 0 {
		return nil, api.Errorf(api.CodeSearchFailed, "Jamendo returned an invalid discovery response"), true
	}
	var items []T
	if err := json.Unmarshal(envelope.Results, &items); err != nil {
		return nil, api.Errorf(api.CodeSearchFailed, "Jamendo returned an invalid discovery response"), true
	}
	return items, nil, true
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
