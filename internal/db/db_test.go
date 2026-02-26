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
