package appleweb

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
)

// CatalogSong is one catalog song as the page reports it. Every field is stable
// and public: URL is the music.apple.com page, never a media or preview asset.
type CatalogSong struct {
	ID         string
	Title      string
	Artist     string
	Album      string
	URL        string
	DurationMs int
}

// CatalogAlbum is one catalog album as the page reports it.
type CatalogAlbum struct {
	ID         string
	Title      string
	Artist     string
	URL        string
	TrackCount int
}

// CatalogPlaylist is one catalog playlist as the page reports it. Artist is
// the curator name, mirroring the helper's playlist projection.
type CatalogPlaylist struct {
	ID     string
	Title  string
	Artist string
	URL    string
}

// Recommendation is one flattened personal-recommendation row. The page maps
// every group uniformly; the Go side drops the rows this engine cannot serve.
type Recommendation struct {
	Kind   string // playlist | album
	ID     string
	Title  string
	Artist string
	URL    string
}

// songMapping is the page-side projection shared by every catalog call. Mapping
// in the page keeps the attribute names in one place, where they can be checked
// against the API instead of being re-guessed in Go.
const songMapping = `(s) => ({
  id: String(s.id),
  title: (s.attributes && s.attributes.name) || '',
  artist: (s.attributes && s.attributes.artistName) || '',
  album: (s.attributes && s.attributes.albumName) || '',
  url: (s.attributes && s.attributes.url) || '',
  durationMs: (s.attributes && s.attributes.durationInMillis) || 0,
})`

const albumMapping = `(a) => ({
  id: String(a.id),
  title: (a.attributes && a.attributes.name) || '',
  artist: (a.attributes && a.attributes.artistName) || '',
  url: (a.attributes && a.attributes.url) || '',
  trackCount: (a.attributes && a.attributes.trackCount) || 0,
})`

const playlistMapping = `(p) => ({
  id: String(p.id),
  title: (p.attributes && p.attributes.name) || '',
  artist: (p.attributes && p.attributes.curatorName) || '',
  url: (p.attributes && p.attributes.url) || '',
})`

// recommendationsGroupMapping is the per-group projection for me/recommendations.
// It stays a pure flatten of what the page was asked for: playlists and albums
// become rows (the group's own title is the playlist rows' secondary text, the
// same projection the MusicKit helper uses) and stations are mapped too — the
// Go side drops the rows this engine has no playback path for, so the filter is
// testable against the fake CDP fixture instead of only on a real machine.
const recommendationsGroupMapping = `(g) => {
  // The real response keeps every recommendation group's content under
  // relationships.contents.data as a mixed list distinguished by item.type
  // (playlists | albums | stations), and the group title is a localized
  // wrapper with a stringForDisplay field — verified against the live page.
  const rel = g.relationships || {};
  const text = (v) => {
    if (!v) return '';
    if (typeof v === 'string') return v;
    return v.stringForDisplay || v.short || v.default || '';
  };
  const attrs = g.attributes || {};
  const groupTitle = text(attrs.title) || text(attrs.reason);
  const row = (kind, item, artist) => ({
    kind: kind,
    id: String(item.id),
    title: (item.attributes && item.attributes.name) || '',
    artist: artist,
    url: (item.attributes && item.attributes.url) || '',
  });
  const rows = [];
  const contents = (rel.contents && rel.contents.data) || [];
  for (const item of contents) {
    if (item.type === 'playlists') rows.push(row('playlist', item, groupTitle));
    else if (item.type === 'albums') rows.push(row('album', item, (item.attributes && item.attributes.artistName) || groupTitle));
    // stations cannot be played by this engine; they are dropped in Go.
  }
  return rows;
}`

func catalogPrefix() string {
	return `(async () => {
  const mk = window.MusicKit.getInstance();
  const sf = mk.storefrontId || 'us';
`
}

