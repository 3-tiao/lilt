// Package radio provides internet radio discovery for lilt.
package radio

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/caiguo/lilt/core"
)

const defaultBase = "https://de1.api.radio-browser.info/json"

type Station struct {
	Name    string
	URL     string
	Country string
	Tags    string
	Codec   string
	Bitrate int
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

type directoryStation struct {
	Name        string `json:"name"`
	URLResolved string `json:"url_resolved"`
	Country     string `json:"country"`
	Tags        string `json:"tags"`
	Codec       string `json:"codec"`
	Bitrate     int    `json:"bitrate"`
}

type Client struct {
	HTTP *http.Client
	Base string
}

func New() *Client {
	return &Client{HTTP: &http.Client{Timeout: 10 * time.Second}, Base: defaultBase}
}

func (c *Client) Countries(ctx context.Context) ([]Country, error) {
	var countries []Country
	if err := c.get(ctx, "/countries", &countries); err != nil {
		return nil, err
	}
	return countries, nil
}

func (c *Client) Tags(ctx context.Context) ([]Tag, error) {
	var tags []Tag
	if err := c.get(ctx, "/tags", &tags); err != nil {
		return nil, err
	}
	return tags, nil
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
	path := fmt.Sprintf("/stations/search?name=%s&hidebroken=true&order=clickcount&reverse=true&limit=%d", url.QueryEscape(term), limit)
	return c.stations(ctx, path)
}

func (c *Client) stations(ctx context.Context, path string) ([]Station, error) {
	var directory []directoryStation
	if err := c.get(ctx, path, &directory); err != nil {
		return nil, err
	}
	stations := make([]Station, 0, len(directory))
	for _, entry := range directory {
		stream := strings.TrimSpace(entry.URLResolved)
		if !strings.HasPrefix(stream, "http://") && !strings.HasPrefix(stream, "https://") {
			continue
		}
		stations = append(stations, Station{Name: entry.Name, URL: stream, Country: entry.Country, Tags: entry.Tags, Codec: entry.Codec, Bitrate: entry.Bitrate})
	}
	return stations, nil
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+path, nil)
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
		items = append(items, core.Item{Kind: "stream", URL: station.URL, Title: station.Name, Artist: subtitle})
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
