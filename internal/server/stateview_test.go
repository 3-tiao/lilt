package server

import (
	"testing"

	"github.com/caiguo/lilt/internal/state"
)

// A canonical am:-prefixed container keeps its provider id unchanged.
func TestCanonicalRecentContainerIDUnchanged(t *testing.T) {
	store := state.New("")
	store.RecentContainers = []state.RecentContainer{
		{ID: "am:pl.abcdef", Source: "apple-music", Kind: "playlist", Title: "Mix"},
	}
	server := &Server{store: store}
	item := server.appState().RecentContainers[0].Item
	if item.Ref != "apple-music:playlist:pl.abcdef" {
		t.Fatalf("ref = %q", item.Ref)
	}
}
