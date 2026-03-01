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
// forceUpdate=true 时使用 INSERT OR REPLACE + 三表孤儿清理。
func Push(ctx context.Context, localDB *sql.DB, client *Client, lastSynced string, forceUpdate bool) (PushResult, error) {
	var result PushResult

	// 确保远端表结构
	schemaChanged, err := ensureRemoteSchema(ctx, client)
	if err != nil {
		return result, fmt.Errorf("ensure remote schema: %w", err)
	}
	if schemaChanged {
		lastSynced = ""
		forceUpdate = true
	}

	// 1. 推送 tags
	if err := pushTags(ctx, localDB, client); err != nil {
		return result, fmt.Errorf("push tags: %w", err)
	}

	// 2. 推送 bookmarks
	bNew, bUpd, err := pushBookmarks(ctx, localDB, client, lastSynced, forceUpdate)
	if err != nil {
		result.New = bNew
		result.Updated = bUpd
		return result, fmt.Errorf("push bookmarks: %w", err)
	}
	result.New += bNew
	result.Updated += bUpd

	// 3. 推送 bookmark_tags
	if err := pushBookmarkTags(ctx, localDB, client, lastSynced); err != nil {
		return result, fmt.Errorf("push bookmark_tags: %w", err)
	}

	// 4. 孤儿清理（force 模式）
	if forceUpdate {
		if err := cleanAllOrphans(ctx, localDB, client); err != nil {
			return result, fmt.Errorf("clean orphans: %w", err)
		}
	}

	return result, nil
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
func pushBookmarks(ctx context.Context, localDB *sql.DB, client *Client, lastSynced string, forceUpdate bool) (newCount, updCount int, err error) {
	if forceUpdate {
		return forcePushBookmarks(ctx, localDB, client)
	}
	return incrementalPushBookmarks(ctx, localDB, client, lastSynced)
}

// forcePushBookmarks 强制推送：INSERT OR REPLACE 全量覆盖远端。
func forcePushBookmarks(ctx context.Context, localDB *sql.DB, client *Client) (newCount, updCount int, err error) {
	bookmarks := queryLocalBookmarks(localDB, "")
	if len(bookmarks) == 0 {
		return 0, 0, nil
	}

	var hasItemError bool
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

		_, bErr := client.Execute(ctx, stmts)
		if bErr != nil {
			fmt.Fprintf(os.Stderr, "  ⚠ batch push failed (%d items): %v\n", len(chunk), bErr)
			// 批量失败时逐条重试
			for j, s := range stmts {
				_, rErr := client.ExecuteOne(ctx, s.SQL, s.Args...)
				if rErr != nil {
					fmt.Fprintf(os.Stderr, "  ⚠ row %d/%d failed: %v\n", i+j+1, len(bookmarks), rErr)
					hasItemError = true
				} else {
					updCount++
				}
			}
		} else {
			updCount += len(chunk)
		}
	}

	if hasItemError {
		err = fmt.Errorf("some bookmarks failed during force push")
	}
	return 0, updCount, err
}

