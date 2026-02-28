package sync

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// ── 测试辅助函数 ──

// setupLocalDB 创建内存 SQLite 并初始化 schema。
func setupLocalDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	schema := `
	CREATE TABLE bookmarks (
		id TEXT PRIMARY KEY, url TEXT NOT NULL UNIQUE, title TEXT NOT NULL DEFAULT '',
		description TEXT DEFAULT '', note TEXT DEFAULT '', ai_note TEXT DEFAULT '',
		site_name TEXT DEFAULT '', tags_text TEXT DEFAULT '',
		click_count INTEGER NOT NULL DEFAULT 0, is_favorite INTEGER NOT NULL DEFAULT 0,
		source TEXT NOT NULL DEFAULT 'cli',
		created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
		deleted_at TEXT DEFAULT NULL
	);
	CREATE TABLE tags (id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE);
	CREATE TABLE bookmark_tags (
		bookmark_id TEXT NOT NULL, tag_id TEXT NOT NULL,
		updated_at TEXT, deleted_at TEXT DEFAULT NULL,
		PRIMARY KEY (bookmark_id, tag_id)
	);`

	for _, stmt := range strings.Split(schema, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt != "" {
			if _, err := db.Exec(stmt); err != nil {
				t.Fatal(err)
			}
		}
	}

	t.Cleanup(func() { db.Close() })
	return db
}

// mockTursoServer 创建 mock Turso HTTP API server。
func mockTursoServer(t *testing.T, handler func(stmts []Stmt) []resultWrapper) (*httptest.Server, *Client) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}

		var req pipelineRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		var stmts []Stmt
		for _, r := range req.Requests {
			if r.Type == "execute" && r.Stmt != nil {
				stmts = append(stmts, *r.Stmt)
			}
		}

		results := handler(stmts)
		results = append(results, resultWrapper{Type: "ok"})

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(pipelineResponse{Results: results})
	}))

	client := NewClient(server.URL, "test-token")
	t.Cleanup(func() { server.Close() })
	return server, client
}

// okResult 创建一个成功的 execute 结果。
func okResult(rows [][]Value) resultWrapper {
	return resultWrapper{
		Type: "ok",
		Response: &response{
			Type: "execute",
			Result: ExecuteResult{
				Rows: rows,
			},
		},
	}
}

// okExecResult 创建一个影响行数的成功结果。
func okExecResult(affected int) resultWrapper {
	return resultWrapper{
		Type: "ok",
		Response: &response{
			Type: "execute",
			Result: ExecuteResult{
				AffectedRowCount: affected,
			},
		},
	}
}

// errResult 创建一个错误结果。
func errResult(msg string) resultWrapper {
	return resultWrapper{
		Type:  "error",
		Error: &apiError{Message: msg, Code: "UNKNOWN"},
	}
}

// insertLocalBookmark 插入一条本地测试书签。
func insertLocalBookmark(t *testing.T, db *sql.DB, id, url, title, updatedAt string) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO bookmarks (id, url, title, description, note, ai_note, site_name, tags_text,
		                        click_count, is_favorite, source, created_at, updated_at)
		 VALUES (?, ?, ?, '', '', '', '', '', 0, 0, 'cli', ?, ?)`,
		id, url, title, updatedAt, updatedAt)
	if err != nil {
		t.Fatal(err)
	}
}

// insertLocalTag 插入一条本地测试标签。
func insertLocalTag(t *testing.T, db *sql.DB, id, name string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO tags (id, name) VALUES (?, ?)`, id, name)
	if err != nil {
		t.Fatal(err)
	}
}

