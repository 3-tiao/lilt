package radio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestParseM3U(t *testing.T) {
	body := `#EXTM3U
#EXTINF:-1,lofi
lofi/stream
#EXTINF:-1,Jazz
https://example.com/jazz
`
	base, _ := url.Parse("https://radio.cliamp.stream/streams.m3u")
	stations := parseM3U(strings.NewReader(body), base)
	if len(stations) != 2 || stations[0].Name != "lofi" || stations[0].URL != "https://radio.cliamp.stream/lofi/stream" || stations[1].URL != "https://example.com/jazz" {
		t.Fatalf("stations = %#v", stations)
	}
}

func TestStationsByTagParsesDirectory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/stations/bytagexact/jazz") {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`[
			{"name":"Jazz FM","url_resolved":"https://jazz.example/stream","country":"UK","tags":"jazz","codec":"MP3","bitrate":320},
			{"name":"bad","url_resolved":"ftp://nope"}
		]`))
	}))
	defer server.Close()
	client := &Client{HTTP: server.Client(), Base: server.URL}
	stations, err := client.StationsByTag(context.Background(), "jazz", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(stations) != 1 || stations[0].Name != "Jazz FM" || stations[0].Bitrate != 320 {
		t.Fatalf("stations = %#v", stations)
	}
	items := ToItems(stations)
	if len(items) != 1 || items[0].Kind != "stream" || !strings.Contains(items[0].Artist, "MP3 320k") {
		t.Fatalf("items = %#v", items)
	}
}
