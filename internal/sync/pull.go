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
	"strings"
)

// PullResult 是 Pull 阶段的结果统计。
type PullResult struct {
	New          int
	Updated      int
	MaxUpdatedAt string // 本轮远端返回行的最大 updated_at，用于推进 pull cursor
}

// Pull 从远端拉取变更数据并合并到本地。
// pullCursor 为空时拉取全量。
// 返回 error 表示有数据失败（API 级或单条级），调用方不应推进 pull_cursor。
func Pull(ctx context.Context, localDB *sql.DB, client *Client, pullCursor string) (PullResult, error) {
	var result PullResult

	// 1. 拉取 bookmarks（增量 / 全量）
	bNew, bUpd, bMax, err := pullBookmarks(ctx, localDB, client, pullCursor)
	result.New += bNew
	result.Updated += bUpd
	result.MaxUpdatedAt = maxStr(result.MaxUpdatedAt, bMax)
	if err != nil {
		return result, fmt.Errorf("pull bookmarks: %w", err)
	}

	// 1b. id-diff 补漏：补回远端活跃、本地缺失的 bookmarks
	bfIDs, bfNew, bfMax, err := backfillMissingBookmarks(ctx, localDB, client)
	result.New += bfNew
	result.MaxUpdatedAt = maxStr(result.MaxUpdatedAt, bfMax)
	if err != nil {
		return result, fmt.Errorf("backfill bookmarks: %w", err)
	}

	// 1c. 为补回的 bookmarks 补其 bookmark_tags 关联（否则显示/过滤丢 tag）
	if err := backfillMissingBookmarkTags(ctx, localDB, client, bfIDs); err != nil {
		return result, fmt.Errorf("backfill bookmark_tags: %w", err)
	}

	// 2. 拉取 tags（全量，INSERT OR IGNORE）
	if err := pullTags(ctx, localDB, client); err != nil {
		return result, fmt.Errorf("pull tags: %w", err)
	}

	// 3. 拉取 bookmark_tags
	btMax, err := pullBookmarkTags(ctx, localDB, client, pullCursor)
	result.MaxUpdatedAt = maxStr(result.MaxUpdatedAt, btMax)
	if err != nil {
		return result, fmt.Errorf("pull bookmark_tags: %w", err)
	}

	return result, nil
}

