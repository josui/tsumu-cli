// pull.go 实现 Pull 阶段：远端 → 本地。
// 从 Turso 远端拉取 last_synced 之后变更的数据，UPSERT 到本地。
// 冲突解决：updated_at LWW（远端更新时间 > 本地时才覆盖）。

package sync

import (
	"context"
	"database/sql"
	"strconv"
)

// PullResult 是 Pull 阶段的结果统计。
type PullResult struct {
	New     int
	Updated int
}

// Pull 从远端拉取变更数据并合并到本地。
// lastSynced 为空时拉取全量。
func Pull(ctx context.Context, localDB *sql.DB, client *Client, lastSynced string) PullResult {
	var result PullResult

	// 1. 拉取 bookmarks
	bNew, bUpd := pullBookmarks(ctx, localDB, client, lastSynced)
	result.New += bNew
	result.Updated += bUpd

	// 2. 拉取 tags（全量，INSERT OR IGNORE）
	pullTags(ctx, localDB, client)

	// 3. 拉取 bookmark_tags
	pullBookmarkTags(ctx, localDB, client, lastSynced)

	return result
}

// pullBookmarks 拉取远端 bookmarks 并 UPSERT 到本地。
// 冲突策略：remote.updated_at > local.updated_at 时覆盖本地。
func pullBookmarks(ctx context.Context, localDB *sql.DB, client *Client, lastSynced string) (newCount, updCount int) {
	q := `SELECT id, url, title, description, note, ai_note, site_name, tags_text,
	             click_count, is_favorite, source, created_at, updated_at, deleted_at
	      FROM bookmarks`
	var args []Arg
	if lastSynced != "" {
		q += ` WHERE updated_at > ?`
		args = append(args, TextArg(lastSynced))
	}

	res, err := client.ExecuteOne(ctx, q, args...)
	if err != nil {
		return 0, 0
	}

	for _, row := range res.Rows {
		if len(row) < 14 {
			continue
		}
		id := row[0].Value
		updatedAt := row[12].Value

		// 检查本地是否存在（包括已 soft delete 的）
		var localUpdatedAt string
		err := localDB.QueryRow(`SELECT updated_at FROM bookmarks WHERE id = ?`, id).Scan(&localUpdatedAt)

		if err == sql.ErrNoRows {
			// 本地不存在，INSERT
			clickCount, _ := strconv.Atoi(row[8].Value)
			isFavorite, _ := strconv.Atoi(row[9].Value)

			_, err = localDB.Exec(
				`INSERT INTO bookmarks (id, url, title, description, note, ai_note, site_name, tags_text,
				                        click_count, is_favorite, source, created_at, updated_at, deleted_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				id, row[1].Value, row[2].Value, row[3].Value, row[4].Value, row[5].Value,
				row[6].Value, row[7].Value, clickCount, isFavorite,
				row[10].Value, row[11].Value, updatedAt, nullableText(row[13]))
			if err == nil {
				newCount++
			}
		} else if err == nil && updatedAt > localUpdatedAt {
			// 远端更新时间更新，覆盖本地
			clickCount, _ := strconv.Atoi(row[8].Value)
			isFavorite, _ := strconv.Atoi(row[9].Value)

			_, err = localDB.Exec(
				`UPDATE bookmarks SET url=?, title=?, description=?, note=?, ai_note=?,
				                      site_name=?, tags_text=?, click_count=?, is_favorite=?,
				                      source=?, updated_at=?, deleted_at=?
				 WHERE id=?`,
				row[1].Value, row[2].Value, row[3].Value, row[4].Value, row[5].Value,
				row[6].Value, row[7].Value, clickCount, isFavorite,
				row[10].Value, updatedAt, nullableText(row[13]), id)
			if err == nil {
				updCount++
			}
		}
	}
	return
}

// pullTags 拉取远端全量 tags，INSERT OR IGNORE（按 name 去重）。
func pullTags(ctx context.Context, localDB *sql.DB, client *Client) {
	res, err := client.ExecuteOne(ctx, `SELECT id, name FROM tags`)
	if err != nil {
		return
	}
	for _, row := range res.Rows {
		if len(row) < 2 {
			continue
		}
		localDB.Exec(`INSERT OR IGNORE INTO tags (id, name) VALUES (?, ?)`,
			row[0].Value, row[1].Value)
	}
}

// pullBookmarkTags 拉取远端 bookmark_tags 变更并合并到本地。
// 冲突策略：remote.updated_at > local.updated_at 时覆盖。
func pullBookmarkTags(ctx context.Context, localDB *sql.DB, client *Client, lastSynced string) {
	q := `SELECT bookmark_id, tag_id, updated_at, deleted_at FROM bookmark_tags`
	var args []Arg
	if lastSynced != "" {
		q += ` WHERE updated_at > ?`
		args = append(args, TextArg(lastSynced))
	}

	res, err := client.ExecuteOne(ctx, q, args...)
	if err != nil {
		return
	}

	for _, row := range res.Rows {
		if len(row) < 4 {
			continue
		}
		bmID := row[0].Value
		tagID := row[1].Value
		updatedAt := row[2].Value
		deletedAt := nullableText(row[3])

		var localUpdatedAt string
		err := localDB.QueryRow(
			`SELECT updated_at FROM bookmark_tags WHERE bookmark_id = ? AND tag_id = ?`,
			bmID, tagID).Scan(&localUpdatedAt)

		if err == sql.ErrNoRows {
			localDB.Exec(
				`INSERT INTO bookmark_tags (bookmark_id, tag_id, updated_at, deleted_at) VALUES (?, ?, ?, ?)`,
				bmID, tagID, updatedAt, deletedAt)
		} else if err == nil && updatedAt > localUpdatedAt {
			localDB.Exec(
				`UPDATE bookmark_tags SET updated_at = ?, deleted_at = ? WHERE bookmark_id = ? AND tag_id = ?`,
				updatedAt, deletedAt, bmID, tagID)
		}
	}
}

// nullableText 将 Hrana Value 转为 Go 的 nullable 值。
// null 类型返回 nil（用于 deleted_at 等可空字段）。
func nullableText(v Value) any {
	if v.Type == "null" {
		return nil
	}
	return v.Value
}
