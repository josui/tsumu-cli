// cli/internal/db/db_test.go
package db

import (
	"path/filepath"
	"testing"
)

func TestOpen_CreatesTables(t *testing.T) {
	// 使用临时目录的数据库文件
	dbPath := filepath.Join(t.TempDir(), "test.db")

	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer database.Close()

	// 验证表存在：go-libsql 的 Exec 不接受返回行的语句（SELECT），
	// 必须用 QueryRow + Scan 来执行 SELECT
	var count int

	// 验证 bookmarks 表存在
	err = database.QueryRow("SELECT count(*) FROM bookmarks").Scan(&count)
	if err != nil {
		t.Fatalf("bookmarks table not created: %v", err)
	}

	// 验证 tags 表存在
	err = database.QueryRow("SELECT count(*) FROM tags").Scan(&count)
	if err != nil {
		t.Fatalf("tags table not created: %v", err)
	}

	// 验证 bookmark_tags 表存在
	err = database.QueryRow("SELECT count(*) FROM bookmark_tags").Scan(&count)
	if err != nil {
		t.Fatalf("bookmark_tags table not created: %v", err)
	}

	// 验证 FTS5 虚拟表存在
	err = database.QueryRow("SELECT count(*) FROM bookmarks_fts").Scan(&count)
	if err != nil {
		t.Fatalf("bookmarks_fts table not created: %v", err)
	}

	// 验证 user_version 已设置
	var version int
	err = database.QueryRow("PRAGMA user_version").Scan(&version)
	if err != nil {
		t.Fatalf("PRAGMA user_version failed: %v", err)
	}
	if version != 1 {
		t.Fatalf("expected user_version=1, got %d", version)
	}
}

func TestOpen_IdempotentMigration(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	// 第一次打开
	db1, err := Open(dbPath)
	if err != nil {
		t.Fatalf("first Open failed: %v", err)
	}
	db1.Close()

	// 第二次打开（migration 应幂等）
	db2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("second Open failed: %v", err)
	}
	defer db2.Close()
}

func TestCreateBookmark(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer database.Close()

	bm, err := CreateBookmark(database, "https://example.com", "Example", "A test site", "example.com")
	if err != nil {
		t.Fatalf("CreateBookmark failed: %v", err)
	}

	if bm.ID == "" {
		t.Error("bookmark ID should not be empty")
	}
	if bm.URL != "https://example.com" {
		t.Errorf("expected URL 'https://example.com', got '%s'", bm.URL)
	}
	if bm.Title != "Example" {
		t.Errorf("expected title 'Example', got '%s'", bm.Title)
	}
}

func TestCreateBookmark_DuplicateURL(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer database.Close()

	_, err = CreateBookmark(database, "https://example.com", "Example", "", "")
	if err != nil {
		t.Fatalf("first create failed: %v", err)
	}

	// 重复 URL 应报错
	_, err = CreateBookmark(database, "https://example.com", "Example 2", "", "")
	if err == nil {
		t.Fatal("expected error for duplicate URL, got nil")
	}
}

func TestToggleFavorite(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer database.Close()

	bm, _ := CreateBookmark(database, "https://example.com", "Example", "", "")

	if bm.IsFavorite {
		t.Error("new bookmark should not be favorite")
	}

	isFav, err := ToggleFavorite(database, bm.ID)
	if err != nil {
		t.Fatalf("ToggleFavorite failed: %v", err)
	}
	if !isFav {
		t.Error("expected favorite after first toggle")
	}

	isFav, err = ToggleFavorite(database, bm.ID)
	if err != nil {
		t.Fatalf("ToggleFavorite failed: %v", err)
	}
	if isFav {
		t.Error("expected not favorite after second toggle")
	}
}

func TestIncrementClickCount(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer database.Close()

	bm, _ := CreateBookmark(database, "https://example.com", "Example", "", "")

	count, err := IncrementClickCount(database, bm.ID)
	if err != nil {
		t.Fatalf("IncrementClickCount failed: %v", err)
	}
	if count != 1 {
		t.Errorf("expected click_count=1, got %d", count)
	}
}

func TestDeleteBookmark(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer database.Close()

	bm, _ := CreateBookmark(database, "https://example.com", "Example", "", "")

	err = DeleteBookmark(database, bm.ID)
	if err != nil {
		t.Fatalf("DeleteBookmark failed: %v", err)
	}

	got, err := GetBookmarkByID(database, bm.ID)
	if err != nil {
		t.Fatalf("GetBookmarkByID failed: %v", err)
	}
	if got != nil {
		t.Error("bookmark should be deleted")
	}
}

func TestAddTagsToBookmark(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer database.Close()

	bm, _ := CreateBookmark(database, "https://example.com", "Example", "", "")

	err = AddTagsToBookmark(database, bm.ID, []string{"design", "color palette"})
	if err != nil {
		t.Fatalf("AddTagsToBookmark failed: %v", err)
	}

	updated, _ := GetBookmarkByID(database, bm.ID)
	if updated.TagsText == "" {
		t.Error("tags_text should not be empty after adding tags")
	}
}

func TestAddTagsToBookmark_Idempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer database.Close()

	bm, _ := CreateBookmark(database, "https://example.com", "Example", "", "")

	_ = AddTagsToBookmark(database, bm.ID, []string{"design"})
	err = AddTagsToBookmark(database, bm.ID, []string{"design", "tool"})
	if err != nil {
		t.Fatalf("second AddTagsToBookmark failed: %v", err)
	}
}

func TestSearch_Default(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer database.Close()

	CreateBookmark(database, "https://coolors.co", "Coolors - Color palette generator", "color tools", "coolors.co")
	CreateBookmark(database, "https://example.com", "Example Site", "nothing special", "example.com")

	results, total, err := Search(database, "color", false, 5, 0)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if total == 0 {
		t.Fatal("expected at least 1 result")
	}
	if results[0].Title != "Coolors - Color palette generator" {
		t.Errorf("unexpected first result: %s", results[0].Title)
	}
}

func TestSearch_DetailedMode(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer database.Close()

	bm, _ := CreateBookmark(database, "https://coolors.co", "Coolors", "color tools", "coolors.co")
	AddTagsToBookmark(database, bm.ID, []string{"design", "color"})

	results, _, err := Search(database, "color", true, 5, 0)
	if err != nil {
		t.Fatalf("Search detailed failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}
	if results[0].Tags == "" {
		t.Error("detailed mode should include tags")
	}
}