// insertLocalBookmarkTag 插入一条本地 bookmark_tag 关联。
func insertLocalBookmarkTag(t *testing.T, db *sql.DB, bmID, tagID, updatedAt string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO bookmark_tags (bookmark_id, tag_id, updated_at) VALUES (?, ?, ?)`,
		bmID, tagID, updatedAt)
	if err != nil {
		t.Fatal(err)
	}
}

// v 创建 text Value 的辅助函数。
func v(s string) Value { return Value{Type: "text", Value: s} }

// vNull 创建 null Value。
func vNull() Value { return Value{Type: "null"} }

// ── Task 9: API 失败阻止 last_synced ──

func TestSyncAll_APIFailure_ReturnsError(t *testing.T) {
	db := setupLocalDB(t)
	insertLocalBookmark(t, db, "bm1", "https://a.com", "A", "2026-01-01T00:00:00Z")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer server.Close()
	client := NewClient(server.URL, "bad-token")

	_, err := SyncAll(context.Background(), db, client, "", SyncIncremental, nil)
	if err == nil {
		t.Error("SyncAll should return error on API failure")
	}
}

func TestSyncAll_Success_NoError(t *testing.T) {
	db := setupLocalDB(t)

	_, client := mockTursoServer(t, func(stmts []Stmt) []resultWrapper {
		var results []resultWrapper
		for range stmts {
			results = append(results, okResult(nil))
		}
		return results
	})

	_, err := SyncAll(context.Background(), db, client, "", SyncIncremental, nil)
	if err != nil {
		t.Errorf("SyncAll should succeed, got: %v", err)
	}
}

// ── Task 10: Pull 场景 ──

func TestPull_Incremental_NewAndLWW(t *testing.T) {
	db := setupLocalDB(t)
	insertLocalBookmark(t, db, "bm1", "https://a.com", "Old Title", "2026-01-01T00:00:00Z")

	_, client := mockTursoServer(t, func(stmts []Stmt) []resultWrapper {
		sql := stmts[0].SQL
		if strings.Contains(sql, "FROM bookmarks") {
			return []resultWrapper{okResult([][]Value{
				// bm1 远端更新
				{v("bm1"), v("https://a.com"), v("New Title"), v(""), v(""), v(""),
					v(""), v(""), v("0"), v("0"), v("cli"),
					v("2026-01-01T00:00:00Z"), v("2026-01-02T00:00:00Z"), vNull()},
				// bm2 远端新增
				{v("bm2"), v("https://b.com"), v("B"), v(""), v(""), v(""),
					v(""), v(""), v("0"), v("0"), v("cli"),
					v("2026-01-01T00:00:00Z"), v("2026-01-01T00:00:00Z"), vNull()},
			})}
		}
		if strings.Contains(sql, "FROM tags") {
			return []resultWrapper{okResult(nil)}
		}
		if strings.Contains(sql, "FROM bookmark_tags") {
			return []resultWrapper{okResult(nil)}
		}
		var results []resultWrapper
		for range stmts {
			results = append(results, okResult(nil))
		}
		return results
	})

	result, err := Pull(context.Background(), db, client, "2025-12-31T00:00:00Z")
	if err != nil {
		t.Fatalf("Pull failed: %v", err)
	}
	if result.New != 1 {
		t.Errorf("New = %d, want 1", result.New)
	}
	if result.Updated != 1 {
		t.Errorf("Updated = %d, want 1", result.Updated)
	}

	var title string
	db.QueryRow(`SELECT title FROM bookmarks WHERE id = 'bm1'`).Scan(&title)
	if title != "New Title" {
		t.Errorf("bm1 title = %q, want 'New Title'", title)
	}
}

func TestPull_LWW_LocalNewer_NoOverwrite(t *testing.T) {
	db := setupLocalDB(t)
	insertLocalBookmark(t, db, "bm1", "https://a.com", "Local Title", "2026-01-02T00:00:00Z")

	_, client := mockTursoServer(t, func(stmts []Stmt) []resultWrapper {
		sql := stmts[0].SQL
		if strings.Contains(sql, "FROM bookmarks") {
			return []resultWrapper{okResult([][]Value{
				{v("bm1"), v("https://a.com"), v("Remote Title"), v(""), v(""), v(""),
					v(""), v(""), v("0"), v("0"), v("cli"),
					v("2026-01-01T00:00:00Z"), v("2026-01-01T00:00:00Z"), vNull()},
			})}
		}
		if strings.Contains(sql, "FROM tags") {
			return []resultWrapper{okResult(nil)}
		}
		if strings.Contains(sql, "FROM bookmark_tags") {
			return []resultWrapper{okResult(nil)}
		}
		var results []resultWrapper
		for range stmts {
			results = append(results, okResult(nil))
		}
		return results
	})

	result, err := Pull(context.Background(), db, client, "")
	if err != nil {
		t.Fatalf("Pull failed: %v", err)
	}
	if result.Updated != 0 {
		t.Errorf("Updated = %d, want 0 (local is newer)", result.Updated)
	}

	var title string
	db.QueryRow(`SELECT title FROM bookmarks WHERE id = 'bm1'`).Scan(&title)
	if title != "Local Title" {
		t.Errorf("title = %q, want 'Local Title' (should not be overwritten)", title)
	}
}

// ── Task 11: Push 场景 ──

func TestPush_Incremental(t *testing.T) {
	db := setupLocalDB(t)
	insertLocalBookmark(t, db, "bm1", "https://a.com", "A", "2026-01-02T00:00:00Z")

	_, client := mockTursoServer(t, func(stmts []Stmt) []resultWrapper {
		var results []resultWrapper
		for _, s := range stmts {
			if strings.Contains(s.SQL, "CREATE TABLE") {
				results = append(results, okResult(nil))
			} else if strings.Contains(s.SQL, "ALTER TABLE") {
				// 模拟列已存在（duplicate column 错误），不触发 schemaChanged
				results = append(results, errResult("duplicate column"))
			} else if strings.Contains(s.SQL, "SELECT") && strings.Contains(s.SQL, "updated_at") && strings.Contains(s.SQL, "IN") {
				// queryRemoteUpdatedAt: 远端不存在
				results = append(results, okResult(nil))
			} else if strings.Contains(s.SQL, "INSERT INTO bookmarks") {
				results = append(results, okExecResult(1))
			} else {
				results = append(results, okResult(nil))
			}
		}
		return results
	})

	result, err := Push(context.Background(), db, client, "2026-01-01T00:00:00Z", false)
	if err != nil {
		t.Fatalf("Push failed: %v", err)
	}
	if result.New != 1 {
		t.Errorf("New = %d, want 1", result.New)
	}
}

func TestPush_Force_CleansOrphans(t *testing.T) {
	db := setupLocalDB(t)
	insertLocalBookmark(t, db, "bm1", "https://a.com", "A", "2026-01-01T00:00:00Z")
	insertLocalTag(t, db, "t1", "golang")
	insertLocalBookmarkTag(t, db, "bm1", "t1", "2026-01-01T00:00:00Z")

	var deleteCalls int
	_, client := mockTursoServer(t, func(stmts []Stmt) []resultWrapper {
		var results []resultWrapper
		for _, s := range stmts {
			if strings.Contains(s.SQL, "DELETE") && strings.Contains(s.SQL, "NOT IN") {
				deleteCalls++
			}
			results = append(results, okResult(nil))
		}
		return results
	})

	_, err := Push(context.Background(), db, client, "", true)
	if err != nil {
		t.Fatalf("Push force failed: %v", err)
	}

	if deleteCalls < 3 {
		t.Errorf("Expected at least 3 orphan cleanup DELETE calls, got %d", deleteCalls)
	}
}

// ── Task 12: SyncMode 端到端 ──

func TestSyncAll_Overwrite_SkipsPull(t *testing.T) {
	db := setupLocalDB(t)
	insertLocalBookmark(t, db, "bm1", "https://a.com", "A", "2026-01-01T00:00:00Z")

	var selectFromBookmarks int
	_, client := mockTursoServer(t, func(stmts []Stmt) []resultWrapper {
		var results []resultWrapper
		for _, s := range stmts {
			if strings.Contains(s.SQL, "SELECT") && strings.Contains(s.SQL, "FROM bookmarks") &&
				!strings.Contains(s.SQL, "COUNT") && !strings.Contains(s.SQL, "updated_at") {
				selectFromBookmarks++
			}
			results = append(results, okResult(nil))
		}
		return results
	})

	_, err := SyncAll(context.Background(), db, client, "2025-12-01T00:00:00Z", SyncOverwrite, nil)
	if err != nil {
		t.Fatalf("SyncAll Overwrite failed: %v", err)
	}

	if selectFromBookmarks > 0 {
		t.Errorf("SyncOverwrite should skip pull, but got %d remote SELECT FROM bookmarks calls", selectFromBookmarks)
	}
}

func TestSyncAll_Full_PullsAndPushes(t *testing.T) {
	db := setupLocalDB(t)

	var pullCalled bool
	_, client := mockTursoServer(t, func(stmts []Stmt) []resultWrapper {
		var results []resultWrapper
		for _, s := range stmts {
			if strings.Contains(s.SQL, "SELECT") && strings.Contains(s.SQL, "FROM bookmarks") &&
				!strings.Contains(s.SQL, "COUNT") && !strings.Contains(s.SQL, "IN") {
				pullCalled = true
			}
			results = append(results, okResult(nil))
		}
		return results
	})

	_, err := SyncAll(context.Background(), db, client, "2025-12-01T00:00:00Z", SyncFull, nil)
	if err != nil {
		t.Fatalf("SyncAll Full failed: %v", err)
	}

	if !pullCalled {
		t.Error("SyncFull should execute pull")
	}
}
