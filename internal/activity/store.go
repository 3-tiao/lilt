// Package activity owns lilt's local activity store: shared Items, Favorites,
// the full qualified playback history, and the derived play stats that power
// Recent. The database is the single source of truth for this data; only the
// server opens it. See docs/internals/local-activity.md for the plan and gates.
package activity

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// schemaVersion is the current on-disk schema version (PRAGMA user_version).
// Bump it and add a migration step whenever the DDL changes.
//
// v2 denormalizes the immutable item source into playback_history so a
// per-source history page is one ordered index range instead of "scan the
// source's rows, then sort".
const schemaVersion = 2

const ddl = `
CREATE TABLE IF NOT EXISTS items (
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
CREATE TABLE IF NOT EXISTS favorites (
    item_id  INTEGER PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
    added_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS playback_history (
    id        INTEGER PRIMARY KEY,
    item_id   INTEGER NOT NULL REFERENCES items(id) ON DELETE RESTRICT,
    source    TEXT NOT NULL,
    played_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS item_play_stats (
    item_id         INTEGER PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
    play_count      INTEGER NOT NULL CHECK (play_count > 0),
    first_played_at INTEGER NOT NULL,
    last_played_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS playback_history_item_time
    ON playback_history(item_id, played_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS playback_history_time
    ON playback_history(played_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS playback_history_source_time
    ON playback_history(source, played_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS item_play_stats_recent
    ON item_play_stats(last_played_at DESC, item_id);
`

// Item is the shared persistent entity behind favorites, history, and recent.
// Source/kind/stable identity are immutable; display fields are refreshed with
// the newest non-empty values.
type Item struct {
	Source       string
	Kind         string
	StableID     string
	ProviderID   string
	Ref          string
	Title        string
	Artist       string
	PublicURL    string
	MetadataJSON string
}

// Stats summarizes the qualified plays of one item.
type Stats struct {
	Ref         string
	PlayCount   int64
	FirstPlayed time.Time
	LastPlayed  time.Time
}

// HistoryEntry is one qualified playback of an item.
type HistoryEntry struct {
	Item     Item
	PlayedAt time.Time
}

// Cursor is the keyset pagination cursor of history.list: strict (playedAt, id)
// descending. A nil Cursor means "start from the newest entry".
type Cursor struct {
	PlayedAtMS int64
	ID         int64
}

// HistoryPage is one page of history.list plus the cursor to continue from.
type HistoryPage struct {
	Entries    []HistoryEntry
	NextCursor *Cursor // nil when the page is the last one
}

// HistoryQuery selects one page of history. Source is an exact source id (empty
// means every source). Before is the exclusive keyset cursor; nil starts at the
// newest entry. Limit is the page size.
//
// Source is applied inside the query: filtering after a page was read would
// return an empty page whenever the newest rows belong to another source.
type HistoryQuery struct {
	Source string
	Before *Cursor
	Limit  int
}

// DB wraps the SQLite activity database. All mutations run in transactions that
// update items, history, and stats together so derived data never drifts.
type DB struct {
	sql *sql.DB
}

