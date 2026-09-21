package activity

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "activity.sqlite3"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestOpenCreatesPrivateDurableDirectoryAndDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "activity.sqlite3")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	for _, target := range []string{filepath.Dir(path), path} {
		info, err := os.Stat(target)
		if err != nil {
			t.Fatalf("stat %s: %v", target, err)
		}
		if got := info.Mode().Perm(); got&0077 != 0 {
			t.Fatalf("%s permissions = %04o, want private", target, got)
		}
	}
}

func sampleItem(n int) Item {
	return Item{
		Source:     "apple-music",
		Kind:       "song",
		StableID:   "am:100000",
		ProviderID: "100000",
		Ref:        "apple-music:song:100000",
		Title:      "Aruarian Dance",
		Artist:     "Nujabes",
	}
}

func TestRecordQualifiedPlayAppendsHistoryAndStats(t *testing.T) {
	db := openTestDB(t)
	base := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

	if err := db.RecordQualifiedPlay(sampleItem(1), base); err != nil {
		t.Fatalf("first play: %v", err)
	}
	if err := db.RecordQualifiedPlay(sampleItem(1), base.Add(time.Minute)); err != nil {
		t.Fatalf("replay: %v", err)
	}

	page, err := db.HistoryPage(HistoryQuery{Limit: 10})
	if err != nil {
		t.Fatalf("HistoryPage: %v", err)
	}
	if len(page.Entries) != 2 {
		t.Fatalf("history entries = %d, want 2", len(page.Entries))
	}
	if !page.Entries[0].PlayedAt.After(page.Entries[1].PlayedAt) {
		t.Fatalf("history not newest first: %v", page.Entries)
	}

	stats, err := db.StatsForRefs([]string{sampleItem(1).Ref, "apple-music:song:unknown"})
	if err != nil {
		t.Fatalf("StatsForRefs: %v", err)
	}
	if stats[0].PlayCount != 2 {
		t.Fatalf("play count = %d, want 2", stats[0].PlayCount)
	}
	if stats[0].FirstPlayed != base || stats[0].LastPlayed != base.Add(time.Minute) {
		t.Fatalf("stats times = %v/%v", stats[0].FirstPlayed, stats[0].LastPlayed)
	}
	if stats[1].PlayCount != 0 || !stats[1].FirstPlayed.IsZero() {
		t.Fatalf("unknown ref stats = %+v", stats[1])
	}
}

func TestRecentIsDerivedFromStats(t *testing.T) {
	db := openTestDB(t)
	base := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	first := sampleItem(1)
	second := sampleItem(2)
	second.StableID, second.Ref = "am:2", "apple-music:song:2"
	second.Title = "Second"

	if err := db.RecordQualifiedPlay(first, base); err != nil {
		t.Fatalf("play first: %v", err)
	}
	if err := db.RecordQualifiedPlay(second, base.Add(time.Minute)); err != nil {
		t.Fatalf("play second: %v", err)
	}
	recent, err := db.RecentItems(10)
	if err != nil {
		t.Fatalf("RecentItems: %v", err)
	}
	if len(recent) != 2 || recent[0].Title != "Second" {
		t.Fatalf("recent = %+v", recent)
	}
}

func TestHistoryPaginationByKeyset(t *testing.T) {
	db := openTestDB(t)
	base := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		item := sampleItem(i)
		item.StableID = "am:" + string(rune('a'+i))
		item.Ref = "apple-music:song:" + item.StableID
		if err := db.RecordQualifiedPlay(item, base.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("play %d: %v", i, err)
		}
	}

	first, err := db.HistoryPage(HistoryQuery{Limit: 2})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if len(first.Entries) != 2 || first.NextCursor == nil {
		t.Fatalf("first page = %+v", first)
	}
	second, err := db.HistoryPage(HistoryQuery{Before: first.NextCursor, Limit: 2})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	// Pages must not overlap or skip.
	if second.Entries[0].PlayedAt.After(first.Entries[1].PlayedAt) {
		t.Fatalf("page overlap: %+v", second)
	}
	third, err := db.HistoryPage(HistoryQuery{Before: second.NextCursor, Limit: 2})
	if err != nil {
		t.Fatalf("third page: %v", err)
	}
	if len(third.Entries) != 1 || third.NextCursor != nil {
		t.Fatalf("third page = %+v", third)
	}
}

func TestFavoriteSetIsIdempotentAndKeepsAddedAt(t *testing.T) {
	db := openTestDB(t)
	first := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	second := first.Add(time.Hour)

	if err := db.SetFavorite(sampleItem(1), true, first); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := db.SetFavorite(sampleItem(1), true, second); err != nil {
		t.Fatalf("re-add: %v", err)
	}
	favorites, err := db.ListFavorites()
	if err != nil {
		t.Fatalf("ListFavorites: %v", err)
	}
	if len(favorites) != 1 {
		t.Fatalf("favorites = %d rows, want 1", len(favorites))
	}

	if err := db.SetFavorite(sampleItem(1), false, second); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := db.SetFavorite(sampleItem(1), false, second); err != nil {
		t.Fatalf("re-remove: %v", err)
	}
	favorites, err = db.ListFavorites()
	if err != nil {
		t.Fatalf("ListFavorites after remove: %v", err)
	}
	if len(favorites) != 0 {
		t.Fatalf("favorites after remove = %+v", favorites)
	}
}

func TestClearHistoryKeepsFavorites(t *testing.T) {
	db := openTestDB(t)
	base := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	if err := db.RecordQualifiedPlay(sampleItem(1), base); err != nil {
		t.Fatalf("play: %v", err)
	}
	if err := db.SetFavorite(sampleItem(1), true, base); err != nil {
		t.Fatalf("favorite: %v", err)
	}
	removed, err := db.ClearHistory()
	if err != nil {
		t.Fatalf("ClearHistory: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	favorites, err := db.ListFavorites()
	if err != nil || len(favorites) != 1 {
		t.Fatalf("favorites after clear = %+v err=%v", favorites, err)
	}
	recent, err := db.RecentItems(10)
	if err != nil || len(recent) != 0 {
		t.Fatalf("recent after clear = %+v err=%v", recent, err)
	}
}

func TestFutureSchemaVersionIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.sqlite3")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := db.sql.Exec("PRAGMA user_version=99"); err != nil {
		t.Fatalf("bump version: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reopened, err := Open(path)
	if err == nil {
		reopened.Close()
		t.Fatal("opening a future-version database must fail")
	}
}
