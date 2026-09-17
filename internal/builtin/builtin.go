// Package builtin exposes lilt's vendored snapshot of the cliamp.stream radio
// catalog. The snapshot is derived from third-party data; lilt does not own or
// operate these streams and provides no availability guarantee.
package builtin

import (
	_ "embed"
	"strings"
)

//go:embed streams.m3u
var streamsM3U string

// Provenance documents where the vendored snapshot came from. The SHA-256 is
// over the raw upstream list and MUST be updated whenever the snapshot changes.
type Provenance struct {
	Upstream    string
	ListURL     string
	RetrievedAt string
	SHA256      string
	Notice      string
}

// Station is one builtin stream entry.
type Station struct {
	Name string
	URL  string
}

// Provenance returns the snapshot's provenance metadata.
func ProvenanceInfo() Provenance {
	return Provenance{
		Upstream:    "cliamp / cliamp.stream (https://github.com/bjarneo/cliamp, https://cliamp.stream/)",
		ListURL:     "https://radio.cliamp.stream/streams.m3u",
		RetrievedAt: "2026-09-17T00:00:00Z",
		SHA256:      "e7ad52410e2da6c8fc99f4e3d29de16e53fd7213d5ea724f274dc274aac7bfc1",
		Notice:      "lilt is not affiliated with or endorsed by cliamp; no availability is guaranteed; station audio rights belong to their respective owners",
	}
}

// Stations parses the vendored snapshot.
func Stations() []Station {
	stations := make([]Station, 0)
	var pending string
	for _, line := range strings.Split(streamsM3U, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#EXTINF:") {
			if comma := strings.Index(line, ","); comma >= 0 {
				pending = strings.TrimSpace(line[comma+1:])
			}
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		stations = append(stations, Station{Name: pending, URL: line})
		pending = ""
	}
	return stations
}
