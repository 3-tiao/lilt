package state

import (
	"path/filepath"
	"testing"

	"github.com/caiguo/lilt/core"
)

func TestRecordRankAndRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store := New(path)
	a := core.Item{Kind: "song", ID: "a"}
	b := core.Item{Kind: "song", ID: "b"}
	store.Record("focus", b)
	store.Record("focus", b)
	ranked := store.Rank("focus", []core.Item{a, b})
	if len(ranked) != 2 || ranked[0].ID != "b" || ranked[1].ID != "a" {
		t.Fatalf("ranked = %#v, want b first", ranked)
	}
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	record := loaded.Presets["focus"]
	if record.Uses != 2 || record.Chosen["b"] != 2 || record.Last != "b" {
		t.Fatalf("record = %#v", record)
	}
}

func TestFavoritesToggleAndSourceIDs(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "state.json"))
	song := core.Item{Kind: "song", ID: "123", Title: "Song"}
	if store.ToggleFavorite("apple-music", song) != true {
		t.Fatal("expected favorited")
	}
	if !store.IsFavorite("apple-music", "am:123") {
		t.Fatal("favorite not stored with stable id")
	}
	if store.ToggleFavorite("apple-music", song) != false {
		t.Fatal("expected un-favorited")
	}
	station := core.Item{Kind: "stream", URL: "https://radio.example/lofi/", Title: "lofi"}
	store.ToggleFavorite("radio", station)
	favorites := store.FavoritesFor("radio")
	if len(favorites) != 1 || favorites[0].ID != "radio:https://radio.example/lofi" {
		t.Fatalf("radio favorites = %#v", favorites)
	}
}

func TestRecentDedupAndOrder(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "state.json"))
	a := core.Item{Kind: "song", ID: "a", Title: "A"}
	b := core.Item{Kind: "song", ID: "b", Title: "B"}
	store.AddRecent("apple-music", a)
	store.AddRecent("apple-music", b)
	store.AddRecent("apple-music", a)
	if len(store.Recent) != 2 || store.Recent[0].ID != "am:a" {
		t.Fatalf("recent = %#v", store.Recent)
	}
}

func TestLocalPlaylists(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "state.json"))
	list := store.SaveQueue("Road", []LocalTrack{{ID: "am:1", Title: "A"}})
	store.AddToLocalPlaylist("Road", LocalTrack{ID: "am:2", Title: "B"})
	store.AddToLocalPlaylist("Road", LocalTrack{ID: "am:2", Title: "B"})
	loaded, ok := store.LocalPlaylist(list.ID)
	if !ok || len(loaded.Items) != 2 {
		t.Fatalf("list = %#v", loaded)
	}
	store.MoveInLocalPlaylist(list.ID, 0, 1)
	moved, _ := store.LocalPlaylist(list.ID)
	if len(moved.Items) != 2 || moved.Items[0].ID != "am:2" || moved.Items[1].ID != "am:1" {
		t.Fatalf("after move = %#v", moved.Items)
	}
	store.RemoveFromLocalPlaylist(list.ID, 0)
	after, _ := store.LocalPlaylist(list.ID)
	if len(after.Items) != 1 || after.Items[0].ID != "am:1" {
		t.Fatalf("after edit = %#v", after.Items)
	}
	store.DeleteLocalPlaylist(list.ID)
	if len(store.Playlists) != 0 {
		t.Fatal("delete failed")
	}
}

func TestLoadMissingFileReturnsEmptyStore(t *testing.T) {
	store, err := Load(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatal(err)
	}
	if store == nil || len(store.Presets) != 0 {
		t.Fatalf("store = %#v", store)
	}
}

func TestRankNilStoreAndStableOrder(t *testing.T) {
	var store *Store
	items := []core.Item{{Kind: "song", ID: "a"}, {Kind: "song", ID: "b"}}
	ranked := store.Rank("focus", items)
	if len(ranked) != 2 || ranked[0].ID != "a" || ranked[1].ID != "b" {
		t.Fatalf("ranked = %#v, want provider order", ranked)
	}
	ranked[0].ID = "changed"
	if items[0].ID != "a" {
		t.Fatal("Rank mutated the provider slice")
	}
}
