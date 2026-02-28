// cli/internal/db/db.go

// Package db 管理 tsumu 的 SQLite 数据库连接和表结构。
// 使用 libSQL 驱动（兼容 SQLite），通过标准 database/sql 接口操作。
package db

import (
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite" // 纯 Go SQLite driver，注册 "sqlite" driver
)

// migrationV1 是初始建表 SQL。
// 包含：bookmarks / tags / bookmark_tags / bookmarks_fts (FTS5) + 索引 + 触发器。
// 参考：docs/cli/tsumu-cli-schema.md
const migrationV1 = `
-- ============================================================
-- bookmarks: 核心书签表
-- ============================================================
CREATE TABLE IF NOT EXISTS bookmarks (
    id          TEXT PRIMARY KEY,                                          -- ULID
    url         TEXT NOT NULL UNIQUE,
    title       TEXT NOT NULL DEFAULT '',
    description TEXT DEFAULT '',
    note        TEXT DEFAULT '',                                           -- 用户备注
    ai_note     TEXT DEFAULT '',                                           -- AI 生成的摘要（检索辅助，不显示）
    site_name   TEXT DEFAULT '',
    tags_text   TEXT DEFAULT '',                                           -- 冗余字段，逗号分隔
    click_count INTEGER NOT NULL DEFAULT 0,
    is_favorite INTEGER NOT NULL DEFAULT 0,
    source      TEXT NOT NULL DEFAULT 'cli',                               -- 'manual'|'chrome'|'cli' 等
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    deleted_at  TEXT DEFAULT NULL
);

CREATE INDEX IF NOT EXISTS idx_bookmarks_created  ON bookmarks(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_bookmarks_clicks   ON bookmarks(click_count DESC);
CREATE INDEX IF NOT EXISTS idx_bookmarks_favorite ON bookmarks(is_favorite) WHERE is_favorite = 1;

-- ============================================================
-- tags: 标签表
-- ============================================================
CREATE TABLE IF NOT EXISTS tags (
    id   TEXT PRIMARY KEY,    -- ULID
    name TEXT NOT NULL UNIQUE
);

-- ============================================================
-- bookmark_tags: 多对多关联表
-- ============================================================
CREATE TABLE IF NOT EXISTS bookmark_tags (
    bookmark_id TEXT NOT NULL REFERENCES bookmarks(id) ON DELETE CASCADE,
    tag_id      TEXT NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    deleted_at  TEXT DEFAULT NULL,
    PRIMARY KEY (bookmark_id, tag_id)
);

CREATE INDEX IF NOT EXISTS idx_bt_tag ON bookmark_tags(tag_id);

-- ============================================================
-- bookmarks_fts: FTS5 全文搜索虚拟表
-- 索引 6 个字段：title / description / note / ai_note / site_name / tags_text
-- content='bookmarks' 表示数据来源是 bookmarks 表（content-sync 模式）
-- ============================================================
CREATE VIRTUAL TABLE IF NOT EXISTS bookmarks_fts USING fts5(
    title,
    description,
    note,
    ai_note,
    site_name,
    tags_text,
    content='bookmarks',
    content_rowid='rowid'
);

-- FTS5 同步触发器：bookmarks 表增删改时自动同步到 FTS 索引
-- INSERT 触发器只对未删除的记录建索引（deleted_at IS NULL）
CREATE TRIGGER IF NOT EXISTS bookmarks_ai AFTER INSERT ON bookmarks
WHEN new.deleted_at IS NULL
BEGIN
    INSERT INTO bookmarks_fts(rowid, title, description, note, ai_note, site_name, tags_text)
    VALUES (new.rowid, new.title, new.description, new.note, new.ai_note, new.site_name, new.tags_text);
END;

CREATE TRIGGER IF NOT EXISTS bookmarks_ad AFTER DELETE ON bookmarks BEGIN
    INSERT INTO bookmarks_fts(bookmarks_fts, rowid, title, description, note, ai_note, site_name, tags_text)
    VALUES ('delete', old.rowid, old.title, old.description, old.note, old.ai_note, old.site_name, old.tags_text);
END;

-- UPDATE 触发器：先删旧索引（仅当旧记录未删除时），再加新索引（仅当新记录未删除时）
CREATE TRIGGER IF NOT EXISTS bookmarks_au AFTER UPDATE ON bookmarks BEGIN
    INSERT INTO bookmarks_fts(bookmarks_fts, rowid, title, description, note, ai_note, site_name, tags_text)
    SELECT 'delete', old.rowid, old.title, old.description, old.note, old.ai_note, old.site_name, old.tags_text
    WHERE old.deleted_at IS NULL;
    INSERT INTO bookmarks_fts(rowid, title, description, note, ai_note, site_name, tags_text)
    SELECT new.rowid, new.title, new.description, new.note, new.ai_note, new.site_name, new.tags_text
    WHERE new.deleted_at IS NULL;
END;

`

