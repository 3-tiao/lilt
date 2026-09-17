package server

import (
	"testing"

	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/state"
)

// Legacy state stored recent containers with bare "playlist:<id>" ids. The
// public ref must still be canonical ("apple-music:playlist:<id>") or opening
// the playlist fails in the helper with invalid_reference.
func TestLegacyRecentContainerIDProducesValidRef(t *testing.T) {
	store := state.New("")
	store.RecentContainers = []state.RecentContainer{
		{ID: "playlist:-3750669790803871374", Kind: "playlist", Title: "Rock"},
	}
	server := &Server{store: store}
	appState := server.appState()
	if len(appState.RecentContainers) != 1 {
		t.Fatalf("containers = %+v", appState.RecentContainers)
	}
	item := appState.RecentContainers[0].Item
	if item.Ref != "apple-music:playlist:-3750669790803871374" {
		t.Fatalf("ref = %q", item.Ref)
	}
	if item.ProviderID != "-3750669790803871374" {
		t.Fatalf("providerId = %q", item.ProviderID)
	}
	if item.Source != api.SourceAppleMusic {
		t.Fatalf("source = %q", item.Source)
	}
}

// A canonical am:-prefixed container keeps its provider id unchanged.
func TestCanonicalRecentContainerIDUnchanged(t *testing.T) {
	store := state.New("")
	store.RecentContainers = []state.RecentContainer{
		{ID: "am:pl.abcdef", Kind: "playlist", Title: "Mix"},
	}
	server := &Server{store: store}
	item := server.appState().RecentContainers[0].Item
	if item.Ref != "apple-music:playlist:pl.abcdef" {
		t.Fatalf("ref = %q", item.Ref)
	}
}
