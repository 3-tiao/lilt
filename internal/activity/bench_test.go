package activity

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The benchmarks model the Phase 0 admission gate from
// docs/internals/local-activity.md §5.2: 1,000,000 qualified plays, 100,000
// items, 10,000 favorites. They are opt-in (go test -bench) and never run as
// part of `go test ./...`; the fixture is generated once and cached on disk.
//
// Run: go test ./internal/activity -bench . -benchtime 10x -run '^$'

const (
	benchItems     = 100_000
	benchHistory   = 1_000_000
	benchFavorites = 10_000
)

func benchDBPath(t testing.TB) string {
	if path := os.Getenv("LILT_ACTIVITY_BENCH_DB"); path != "" {
		return path
	}
	return filepath.Join(os.TempDir(), "lilt-activity-bench", "activity.sqlite3")
}

func benchItem(i int) Item {
	switch {
	case i < 60_000:
		return Item{
			Source: "apple-music", Kind: "song",
			StableID: fmt.Sprintf("am:%d", i), ProviderID: fmt.Sprintf("%d", i),
			Ref:   fmt.Sprintf("apple-music:song:%d", i),
			Title: fmt.Sprintf("Track %06d", i), Artist: fmt.Sprintf("Artist %04d", i%4000),
		}
	case i < 85_000:
		n := i - 60_000
		return Item{
			Source: "audius", Kind: "song",
			StableID: fmt.Sprintf("audius:song:track-%d", n), ProviderID: fmt.Sprintf("track-%d", n),
			Ref:   fmt.Sprintf("audius:song:track-%d", n),
			Title: fmt.Sprintf("Track %06d", i), Artist: fmt.Sprintf("Artist %04d", i%4000),
		}
	default:
		n := i - 85_000
		return Item{
			Source: "radio", Kind: "stream",
			StableID: fmt.Sprintf("radio:https://bench.example/%d/stream", n),
			Ref:      fmt.Sprintf("radio:https://bench.example/%d/stream", n),
			Title:    fmt.Sprintf("Station %05d", n),
		}
	}
}

var benchFixture struct {
	once    sync.Once
	path    string
	dbBytes int64
	err     error
}

// benchFixtureDB generates (once) and opens the benchmark database.
func benchFixtureDB(t testing.TB) *DB {
	t.Helper()
	benchFixture.once.Do(func() {
		path := benchDBPath(t)
		benchFixture.path = path
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			benchFixture.err = err
			return
		}
		if info, err := os.Stat(path); err == nil && info.Size() > 0 {
			probe, openErr := Open(path)
			if openErr == nil {
				var items, history int64
				_ = probe.sql.QueryRow("SELECT COUNT(*) FROM items").Scan(&items)
				_ = probe.sql.QueryRow("SELECT COUNT(*) FROM playback_history").Scan(&history)
				probe.Close()
				if items == benchItems && history == benchHistory {
					benchFixture.dbBytes = info.Size()
					benchBenchDB, benchFixture.err = Open(path)
					return
				}
			}
		}
		_ = os.Remove(path)
		start := time.Now()
		benchFixture.err = generateFixture(path)
		if benchFixture.err != nil {
			return
		}
		info, _ := os.Stat(path)
		benchFixture.dbBytes = info.Size()
		t.Logf("fixture generated in %s (%.1f MB)", time.Since(start), float64(info.Size())/1e6)
		benchBenchDB, benchFixture.err = Open(path)
	})
	if benchFixture.err != nil {
		t.Fatalf("fixture: %v", benchFixture.err)
	}
	return benchBenchDB
}

var benchBenchDB *DB

