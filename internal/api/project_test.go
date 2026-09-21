package api

import (
	"testing"

	"github.com/caiguo/lilt/core"
)

// The projection is the only core → wire mapping, so every source spelling that
// used to produce a different id must now land on one canonical identity.
func TestProjectCoreItemCanonicalizesEverySpelling(t *testing.T) {
	for _, test := range []struct {
		name     string
		source   SourceID
		item     core.Item
		wantKind string
		wantID   string
		wantRef  string
		wantProv string
	}{
		{
			name: "apple provider id", source: SourceAppleMusic,
			item:     core.Item{Kind: KindSong, ID: "1440845629", Title: "Song"},
			wantKind: KindSong, wantID: "am:1440845629", wantRef: "apple-music:song:1440845629", wantProv: "1440845629",
		},
		{
			name: "apple client spelling", source: SourceAppleMusic,
			item:     core.Item{Kind: KindSong, ID: "apple-music:1440845629"},
			wantKind: KindSong, wantID: "am:1440845629", wantRef: "apple-music:song:1440845629", wantProv: "1440845629",
		},
		{
			name: "apple album with colons in the id", source: SourceAppleMusic,
			item:     core.Item{Kind: KindAlbum, ID: "fake:album"},
			wantKind: KindAlbum, wantID: "am:fake:album", wantRef: "apple-music:album:fake:album", wantProv: "fake:album",
		},
		{
			name: "audius pre-kind client spelling", source: SourceAudius,
			item:     core.Item{Kind: KindSong, ID: "audius:track-1"},
			wantKind: KindSong, wantID: "audius:song:track-1", wantRef: "audius:song:track-1", wantProv: "track-1",
		},
		{
			name: "audius provider id", source: SourceAudius,
			item:     core.Item{Kind: KindSong, ID: "track-1"},
			wantKind: KindSong, wantID: "audius:song:track-1", wantRef: "audius:song:track-1", wantProv: "track-1",
		},
		{
			name: "radio normalizes the stream url", source: SourceRadio,
			item:     core.Item{Kind: KindStream, URL: "HTTPS://Radio.Example:443/Live/?token=one#frag"},
			wantKind: KindStream, wantID: "radio:https://radio.example/Live?token=one",
			wantRef: "https://radio.example/Live?token=one", wantProv: "",
		},
		{
			name: "missing kind defaults per source", source: SourceAppleMusic,
			item:     core.Item{ID: "1"},
			wantKind: KindSong, wantID: "am:1", wantRef: "apple-music:song:1", wantProv: "1",
		},
	} {
		got := ProjectCoreItem(test.item, test.source)
		if got.Kind != test.wantKind {
			t.Errorf("%s: Kind = %q, want %q", test.name, got.Kind, test.wantKind)
		}
		if got.ID != test.wantID {
			t.Errorf("%s: ID = %q, want %q", test.name, got.ID, test.wantID)
		}
		if got.Ref != test.wantRef {
			t.Errorf("%s: Ref = %q, want %q", test.name, got.Ref, test.wantRef)
		}
		if got.ProviderID != test.wantProv {
			t.Errorf("%s: ProviderID = %q, want %q", test.name, got.ProviderID, test.wantProv)
		}
		if got.Source != test.source {
			t.Errorf("%s: Source = %q, want %q", test.name, got.Source, test.source)
		}
	}
}

// Radio reports its normalized stream URL as both id and url, and drops the
// caller's raw spelling.
func TestProjectCoreItemRadioNormalizesURL(t *testing.T) {
	got := ProjectCoreItem(core.Item{
		Kind:  KindStream,
		URL:   "http://Radio.Example:80/live/",
		Title: "Station",
		Radio: &core.RadioMetadata{Origin: OriginDirectory, Bitrate: 128},
	}, SourceRadio)
	if got.URL != "http://radio.example/live" {
		t.Fatalf("URL = %q, want the normalized stream url", got.URL)
	}
	if got.ID != "radio:http://radio.example/live" {
		t.Fatalf("ID = %q", got.ID)
	}
	if got.Radio == nil || got.Radio.Bitrate != 128 || got.Radio.Origin != OriginDirectory {
		t.Fatalf("Radio = %+v", got.Radio)
	}
}

// A ref already carried by the item is preserved: the source knows its own
// playable spelling, and projection must not rewrite it.
func TestProjectCoreItemPreservesExplicitRef(t *testing.T) {
	got := ProjectCoreItem(core.Item{Kind: KindPlaylist, ID: "pl.abc", Ref: "apple-music:playlist:pl.abc"}, SourceAppleMusic)
	if got.Ref != "apple-music:playlist:pl.abc" {
		t.Fatalf("Ref = %q", got.Ref)
	}
}

func TestProjectCoreItemWithoutIdentityKeepsTheItem(t *testing.T) {
	got := ProjectCoreItem(core.Item{Kind: KindSong, Title: "Title only"}, SourceAppleMusic)
	if got.ID != "" || got.Ref != "" {
		t.Fatalf("projection invented an identity: %+v", got)
	}
	if got.Title != "Title only" {
		t.Fatalf("Title = %q", got.Title)
	}
}
