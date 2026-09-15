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

func TestPopularAndFilteredSearchBuildQueriesAndParseMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("hidebroken") != "true" || q.Get("order") != "clickcount" || q.Get("reverse") != "true" || q.Get("offset") != "0" || q.Get("limit") != "20" {
			t.Fatalf("query = %v", q)
		}
		if r.URL.Path == "/stations/search" && (q.Get("language") != "Japanese" || q.Get("tag") != "City Pop" || q.Get("countrycode") != "JP" || q.Get("name") != "Tokyo") {
			t.Fatalf("AND query = %v", q)
		}
		_, _ = w.Write([]byte(`[
			{"stationuuid":"uuid","name":"One","url_resolved":"https://one.example/live","countrycode":"JP","language":"Japanese","clickcount":42},
			{"stationuuid":"duplicate-url","name":"Duplicate URL","url_resolved":"https://one.example/live"},
			{"stationuuid":"uuid","name":"Duplicate UUID","url_resolved":"https://two.example/live"}
		]`))
	}))
	defer server.Close()
	c := &Client{HTTP: server.Client(), Base: server.URL}
	stations, err := c.Popular(context.Background(), Filter{}, 20, 0)
	if err != nil || len(stations) != 1 || stations[0].StationUUID != "uuid" || stations[0].ClickCount != 42 {
		t.Fatalf("popular = %#v, %v", stations, err)
	}
	if items := ToItems(stations); len(items) != 1 || items[0].ID != "uuid" {
		t.Fatalf("items lost station UUID: %#v", items)
	}
	stations, err = c.SearchFiltered(context.Background(), "Tokyo", Filter{Language: "Japanese", Tag: "City Pop", CountryCode: "JP"}, 0, 20)
	if err != nil || len(stations) != 1 {
		t.Fatalf("filtered = %#v, %v", stations, err)
	}
}

func TestDiscoveryOptionsPreferPopularUsableValues(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("hidebroken") != "true" || q.Get("order") != "stationcount" || q.Get("reverse") != "true" {
			t.Fatalf("query = %v", q)
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	c := &Client{HTTP: server.Client(), Base: server.URL}
	if _, err := c.Languages(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Tags(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Countries(context.Background()); err != nil {
		t.Fatal(err)
	}
}