// generateFixture bulk-loads the fixture with the same tables the production
// code writes, keeping item_play_stats derived from history in one pass.
func generateFixture(path string) error {
	db, err := Open(path)
	if err != nil {
		return err
	}
	defer db.Close()
	rng := rand.New(rand.NewSource(42))
	base := time.Date(2025, 9, 20, 0, 0, 0, 0, time.UTC)

	// Items, 1000 rows per multi-row insert.
	if err := bulkRows(db, benchItems, 1000, 5000, func(start, end int) (string, []any) {
		var sb strings.Builder
		sb.WriteString(`INSERT INTO items (source, kind, stable_id, provider_id, ref, title, artist, public_url, metadata_json, created_at, updated_at) VALUES `)
		args := make([]any, 0, (end-start)*11)
		for i := start; i < end; i++ {
			if i > start {
				sb.WriteByte(',')
			}
			sb.WriteString("(?,?,?,?,?,?,?,?,?,?,?)")
			item := benchItem(i)
			args = append(args, item.Source, item.Kind, item.StableID, item.ProviderID,
				item.Ref, item.Title, item.Artist, item.PublicURL, item.MetadataJSON, 0, 0)
		}
		return sb.String(), args
	}); err != nil {
		return err
	}

	// History, 1M rows with strictly increasing played_at (30s apart). The source
	// is copied from the item row so the fixture matches what the writer stores.
	if err := bulkRows(db, benchHistory, 1000, 5000, func(start, end int) (string, []any) {
		var sb strings.Builder
		sb.WriteString(`INSERT INTO playback_history (item_id, source, played_at) VALUES `)
		args := make([]any, 0, (end-start)*3)
		for i := start; i < end; i++ {
			if i > start {
				sb.WriteByte(',')
			}
			sb.WriteString("(?,?,?)")
			itemID := 1 + rng.Intn(benchItems)
			at := base.Add(time.Duration(i) * 30 * time.Second).UnixMilli()
			args = append(args, itemID, benchItem(itemID-1).Source, at)
		}
		return sb.String(), args
	}); err != nil {
		return err
	}

	// Derived stats in one pass.
	if _, err := db.sql.Exec(`
        INSERT INTO item_play_stats (item_id, play_count, first_played_at, last_played_at)
        SELECT item_id, COUNT(*), MIN(played_at), MAX(played_at)
        FROM playback_history GROUP BY item_id`); err != nil {
		return err
	}

	// Favorites: the newest 10,000 items.
	if _, err := db.sql.Exec(`
        INSERT INTO favorites (item_id, added_at)
        SELECT id, 0 FROM items ORDER BY id DESC LIMIT 10000`); err != nil {
		return err
	}
	// VACUUM after the bulk load so reported file size is steady-state.
	if _, err := db.sql.Exec("VACUUM"); err != nil {
		return err
	}
	return nil
}

// bulkRows writes count rows in multi-row INSERT statements of batchSize rows,
// grouping batchSize*rowsPerTransaction rows per transaction.
func bulkRows(db *DB, count, batchSize, rowsPerTransaction int, build func(start, end int) (string, []any)) error {
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	inTx := 0
	for start := 0; start < count; start += batchSize {
		end := min(start+batchSize, count)
		query, args := build(start, end)
		if _, err := tx.Exec(query, args...); err != nil {
			tx.Rollback()
			return fmt.Errorf("rows %d-%d: %w", start, end, err)
		}
		inTx += end - start
		if inTx >= rowsPerTransaction {
			if err := tx.Commit(); err != nil {
				return err
			}
			if tx, err = db.sql.Begin(); err != nil {
				return err
			}
			inTx = 0
		}
	}
	if inTx > 0 {
		return tx.Commit()
	}
	return tx.Commit()
}

func benchRefs(t testing.TB, n int) []string {
	t.Helper()
	rng := rand.New(rand.NewSource(7))
	refs := make([]string, n)
	for i := range refs {
		refs[i] = benchItem(rng.Intn(benchItems)).Ref
	}
	return refs
}

