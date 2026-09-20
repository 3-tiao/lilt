package activity

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// v1DDL is the frozen schema version 1, without the denormalized history source
// or its per-source index. Only migrations may depend on it.
const v1DDL = `
CREATE TABLE items (
    id            INTEGER PRIMARY KEY,
    source        TEXT NOT NULL,
    kind          TEXT NOT NULL,
    stable_id     TEXT NOT NULL,
    provider_id   TEXT,
    ref           TEXT NOT NULL,
    title         TEXT NOT NULL,
    artist        TEXT,
    public_url    TEXT,
    metadata_json TEXT,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL,
    UNIQUE (source, stable_id),
    UNIQUE (ref)
);
CREATE TABLE favorites (
    item_id  INTEGER PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
    added_at INTEGER NOT NULL
);
CREATE TABLE playback_history (
    id        INTEGER PRIMARY KEY,
    item_id   INTEGER NOT NULL REFERENCES items(id) ON DELETE RESTRICT,
    played_at INTEGER NOT NULL
);
CREATE TABLE item_play_stats (
    item_id         INTEGER PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
    play_count      INTEGER NOT NULL CHECK (play_count > 0),
    first_played_at INTEGER NOT NULL,
    last_played_at  INTEGER NOT NULL
);
CREATE INDEX playback_history_item_time
    ON playback_history(item_id, played_at DESC, id DESC);
CREATE INDEX playback_history_time
    ON playback_history(played_at DESC, id DESC);
CREATE INDEX item_play_stats_recent
    ON item_play_stats(last_played_at DESC, item_id);
`

// A version 1 database migrates in place: the source column is backfilled from
// items and per-source pagination works afterwards.
func TestMigrateV1BackfillsHistorySource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.sqlite3")
	handle, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open v1: %v", err)
	}
	if _, err := handle.Exec(v1DDL); err != nil {
		t.Fatalf("v1 ddl: %v", err)
	}
	base := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	atMS := base.UnixMilli()
	if _, err := handle.Exec(`
        INSERT INTO items (source, kind, stable_id, provider_id, ref, title, created_at, updated_at)
        VALUES ('radio', 'stream', 'radio:https://x/stream', '', 'radio:https://x/stream', 'Station', ?, ?)`,
		atMS, atMS); err != nil {
		t.Fatalf("seed item: %v", err)
	}
	if _, err := handle.Exec(
		"INSERT INTO playback_history (item_id, played_at) VALUES (1, ?)", atMS); err != nil {
		t.Fatalf("seed history: %v", err)
	}
	if _, err := handle.Exec("PRAGMA user_version=1"); err != nil {
		t.Fatalf("set version: %v", err)
	}
	if err := handle.Close(); err != nil {
		t.Fatalf("close v1: %v", err)
	}

	db, err := Open(path)
	if err != nil {
		t.Fatalf("migrating open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	var version int64
	if err := db.sql.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != schemaVersion {
		t.Fatalf("version after migration = %d, want %d", version, schemaVersion)
	}
	page, err := db.HistoryPage(HistoryQuery{Source: "radio", Limit: 10})
	if err != nil {
		t.Fatalf("HistoryPage: %v", err)
	}
	if len(page.Entries) != 1 {
		t.Fatalf("backfilled history = %+v, want the migrated row", page.Entries)
	}
	details := queryPlan(t, db, `SELECT i.source FROM playback_history h JOIN items i ON i.id = h.item_id
        WHERE h.source = ? ORDER BY h.played_at DESC, h.id DESC LIMIT 10`, "radio")
	if !planContains(details, "playback_history_source_time") {
		t.Fatalf("migration did not create the per-source index: %v", details)
	}
	// New rows carry the source after migration too.
	if err := db.RecordQualifiedPlay(Item{
		Source: "apple-music", Kind: "song", StableID: "am:1", Ref: "apple-music:song:1",
		ProviderID: "1", Title: "Song",
	}, base.Add(time.Hour)); err != nil {
		t.Fatalf("record after migration: %v", err)
	}
	apple, err := db.HistoryPage(HistoryQuery{Source: "apple-music", Limit: 10})
	if err != nil || len(apple.Entries) != 1 {
		t.Fatalf("apple history after migration = %+v err=%v", apple.Entries, err)
	}
}

// Migrations are idempotent: opening an already-migrated database changes
// nothing and keeps the data.
func TestMigrationIsIdempotentOnReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.sqlite3")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	base := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	if err := db.RecordQualifiedPlay(sampleItem(1), base); err != nil {
		t.Fatalf("play: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	for i := 0; i < 3; i++ {
		reopened, err := Open(path)
		if err != nil {
			t.Fatalf("reload %d: %v", i, err)
		}
		page, err := reopened.HistoryPage(HistoryQuery{Limit: 10})
		if err != nil {
			t.Fatalf("reload %d page: %v", i, err)
		}
		if len(page.Entries) != 1 {
			t.Fatalf("reload %d history = %+v", i, page.Entries)
		}
		if err := reopened.Close(); err != nil {
			t.Fatalf("reload %d close: %v", i, err)
		}
	}
}

func TestUnknownSchemaVersionIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.sqlite3")
	handle, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := handle.Exec("PRAGMA user_version=1"); err != nil {
		t.Fatalf("set version: %v", err)
	}
	if err := handle.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("a v1 database without the v1 tables must not migrate silently")
	}
}