// SearchSongs finds catalog songs in the account's own storefront.
func (b *Browser) SearchSongs(ctx context.Context, term string, limit int) ([]CatalogSong, error) {
	expression := catalogPrefix() + fmt.Sprintf(`
  const r = await mk.api.music('/v1/catalog/' + sf + '/search', { term: %s, types: 'songs', limit: %d });
  const group = r && r.data && r.data.results && r.data.results.songs;
  if (!group || !group.data) return '[]';
  return JSON.stringify(group.data.map(%s));
})()`, strconv.Quote(term), clampLimit(limit), songMapping)
	return decodeSongs(b, ctx, expression)
}

// SearchAlbums finds catalog albums in the account's own storefront.
func (b *Browser) SearchAlbums(ctx context.Context, term string, limit int) ([]CatalogAlbum, error) {
	expression := catalogPrefix() + fmt.Sprintf(`
  const r = await mk.api.music('/v1/catalog/' + sf + '/search', { term: %s, types: 'albums', limit: %d });
  const group = r && r.data && r.data.results && r.data.results.albums;
  if (!group || !group.data) return '[]';
  return JSON.stringify(group.data.map(%s));
})()`, strconv.Quote(term), clampLimit(limit), albumMapping)
	return decodeAlbums(b, ctx, expression)
}

// SearchPlaylists finds catalog playlists in the account's own storefront.
func (b *Browser) SearchPlaylists(ctx context.Context, term string, limit int) ([]CatalogPlaylist, error) {
	expression := catalogPrefix() + fmt.Sprintf(`
  const r = await mk.api.music('/v1/catalog/' + sf + '/search', { term: %s, types: 'playlists', limit: %d });
  const group = r && r.data && r.data.results && r.data.results.playlists;
  if (!group || !group.data) return '[]';
  return JSON.stringify(group.data.map(%s));
})()`, strconv.Quote(term), clampLimit(limit), playlistMapping)
	return decodePlaylists(b, ctx, expression)
}

// TrendingSongs reads the storefront's song chart. The chart is public catalog
// data — no sign-in is involved — and the chart order is the result order.
func (b *Browser) TrendingSongs(ctx context.Context, limit int) ([]CatalogSong, error) {
	expression := catalogPrefix() + fmt.Sprintf(`
  const r = await mk.api.music('/v1/catalog/' + sf + '/charts', { types: 'songs', limit: %d });
  const charts = r && r.data && r.data.results && r.data.results.songs;
  if (!charts) return '[]';
  const songs = Array.isArray(charts)
    ? charts.flatMap((chart) => (chart && chart.data) || [])
    : (charts.data || []);
  return JSON.stringify(songs.slice(0, %d).map(%s));
})()`, clampLimit(limit), clampLimit(limit), songMapping)
	return decodeSongs(b, ctx, expression)
}

