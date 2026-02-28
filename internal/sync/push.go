// push.go 实现 Push 阶段：本地 → 远端。
// 读取本地 last_synced 之后变更的数据，通过 HTTP API UPSERT 到远端。
// 冲突解决：先查询远端 updated_at，本地更新 > 远端时才写入。

package sync

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
)

// PushResult 是 Push 阶段的结果统计。
type PushResult struct {
	New     int
	Updated int
}

// Push 将本地变更推送到远端。
// lastSynced 为空时推送全量。
// forceUpdate=true 时忽略 LWW 比较，强制用本地数据覆盖远端。
func Push(ctx context.Context, localDB *sql.DB, client *Client, lastSynced string, forceUpdate bool) PushResult {
	var result PushResult

	// 确保远端有正确的表结构。
	// 如果远端 schema 刚升级（补了新列），也需要强制推送以回填新列数据。
	schemaChanged := ensureRemoteSchema(ctx, client)
	if schemaChanged {
		lastSynced = ""
		forceUpdate = true
	}

	// 1. 推送 tags（全量，INSERT OR IGNORE）
	pushTags(ctx, localDB, client)

	// 2. 推送 bookmarks
	bNew, bUpd := pushBookmarks(ctx, localDB, client, lastSynced, forceUpdate)
	result.New += bNew
	result.Updated += bUpd

	// 3. 推送 bookmark_tags
	pushBookmarkTags(ctx, localDB, client, lastSynced)

	return result
}

// localBM 是本地书签数据（push 用）
type localBM struct {
	id, url, title, description, note, aiNote string
	siteName, tagsText                         string
	clickCount, isFavorite                     int
	source, createdAt, updatedAt               string
	deletedAt                                  sql.NullString
}

// queryLocalBookmarks 查询本地书签。lastSynced 为空时查询全量。
func queryLocalBookmarks(localDB *sql.DB, lastSynced string) []localBM {
	q := `SELECT id, url, title, description, note, ai_note, site_name, tags_text,
	             click_count, is_favorite, source, created_at, updated_at, deleted_at
	      FROM bookmarks`
	var queryArgs []any
	if lastSynced != "" {
		q += ` WHERE updated_at > ?`
		queryArgs = append(queryArgs, lastSynced)
	}

	rows, err := localDB.Query(q, queryArgs...)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var bookmarks []localBM
	for rows.Next() {
		var bm localBM
		if err := rows.Scan(&bm.id, &bm.url, &bm.title, &bm.description, &bm.note, &bm.aiNote,
			&bm.siteName, &bm.tagsText, &bm.clickCount, &bm.isFavorite,
			&bm.source, &bm.createdAt, &bm.updatedAt, &bm.deletedAt); err != nil {
			continue
		}
		bookmarks = append(bookmarks, bm)
	}
	return bookmarks
}

// deletedAtArg 将 sql.NullString 转换为 Turso API 的 Arg。
func deletedAtArg(d sql.NullString) Arg {
	if d.Valid {
		return TextArg(d.String)
	}
	return NullArg()
}

// pushBookmarks 推送本地变更的 bookmarks 到远端。
// 普通模式：LWW（local.updated_at > remote.updated_at）时覆盖远端。
// Force 模式：INSERT OR REPLACE 全量覆盖 + 清理远端孤儿行，确保远端 = 本地精确镜像。
func pushBookmarks(ctx context.Context, localDB *sql.DB, client *Client, lastSynced string, forceUpdate bool) (newCount, updCount int) {
	if forceUpdate {
		return forcePushBookmarks(ctx, localDB, client)
	}
	return incrementalPushBookmarks(ctx, localDB, client, lastSynced)
}

