package radio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
