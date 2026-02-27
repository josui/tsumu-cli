// cli/internal/db/db.go

// Package db 管理 tsumu 的 SQLite 数据库连接和表结构。
// 使用 libSQL 驱动（兼容 SQLite），通过标准 database/sql 接口操作。
package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/tursodatabase/go-libsql" // libSQL 驱动 + embedded replica API
	// 直接 import（不带下划线）会同时执行 init() 注册驱动，
	// 所以不需要再重复下划线 import。
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

`

// Store 包装数据库连接和可选的 sync connector。
// 下游代码通过 Store.DB 获取 *sql.DB，无需关心是否启用了同步。
type Store struct {
	DB        *sql.DB
	Connector *libsql.Connector // nil = 纯本地模式
}

// Sync 手动触发同步。纯本地模式下为 no-op。
func (s *Store) Sync() (libsql.Replicated, error) {
	if s.Connector == nil {
		return libsql.Replicated{}, nil
	}
	return s.Connector.Sync()
}

// Close 关闭数据库和 connector。
func (s *Store) Close() error {
	if err := s.DB.Close(); err != nil {
		return err
	}
	if s.Connector != nil {
		return s.Connector.Close()
	}
	return nil
}

// IsSynced 返回是否启用了云端同步。
func (s *Store) IsSynced() bool {
	return s.Connector != nil
}

// SyncOpts 定义 Turso 云端同步选项。nil 表示纯本地模式。
type SyncOpts struct {
	PrimaryURL string        // libsql://xxx.turso.io
	AuthToken  string
	Interval   time.Duration // 0 = 手动 sync
}

// OpenStore 打开数据库，返回 Store。
// syncOpts 为 nil 时使用纯本地模式（与之前的 Open 行为一致）。
func OpenStore(dbPath string, syncOpts *SyncOpts) (*Store, error) {
	store := &Store{}

	if syncOpts != nil && syncOpts.PrimaryURL != "" {
		// Embedded replica 模式：本地文件 + 远端同步
		opts := []libsql.Option{
			libsql.WithAuthToken(syncOpts.AuthToken),
			libsql.WithReadYourWrites(true),
		}
		if syncOpts.Interval > 0 {
			opts = append(opts, libsql.WithSyncInterval(syncOpts.Interval))
		}
		connector, err := libsql.NewEmbeddedReplicaConnector(dbPath, syncOpts.PrimaryURL, opts...)
		if err != nil {
			return nil, fmt.Errorf("failed to create sync connector: %w", err)
		}
		store.Connector = connector
		store.DB = sql.OpenDB(connector)
	} else {
		// 纯本地模式（现有逻辑）
		// "libsql" 是驱动名，由 go-libsql 包的 init() 注册
		dsn := "file:" + dbPath
		database, err := sql.Open("libsql", dsn)
		if err != nil {
			return nil, fmt.Errorf("failed to open database: %w", err)
		}
		store.DB = database
	}

	// 以下 PRAGMA 和 migration 两种模式共用
	store.DB.SetMaxOpenConns(1)

	// go-libsql 注意：所有 PRAGMA 都会返回行，必须用 QueryRow + Scan，
	// 不能用 Exec（会报 "Execute returned rows" 错误）。

	// 设置忙等待超时（毫秒）：其他进程持锁时等待而非立即失败
	var busyTimeout int
	if err := store.DB.QueryRow("PRAGMA busy_timeout=5000").Scan(&busyTimeout); err != nil {
		store.Close()
		return nil, fmt.Errorf("failed to set busy_timeout: %w", err)
	}

	// 启用 WAL 模式：提升并发读写性能（Write-Ahead Logging）
	var walMode string
	if err := store.DB.QueryRow("PRAGMA journal_mode=WAL").Scan(&walMode); err != nil {
		store.Close()
		return nil, fmt.Errorf("failed to set WAL mode: %w", err)
	}

	// 启用外键约束（SQLite 默认关闭）
	if _, err := store.DB.Exec("PRAGMA foreign_keys=ON"); err != nil {
		store.Close()
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	// 执行 migration
	if err := migrate(store.DB); err != nil {
		store.Close()
		return nil, fmt.Errorf("migration failed: %w", err)
	}

	return store, nil
}

// migrate 检查数据库并执行必要的 migration。
// 使用表存在性检测（兼容 Turso，PRAGMA user_version 在 Turso 不可写）。
// migrationV1 的所有语句都用 IF NOT EXISTS，天然幂等。
func migrate(db *sql.DB) error {
	// 检查 bookmarks 表是否存在，存在则跳过
	var tableName string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='bookmarks'`).Scan(&tableName)
	if err == nil {
		// 表已存在，migration 已执行过
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
