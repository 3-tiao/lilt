package server

import (
	"testing"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
)

func TestSourceFromStateClassifiesRadioShape(t *testing.T) {
	if got := sourceFromState(core.PlaybackState{IsLive: true}); got != api.SourceRadio {
		t.Fatalf("live -> %s", got)
	}
	if got := sourceFromState(core.PlaybackState{Mode: "stream"}); got != api.SourceRadio {
		t.Fatalf("stream mode -> %s", got)
	}
	if got := sourceFromState(core.PlaybackState{Track: &core.Item{Kind: api.KindStream}}); got != api.SourceRadio {
		t.Fatalf("stream track -> %s", got)
	}
	if got := sourceFromState(core.PlaybackState{Mode: "full", Track: &core.Item{Kind: api.KindSong}}); got != api.SourceAppleMusic {
		t.Fatalf("finite -> %s", got)
	}
}

func TestProjectItemUsesAppleMusicStableID(t *testing.T) {
	item := ProjectItem(core.Item{Kind: api.KindSong, ID: "1646769334", Title: "Song"}, api.SourceAppleMusic)
	if item.ID != "am:1646769334" {
		t.Errorf("ID = %q, want am:1646769334", item.ID)
	}
	if item.Ref != "apple-music:song:1646769334" {
		t.Errorf("Ref = %q, want canonical Apple Music ref", item.Ref)
	}
}

func TestProjectItemUsesAudiusStableID(t *testing.T) {
	item := ProjectItem(core.Item{Kind: api.KindPlaylist, ID: "abc123", Title: "Playlist"}, api.SourceAudius)
	if item.ID != "audius:playlist:abc123" {
		t.Errorf("ID = %q, want audius:playlist:abc123", item.ID)
	}
	if item.Ref != item.ID {
		t.Errorf("Ref = %q, want canonical Audius ref", item.Ref)
	}
}
