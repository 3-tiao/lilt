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

func clampLimit(limit int) int {
	if limit <= 0 {
		return 25
	}
	if limit > 25 {
		return 25
	}
	return limit
}
