// cli/internal/db/db.go

// Package db 管理 tsumu 的 SQLite 数据库连接和表结构。
// 使用 libSQL 驱动（兼容 SQLite），通过标准 database/sql 接口操作。
package db

import (
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/tursodatabase/go-libsql" // libSQL 驱动注册：
	// 下划线 import 表示只执行 init()（注册 SQL 驱动），不直接使用包内的符号。
	// 这是 Go database/sql 的惯例写法。
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
    site_name   TEXT DEFAULT '',
    tags_text   TEXT DEFAULT '',                                           -- 冗余字段，逗号分隔
    click_count INTEGER NOT NULL DEFAULT 0,
    is_favorite INTEGER NOT NULL DEFAULT 0,
    source      TEXT NOT NULL DEFAULT 'cli',                               -- 'manual'|'chrome'|'cli' 等
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
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
    PRIMARY KEY (bookmark_id, tag_id)
);

CREATE INDEX IF NOT EXISTS idx_bt_tag ON bookmark_tags(tag_id);

-- ============================================================
-- bookmarks_fts: FTS5 全文搜索虚拟表
-- 索引 5 个字段：title / description / note / site_name / tags_text
-- content='bookmarks' 表示数据来源是 bookmarks 表（content-sync 模式）
-- ============================================================
CREATE VIRTUAL TABLE IF NOT EXISTS bookmarks_fts USING fts5(
    title,
    description,
    note,
    site_name,
    tags_text,
    content='bookmarks',
    content_rowid='rowid'
);

-- FTS5 同步触发器：bookmarks 表增删改时自动同步到 FTS 索引
CREATE TRIGGER IF NOT EXISTS bookmarks_ai AFTER INSERT ON bookmarks BEGIN
    INSERT INTO bookmarks_fts(rowid, title, description, note, site_name, tags_text)
    VALUES (new.rowid, new.title, new.description, new.note, new.site_name, new.tags_text);
END;

CREATE TRIGGER IF NOT EXISTS bookmarks_ad AFTER DELETE ON bookmarks BEGIN
    INSERT INTO bookmarks_fts(bookmarks_fts, rowid, title, description, note, site_name, tags_text)
    VALUES ('delete', old.rowid, old.title, old.description, old.note, old.site_name, old.tags_text);
END;

CREATE TRIGGER IF NOT EXISTS bookmarks_au AFTER UPDATE ON bookmarks BEGIN
    INSERT INTO bookmarks_fts(bookmarks_fts, rowid, title, description, note, site_name, tags_text)
    VALUES ('delete', old.rowid, old.title, old.description, old.note, old.site_name, old.tags_text);
    INSERT INTO bookmarks_fts(rowid, title, description, note, site_name, tags_text)
    VALUES (new.rowid, new.title, new.description, new.note, new.site_name, new.tags_text);
END;

-- 设置 migration 版本号
PRAGMA user_version = 1;
`

// Open 打开（或创建）SQLite 数据库，并执行 migration。
// dbPath 是数据库文件的完整路径（例如 ~/.tsumu/tsumu.db）。
// 返回的 *sql.DB 是 Go 标准数据库连接池，线程安全，可复用。
func Open(dbPath string) (*sql.DB, error) {
	// "libsql" 是驱动名，由 go-libsql 包的 init() 注册
	// go-libsql 要求 DSN 以 "file:" 开头（本地文件路径）
	dsn := "file:" + dbPath
	db, err := sql.Open("libsql", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// 限制连接池为单连接：SQLite 是文件级锁，多连接会导致 "database is locked"
	db.SetMaxOpenConns(1)

	// go-libsql 注意：所有 PRAGMA 都会返回行，必须用 QueryRow + Scan，
	// 不能用 Exec（会报 "Execute returned rows" 错误）。

	// 设置忙等待超时（毫秒）：其他进程持锁时等待而非立即失败
	var busyTimeout int
	if err := db.QueryRow("PRAGMA busy_timeout=5000").Scan(&busyTimeout); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to set busy_timeout: %w", err)
	}

	// 启用 WAL 模式：提升并发读写性能（Write-Ahead Logging）
	var walMode string
	if err := db.QueryRow("PRAGMA journal_mode=WAL").Scan(&walMode); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to set WAL mode: %w", err)
	}

	// 启用外键约束（SQLite 默认关闭）
	// 注意：PRAGMA foreign_keys=ON 是纯设置操作，不返回行，可以用 Exec。
	// 而 PRAGMA journal_mode 和 busy_timeout 会返回当前值，必须用 QueryRow。
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	// 执行 migration
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migration failed: %w", err)
	}

	return db, nil
}

// migrate 检查数据库版本并执行必要的 migration。
// 使用 PRAGMA user_version 做版本控制，确保幂等。
func migrate(db *sql.DB) error {
	var version int
	// PRAGMA 不支持 ? 占位符，直接拼接（这里没有用户输入，安全）
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("failed to read user_version: %w", err)
	}

	// 已经是最新版本，跳过
	if version >= 1 {
		return nil
	}

	// 执行 v1 migration（建表 + FTS5 + 触发器）
	// go-libsql 的 Exec 只执行多语句中的第一条，所以需要逐条执行。
	// 按分号拆分 SQL，跳过空语句和注释。
	if err := execStatements(db, migrationV1); err != nil {
		return fmt.Errorf("migration v1 failed: %w", err)
	}

	return nil
}

// execStatements 将多条 SQL 语句逐条执行。
// go-libsql 不支持一次 Exec 多条语句，所以需要拆分。
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
