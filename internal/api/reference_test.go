package api

import "testing"

func TestParseReference(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		wantSource SourceID
		wantKind   string
		wantID     string
		wantURL    string
		wantErr    bool
	}{
		{name: "canonical song", raw: "apple-music:song:1646769334", wantSource: SourceAppleMusic, wantKind: KindSong, wantID: "1646769334"},
		{name: "canonical playlist", raw: "apple-music:playlist:pl.u-abc", wantSource: SourceAppleMusic, wantKind: KindPlaylist, wantID: "pl.u-abc"},
		{name: "legacy bare song", raw: "song:1440845629", wantSource: SourceAppleMusic, wantKind: KindSong, wantID: "1440845629"},
		{name: "legacy bare playlist", raw: "playlist:pl.u-abc", wantSource: SourceAppleMusic, wantKind: KindPlaylist, wantID: "pl.u-abc"},
		{name: "apple url song", raw: "https://music.apple.com/us/song/aruarian-dance/1440845629", wantSource: SourceAppleMusic, wantKind: KindSong, wantID: "1440845629"},
		{name: "apple url album track", raw: "https://music.apple.com/us/album/x/1440845629?i=1440845630", wantSource: SourceAppleMusic, wantKind: KindSong, wantID: "1440845630"},
		{name: "radio stream", raw: "https://radio.cliamp.stream/lofi/stream", wantSource: SourceRadio, wantKind: KindStream, wantURL: "https://radio.cliamp.stream/lofi/stream"},
		{name: "radio identity rejected", raw: "radio:https://x/stream", wantErr: true},
		{name: "empty", raw: "  ", wantErr: true},
		{name: "no id", raw: "song:", wantErr: true},
		{name: "unknown kind", raw: "movie:1", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseReference(test.raw)
			if test.wantErr {
				if err == nil {
					t.Fatalf("ParseReference(%q) = %+v, want error", test.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseReference(%q) unexpected error: %v", test.raw, err)
			}
			if got.Source != test.wantSource || got.Kind != test.wantKind || got.ID != test.wantID || got.URL != test.wantURL {
				t.Fatalf("ParseReference(%q) = %+v, want source=%s kind=%s id=%s url=%s", test.raw, got, test.wantSource, test.wantKind, test.wantID, test.wantURL)
			}
		})
	}
}

func TestReferenceRef(t *testing.T) {
	if got := AppleMusicRef(KindSong, "1"); got != "apple-music:song:1" {
		t.Fatalf("AppleMusicRef = %q", got)
	}
	if got := RadioRef("https://x/stream"); got != "radio:https://x/stream" {
		t.Fatalf("RadioRef = %q", got)
	}
	parsed, err := ParseReference("apple-music:playlist:pl.1")
	if err != nil {
		t.Fatal(err)
	}
	if got := parsed.Ref(); got != "apple-music:playlist:pl.1" {
		t.Fatalf("Ref = %q", got)
	}
	stream, streamErr := ParseReference("https://x/stream")
	if streamErr != nil {
		t.Fatal(streamErr)
	}
	if got := stream.Ref(); got != "https://x/stream" {
		t.Fatalf("stream Ref = %q", got)
	}
}
