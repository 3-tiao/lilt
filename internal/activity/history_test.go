package activity

import (
	"testing"
	"time"
)

// seedHistory writes n plays for one source, oldest first, one minute apart.
func seedHistory(t *testing.T, db *DB, source, prefix string, n int, base time.Time) {
	t.Helper()
	for i := 0; i < n; i++ {
		item := Item{
			Source:     source,
			Kind:       "song",
			StableID:   prefix + string(rune('a'+i%26)) + "-" + itoa(i),
			Ref:        source + ":song:" + prefix + itoa(i),
			Title:      prefix + itoa(i),
			ProviderID: prefix + itoa(i),
		}
		if source == "radio" {
			item.Kind = "stream"
		}
		if err := db.RecordQualifiedPlay(item, base.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("seed %s %d: %v", source, i, err)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// A source filter belongs in the query, not after pagination: the newest page
// can be entirely another source while the requested source has plenty of rows.
func TestHistoryPageFiltersBySourceBeforeLimiting(t *testing.T) {
	db := openTestDB(t)
	base := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	seedHistory(t, db, "apple-music", "am", 60, base)
	seedHistory(t, db, "radio", "r", 5, base.Add(time.Hour))

	page, err := db.HistoryPage(HistoryQuery{Source: "radio", Limit: 10})
	if err != nil {
		t.Fatalf("HistoryPage: %v", err)
	}
	if len(page.Entries) != 5 {
		t.Fatalf("radio entries = %d, want 5 (post-filtering would return 0)", len(page.Entries))
	}
	for _, entry := range page.Entries {
		if entry.Item.Source != "radio" {
			t.Fatalf("wrong source in filtered page: %+v", entry.Item)
		}
	}
	if page.NextCursor != nil {
		t.Fatalf("radio page is the last one, cursor = %+v", page.NextCursor)
	}

	apple, err := db.HistoryPage(HistoryQuery{Source: "apple-music", Limit: 10})
	if err != nil {
		t.Fatalf("HistoryPage apple: %v", err)
	}
	if len(apple.Entries) != 10 {
		t.Fatalf("apple entries = %d, want a full page of 10", len(apple.Entries))
	}
	if apple.NextCursor == nil {
		t.Fatal("apple page has more rows, want a cursor")
	}
	for _, entry := range apple.Entries {
		if entry.Item.Source != "apple-music" {
			t.Fatalf("wrong source in filtered page: %+v", entry.Item)
		}
	}
}

// Keyset pagination of a filtered query must stay stable across pages.
func TestHistoryPageFilteredPaginationDoesNotOverlapOrSkip(t *testing.T) {
	db := openTestDB(t)
	base := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	seedHistory(t, db, "apple-music", "am", 30, base)
	seedHistory(t, db, "radio", "r", 25, base.Add(time.Hour))

	var seen int
	var cursor *Cursor
	for page := 0; page < 10; page++ {
		result, err := db.HistoryPage(HistoryQuery{Source: "radio", Before: cursor, Limit: 10})
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		for _, entry := range result.Entries {
			if entry.Item.Source != "radio" {
				t.Fatalf("page %d leaked %q", page, entry.Item.Source)
			}
		}
		seen += len(result.Entries)
		if result.NextCursor == nil {
			break
		}
		cursor = result.NextCursor
	}
	if seen != 25 {
		t.Fatalf("filtered pagination returned %d rows, want 25", seen)
	}
}

// A source with no history returns an empty page and no cursor.
func TestHistoryPageUnknownSourceIsEmpty(t *testing.T) {
	db := openTestDB(t)
	seedHistory(t, db, "apple-music", "am", 3, time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC))

	page, err := db.HistoryPage(HistoryQuery{Source: "audius", Limit: 10})
	if err != nil {
		t.Fatalf("HistoryPage: %v", err)
	}
	if len(page.Entries) != 0 || page.NextCursor != nil {
		t.Fatalf("page = %+v, want empty without cursor", page)
	}
}

// The cursor exists only when a row beyond the page was actually read: a page
// exactly the size of the history must not promise another one.
func TestHistoryPageCursorOnlyWhenMoreRowsExist(t *testing.T) {
	base := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

	for _, test := range []struct {
		total      int
		limit      int
		wantCursor bool
	}{
		{49, 50, false},
		{50, 50, false},
		{51, 50, true},
		{100, 50, true},
	} {
		db := openTestDB(t)
		seedHistory(t, db, "apple-music", "am", test.total, base)

		page, err := db.HistoryPage(HistoryQuery{Limit: test.limit})
		if err != nil {
			t.Fatalf("total=%d: %v", test.total, err)
		}
		if got := page.NextCursor != nil; got != test.wantCursor {
			t.Fatalf("total=%d limit=%d: cursor=%v, want %v", test.total, test.limit, got, test.wantCursor)
		}
		want := test.limit
		if test.total < test.limit {
			want = test.total
		}
		if len(page.Entries) != want {
			t.Fatalf("total=%d limit=%d: entries=%d, want %d", test.total, test.limit, len(page.Entries), want)
		}
	}
}

// The last page of an exactly divisible history stops cleanly.
func TestHistoryPageLastPageHasNoCursor(t *testing.T) {
	db := openTestDB(t)
	base := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	seedHistory(t, db, "apple-music", "am", 100, base)

	first, err := db.HistoryPage(HistoryQuery{Limit: 50})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if len(first.Entries) != 50 || first.NextCursor == nil {
		t.Fatalf("first page = %d entries, cursor=%v", len(first.Entries), first.NextCursor)
	}
	second, err := db.HistoryPage(HistoryQuery{Before: first.NextCursor, Limit: 50})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if len(second.Entries) != 50 {
		t.Fatalf("second page = %d entries, want 50", len(second.Entries))
	}
	if second.NextCursor != nil {
		t.Fatalf("second page is last, cursor = %+v", second.NextCursor)
	}
}
