package activity

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

var resetStamp = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

func archiveName(path, suffix string) string {
	return path + ".archive-20260920T120000.000000000Z" + suffix
}

// Reset closes the live handle, archives the database with its companions, and
// returns a fresh empty store.
func TestResetArchivesAndRecreatesEmptyStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.sqlite3")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	base := time.Date(2026, 9, 20, 11, 0, 0, 0, time.UTC)
	if err := db.RecordQualifiedPlay(sampleItem(1), base); err != nil {
		t.Fatalf("play: %v", err)
	}
	if err := db.SetFavorite(sampleItem(1), true, base); err != nil {
		t.Fatalf("favorite: %v", err)
	}

	fresh, result, err := Reset(db, path, resetStamp)
	if err != nil {
		t.Fatalf("Reset: %v", err)
	}
	t.Cleanup(func() { _ = fresh.Close() })

	if !result.Archived || result.ArchivePath != archiveName(path, "") {
		t.Fatalf("result = %+v", result)
	}
	if _, statErr := os.Stat(result.ArchivePath); statErr != nil {
		t.Fatalf("archive missing: %v", statErr)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("fresh database missing: %v", statErr)
	}
	// The archived file still holds the old data, the new store is empty. The
	// copy keeps a plain .sqlite3 name so the driver opens it as a database.
	archivedCopy := filepath.Join(t.TempDir(), "archived.sqlite3")
	contents, err := os.ReadFile(result.ArchivePath)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	if err := os.WriteFile(archivedCopy, contents, 0600); err != nil {
		t.Fatalf("copy archive: %v", err)
	}
	archived, err := Open(archivedCopy)
	if err != nil {
		t.Fatalf("opening the archive must work: %v", err)
	}
	defer archived.Close()
	stats, err := archived.StatsForRefs([]string{sampleItem(1).Ref})
	if err != nil || stats[0].PlayCount != 1 {
		t.Fatalf("archived stats = %+v err=%v", stats, err)
	}
	favorites, err := fresh.ListFavorites()
	if err != nil || len(favorites) != 0 {
		t.Fatalf("fresh favorites = %+v err=%v", favorites, err)
	}
	history, err := fresh.HistoryPage(HistoryQuery{Limit: 10})
	if err != nil || len(history.Entries) != 0 {
		t.Fatalf("fresh history = %+v err=%v", history, err)
	}
}

// A failed rename must leave every original file in place: reset never destroys
// data it could not archive.
func TestResetRestoresFilesWhenArchivingFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "activity.sqlite3")
	// A corrupt database plus a stray WAL companion: the main file renames
	// first, then the companion rename fails because its target is a directory.
	if err := os.WriteFile(path, []byte("not a database"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+"-wal", []byte("stray wal"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(archiveName(path, "-wal"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(archiveName(path, "-shm"), 0700); err != nil {
		t.Fatal(err)
	}

	if _, _, err := Reset(nil, path, resetStamp); err == nil {
		t.Fatal("Reset must fail when a companion cannot be archived")
	}
	// Rollback put the main file back and left the stray companion alone.
	restored, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("database was not restored: %v", err)
	}
	if string(restored) != "not a database" {
		t.Fatalf("restored content = %q", restored)
	}
	if _, statErr := os.Stat(archiveName(path, "")); statErr == nil {
		t.Fatal("partial archive left behind after rollback")
	}
}

// Reset creates a missing durable root just like the normal open path.
func TestResetCreatesMissingDatabaseDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "activity.sqlite3")
	db, _, err := Reset(nil, path, resetStamp)
	if err != nil {
		t.Fatalf("Reset: %v", err)
	}
	defer db.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("fresh database: %v", err)
	}
}

// The reset store is durable: it can be written to and reopened.
func TestResetStoreSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.sqlite3")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	base := time.Date(2026, 9, 20, 11, 0, 0, 0, time.UTC)
	if err := db.RecordQualifiedPlay(sampleItem(1), base); err != nil {
		t.Fatalf("play: %v", err)
	}
	fresh, _, err := Reset(db, path, resetStamp)
	if err != nil {
		t.Fatalf("Reset: %v", err)
	}
	after := sampleItem(1)
	after.StableID, after.ProviderID = "am:200000", "200000"
	after.Ref, after.Title = "apple-music:song:200000", "After Reset"
	if err := fresh.RecordQualifiedPlay(after, base.Add(time.Hour)); err != nil {
		t.Fatalf("play after reset: %v", err)
	}
	if err := fresh.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	page, err := reopened.HistoryPage(HistoryQuery{Limit: 10})
	if err != nil {
		t.Fatalf("HistoryPage: %v", err)
	}
	if len(page.Entries) != 1 || page.Entries[0].Item.Ref != after.Ref {
		t.Fatalf("history after reset = %+v", page.Entries)
	}
}