// Store 包装本地 SQLite 数据库连接。
type Store struct {
	DB     *sql.DB
	dbPath string
}

// Close 关闭数据库连接。
func (s *Store) Close() error {
	return s.DB.Close()
}

// OpenStore 打开本地 SQLite 数据库。
func OpenStore(dbPath string) (*Store, error) {
	store := &Store{dbPath: dbPath}
	if err := store.openLocal(); err != nil {
		return nil, err
	}
	if err := store.initDB(); err != nil {
		store.Close()
		return nil, err
	}
	return store, nil
}

// openLocal 以纯本地模式打开 DB。
func (s *Store) openLocal() error {
	database, err := sql.Open("sqlite", "file:"+s.dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	s.DB = database
	return nil
}

// ReopenLocal 关闭后重新以本地模式打开并初始化 PRAGMA。
func (s *Store) ReopenLocal() error {
	if err := s.openLocal(); err != nil {
		return err
	}
	return s.initDB()
}

// initDB 设置 PRAGMA 和执行 migration。
func (s *Store) initDB() error {
	s.DB.SetMaxOpenConns(1)

	var busyTimeout int
	if err := s.DB.QueryRow("PRAGMA busy_timeout=5000").Scan(&busyTimeout); err != nil {
		return fmt.Errorf("failed to set busy_timeout: %w", err)
	}

	var walMode string
	if err := s.DB.QueryRow("PRAGMA journal_mode=WAL").Scan(&walMode); err != nil {
		return fmt.Errorf("failed to set WAL mode: %w", err)
	}

	if _, err := s.DB.Exec("PRAGMA foreign_keys=ON"); err != nil {
		return fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	if err := migrate(s.DB); err != nil {
		return fmt.Errorf("migration failed: %w", err)
	}

	return nil
}

// migrationV2 为已有数据库添加 ai_note 列并重建 FTS5 索引。
// 新建库不需要这段（v1 已包含 ai_note），仅用于升级旧库。
const migrationV2 = `
-- 添加 ai_note 列（AI 生成的摘要，用于检索辅助）
ALTER TABLE bookmarks ADD COLUMN ai_note TEXT DEFAULT '';

-- 重建 FTS5 虚拟表（列变更无法 ALTER，必须 drop + recreate）
DROP TRIGGER IF EXISTS bookmarks_ai;
DROP TRIGGER IF EXISTS bookmarks_ad;
DROP TRIGGER IF EXISTS bookmarks_au;
DROP TABLE IF EXISTS bookmarks_fts;

CREATE VIRTUAL TABLE bookmarks_fts USING fts5(
    title, description, note, ai_note, site_name, tags_text,
    content='bookmarks', content_rowid='rowid'
);

-- 将已有数据灌入新 FTS 索引
INSERT INTO bookmarks_fts(rowid, title, description, note, ai_note, site_name, tags_text)
SELECT rowid, title, description, note, ai_note, site_name, tags_text FROM bookmarks;

-- 重建触发器
CREATE TRIGGER bookmarks_ai AFTER INSERT ON bookmarks BEGIN
    INSERT INTO bookmarks_fts(rowid, title, description, note, ai_note, site_name, tags_text)
    VALUES (new.rowid, new.title, new.description, new.note, new.ai_note, new.site_name, new.tags_text);
END;

CREATE TRIGGER bookmarks_ad AFTER DELETE ON bookmarks BEGIN
    INSERT INTO bookmarks_fts(bookmarks_fts, rowid, title, description, note, ai_note, site_name, tags_text)
    VALUES ('delete', old.rowid, old.title, old.description, old.note, old.ai_note, old.site_name, old.tags_text);
END;

CREATE TRIGGER bookmarks_au AFTER UPDATE ON bookmarks BEGIN
    INSERT INTO bookmarks_fts(bookmarks_fts, rowid, title, description, note, ai_note, site_name, tags_text)
    VALUES ('delete', old.rowid, old.title, old.description, old.note, old.ai_note, old.site_name, old.tags_text);
    INSERT INTO bookmarks_fts(rowid, title, description, note, ai_note, site_name, tags_text)
    VALUES (new.rowid, new.title, new.description, new.note, new.ai_note, new.site_name, new.tags_text);
END;
`

// migrationV3 的 SQL 已内联到 migrateV3() 函数中，以实现逐步检查和幂等执行。

// migrate 检查数据库并执行必要的 migration。
// 使用表/列存在性检测（兼容 Turso，PRAGMA user_version 在 Turso 不可写）。
// migrationV1 的所有语句都用 IF NOT EXISTS，天然幂等。
func migrate(db *sql.DB) error {
	// 检查 bookmarks 表是否存在
	var tableName string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='bookmarks'`).Scan(&tableName)
	if err != nil {
		// 表不存在，执行完整 v1 migration（已包含 ai_note）
		if err := execStatements(db, migrationV1); err != nil {
			return fmt.Errorf("migration v1 failed: %w", err)
		}
		return nil
	}

	// 表已存在，检查是否需要 v2 升级（ai_note 列）
	if !columnExists(db, "bookmarks", "ai_note") {
		if err := execStatements(db, migrationV2); err != nil {
			return fmt.Errorf("migration v2 failed: %w", err)
		}
	}

	// V3: soft delete 支持（bookmarks.deleted_at, bookmark_tags.updated_at/deleted_at）
	// 检查 bookmark_tags.deleted_at（V3 的最后一个 ALTER），确保部分完成的 V3 也能重跑。
	// 各 ALTER TABLE 单独执行并忽略 "duplicate column" 错误，保证幂等性。
	if !columnExists(db, "bookmark_tags", "deleted_at") {
		if err := migrateV3(db); err != nil {
			return fmt.Errorf("migration v3 failed: %w", err)
		}
	}

	return nil
}

// migrateV3 执行 V3 migration（soft delete 支持），每步单独检查列是否存在，保证幂等。
// 解决部分完成场景：如 bookmarks.deleted_at 已添加但 bookmark_tags.deleted_at 未添加时，
// 只跳过已完成的 ALTER，继续执行剩余步骤。
func migrateV3(db *sql.DB) error {
	// 逐个添加列，已存在则跳过
	alters := []struct {
		table, column string
	}{
		{"bookmarks", "deleted_at"},
		{"bookmark_tags", "updated_at"},
		{"bookmark_tags", "deleted_at"},
	}
	for _, a := range alters {
		if !columnExists(db, a.table, a.column) {
			stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s TEXT DEFAULT NULL", a.table, a.column)
			if _, err := db.Exec(stmt); err != nil {
				return fmt.Errorf("add column %s.%s failed: %w", a.table, a.column, err)
			}
		}
	}

	// 回填 bookmark_tags.updated_at
	_, err := db.Exec(`
		UPDATE bookmark_tags
		SET updated_at = COALESCE(
			(SELECT created_at FROM bookmarks WHERE id = bookmark_tags.bookmark_id),
			strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
		)
		WHERE updated_at IS NULL`)
	if err != nil {
		return fmt.Errorf("backfill bookmark_tags.updated_at failed: %w", err)
	}

	// 重建 FTS5 触发器：soft delete 感知
	triggerSQL := `
DROP TRIGGER IF EXISTS bookmarks_ai;
DROP TRIGGER IF EXISTS bookmarks_ad;
DROP TRIGGER IF EXISTS bookmarks_au;

CREATE TRIGGER bookmarks_ai AFTER INSERT ON bookmarks
WHEN new.deleted_at IS NULL
BEGIN
    INSERT INTO bookmarks_fts(rowid, title, description, note, ai_note, site_name, tags_text)
    VALUES (new.rowid, new.title, new.description, new.note, new.ai_note, new.site_name, new.tags_text);
END;

CREATE TRIGGER bookmarks_ad AFTER DELETE ON bookmarks BEGIN
    INSERT INTO bookmarks_fts(bookmarks_fts, rowid, title, description, note, ai_note, site_name, tags_text)
    VALUES ('delete', old.rowid, old.title, old.description, old.note, old.ai_note, old.site_name, old.tags_text);
END;

CREATE TRIGGER bookmarks_au AFTER UPDATE ON bookmarks BEGIN
    INSERT INTO bookmarks_fts(bookmarks_fts, rowid, title, description, note, ai_note, site_name, tags_text)
    SELECT 'delete', old.rowid, old.title, old.description, old.note, old.ai_note, old.site_name, old.tags_text
    WHERE old.deleted_at IS NULL;
    INSERT INTO bookmarks_fts(rowid, title, description, note, ai_note, site_name, tags_text)
    SELECT new.rowid, new.title, new.description, new.note, new.ai_note, new.site_name, new.tags_text
    WHERE new.deleted_at IS NULL;
END;
`
	if err := execStatements(db, triggerSQL); err != nil {
		return fmt.Errorf("rebuild triggers failed: %w", err)
	}

	return nil
}

// columnExists 检查表中是否存在指定列。
func columnExists(db *sql.DB, table, column string) bool {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var dfltValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &pk); err != nil {
			return false
		}
		if name == column {
			return true
		}
	}
	return false
}

// execStatements 将多条 SQL 语句逐条执行。
// SQLite driver 不支持一次 Exec 多条语句，所以需要拆分。
// 使用简单的分号分割（适合我们的建表 SQL，不含存储过程等复杂场景）。
func execStatements(db *sql.DB, sqlBlock string) error {
	// 先按 ";\n" 分割，保留触发器中的分号（触发器内的分号后面没有换行在行首）
	// 但实际上触发器里有 ";\nEND;"，所以需要更聪明的分割方式。
	// 改用按 "END;" 先合并触发器块，再按分号分割。

	// 更简单的方案：按行扫描，遇到完整语句就执行
	statements := splitSQL(sqlBlock)
	for _, stmt := range statements {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" || strings.HasPrefix(stmt, "--") {
			continue
		}
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("exec failed for statement [%.60s...]: %w", stmt, err)
		}
	}
	return nil
}

// splitSQL 将一段 SQL 按语句分割。
// 处理 CREATE TRIGGER ... END; 这种多行语句。
func splitSQL(sqlBlock string) []string {
	var statements []string
	var current strings.Builder
	inTrigger := false

	for _, line := range strings.Split(sqlBlock, "\n") {
		trimmed := strings.TrimSpace(line)

		// 跳过纯注释行
		if strings.HasPrefix(trimmed, "--") {
			continue
		}

		// 检测 CREATE TRIGGER 开始
		upper := strings.ToUpper(trimmed)
		if strings.HasPrefix(upper, "CREATE TRIGGER") {
			inTrigger = true
		}

		current.WriteString(line)
		current.WriteString("\n")

		if inTrigger {
			// 触发器以 "END;" 结束
			if strings.HasPrefix(upper, "END;") || strings.HasSuffix(upper, "END;") {
				inTrigger = false
				statements = append(statements, current.String())
				current.Reset()
			}
		} else {
			// 普通语句以分号结尾
			if strings.HasSuffix(trimmed, ";") {
				statements = append(statements, current.String())
				current.Reset()
			}
		}
	}

	// 处理最后没有分号的语句（如 PRAGMA）
	if remaining := strings.TrimSpace(current.String()); remaining != "" {
		statements = append(statements, remaining)
	}

	return statements
}