func BenchmarkOpenAndStartupQueries(b *testing.B) {
	benchFixtureDB(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		start := time.Now()
		db, err := Open(benchFixture.path)
		if err != nil {
			b.Fatalf("Open: %v", err)
		}
		recent, err := db.RecentItems(100)
		if err != nil {
			b.Fatalf("RecentItems: %v", err)
		}
		favorites, err := db.ListFavorites()
		if err != nil {
			b.Fatalf("ListFavorites: %v", err)
		}
		_ = db.Close()
		if len(recent) != 100 || len(favorites) != benchFavorites {
			b.Fatalf("startup rows: recent=%d favorites=%d", len(recent), len(favorites))
		}
		b.ReportMetric(float64(time.Since(start).Microseconds()), "µs/op-wallclock")
	}
}

func BenchmarkRecent100(b *testing.B) {
	db := benchFixtureDB(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		recent, err := db.RecentItems(100)
		if err != nil || len(recent) != 100 {
			b.Fatalf("recent=%d err=%v", len(recent), err)
		}
	}
}

func BenchmarkStats500Refs(b *testing.B) {
	db := benchFixtureDB(b)
	refs := benchRefs(b, 500)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		stats, err := db.StatsForRefs(refs)
		if err != nil || len(stats) != 500 {
			b.Fatalf("stats=%d err=%v", len(stats), err)
		}
	}
}

func BenchmarkHistoryPage200(b *testing.B) {
	db := benchFixtureDB(b)
	// Start from a mid-history cursor so the benchmark exercises the keyset
	// seek, not just the newest rows.
	var midID, midPlayed int64
	if err := db.sql.QueryRow(
		"SELECT id, played_at FROM playback_history WHERE id = (SELECT id FROM playback_history WHERE id = 500000)",
	).Scan(&midID, &midPlayed); err != nil {
		b.Fatalf("cursor seed: %v", err)
	}
	cursor := &Cursor{PlayedAtMS: midPlayed, ID: midID}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		page, err := db.HistoryPage(HistoryQuery{Before: cursor, Limit: 200})
		if err != nil || len(page.Entries) != 200 {
			b.Fatalf("page=%d err=%v", len(page.Entries), err)
		}
	}
}

// A source-filtered page must cost the same as an unfiltered one: the
// per-source keyset index supplies the order without sorting.
func BenchmarkHistoryPageSource200(b *testing.B) {
	db := benchFixtureDB(b)
	// radio items are the top ids, and the fixture interleaves them across the
	// history, so this is a sparse-source page rather than a contiguous block.
	var midID, midPlayed int64
	if err := db.sql.QueryRow(
		"SELECT id, played_at FROM playback_history WHERE id = 500000",
	).Scan(&midID, &midPlayed); err != nil {
		b.Fatalf("cursor seed: %v", err)
	}
	cursor := &Cursor{PlayedAtMS: midPlayed, ID: midID}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		page, err := db.HistoryPage(HistoryQuery{Source: "radio", Before: cursor, Limit: 200})
		if err != nil {
			b.Fatalf("page: %v", err)
		}
		if len(page.Entries) != 200 {
			b.Fatalf("page has %d entries, want 200", len(page.Entries))
		}
	}
}

func BenchmarkRecordQualifiedPlay(b *testing.B) {
	db := benchFixtureDB(b)
	at := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		item := benchItem(i % benchItems)
		if err := db.RecordQualifiedPlay(item, at.Add(time.Duration(i)*time.Second)); err != nil {
			b.Fatalf("record: %v", err)
		}
	}
}

// TestReportFixtureSize logs the steady-state database size so the gate
// (< 250 MB) is visible next to the benchmark numbers.
func TestReportFixtureSize(t *testing.T) {
	if os.Getenv("LILT_ACTIVITY_BENCH") == "" {
		t.Skip("bench-only report; run benchmarks with LILT_ACTIVITY_BENCH=1")
	}
	benchFixtureDB(t)
	t.Logf("fixture size: %.1f MB", float64(benchFixture.dbBytes)/1e6)
}