// pullBookmarks 拉取远端 bookmarks 并 UPSERT 到本地。
// pullCursor 为空时全量拉取；否则只拉 updated_at > pullCursor 的行。
// API 调用失败返回 error。单条 INSERT/UPDATE 失败打印 warning 并标记 hasItemError。
func pullBookmarks(ctx context.Context, localDB *sql.DB, client *Client, pullCursor string) (newCount, updCount int, maxUT string, err error) {
	q := `SELECT id, url, title, description, note, ai_note, site_name, tags_text,
	             click_count, is_favorite, source, created_at, updated_at, deleted_at
	      FROM bookmarks`
	var args []Arg
	if pullCursor != "" {
		q += ` WHERE updated_at > ?`
		args = append(args, TextArg(pullCursor))
	}

	res, err := client.ExecuteOne(ctx, q, args...)
	if err != nil {
		return 0, 0, "", err // API 级错误
	}

	var hasItemError bool
	for _, row := range res.Rows {
		if len(row) < 14 {
			continue
		}
		id := row[0].Value
		updatedAt := row[12].Value
		maxUT = maxStr(maxUT, updatedAt)

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

// backfillMissingBookmarks 做 id 集合差补漏：补回「远端活跃、本地完全不存在」的 bookmarks。
// 不依赖时间戳，兜住增量 pull 因时间戳过滤而漏掉的旧行（迟到的旧数据 / 导入 / 时钟偏移）。
// 返回补回的 id 列表（供补 bookmark_tags）、新增数、补回行的最大 updated_at。
func backfillMissingBookmarks(ctx context.Context, localDB *sql.DB, client *Client) (backfilledIDs []string, newCount int, maxUT string, err error) {
	// 1. 远端活跃 id
	res, err := client.ExecuteOne(ctx, `SELECT id FROM bookmarks WHERE deleted_at IS NULL`)
	if err != nil {
		return nil, 0, "", err // API 级错误
	}
	var remoteIDs []string
	for _, row := range res.Rows {
		if len(row) >= 1 {
			remoteIDs = append(remoteIDs, row[0].Value)
		}
	}
	if len(remoteIDs) == 0 {
		return nil, 0, "", nil
	}

	// 2. 本地全部 id（含已删）
	// 本地查询失败必须返回 error，不能用空集合继续：否则会把所有远端行误判为缺失，
	// 触发对已存在行的重复 INSERT，徒增 PK 冲突噪音。
	localIDs := make(map[string]bool)
	rows, qErr := localDB.Query(`SELECT id FROM bookmarks`)
	if qErr != nil {
		return nil, 0, "", fmt.Errorf("backfill: query local ids: %w", qErr)
	}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			localIDs[id] = true
		}
	}
	rows.Close()

	// 3. 差集 = 远端活跃 ∩ 本地不存在
	var missing []string
	for _, id := range remoteIDs {
		if !localIDs[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return nil, 0, "", nil
	}

	// 4. 分批拉全行并 INSERT（差集保证本地无，恒为 INSERT）
	var hasItemError bool
	const chunkSize = 500
	for i := 0; i < len(missing); i += chunkSize {
		end := i + chunkSize
		if end > len(missing) {
			end = len(missing)
		}
		chunk := missing[i:end]

		placeholders := make([]string, len(chunk))
		args := make([]Arg, len(chunk))
		for j, id := range chunk {
			placeholders[j] = "?"
			args[j] = TextArg(id)
		}
		q := fmt.Sprintf(`SELECT id, url, title, description, note, ai_note, site_name, tags_text,
		                         click_count, is_favorite, source, created_at, updated_at, deleted_at
		                  FROM bookmarks WHERE id IN (%s)`, strings.Join(placeholders, ","))

		rowsRes, rErr := client.ExecuteOne(ctx, q, args...)
		if rErr != nil {
			return backfilledIDs, newCount, maxUT, rErr // API 级错误
		}

		for _, row := range rowsRes.Rows {
			if len(row) < 14 {
				continue
			}
			id := row[0].Value
			updatedAt := row[12].Value
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
				fmt.Fprintf(os.Stderr, "  ⚠ backfill insert failed [%s]: %v\n", shortID(id), iErr)
				hasItemError = true
			} else {
				backfilledIDs = append(backfilledIDs, id)
				newCount++
				maxUT = maxStr(maxUT, updatedAt)
			}
		}
	}

	if hasItemError {
		err = fmt.Errorf("some bookmarks failed during backfill")
	}
	return
}

