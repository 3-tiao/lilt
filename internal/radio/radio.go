// Package radio provides internet radio discovery for lilt.
package radio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/caiguo/lilt/core"
)

const defaultBase = "https://de1.api.radio-browser.info/json"
const fallbackBase = "https://de2.api.radio-browser.info/json"

type Station struct {
	StationUUID string
	Name        string
	URL         string
	Country     string
	CountryCode string
	Tags        string
	Language    string
	Codec       string
	Bitrate     int
	ClickCount  int
}

type Country struct {
	Name         string `json:"name"`
	Code         string `json:"iso_3166_1"`
	StationCount int    `json:"stationcount"`
}

type Tag struct {
	Name         string `json:"name"`
	StationCount int    `json:"stationcount"`
}

type Language struct {
	Name         string `json:"name"`
	StationCount int    `json:"stationcount"`
}

// Filter represents the optional directory facets. All populated fields are
// sent to Radio Browser's advanced search endpoint and therefore combine AND.
type Filter struct{ Language, Tag, CountryCode string }

type directoryStation struct {
	StationUUID string `json:"stationuuid"`
	Name        string `json:"name"`
	URLResolved string `json:"url_resolved"`
	Country     string `json:"country"`
	CountryCode string `json:"countrycode"`
	Language    string `json:"language"`
	Tags        string `json:"tags"`
	Codec       string `json:"codec"`
	Bitrate     int    `json:"bitrate"`
	ClickCount  int    `json:"clickcount"`
}

type Client struct {
	HTTP *http.Client
	Base string
	// Fallbacks are tried in order when Base fails. Radio Browser publishes
	// several mirrors; the directory SRV record only advertises one, so a
	// static ordered fallback keeps discovery working during a single-mirror
	// outage without a dependency on DNS service discovery.
	Fallbacks []string
}

func New() *Client {
	return &Client{
		HTTP:      &http.Client{Timeout: 7 * time.Second},
		Base:      defaultBase,
		Fallbacks: []string{fallbackBase},
	}
}

func (c *Client) Countries(ctx context.Context) ([]Country, error) {
	var countries []Country
	if err := c.get(ctx, "/countries?hidebroken=true&order=stationcount&reverse=true", &countries); err != nil {
		return nil, err
	}
	return countries, nil
}

func (c *Client) Tags(ctx context.Context) ([]Tag, error) {
	var tags []Tag
	if err := c.get(ctx, "/tags?hidebroken=true&order=stationcount&reverse=true", &tags); err != nil {
		return nil, err
	}
	return tags, nil
}

func (c *Client) Languages(ctx context.Context) ([]Language, error) {
	var languages []Language
	if err := c.get(ctx, "/languages?hidebroken=true&order=stationcount&reverse=true", &languages); err != nil {
		return nil, err
	}
	return languages, nil
}

func (c *Client) StationsByCountry(ctx context.Context, code string, limit int) ([]Station, error) {
	path := fmt.Sprintf("/stations/bycountrycodeexact/%s?hidebroken=true&order=clickcount&reverse=true&limit=%d", url.PathEscape(code), limit)
	return c.stations(ctx, path)
}

func (c *Client) StationsByTag(ctx context.Context, tag string, limit int) ([]Station, error) {
	path := fmt.Sprintf("/stations/bytagexact/%s?hidebroken=true&order=clickcount&reverse=true&limit=%d", url.PathEscape(tag), limit)
	return c.stations(ctx, path)
}

func (c *Client) Search(ctx context.Context, term string, limit int) ([]Station, error) {
	return c.SearchFiltered(ctx, term, Filter{}, 0, limit)
}

func (c *Client) TopClick(ctx context.Context, limit, offset int) ([]Station, error) {
	return c.stations(ctx, fmt.Sprintf("/stations/topclick/%d?hidebroken=true&order=clickcount&reverse=true&offset=%d&limit=%d", limit, offset, limit))
}