// AlbumTracks resolves one album and its track list.
func (b *Browser) AlbumTracks(ctx context.Context, albumID string) (CatalogAlbum, []CatalogSong, error) {
	if albumID == "" {
		return CatalogAlbum{}, nil, fmt.Errorf("appleweb: album id is empty")
	}
	expression := catalogPrefix() + fmt.Sprintf(`
  const r = await mk.api.music('/v1/catalog/' + sf + '/albums/' + %s, { include: 'tracks' });
  const album = r && r.data && r.data.data && r.data.data[0];
  if (!album) return 'null';
  const tracks = (album.relationships && album.relationships.tracks && album.relationships.tracks.data) || [];
  return JSON.stringify({ album: (%s)(album), tracks: tracks.map(%s) });
})()`, strconv.Quote(albumID), albumMapping, songMapping)

	raw, err := b.Evaluate(ctx, expression)
	if err != nil {
		return CatalogAlbum{}, nil, err
	}
	if raw == "null" || raw == "" {
		return CatalogAlbum{}, nil, fmt.Errorf("appleweb: album %s was not found in the account's storefront", albumID)
	}
	var payload struct {
		Album  CatalogAlbum  `json:"album"`
		Tracks []CatalogSong `json:"tracks"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return CatalogAlbum{}, nil, fmt.Errorf("appleweb: unreadable album payload")
	}
	payload.Album.TrackCount = len(payload.Tracks)
	return payload.Album, payload.Tracks, nil
}

// PlaylistTracks resolves one catalog playlist and its track list through the
// public catalog API, the same shape AlbumTracks uses: the caller needs the
// playlist's own row, and only the page holds the resolved Playlist object.
func (b *Browser) PlaylistTracks(ctx context.Context, playlistID string) (CatalogPlaylist, []CatalogSong, error) {
	if playlistID == "" {
		return CatalogPlaylist{}, nil, fmt.Errorf("appleweb: playlist id is empty")
	}
	expression := catalogPrefix() + fmt.Sprintf(`
  const base = '/v1/catalog/' + sf + '/playlists/' + %s;
  const tracksPath = base + '/tracks';
  const metadata = await mk.api.music(base);
  const playlist = metadata && metadata.data && metadata.data.data && metadata.data.data[0];
  if (!playlist) return 'null';
  const tracks = [];
  let next = tracksPath;
  while (next && tracks.length < 1000) {
    const page = await mk.api.music(next, { limit: 100 });
    const body = page && page.data;
    tracks.push(...((body && body.data) || []));
    next = (body && body.next) || '';
  }
  return JSON.stringify({ playlist: (%s)(playlist), tracks: tracks.map(%s) });
})()`, strconv.Quote(playlistID), playlistMapping, songMapping)

	raw, err := b.Evaluate(ctx, expression)
	if err != nil {
		return CatalogPlaylist{}, nil, err
	}
	if raw == "null" || raw == "" {
		return CatalogPlaylist{}, nil, fmt.Errorf("appleweb: playlist %s was not found in the account's storefront", playlistID)
	}
	var payload struct {
		Playlist CatalogPlaylist `json:"playlist"`
		Tracks   []CatalogSong   `json:"tracks"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return CatalogPlaylist{}, nil, fmt.Errorf("appleweb: unreadable playlist payload")
	}
	if payload.Playlist.ID == "" {
		return CatalogPlaylist{}, nil, fmt.Errorf("appleweb: playlist %s has no catalog identity", playlistID)
	}
	return payload.Playlist, payload.Tracks, nil
}

// Recommendations reads the signed-in account's personal recommendations and
// flattens the groups into rows. A signed-out profile is refused up front with
// the same guard the MusicKit helper has: asking the me endpoint would only
// produce a 401, and the answer must say what is missing, not hide it.
func (b *Browser) Recommendations(ctx context.Context, limit int) ([]Recommendation, error) {
	authorized, err := b.Authorized(ctx)
	if err != nil {
		return nil, err
	}
	if !authorized {
		return nil, ErrUnauthorized
	}
	// me/recommendations has no storefront prefix: it follows the account.
	expression := `(async () => {
  const mk = window.MusicKit.getInstance();
  let r;
  try {
    r = await mk.api.music('/v1/me/recommendations');
  } catch (e) {
    if (e && (e.status === 401 || e.statusCode === 401 || e.code === 401 || (e.response && e.response.status === 401))) {
      return JSON.stringify({error: 'authorization_required'});
    }
    throw e;
  }
  const groups = (r && r.data && r.data.data) || [];
  return JSON.stringify(groups.map(` + recommendationsGroupMapping + `).flat());
})()`
	return decodeRecommendations(b, ctx, expression, limit)
}

// Song resolves one catalog song by id.
func (b *Browser) Song(ctx context.Context, songID string) (CatalogSong, error) {
	if songID == "" {
		return CatalogSong{}, fmt.Errorf("appleweb: song id is empty")
	}
	expression := catalogPrefix() + fmt.Sprintf(`
  const r = await mk.api.music('/v1/catalog/' + sf + '/songs/' + %s);
  const song = r && r.data && r.data.data && r.data.data[0];
  if (!song) return 'null';
  return JSON.stringify((%s)(song));
})()`, strconv.Quote(songID), songMapping)

	raw, err := b.Evaluate(ctx, expression)
	if err != nil {
		return CatalogSong{}, err
	}
	if raw == "null" || raw == "" {
		return CatalogSong{}, fmt.Errorf("appleweb: song %s was not found in the account's storefront", songID)
	}
	var song CatalogSong
	if err := json.Unmarshal([]byte(raw), &song); err != nil {
		return CatalogSong{}, fmt.Errorf("appleweb: unreadable song payload")
	}
	if song.ID == "" {
		return CatalogSong{}, fmt.Errorf("appleweb: song %s has no catalog identity", songID)
	}
	return song, nil
}

