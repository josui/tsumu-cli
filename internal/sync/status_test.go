package sync

import (
	"testing"

	_ "modernc.org/sqlite"
)

func TestPendingCount_NoLastSynced(t *testing.T) {
	db := setupLocalDB(t)
	insertLocalBookmark(t, db, "bm1", "https://a.com", "A", "2026-01-01T00:00:00Z")
	insertLocalBookmark(t, db, "bm2", "https://b.com", "B", "2026-01-02T00:00:00Z")

	count := PendingCount(db, "")
	if count != 2 {
		t.Errorf("PendingCount('') = %d, want 2", count)
	}
}

func TestPendingCount_WithLastSynced(t *testing.T) {
	db := setupLocalDB(t)
	insertLocalBookmark(t, db, "bm1", "https://a.com", "A", "2026-01-01T00:00:00Z")
	insertLocalBookmark(t, db, "bm2", "https://b.com", "B", "2026-01-02T00:00:00Z")

	count := PendingCount(db, "2026-01-01T12:00:00Z")
	if count != 1 {
		t.Errorf("PendingCount = %d, want 1", count)
	}
}

func TestPendingCount_ExcludesSoftDeleted(t *testing.T) {
	db := setupLocalDB(t)
	insertLocalBookmark(t, db, "bm1", "https://a.com", "A", "2026-01-02T00:00:00Z")
	db.Exec(`UPDATE bookmarks SET deleted_at = '2026-01-03T00:00:00Z' WHERE id = 'bm1'`)

	count := PendingCount(db, "2026-01-01T00:00:00Z")
	if count != 0 {
		t.Errorf("PendingCount = %d, want 0 (soft deleted should be excluded)", count)
	}
}
