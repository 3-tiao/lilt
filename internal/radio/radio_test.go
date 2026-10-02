package radio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
			{"stationuuid":"uuid","changeuuid":"change-1","name":"One","url":"https://one.example/playlist","url_resolved":"https://one.example/live","country":"Japan","countrycode":"JP","language":"Japanese","languagecodes":"jpn","tags":"city pop,pop","codec":"AAC","bitrate":128,"hls":1,"votes":7,"clickcount":42,"clicktrend":3,"lastcheckok":1,"lastchecktime":"2026-09-16 00:00:00"},
			{"stationuuid":"duplicate-url","name":"Duplicate URL","url_resolved":"https://one.example/live"},
			{"stationuuid":"uuid","name":"Duplicate UUID","url_resolved":"https://two.example/live"}
		]`))
	}))
	defer server.Close()
	c := &Client{HTTP: server.Client(), Base: server.URL}
	stations, err := c.Popular(context.Background(), Filter{}, 20, 0)
	if err != nil || len(stations) != 1 || stations[0].StationUUID != "uuid" || stations[0].ChangeUUID != "change-1" || stations[0].ClickCount != 42 || !stations[0].HLS || len(stations[0].Tags) != 2 || stations[0].Languages[0] != "jpn" {
		t.Fatalf("popular = %#v, %v", stations, err)
	}
	if items := ToItems(stations); len(items) != 1 || items[0].ID != "uuid" || items[0].Radio == nil || items[0].Radio.ClickTrend != 3 || items[0].Radio.CountryCode != "JP" {
		t.Fatalf("items lost typed station metadata: %#v", items)
	}
	stations, err = c.SearchFiltered(context.Background(), "Tokyo", Filter{Language: "Japanese", Tag: "City Pop", CountryCode: "JP"}, 0, 20)
	if err != nil || len(stations) != 1 {
		t.Fatalf("filtered = %#v, %v", stations, err)
	}
}

func TestDirectoryFailsOverToMirror(t *testing.T) {
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer broken.Close()
	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"stationuuid":"uuid","name":"Mirror FM","url_resolved":"https://mirror.example/live"}]`))
	}))
	defer mirror.Close()

	c := &Client{HTTP: broken.Client(), Base: broken.URL, Fallbacks: []string{mirror.URL}}
	stations, err := c.Popular(context.Background(), Filter{}, 20, 0)
	if err != nil || len(stations) != 1 || stations[0].Name != "Mirror FM" {
		t.Fatalf("fallback = %#v, %v", stations, err)
	}

	c = &Client{HTTP: broken.Client(), Base: broken.URL, Fallbacks: []string{mirror.URL, broken.URL}}
	var sink []Country
	if err := c.get(context.Background(), "/countries", &sink); err != nil {
		t.Fatalf("first working base should win: %v", err)
	}

	dead := &Client{HTTP: broken.Client(), Base: broken.URL, Fallbacks: []string{broken.URL}}
	if err := dead.get(context.Background(), "/countries", &sink); err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("all-broken error = %v", err)
	}
}

func TestStreamNameReadsIcyHeader(t *testing.T) {
	icy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Icy-MetaData") != "1" {
			t.Fatalf("missing ICY request header")
		}
		w.Header().Set("icy-name", "  Indie Pop Rocks  ")
		w.WriteHeader(http.StatusOK)
	}))
	defer icy.Close()
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer plain.Close()

	c := &Client{HTTP: icy.Client(), Base: icy.URL}
	if name := c.StreamName(context.Background(), icy.URL); name != "Indie Pop Rocks" {
		t.Fatalf("icy name = %q", name)
	}
	if name := c.StreamName(context.Background(), plain.URL); name != "" {
		t.Fatalf("plain stream should report no name, got %q", name)
	}
	if name := c.StreamName(context.Background(), "://not a url"); name != "" {
		t.Fatalf("invalid url = %q", name)
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