// forcePushBookmarks 强制推送：INSERT OR REPLACE 全量覆盖远端。
// 处理 ID 变更和 URL 冲突（本地删除重建书签后 ID 不同但 URL 相同的情况）。
// 推送完毕后清理远端孤儿行（本地不存在的 ID），确保远端完全一致。
func forcePushBookmarks(ctx context.Context, localDB *sql.DB, client *Client) (newCount, updCount int) {
	bookmarks := queryLocalBookmarks(localDB, "")
	if len(bookmarks) == 0 {
		return 0, 0
	}

	// INSERT OR REPLACE 批量推送。
	// 与普通 INSERT 不同，OR REPLACE 会在遇到 UNIQUE 冲突（id 或 url）时
	// 先删除冲突行再插入，彻底解决 ID 变更导致的 URL 冲突问题。
	const chunkSize = 20
	for i := 0; i < len(bookmarks); i += chunkSize {
		end := i + chunkSize
		if end > len(bookmarks) {
			end = len(bookmarks)
		}
		chunk := bookmarks[i:end]

		var stmts []Stmt
		for _, bm := range chunk {
			stmts = append(stmts, Stmt{
				SQL: `INSERT OR REPLACE INTO bookmarks
				      (id, url, title, description, note, ai_note, site_name, tags_text,
				       click_count, is_favorite, source, created_at, updated_at, deleted_at)
				      VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				Args: []Arg{
					TextArg(bm.id), TextArg(bm.url), TextArg(bm.title), TextArg(bm.description),
					TextArg(bm.note), TextArg(bm.aiNote), TextArg(bm.siteName), TextArg(bm.tagsText),
					IntArg(bm.clickCount), IntArg(bm.isFavorite),
					TextArg(bm.source), TextArg(bm.createdAt), TextArg(bm.updatedAt), deletedAtArg(bm.deletedAt),
				},
			})
		}

		_, err := client.Execute(ctx, stmts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  ⚠ batch push failed (%d items): %v\n", len(chunk), err)
			// 批量失败时逐条重试，定位具体失败的语句
			for j, s := range stmts {
				_, err2 := client.ExecuteOne(ctx, s.SQL, s.Args...)
				if err2 != nil {
					fmt.Fprintf(os.Stderr, "  ⚠ row %d/%d failed: %v\n", i+j+1, len(bookmarks), err2)
				} else {
					updCount++
				}
			}
		} else {
			updCount += len(chunk)
		}
	}

	// 清理远端孤儿行：删除远端存在但本地不存在的 ID。
	// 确保远端是本地的精确镜像。
	localIDs := make([]string, len(bookmarks))
	for i, bm := range bookmarks {
		localIDs[i] = bm.id
	}
	cleanRemoteOrphans(ctx, client, "bookmarks", "id", localIDs)

	return 0, updCount
}

// incrementalPushBookmarks 增量推送：仅推送 lastSynced 之后变更的数据。
// 使用 LWW 策略（local.updated_at > remote.updated_at）解决冲突。
func incrementalPushBookmarks(ctx context.Context, localDB *sql.DB, client *Client, lastSynced string) (newCount, updCount int) {
	bookmarks := queryLocalBookmarks(localDB, lastSynced)
	if len(bookmarks) == 0 {
		return 0, 0
	}

	// 批量查询远端这些 id 的 updated_at
	ids := make([]string, len(bookmarks))
	for i, bm := range bookmarks {
		ids[i] = bm.id
	}
	remoteUpdates := queryRemoteUpdatedAt(ctx, client, "bookmarks", "id", ids)

	// 逐条推送
	for _, bm := range bookmarks {
		remoteUT, exists := remoteUpdates[bm.id]
		da := deletedAtArg(bm.deletedAt)

		if !exists {
			// 远端不存在，INSERT
			_, err := client.ExecuteOne(ctx,
				`INSERT INTO bookmarks (id, url, title, description, note, ai_note, site_name, tags_text,
				                        click_count, is_favorite, source, created_at, updated_at, deleted_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				TextArg(bm.id), TextArg(bm.url), TextArg(bm.title), TextArg(bm.description),
				TextArg(bm.note), TextArg(bm.aiNote), TextArg(bm.siteName), TextArg(bm.tagsText),
				IntArg(bm.clickCount), IntArg(bm.isFavorite),
				TextArg(bm.source), TextArg(bm.createdAt), TextArg(bm.updatedAt), da)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  ⚠ push insert failed [%s]: %v\n", bm.id[:8], err)
			} else {
				newCount++
			}
		} else if bm.updatedAt > remoteUT {
			// 本地更新时间更新，覆盖远端
			_, err := client.ExecuteOne(ctx,
				`UPDATE bookmarks SET url=?, title=?, description=?, note=?, ai_note=?,
				                      site_name=?, tags_text=?, click_count=?, is_favorite=?,
				                      source=?, updated_at=?, deleted_at=?
				 WHERE id=?`,
				TextArg(bm.url), TextArg(bm.title), TextArg(bm.description),
				TextArg(bm.note), TextArg(bm.aiNote), TextArg(bm.siteName), TextArg(bm.tagsText),
				IntArg(bm.clickCount), IntArg(bm.isFavorite),
				TextArg(bm.source), TextArg(bm.updatedAt), da, TextArg(bm.id))
			if err != nil {
				fmt.Fprintf(os.Stderr, "  ⚠ push update failed [%s]: %v\n", bm.id[:8], err)
			} else {
				updCount++
			}
		}
	}
	return
}

// cleanRemoteOrphans 删除远端存在但本地不存在的行。
// 用于 force sync 后确保远端是本地的精确镜像。
func cleanRemoteOrphans(ctx context.Context, client *Client, table, idCol string, localIDs []string) {
	if len(localIDs) == 0 {
		return
	}

	placeholders := make([]string, len(localIDs))
	args := make([]Arg, len(localIDs))
	for i, id := range localIDs {
		placeholders[i] = "?"
		args[i] = TextArg(id)
	}

	q := fmt.Sprintf(`DELETE FROM %s WHERE %s NOT IN (%s)`,
		table, idCol, strings.Join(placeholders, ","))
	client.ExecuteOne(ctx, q, args...)
}

// pushTags 推送本地全量 tags 到远端（INSERT OR IGNORE）。
func pushTags(ctx context.Context, localDB *sql.DB, client *Client) {
	rows, err := localDB.Query(`SELECT id, name FROM tags`)
	if err != nil {
		return
	}
	defer rows.Close()

	var stmts []Stmt
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			continue
		}
		stmts = append(stmts, Stmt{
			SQL:  `INSERT OR IGNORE INTO tags (id, name) VALUES (?, ?)`,
			Args: []Arg{TextArg(id), TextArg(name)},
		})
	}

	if len(stmts) > 0 {
		client.Execute(ctx, stmts)
	}
}