func (c *Client) SearchFiltered(ctx context.Context, term string, filter Filter, offset, limit int) ([]Station, error) {
	values := url.Values{"hidebroken": {"true"}, "order": {"clickcount"}, "reverse": {"true"}, "offset": {fmt.Sprint(offset)}, "limit": {fmt.Sprint(limit)}}
	if term != "" {
		values.Set("name", term)
	}
	if filter.Language != "" {
		values.Set("language", filter.Language)
		values.Set("languageExact", "true")
	}
	if filter.Tag != "" {
		values.Set("tag", filter.Tag)
		values.Set("tagExact", "true")
	}
	if filter.CountryCode != "" {
		values.Set("countrycode", filter.CountryCode)
	}
	return c.stations(ctx, "/stations/search?"+values.Encode())
}

func (c *Client) Popular(ctx context.Context, filter Filter, limit, offset int) ([]Station, error) {
	if filter.Language == "" && filter.Tag == "" && filter.CountryCode == "" {
		return c.TopClick(ctx, limit, offset)
	}
	return c.SearchFiltered(ctx, "", filter, offset, limit)
}

func (c *Client) stations(ctx context.Context, path string) ([]Station, error) {
	var directory []directoryStation
	if err := c.get(ctx, path, &directory); err != nil {
		return nil, err
	}
	stations := make([]Station, 0, len(directory))
	seenUUID := make(map[string]struct{}, len(directory))
	seenURL := make(map[string]struct{}, len(directory))
	for _, entry := range directory {
		stream := strings.TrimSpace(entry.URLResolved)
		if !strings.HasPrefix(stream, "http://") && !strings.HasPrefix(stream, "https://") {
			continue
		}
		if _, exists := seenURL[stream]; exists {
			continue
		}
		if entry.StationUUID != "" {
			if _, exists := seenUUID[entry.StationUUID]; exists {
				continue
			}
			seenUUID[entry.StationUUID] = struct{}{}
		}
		seenURL[stream] = struct{}{}
		stations = append(stations, Station{StationUUID: entry.StationUUID, Name: entry.Name, URL: stream, Country: entry.Country, CountryCode: entry.CountryCode, Language: entry.Language, Tags: entry.Tags, Codec: entry.Codec, Bitrate: entry.Bitrate, ClickCount: entry.ClickCount})
	}
	return stations, nil
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	var lastErr error
	for _, base := range append([]string{c.Base}, c.Fallbacks...) {
		if base == "" {
			continue
		}
		if err := c.getFrom(ctx, base, path, out); err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return err
			}
			continue
		}
		return nil
	}
	if lastErr == nil {
		lastErr = errors.New("radio-browser: no directory base configured")
	}
	return lastErr
}

func (c *Client) getFrom(ctx context.Context, base, path string, out any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "lilt/0.1")
	response, err := c.HTTP.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("radio-browser: HTTP %d", response.StatusCode)
	}
	return json.NewDecoder(response.Body).Decode(out)
}

// StreamName probes a stream for its ICY station name. Icecast and Shoutcast
// servers commonly answer with an icy-name header; an empty result means the
// caller should keep the raw URL as the title.
func (c *Client) StreamName(ctx context.Context, streamURL string) string {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(streamURL), nil)
	if err != nil {
		return ""
	}
	request.Header.Set("User-Agent", "lilt/0.1")
	request.Header.Set("Icy-MetaData", "1")
	response, err := c.HTTP.Do(request)
	if err != nil {
		return ""
	}
	defer response.Body.Close()
	for _, header := range []string{"icy-name", "ice-name"} {
		if name := strings.TrimSpace(response.Header.Get(header)); name != "" {
			return name
		}
	}
	return ""
}

// ToItems converts stations into playable items.
func ToItems(stations []Station) []core.Item {
	items := make([]core.Item, 0, len(stations))
	for _, station := range stations {
		subtitle := strings.TrimSpace(strings.Join(nonEmpty(station.Country, "", station.Tags), " · "))
		if station.Bitrate > 0 {
			codec := strings.ToUpper(station.Codec)
			if codec == "" {
				codec = "STREAM"
			}
			subtitle = strings.TrimSpace(fmt.Sprintf("%s %dk", codec, station.Bitrate) + " · " + subtitle)
		}
		items = append(items, core.Item{Kind: "stream", ID: station.StationUUID, URL: station.URL, Title: station.Name, Artist: subtitle})
	}
	return items
}

func nonEmpty(values ...string) []string {
	kept := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			kept = append(kept, strings.TrimSpace(value))
		}
	}
	return kept
}