// backfillMissingBookmarkTags 为刚 backfill 的 bookmarks 补回它们的 bookmark_tags 关联。
// 这些 bookmark 本地刚插入、无任何关联，故恒为 INSERT。
// 窄范围（按 bookmark_id IN），不做通用复合键 id-diff。
func backfillMissingBookmarkTags(ctx context.Context, localDB *sql.DB, client *Client, bmIDs []string) error {
	if len(bmIDs) == 0 {
		return nil
	}

	var hasItemError bool
	const chunkSize = 500
	for i := 0; i < len(bmIDs); i += chunkSize {
		end := i + chunkSize
		if end > len(bmIDs) {
			end = len(bmIDs)
		}
		chunk := bmIDs[i:end]

		placeholders := make([]string, len(chunk))
		args := make([]Arg, len(chunk))
		for j, id := range chunk {
			placeholders[j] = "?"
			args[j] = TextArg(id)
		}
		q := fmt.Sprintf(
			`SELECT bookmark_id, tag_id, updated_at, deleted_at FROM bookmark_tags WHERE bookmark_id IN (%s)`,
			strings.Join(placeholders, ","))

		res, err := client.ExecuteOne(ctx, q, args...)
		if err != nil {
			return err // API 级错误
		}

		for _, row := range res.Rows {
			if len(row) < 4 {
				continue
			}
			_, iErr := localDB.Exec(
				`INSERT OR IGNORE INTO bookmark_tags (bookmark_id, tag_id, updated_at, deleted_at) VALUES (?, ?, ?, ?)`,
				row[0].Value, row[1].Value, row[2].Value, nullableText(row[3]))
			if iErr != nil {
				fmt.Fprintf(os.Stderr, "  ⚠ backfill bookmark_tag failed [%s-%s]: %v\n", shortID(row[0].Value), shortID(row[1].Value), iErr)
				hasItemError = true
			}
		}
	}

	if hasItemError {
		return fmt.Errorf("some bookmark_tags failed during backfill")
	}
	return nil
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
// pullCursor 为空时全量拉取；否则只拉 updated_at > pullCursor 的行。
func pullBookmarkTags(ctx context.Context, localDB *sql.DB, client *Client, pullCursor string) (maxUT string, err error) {
	q := `SELECT bookmark_id, tag_id, updated_at, deleted_at FROM bookmark_tags`
	var args []Arg
	if pullCursor != "" {
		q += ` WHERE updated_at > ?`
		args = append(args, TextArg(pullCursor))
	}

	res, err := client.ExecuteOne(ctx, q, args...)
	if err != nil {
		return "", err // API 级错误
	}

	// 记录被变更的 bookmark_id，pull 完成后刷新 tags_text
	affectedBmIDs := make(map[string]bool)

	for _, row := range res.Rows {
		if len(row) < 4 {
			continue
		}
		bmID := row[0].Value
		tagID := row[1].Value
		updatedAt := row[2].Value
		maxUT = maxStr(maxUT, updatedAt)
		deletedAt := nullableText(row[3])

		var localUpdatedAt string
		qErr := localDB.QueryRow(
			`SELECT updated_at FROM bookmark_tags WHERE bookmark_id = ? AND tag_id = ?`,
			bmID, tagID).Scan(&localUpdatedAt)

		if qErr == sql.ErrNoRows {
			_, iErr := localDB.Exec(
				`INSERT INTO bookmark_tags (bookmark_id, tag_id, updated_at, deleted_at) VALUES (?, ?, ?, ?)`,
				bmID, tagID, updatedAt, deletedAt)
			if iErr == nil {
				affectedBmIDs[bmID] = true
			}
		} else if qErr == nil && updatedAt > localUpdatedAt {
			_, uErr := localDB.Exec(
				`UPDATE bookmark_tags SET updated_at = ?, deleted_at = ? WHERE bookmark_id = ? AND tag_id = ?`,
				updatedAt, deletedAt, bmID, tagID)
			if uErr == nil {
				affectedBmIDs[bmID] = true
			}
		}
	}

	// 刷新被影响的 bookmark 的 tags_text 冗余字段，使 FTS5 索引保持一致。
	// 不更新 updated_at，避免产生假变更触发下次 push 反推。
	for bmID := range affectedBmIDs {
		localDB.Exec(
			`UPDATE bookmarks SET tags_text = (
				SELECT COALESCE(GROUP_CONCAT(t.name, ','), '')
				FROM bookmark_tags bt
				JOIN tags t ON bt.tag_id = t.id
				WHERE bt.bookmark_id = ? AND bt.deleted_at IS NULL
			) WHERE id = ?`,
			bmID, bmID)
	}

	return maxUT, nil
}

// shortID 返回 id 的前 8 字符用于日志，短于 8 字符时返回全量，避免切片越界 panic。
func shortID(id string) string {
	if len(id) < 8 {
		return id
	}
	return id[:8]
}

// nullableText 将 Hrana Value 转为 Go 的 nullable 值。
// null 类型返回 nil（用于 deleted_at 等可空字段）。
func nullableText(v Value) any {
	if v.Type == "null" {
		return nil
	}
	return v.Value
}