// pushBookmarkTags 推送本地变更的 bookmark_tags 到远端。
func pushBookmarkTags(ctx context.Context, localDB *sql.DB, client *Client, lastSynced string) {
	q := `SELECT bookmark_id, tag_id, updated_at, deleted_at FROM bookmark_tags`
	var queryArgs []any
	if lastSynced != "" {
		q += ` WHERE updated_at > ?`
		queryArgs = append(queryArgs, lastSynced)
	}

	rows, err := localDB.Query(q, queryArgs...)
	if err != nil {
		return
	}
	defer rows.Close()

	type localBT struct {
		bmID, tagID, updatedAt string
		deletedAt              sql.NullString
	}
	var links []localBT

	for rows.Next() {
		var l localBT
		if err := rows.Scan(&l.bmID, &l.tagID, &l.updatedAt, &l.deletedAt); err != nil {
			continue
		}
		links = append(links, l)
	}

	for _, l := range links {
		var deletedAtArg Arg
		if l.deletedAt.Valid {
			deletedAtArg = TextArg(l.deletedAt.String)
		} else {
			deletedAtArg = NullArg()
		}

		// 查询远端是否存在及其 updated_at
		res, err := client.ExecuteOne(ctx,
			`SELECT updated_at FROM bookmark_tags WHERE bookmark_id = ? AND tag_id = ?`,
			TextArg(l.bmID), TextArg(l.tagID))
		if err != nil {
			continue
		}

		if len(res.Rows) == 0 {
			// 远端不存在，INSERT
			client.ExecuteOne(ctx,
				`INSERT INTO bookmark_tags (bookmark_id, tag_id, updated_at, deleted_at) VALUES (?, ?, ?, ?)`,
				TextArg(l.bmID), TextArg(l.tagID), TextArg(l.updatedAt), deletedAtArg)
		} else if l.updatedAt > res.Rows[0][0].Value {
			// 本地更新，覆盖远端
			client.ExecuteOne(ctx,
				`UPDATE bookmark_tags SET updated_at = ?, deleted_at = ? WHERE bookmark_id = ? AND tag_id = ?`,
				TextArg(l.updatedAt), deletedAtArg, TextArg(l.bmID), TextArg(l.tagID))
		}
	}
}

