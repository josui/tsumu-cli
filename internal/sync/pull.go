// pull.go 实现 Pull 阶段：远端 → 本地。
// 从 Turso 远端拉取 last_synced 之后变更的数据，UPSERT 到本地。
// 冲突解决：updated_at LWW（远端更新时间 > 本地时才覆盖）。

package sync

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
)

// PullResult 是 Pull 阶段的结果统计。
type PullResult struct {
	New     int
	Updated int
}

// Pull 从远端拉取变更数据并合并到本地。
// lastSynced 为空时拉取全量。
// 返回 error 表示有数据失败（API 级或单条级），调用方不应推进 last_synced。
func Pull(ctx context.Context, localDB *sql.DB, client *Client, lastSynced string) (PullResult, error) {
	var result PullResult

	// 1. 拉取 bookmarks
	bNew, bUpd, err := pullBookmarks(ctx, localDB, client, lastSynced)
	if err != nil {
		result.New = bNew
		result.Updated = bUpd
		return result, fmt.Errorf("pull bookmarks: %w", err)
	}
	result.New += bNew
	result.Updated += bUpd

	// 2. 拉取 tags（全量，INSERT OR IGNORE）
	if err := pullTags(ctx, localDB, client); err != nil {
		return result, fmt.Errorf("pull tags: %w", err)
	}

	// 3. 拉取 bookmark_tags
	if err := pullBookmarkTags(ctx, localDB, client, lastSynced); err != nil {
		return result, fmt.Errorf("pull bookmark_tags: %w", err)
	}

	return result, nil
}

// pullBookmarks 拉取远端 bookmarks 并 UPSERT 到本地。
// API 调用失败返回 error。单条 INSERT/UPDATE 失败打印 warning 并标记 hasItemError。
func pullBookmarks(ctx context.Context, localDB *sql.DB, client *Client, lastSynced string) (newCount, updCount int, err error) {
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
		return 0, 0, err // API 级错误
	}

	var hasItemError bool
	for _, row := range res.Rows {
		if len(row) < 14 {
			continue
		}
		id := row[0].Value
		updatedAt := row[12].Value

		var localUpdatedAt string
		qErr := localDB.QueryRow(`SELECT updated_at FROM bookmarks WHERE id = ?`, id).Scan(&localUpdatedAt)

		if qErr == sql.ErrNoRows {
			clickCount, _ := strconv.Atoi(row[8].Value)
			isFavorite, _ := strconv.Atoi(row[9].Value)

			_, iErr := localDB.Exec(
				`INSERT INTO bookmarks (id, url, title, description, note, ai_note, site_name, tags_text,
				                        click_count, is_favorite, source, created_at, updated_at, deleted_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				id, row[1].Value, row[2].Value, row[3].Value, row[4].Value, row[5].Value,
				row[6].Value, row[7].Value, clickCount, isFavorite,
				row[10].Value, row[11].Value, updatedAt, nullableText(row[13]))
			if iErr != nil {
				fmt.Fprintf(os.Stderr, "  ⚠ pull insert failed [%s]: %v\n", id[:8], iErr)
				hasItemError = true
			} else {
				newCount++
			}
		} else if qErr == nil && updatedAt > localUpdatedAt {
			clickCount, _ := strconv.Atoi(row[8].Value)
			isFavorite, _ := strconv.Atoi(row[9].Value)

			_, uErr := localDB.Exec(
				`UPDATE bookmarks SET url=?, title=?, description=?, note=?, ai_note=?,
				                      site_name=?, tags_text=?, click_count=?, is_favorite=?,
				                      source=?, updated_at=?, deleted_at=?
				 WHERE id=?`,
				row[1].Value, row[2].Value, row[3].Value, row[4].Value, row[5].Value,
				row[6].Value, row[7].Value, clickCount, isFavorite,
				row[10].Value, updatedAt, nullableText(row[13]), id)
			if uErr != nil {
				fmt.Fprintf(os.Stderr, "  ⚠ pull update failed [%s]: %v\n", id[:8], uErr)
				hasItemError = true
			} else {
				updCount++
			}
		}
	}

	if hasItemError {
		err = fmt.Errorf("some bookmarks failed during pull")
	}
	return
}

// pullTags 拉取远端全量 tags，INSERT OR IGNORE。
func pullTags(ctx context.Context, localDB *sql.DB, client *Client) error {
	res, err := client.ExecuteOne(ctx, `SELECT id, name FROM tags`)
	if err != nil {
		return err // API 级错误
	}
	for _, row := range res.Rows {
		if len(row) < 2 {
			continue
		}
		localDB.Exec(`INSERT OR IGNORE INTO tags (id, name) VALUES (?, ?)`,
			row[0].Value, row[1].Value)
	}
	return nil
}

// pullBookmarkTags 拉取远端 bookmark_tags 变更并合并到本地。
func pullBookmarkTags(ctx context.Context, localDB *sql.DB, client *Client, lastSynced string) error {
	q := `SELECT bookmark_id, tag_id, updated_at, deleted_at FROM bookmark_tags`
	var args []Arg
	if lastSynced != "" {
		q += ` WHERE updated_at > ?`
		args = append(args, TextArg(lastSynced))
	}

	res, err := client.ExecuteOne(ctx, q, args...)
	if err != nil {
		return err // API 级错误
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
		qErr := localDB.QueryRow(
			`SELECT updated_at FROM bookmark_tags WHERE bookmark_id = ? AND tag_id = ?`,
			bmID, tagID).Scan(&localUpdatedAt)

		if qErr == sql.ErrNoRows {
			localDB.Exec(
				`INSERT INTO bookmark_tags (bookmark_id, tag_id, updated_at, deleted_at) VALUES (?, ?, ?, ?)`,
				bmID, tagID, updatedAt, deletedAt)
		} else if qErr == nil && updatedAt > localUpdatedAt {
			localDB.Exec(
				`UPDATE bookmark_tags SET updated_at = ?, deleted_at = ? WHERE bookmark_id = ? AND tag_id = ?`,
				updatedAt, deletedAt, bmID, tagID)
		}
	}
	return nil
}

// nullableText 将 Hrana Value 转为 Go 的 nullable 值。
// null 类型返回 nil（用于 deleted_at 等可空字段）。
func nullableText(v Value) any {
	if v.Type == "null" {
		return nil
	}
	return v.Value
}