// incrementalPushBookmarks 增量推送：仅推送 lastSynced 之后变更的数据。
func incrementalPushBookmarks(ctx context.Context, localDB *sql.DB, client *Client, lastSynced string) (newCount, updCount int, err error) {
	bookmarks := queryLocalBookmarks(localDB, lastSynced)
	if len(bookmarks) == 0 {
		return 0, 0, nil
	}

	ids := make([]string, len(bookmarks))
	for i, bm := range bookmarks {
		ids[i] = bm.id
	}
	remoteUpdates, qErr := queryRemoteUpdatedAt(ctx, client, "bookmarks", "id", ids)
	if qErr != nil {
		return 0, 0, qErr // API 级错误
	}

	var hasItemError bool
	for _, bm := range bookmarks {
		remoteUT, exists := remoteUpdates[bm.id]
		da := deletedAtArg(bm.deletedAt)

		if !exists {
			_, iErr := client.ExecuteOne(ctx,
				`INSERT INTO bookmarks (id, url, title, description, note, ai_note, site_name, tags_text,
				                        click_count, is_favorite, source, created_at, updated_at, deleted_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				TextArg(bm.id), TextArg(bm.url), TextArg(bm.title), TextArg(bm.description),
				TextArg(bm.note), TextArg(bm.aiNote), TextArg(bm.siteName), TextArg(bm.tagsText),
				IntArg(bm.clickCount), IntArg(bm.isFavorite),
				TextArg(bm.source), TextArg(bm.createdAt), TextArg(bm.updatedAt), da)
			if iErr != nil {
				fmt.Fprintf(os.Stderr, "  ⚠ push insert failed [%s]: %v\n", bm.id[:8], iErr)
				hasItemError = true
			} else {
				newCount++
			}
		} else if bm.updatedAt > remoteUT {
			_, uErr := client.ExecuteOne(ctx,
				`UPDATE bookmarks SET url=?, title=?, description=?, note=?, ai_note=?,
				                      site_name=?, tags_text=?, click_count=?, is_favorite=?,
				                      source=?, updated_at=?, deleted_at=?
				 WHERE id=?`,
				TextArg(bm.url), TextArg(bm.title), TextArg(bm.description),
				TextArg(bm.note), TextArg(bm.aiNote), TextArg(bm.siteName), TextArg(bm.tagsText),
				IntArg(bm.clickCount), IntArg(bm.isFavorite),
				TextArg(bm.source), TextArg(bm.updatedAt), da, TextArg(bm.id))
			if uErr != nil {
				fmt.Fprintf(os.Stderr, "  ⚠ push update failed [%s]: %v\n", bm.id[:8], uErr)
				hasItemError = true
			} else {
				updCount++
			}
		}
	}

	if hasItemError {
		err = fmt.Errorf("some bookmarks failed during incremental push")
	}
	return
}

// cleanRemoteOrphans 删除远端存在但本地不存在的行。
func cleanRemoteOrphans(ctx context.Context, client *Client, table, idCol string, localIDs []string) error {
	if len(localIDs) == 0 {
		return nil
	}

	placeholders := make([]string, len(localIDs))
	args := make([]Arg, len(localIDs))
	for i, id := range localIDs {
		placeholders[i] = "?"
		args[i] = TextArg(id)
	}

	q := fmt.Sprintf(`DELETE FROM %s WHERE %s NOT IN (%s)`,
		table, idCol, strings.Join(placeholders, ","))
	_, err := client.ExecuteOne(ctx, q, args...)
	return err
}

// pushTags 推送本地全量 tags 到远端（INSERT OR IGNORE）。
func pushTags(ctx context.Context, localDB *sql.DB, client *Client) error {
	rows, err := localDB.Query(`SELECT id, name FROM tags`)
	if err != nil {
		return nil // 本地查询失败不阻塞
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
		_, err := client.Execute(ctx, stmts)
		if err != nil {
			return err // API 级错误
		}
	}
	return nil
}

// ensureReferencedBookmarks 确保指定的 bookmark_id 在远端都存在。
// 远端 bookmark_tags 可能带有 FOREIGN KEY 约束，增量推送时只推了 updated_at 变化的
// bookmark，但 bookmark_tag 引用的 bookmark 可能没在本轮推送范围内，导致 FK 失败。
// 这里查询远端缺失的 bookmark，再从本地补推。
func ensureReferencedBookmarks(ctx context.Context, localDB *sql.DB, client *Client, bmIDs []string) error {

	// 查询远端哪些 bookmark_id 已存在
	remoteUpdates, err := queryRemoteUpdatedAt(ctx, client, "bookmarks", "id", bmIDs)
	if err != nil {
		return err
	}

	// 找出远端缺失的 bookmark_id
	var missing []string
	for _, id := range bmIDs {
		if _, exists := remoteUpdates[id]; !exists {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	// 从本地查询缺失的 bookmark 并推送到远端
	placeholders := make([]string, len(missing))
	queryArgs := make([]any, len(missing))
	for i, id := range missing {
		placeholders[i] = "?"
		queryArgs[i] = id
	}

	q := fmt.Sprintf(`SELECT id, url, title, description, note, ai_note, site_name, tags_text,
	                          click_count, is_favorite, source, created_at, updated_at, deleted_at
	                   FROM bookmarks WHERE id IN (%s)`, strings.Join(placeholders, ","))
	rows, err := localDB.Query(q, queryArgs...)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var bm localBM
		if err := rows.Scan(&bm.id, &bm.url, &bm.title, &bm.description, &bm.note, &bm.aiNote,
			&bm.siteName, &bm.tagsText, &bm.clickCount, &bm.isFavorite,
			&bm.source, &bm.createdAt, &bm.updatedAt, &bm.deletedAt); err != nil {
			continue
		}
		_, iErr := client.ExecuteOne(ctx,
			`INSERT OR IGNORE INTO bookmarks (id, url, title, description, note, ai_note, site_name, tags_text,
			                        click_count, is_favorite, source, created_at, updated_at, deleted_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			TextArg(bm.id), TextArg(bm.url), TextArg(bm.title), TextArg(bm.description),
			TextArg(bm.note), TextArg(bm.aiNote), TextArg(bm.siteName), TextArg(bm.tagsText),
			IntArg(bm.clickCount), IntArg(bm.isFavorite),
			TextArg(bm.source), TextArg(bm.createdAt), TextArg(bm.updatedAt), deletedAtArg(bm.deletedAt))
		if iErr != nil {
			fmt.Fprintf(os.Stderr, "  ⚠ ensure bookmark [%s] failed: %v\n", bm.id[:8], iErr)
		}
	}
	return nil
}

// pushBookmarkTags 推送本地变更的 bookmark_tags 到远端。
func pushBookmarkTags(ctx context.Context, localDB *sql.DB, client *Client, lastSynced string) error {
	q := `SELECT bookmark_id, tag_id, updated_at, deleted_at FROM bookmark_tags`
	var queryArgs []any
	if lastSynced != "" {
		q += ` WHERE updated_at > ?`
		queryArgs = append(queryArgs, lastSynced)
	}

	rows, err := localDB.Query(q, queryArgs...)
	if err != nil {
		return nil // 本地查询失败不阻塞
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

	if len(links) == 0 {
		return nil
	}

	// 远端 bookmark_tags 可能有 FOREIGN KEY 约束，
	// 增量模式下被引用的 bookmark 可能没在本轮推送，需要先补推。
	seen := make(map[string]bool)
	var refBmIDs []string
	for _, l := range links {
		if !seen[l.bmID] {
			seen[l.bmID] = true
			refBmIDs = append(refBmIDs, l.bmID)
		}
	}
	if err := ensureReferencedBookmarks(ctx, localDB, client, refBmIDs); err != nil {
		fmt.Fprintf(os.Stderr, "  ⚠ ensure referenced bookmarks: %v\n", err)
	}

	var hasItemError bool
	for _, l := range links {
		var da Arg
		if l.deletedAt.Valid {
			da = TextArg(l.deletedAt.String)
		} else {
			da = NullArg()
		}

		res, err := client.ExecuteOne(ctx,
			`SELECT updated_at FROM bookmark_tags WHERE bookmark_id = ? AND tag_id = ?`,
			TextArg(l.bmID), TextArg(l.tagID))
		if err != nil {
			return err // API 级错误
		}

		if len(res.Rows) == 0 {
			_, iErr := client.ExecuteOne(ctx,
				`INSERT INTO bookmark_tags (bookmark_id, tag_id, updated_at, deleted_at) VALUES (?, ?, ?, ?)`,
				TextArg(l.bmID), TextArg(l.tagID), TextArg(l.updatedAt), da)
			if iErr != nil {
				fmt.Fprintf(os.Stderr, "  ⚠ push bookmark_tag insert failed [%s-%s]: %v\n", l.bmID[:8], l.tagID[:8], iErr)
				hasItemError = true
			}
		} else if l.updatedAt > res.Rows[0][0].Value {
			_, uErr := client.ExecuteOne(ctx,
				`UPDATE bookmark_tags SET updated_at = ?, deleted_at = ? WHERE bookmark_id = ? AND tag_id = ?`,
				TextArg(l.updatedAt), da, TextArg(l.bmID), TextArg(l.tagID))
			if uErr != nil {
				fmt.Fprintf(os.Stderr, "  ⚠ push bookmark_tag update failed [%s-%s]: %v\n", l.bmID[:8], l.tagID[:8], uErr)
				hasItemError = true
			}
		}
	}

	if hasItemError {
		return fmt.Errorf("some bookmark_tags failed during push")
	}
	return nil
}

// queryRemoteUpdatedAt 批量查询远端指定表指定 id 列的 updated_at 值。
func queryRemoteUpdatedAt(ctx context.Context, client *Client, table, idCol string, ids []string) (map[string]string, error) {
	result := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return result, nil
	}

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
		return nil, err
	}

	for _, row := range res.Rows {
		if len(row) >= 2 {
			result[row[0].Value] = row[1].Value
		}
	}
	return result, nil
}

// ensureRemoteSchema 确保远端数据库有正确的表结构。
// 返回 (changed, error)。changed=true 表示有 schema 变更（需要强制全量推送）。
func ensureRemoteSchema(ctx context.Context, client *Client) (changed bool, err error) {
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
	if _, err := client.Execute(ctx, stmts); err != nil {
		return false, err // API 级错误：无法建表
	}

	alterStmts := []string{
		`ALTER TABLE bookmarks ADD COLUMN ai_note TEXT DEFAULT ''`,
		`ALTER TABLE bookmarks ADD COLUMN deleted_at TEXT DEFAULT NULL`,
		`ALTER TABLE bookmark_tags ADD COLUMN updated_at TEXT DEFAULT NULL`,
		`ALTER TABLE bookmark_tags ADD COLUMN deleted_at TEXT DEFAULT NULL`,
	}
	for _, sql := range alterStmts {
		_, aErr := client.ExecuteOne(ctx, sql)
		if aErr == nil {
			changed = true
		}
	}

	// 远端 bookmark_tags 可能是带 FOREIGN KEY 约束的旧表，
	// 增量 push 时会导致 FK 校验失败。检测并重建为无 FK 版本。
	if migrated, mErr := migrateBookmarkTagsDropFK(ctx, client); mErr != nil {
		fmt.Fprintf(os.Stderr, "  ⚠ migrate bookmark_tags FK: %v\n", mErr)
	} else if migrated {
		changed = true
	}

	return changed, nil
}

// migrateBookmarkTagsDropFK 检测远端 bookmark_tags 是否带 FOREIGN KEY 约束，
// 如果是则重建为无 FK 的版本。SQLite 不支持 ALTER TABLE DROP CONSTRAINT，
// 只能通过创建新表 → 复制数据 → 删旧表 → 重命名实现。
func migrateBookmarkTagsDropFK(ctx context.Context, client *Client) (migrated bool, err error) {
	res, err := client.ExecuteOne(ctx, `PRAGMA foreign_key_list(bookmark_tags)`)
	if err != nil {
		return false, nil // PRAGMA 不支持时静默跳过
	}
	if len(res.Rows) == 0 {
		return false, nil // 无 FK，无需迁移
	}

	// 有 FK 约束，需要重建
	stmts := []Stmt{
		{SQL: `PRAGMA foreign_keys = OFF`},
		{SQL: `CREATE TABLE IF NOT EXISTS bookmark_tags_v2 (
			bookmark_id TEXT NOT NULL, tag_id TEXT NOT NULL,
			updated_at TEXT, deleted_at TEXT DEFAULT NULL,
			PRIMARY KEY (bookmark_id, tag_id)
		)`},
		{SQL: `INSERT OR IGNORE INTO bookmark_tags_v2 SELECT bookmark_id, tag_id, updated_at, deleted_at FROM bookmark_tags`},
		{SQL: `DROP TABLE bookmark_tags`},
		{SQL: `ALTER TABLE bookmark_tags_v2 RENAME TO bookmark_tags`},
	}
	if _, err := client.Execute(ctx, stmts); err != nil {
		return false, fmt.Errorf("recreate bookmark_tags without FK: %w", err)
	}
	return true, nil
}

// btKey 是 bookmark_tags 的复合主键。
type btKey struct {
	bmID  string
	tagID string
}

// cleanAllOrphans 清理远端三表的孤儿数据。
// 按外键依赖顺序：bookmark_tags → tags → bookmarks。
func cleanAllOrphans(ctx context.Context, localDB *sql.DB, client *Client) error {
	// 1. 清理 bookmark_tags 孤儿
	btRows, err := localDB.Query(`SELECT bookmark_id, tag_id FROM bookmark_tags`)
	if err == nil {
		var btKeys []btKey
		for btRows.Next() {
			var k btKey
			if btRows.Scan(&k.bmID, &k.tagID) == nil {
				btKeys = append(btKeys, k)
			}
		}
		btRows.Close()

		if err := cleanRemoteOrphansBT(ctx, client, btKeys); err != nil {
			return fmt.Errorf("clean bookmark_tags orphans: %w", err)
		}
	}

	// 2. 清理 tags 孤儿
	tagRows, err := localDB.Query(`SELECT id FROM tags`)
	if err == nil {
		var tagIDs []string
		for tagRows.Next() {
			var id string
			if tagRows.Scan(&id) == nil {
				tagIDs = append(tagIDs, id)
			}
		}
		tagRows.Close()

		if err := cleanRemoteOrphans(ctx, client, "tags", "id", tagIDs); err != nil {
			return fmt.Errorf("clean tags orphans: %w", err)
		}
	}

	// 3. 清理 bookmarks 孤儿
	bmRows, err := localDB.Query(`SELECT id FROM bookmarks`)
	if err == nil {
		var bmIDs []string
		for bmRows.Next() {
			var id string
			if bmRows.Scan(&id) == nil {
				bmIDs = append(bmIDs, id)
			}
		}
		bmRows.Close()

		if err := cleanRemoteOrphans(ctx, client, "bookmarks", "id", bmIDs); err != nil {
			return fmt.Errorf("clean bookmarks orphans: %w", err)
		}
	}

	return nil
}

// cleanRemoteOrphansBT 清理远端 bookmark_tags 表中本地不存在的复合键。
func cleanRemoteOrphansBT(ctx context.Context, client *Client, localKeys []btKey) error {
	if len(localKeys) == 0 {
		// 本地无数据，清除远端全部
		_, err := client.ExecuteOne(ctx, `DELETE FROM bookmark_tags`)
		return err
	}

	// 用 bookmark_id || '|' || tag_id 做拼接比较
	placeholders := make([]string, len(localKeys))
	args := make([]Arg, len(localKeys))
	for i, k := range localKeys {
		placeholders[i] = "?"
		args[i] = TextArg(k.bmID + "|" + k.tagID)
	}

	q := fmt.Sprintf(`DELETE FROM bookmark_tags WHERE bookmark_id || '|' || tag_id NOT IN (%s)`,
		strings.Join(placeholders, ","))
	_, err := client.ExecuteOne(ctx, q, args...)
	return err
}