// queryRemoteUpdatedAt 批量查询远端指定表指定 id 列的 updated_at 值。
// 使用 IN 查询一次获取，减少 HTTP 请求数。返回 map[id]updated_at。
func queryRemoteUpdatedAt(ctx context.Context, client *Client, table, idCol string, ids []string) map[string]string {
	result := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return result
	}

	// 构造 IN 查询
	placeholders := make([]string, len(ids))
	args := make([]Arg, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = TextArg(id)
	}

	q := fmt.Sprintf(`SELECT %s, updated_at FROM %s WHERE %s IN (%s)`,
		idCol, table, idCol, strings.Join(placeholders, ","))

	res, err := client.ExecuteOne(ctx, q, args...)
	if err != nil {
		return result
	}

	for _, row := range res.Rows {
		if len(row) >= 2 {
			result[row[0].Value] = row[1].Value
		}
	}
	return result
}

// ensureRemoteSchema 确保远端数据库有正确的表结构。
// 1. CREATE TABLE IF NOT EXISTS 建表（新库）。
// 2. ALTER TABLE ADD COLUMN 补列（旧库升级）— 已存在的列会报错，静默忽略。
// 返回 true 表示有 schema 变更（需要强制全量推送以回填新列数据）。
// 注意：远端不需要 FTS5 表（搜索只在本地执行）。
func ensureRemoteSchema(ctx context.Context, client *Client) (changed bool) {
	// 建表（新库直接包含所有列）
	stmts := []Stmt{
		{SQL: `CREATE TABLE IF NOT EXISTS bookmarks (
			id TEXT PRIMARY KEY, url TEXT NOT NULL UNIQUE, title TEXT NOT NULL DEFAULT '',
			description TEXT DEFAULT '', note TEXT DEFAULT '', ai_note TEXT DEFAULT '',
			site_name TEXT DEFAULT '', tags_text TEXT DEFAULT '',
			click_count INTEGER NOT NULL DEFAULT 0, is_favorite INTEGER NOT NULL DEFAULT 0,
			source TEXT NOT NULL DEFAULT 'cli',
			created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
			deleted_at TEXT DEFAULT NULL
		)`},
		{SQL: `CREATE TABLE IF NOT EXISTS tags (id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE)`},
		{SQL: `CREATE TABLE IF NOT EXISTS bookmark_tags (
			bookmark_id TEXT NOT NULL, tag_id TEXT NOT NULL,
			updated_at TEXT, deleted_at TEXT DEFAULT NULL,
			PRIMARY KEY (bookmark_id, tag_id)
		)`},
	}
	client.Execute(ctx, stmts)

	// 补列（旧库可能缺少这些列）。
	// ALTER 成功 = 列确实不存在且刚被添加 → schema 发生了变更。
	// ALTER 失败 = 列已存在（duplicate column 错误）→ 无变更。
	alterStmts := []string{
		`ALTER TABLE bookmarks ADD COLUMN ai_note TEXT DEFAULT ''`,
		`ALTER TABLE bookmarks ADD COLUMN deleted_at TEXT DEFAULT NULL`,
		`ALTER TABLE bookmark_tags ADD COLUMN updated_at TEXT DEFAULT NULL`,
		`ALTER TABLE bookmark_tags ADD COLUMN deleted_at TEXT DEFAULT NULL`,
	}
	for _, sql := range alterStmts {
		_, err := client.ExecuteOne(ctx, sql)
		if err == nil {
			// ALTER 成功说明列是新加的，需要回填数据
			changed = true
		}
	}
	return
}
