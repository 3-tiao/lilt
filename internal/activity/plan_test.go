package activity

import (
	"strings"
	"testing"
	"time"
)

// queryPlan returns the EXPLAIN QUERY PLAN detail lines for one statement.
func queryPlan(t *testing.T, db *DB, query string, args ...any) []string {
	t.Helper()
	rows, err := db.sql.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN: %v", err)
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var (
			id, parent, notUsed int
			detail              string
		)
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("plan rows: %v", err)
	}
	return details
}

func planContains(details []string, fragment string) bool {
	for _, detail := range details {
		if strings.Contains(detail, fragment) {
			return true
		}
	}
	return false
}

// Every critical query must stay on a bounded index path. A regression here
// becomes a full scan, or a sort of the whole history, once the store holds a
// million plays.
func TestCriticalQueriesUseBoundedIndexPaths(t *testing.T) {
	db := openTestDB(t)
	base := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	seedHistory(t, db, "apple-music", "am", 5, base)
	seedHistory(t, db, "radio", "r", 5, base.Add(time.Hour))

	for _, test := range []struct {
		name     string
		query    string
		wantPlan string
	}{
		{
			name: "recent derives from the stats index",
			query: `SELECT i.source FROM item_play_stats s JOIN items i ON i.id = s.item_id
                ORDER BY s.last_played_at DESC, s.item_id DESC LIMIT 100`,
			wantPlan: "item_play_stats_recent",
		},
		{
			name: "unfiltered history walks the global keyset index",
			query: `SELECT i.source FROM playback_history h JOIN items i ON i.id = h.item_id
                ORDER BY h.played_at DESC, h.id DESC LIMIT 200`,
			wantPlan: "playback_history_time",
		},
		{
			name: "filtered history walks the per-source keyset index",
			query: `SELECT i.source FROM playback_history h JOIN items i ON i.id = h.item_id
                WHERE h.source = 'radio' AND (h.played_at, h.id) < (1, 1)
                ORDER BY h.played_at DESC, h.id DESC LIMIT 200`,
			wantPlan: "playback_history_source_time",
		},
		{
			name:     "lookup by ref uses the unique ref index",
			query:    `SELECT source FROM items WHERE ref = 'x'`,
			wantPlan: "sqlite_autoindex_items_2",
		},
	} {
		details := queryPlan(t, db, test.query)
		if !planContains(details, test.wantPlan) {
			t.Errorf("%s: plan %v does not use %s", test.name, details, test.wantPlan)
		}
	}
}

// Paging history by source must not sort the source's rows first: the keyset
// index supplies the order.
func TestFilteredHistoryPlanDoesNotSort(t *testing.T) {
	db := openTestDB(t)
	seedHistory(t, db, "radio", "r", 5, time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC))

	details := queryPlan(t, db, `SELECT i.source FROM playback_history h JOIN items i ON i.id = h.item_id
        WHERE h.source = ? AND (h.played_at, h.id) < (?, ?)
        ORDER BY h.played_at DESC, h.id DESC LIMIT 200`, "radio", 1, 1)
	if planContains(details, "TEMP B-TREE") {
		t.Fatalf("filtered history sorts before LIMIT: %v", details)
	}
}