// Open opens (creating if needed) the activity database at path, applies
// pragmas, and ensures the schema. A database written by a newer schema version
// is rejected without touching the file.
func Open(path string) (*DB, error) {
	handle, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// The server serializes mutations; one connection keeps SQLite's lock
	// semantics simple and deterministic.
	handle.SetMaxOpenConns(1)
	db := &DB{sql: handle}
	if err := db.pragmas(); err != nil {
		handle.Close()
		return nil, err
	}
	if err := db.ensureSchema(); err != nil {
		handle.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) pragmas() error {
	for _, pragma := range []string{
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=FULL",
	} {
		if _, err := db.sql.Exec(pragma); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) ensureSchema() error {
	var version int64
	if err := db.sql.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > schemaVersion {
		return fmt.Errorf("activity database version %d is newer than supported version %d", version, schemaVersion)
	}
	if version == schemaVersion {
		return nil
	}
	if version == 0 {
		return db.createSchema()
	}
	if version == 1 {
		return db.migrateV1ToV2()
	}
	return fmt.Errorf("activity database version %d cannot be migrated to version %d", version, schemaVersion)
}

func (db *DB) createSchema() error {
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(ddl); err != nil {
		return err
	}
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version=%d", schemaVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

// migrateV1ToV2 denormalizes the immutable item source onto playback_history
// and adds the per-source ordered index. An item's source never changes once
// created, so the backfill is deterministic.
func (db *DB) migrateV1ToV2() error {
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stmt := range []string{
		`ALTER TABLE playback_history ADD COLUMN source TEXT NOT NULL DEFAULT ''`,
		`UPDATE playback_history SET source = (SELECT source FROM items WHERE items.id = playback_history.item_id)`,
		`CREATE INDEX IF NOT EXISTS playback_history_source_time ON playback_history(source, played_at DESC, id DESC)`,
		"PRAGMA user_version=2",
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Close closes the underlying database handle.
func (db *DB) Close() error { return db.sql.Close() }

// itemColumns is the shared item projection. Nullable columns are coalesced so
// the scanner can keep using plain strings; the schema allows NULL, and rows
// written before a field had a value must still load.
const itemColumns = `i.source, i.kind, i.stable_id, COALESCE(i.provider_id, ''), i.ref, i.title,
        COALESCE(i.artist, ''), COALESCE(i.public_url, ''), COALESCE(i.metadata_json, '')`

const upsertItemSQL = `
INSERT INTO items (source, kind, stable_id, provider_id, ref, title, artist, public_url, metadata_json, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(source, stable_id) DO UPDATE SET
    title = CASE WHEN excluded.title != '' THEN excluded.title ELSE items.title END,
    artist = CASE WHEN COALESCE(excluded.artist, '') != '' THEN excluded.artist ELSE items.artist END,
    public_url = CASE WHEN COALESCE(excluded.public_url, '') != '' THEN excluded.public_url ELSE items.public_url END,
    metadata_json = CASE WHEN COALESCE(excluded.metadata_json, '') != '' THEN excluded.metadata_json ELSE items.metadata_json END,
    updated_at = excluded.updated_at
RETURNING id`

// upsertItem returns the item's row id, creating the row or refreshing its
// display snapshot. Identity fields never change once created.
func upsertItem(tx *sql.Tx, item Item, atMS int64) (int64, error) {
	var id int64
	err := tx.QueryRow(upsertItemSQL,
		item.Source, item.Kind, item.StableID, item.ProviderID, item.Ref,
		item.Title, item.Artist, item.PublicURL, item.MetadataJSON, atMS, atMS,
	).Scan(&id)
	return id, err
}

// RecordQualifiedPlay persists one playback that reached the "listened"
// threshold: item upsert, one immutable history row, and the stats update in a
// single transaction. Replays append new history rows and advance stats.
func (db *DB) RecordQualifiedPlay(item Item, playedAt time.Time) error {
	atMS := playedAt.UnixMilli()
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	itemID, err := upsertItem(tx, item, atMS)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(
		"INSERT INTO playback_history (item_id, source, played_at) VALUES (?, ?, ?)",
		itemID, item.Source, atMS,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(`
        INSERT INTO item_play_stats (item_id, play_count, first_played_at, last_played_at)
        VALUES (?, 1, ?, ?)
        ON CONFLICT(item_id) DO UPDATE SET
            play_count = play_count + 1,
            last_played_at = excluded.last_played_at`,
		itemID, atMS, atMS,
	); err != nil {
		return err
	}
	return tx.Commit()
}

// SetFavorite idempotently adds or removes a favorite. A repeated add keeps the
// original added_at; removing a missing favorite succeeds.
func (db *DB) SetFavorite(item Item, favorited bool, at time.Time) error {
	atMS := at.UnixMilli()
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	itemID, err := upsertItem(tx, item, atMS)
	if err != nil {
		return err
	}
	if favorited {
		if _, err := tx.Exec(
			"INSERT OR IGNORE INTO favorites (item_id, added_at) VALUES (?, ?)",
			itemID, atMS,
		); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec("DELETE FROM favorites WHERE item_id = ?", itemID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListFavorites returns every favorite, newest first, joined with its item.
func (db *DB) ListFavorites() ([]Item, error) {
	rows, err := db.sql.Query(`
        SELECT ` + itemColumns + `
        FROM favorites f JOIN items i ON i.id = f.item_id
        ORDER BY f.added_at DESC, f.item_id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanItems(rows)
}

// RecentEntry pairs an item with the last time it was qualified-played.
type RecentEntry struct {
	Item     Item
	PlayedAt time.Time
}

// RecentEntries returns the most recently played distinct items with their
// last-play time, derived from item_play_stats, newest first.
func (db *DB) RecentEntries(limit int) ([]RecentEntry, error) {
	rows, err := db.sql.Query(`
        SELECT `+itemColumns+`, s.last_played_at
        FROM item_play_stats s JOIN items i ON i.id = s.item_id
        ORDER BY s.last_played_at DESC, s.item_id DESC
        LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []RecentEntry{}
	for rows.Next() {
		var (
			entry    RecentEntry
			playedMS int64
		)
		if err := rows.Scan(
			&entry.Item.Source, &entry.Item.Kind, &entry.Item.StableID,
			&entry.Item.ProviderID, &entry.Item.Ref, &entry.Item.Title,
			&entry.Item.Artist, &entry.Item.PublicURL, &entry.Item.MetadataJSON,
			&playedMS,
		); err != nil {
			return nil, err
		}
		entry.PlayedAt = timeFromMS(playedMS)
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// FindItemByRef returns the stored item for a ref, if the activity store
// already knows it.
func (db *DB) FindItemByRef(ref string) (Item, bool, error) {
	var item Item
	err := db.sql.QueryRow(`
        SELECT `+itemColumns+`
        FROM items i WHERE i.ref = ?`, ref).Scan(
		&item.Source, &item.Kind, &item.StableID, &item.ProviderID,
		&item.Ref, &item.Title, &item.Artist, &item.PublicURL, &item.MetadataJSON,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, false, nil
	}
	if err != nil {
		return Item{}, false, err
	}
	return item, true, nil
}

// RemoveFavorite idempotently removes a favorite by (source, stable id).
// Removing an unknown favorite succeeds.
func (db *DB) RemoveFavorite(source, stableID string) error {
	_, err := db.sql.Exec(`
        DELETE FROM favorites
        WHERE item_id = (SELECT id FROM items WHERE source = ? AND stable_id = ?)`,
		source, stableID)
	return err
}

// RecentItems returns the most recently played distinct items, derived from
// item_play_stats; the raw history table is never scanned.
func (db *DB) RecentItems(limit int) ([]Item, error) {
	rows, err := db.sql.Query(`
        SELECT `+itemColumns+`
        FROM item_play_stats s JOIN items i ON i.id = s.item_id
        ORDER BY s.last_played_at DESC, s.item_id DESC
        LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanItems(rows)
}

// StatsForRefs resolves a batch of refs to their play stats in one query.
// Unknown refs come back with PlayCount 0 and zero times, keeping input order.
func (db *DB) StatsForRefs(refs []string) ([]Stats, error) {
	stats := make([]Stats, len(refs))
	for i, ref := range refs {
		stats[i].Ref = ref
	}
	for start := 0; start < len(refs); start += 500 {
		end := min(start+500, len(refs))
		batch := refs[start:end]
		query := `
            SELECT i.ref, COALESCE(s.play_count, 0), COALESCE(s.first_played_at, 0), COALESCE(s.last_played_at, 0)
            FROM items i LEFT JOIN item_play_stats s ON s.item_id = i.id
            WHERE i.ref IN (` + strings.Repeat("?,", len(batch)-1) + "?)"
		args := make([]any, len(batch))
		for i, ref := range batch {
			args[i] = ref
		}
		rows, err := db.sql.Query(query, args...)
		if err != nil {
			return nil, err
		}
		found := map[string]Stats{}
		for rows.Next() {
			var s Stats
			var firstMS, lastMS int64
			if err := rows.Scan(&s.Ref, &s.PlayCount, &firstMS, &lastMS); err != nil {
				rows.Close()
				return nil, err
			}
			s.FirstPlayed, s.LastPlayed = timeFromMS(firstMS), timeFromMS(lastMS)
			found[s.Ref] = s
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		for i := start; i < end; i++ {
			if s, ok := found[refs[i]]; ok {
				stats[i] = s
			}
		}
	}
	return stats, nil
}

// HistoryPage reads one keyset-paginated page of the history. The cursor is
// strict (playedAt, id) descending, so pages stay stable while new entries are
// appended.
func (db *DB) HistoryPage(query HistoryQuery) (HistoryPage, error) {
	limit := query.Limit
	if limit <= 0 {
		return HistoryPage{}, fmt.Errorf("history page limit must be positive, got %d", limit)
	}
	const columns = `SELECT ` + itemColumns + `, h.played_at, h.id
            FROM playback_history h JOIN items i ON i.id = h.item_id`
	conditions := []string{}
	args := []any{}
	if query.Source != "" {
		conditions = append(conditions, "h.source = ?")
		args = append(args, query.Source)
	}
	if query.Before != nil {
		conditions = append(conditions, "(h.played_at, h.id) < (?, ?)")
		args = append(args, query.Before.PlayedAtMS, query.Before.ID)
	}
	sqlText := columns
	if len(conditions) > 0 {
		sqlText += "\n            WHERE " + strings.Join(conditions, " AND ")
	}
	sqlText += "\n            ORDER BY h.played_at DESC, h.id DESC\n            LIMIT ?"
	// One extra row is read as the lookahead that decides whether a next page
	// exists; a page that exactly exhausts the history must not promise one.
	args = append(args, limit+1)

	rows, err := db.sql.Query(sqlText, args...)
	if err != nil {
		return HistoryPage{}, err
	}
	defer rows.Close()
	page := HistoryPage{Entries: []HistoryEntry{}}
	var pageCursor Cursor
	read := 0
	for rows.Next() {
		var (
			entry    HistoryEntry
			playedMS int64
			rowID    int64
		)
		if err := rows.Scan(
			&entry.Item.Source, &entry.Item.Kind, &entry.Item.StableID,
			&entry.Item.ProviderID, &entry.Item.Ref, &entry.Item.Title,
			&entry.Item.Artist, &entry.Item.PublicURL, &entry.Item.MetadataJSON,
			&playedMS, &rowID,
		); err != nil {
			return HistoryPage{}, err
		}
		read++
		if len(page.Entries) < limit {
			entry.PlayedAt = timeFromMS(playedMS)
			page.Entries = append(page.Entries, entry)
			// The cursor is the page's last row, never the lookahead row.
			pageCursor = Cursor{PlayedAtMS: playedMS, ID: rowID}
		}
	}
	if err := rows.Err(); err != nil {
		return HistoryPage{}, err
	}
	if read > limit {
		page.NextCursor = &pageCursor
	}
	// The row id only feeds the keyset cursor; it is not part of the public
	// entry.
	return page, nil
}

// ClearHistory removes every history row and the derived stats. Favorites and
// items are kept. Returns the number of removed history rows.
func (db *DB) ClearHistory() (int64, error) {
	tx, err := db.sql.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var removed int64
	if err := tx.QueryRow("SELECT COUNT(*) FROM playback_history").Scan(&removed); err != nil {
		return 0, err
	}
	for _, stmt := range []string{
		"DELETE FROM item_play_stats",
		"DELETE FROM playback_history",
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return 0, err
		}
	}
	return removed, tx.Commit()
}

func scanItems(rows *sql.Rows) ([]Item, error) {
	items := []Item{}
	for rows.Next() {
		var item Item
		if err := rows.Scan(
			&item.Source, &item.Kind, &item.StableID, &item.ProviderID,
			&item.Ref, &item.Title, &item.Artist, &item.PublicURL, &item.MetadataJSON,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func timeFromMS(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}
