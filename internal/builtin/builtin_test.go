package builtin

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestStationsParseAndProvenance(t *testing.T) {
	stations := Stations()
	if len(stations) != 15 {
		t.Fatalf("parsed %d stations, want 15", len(stations))
	}
	for _, station := range stations {
		if station.Name == "" || station.URL == "" {
			t.Fatalf("incomplete station: %+v", station)
		}
	}
	provenance := ProvenanceInfo()
	if provenance.ListURL != "https://radio.cliamp.stream/streams.m3u" {
		t.Fatalf("list URL = %q", provenance.ListURL)
	}
	if provenance.Notice == "" {
		t.Fatal("provenance notice is empty")
	}
	sum := sha256.Sum256([]byte(streamsM3U))
	if got := hex.EncodeToString(sum[:]); got != provenance.SHA256 {
		t.Fatalf("snapshot hash = %s, provenance = %s", got, provenance.SHA256)
	}
}