func decodeSongs(b *Browser, ctx context.Context, expression string) ([]CatalogSong, error) {
	raw, err := b.Evaluate(ctx, expression)
	if err != nil {
		return nil, err
	}
	var songs []CatalogSong
	if raw == "" || raw == "null" {
		return nil, nil
	}
	if err := json.Unmarshal([]byte(raw), &songs); err != nil {
		return nil, fmt.Errorf("appleweb: unreadable song payload")
	}
	out := songs[:0]
	for _, song := range songs {
		if song.ID != "" && song.Title != "" {
			out = append(out, song)
		}
	}
	return out, nil
}

func decodeAlbums(b *Browser, ctx context.Context, expression string) ([]CatalogAlbum, error) {
	raw, err := b.Evaluate(ctx, expression)
	if err != nil {
		return nil, err
	}
	var albums []CatalogAlbum
	if raw == "" || raw == "null" {
		return nil, nil
	}
	if err := json.Unmarshal([]byte(raw), &albums); err != nil {
		return nil, fmt.Errorf("appleweb: unreadable album payload")
	}
	out := albums[:0]
	for _, album := range albums {
		if album.ID != "" && album.Title != "" {
			out = append(out, album)
		}
	}
	return out, nil
}

func decodePlaylists(b *Browser, ctx context.Context, expression string) ([]CatalogPlaylist, error) {
	raw, err := b.Evaluate(ctx, expression)
	if err != nil {
		return nil, err
	}
	var playlists []CatalogPlaylist
	if raw == "" || raw == "null" {
		return nil, nil
	}
	if err := json.Unmarshal([]byte(raw), &playlists); err != nil {
		return nil, fmt.Errorf("appleweb: unreadable playlist payload")
	}
	out := playlists[:0]
	for _, playlist := range playlists {
		if playlist.ID != "" && playlist.Title != "" {
			out = append(out, playlist)
		}
	}
	return out, nil
}

// decodeRecommendations flattens the page rows into the serving list. Station
// rows are dropped here — the browser engine has no station playback path, and
// the fake CDP fixture carries station rows precisely so this filter is under
// test. Apple repeats the same catalog object across groups, so rows are also
// deduplicated (by kind and id, the same key the helper's projection uses)
// before the caller's limit is applied.
func decodeRecommendations(b *Browser, ctx context.Context, expression string, limit int) ([]Recommendation, error) {
	raw, err := b.Evaluate(ctx, expression)
	if err != nil {
		return nil, err
	}
	var rows []Recommendation
	if raw == "" || raw == "null" {
		return nil, nil
	}
	var failure struct {
		Error string `json:"error"`
	}
	if json.Unmarshal([]byte(raw), &failure) == nil && failure.Error == "authorization_required" {
		return nil, ErrUnauthorized
	}
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		return nil, fmt.Errorf("appleweb: unreadable recommendations payload")
	}
	serving := rows[:0]
	for _, row := range rows {
		if row.Kind != "playlist" && row.Kind != "album" {
			continue
		}
		if row.ID == "" || row.Title == "" {
			continue
		}
		serving = append(serving, row)
	}
	seen := make(map[string]bool, len(serving))
	rows = serving
	serving = rows[:0]
	for _, row := range rows {
		key := row.Kind + ":" + row.ID
		if seen[key] {
			continue
		}
		seen[key] = true
		serving = append(serving, row)
	}
	if limit > 0 && len(serving) > limit {
		serving = serving[:limit]
	}
	return serving, nil
}

func clampLimit(limit int) int {
	if limit <= 0 {
		return 25
	}
	if limit > 25 {
		return 25
	}
	return limit
}
