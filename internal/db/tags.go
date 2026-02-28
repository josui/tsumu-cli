// cli/internal/db/tags.go

// tags.go 负责 tags 和 bookmark_tags 表的操作。
// 包括创建标签、关联书签、同步 tags_text 冗余字段。

package db

import (
	"database/sql"
	"fmt"
	"strings"
)

// ListAllTags returns all tag names sorted alphabetically.
func ListAllTags(database *sql.DB) ([]string, error) {
	rows, err := database.Query(`SELECT name FROM tags ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list tags failed: %w", err)
	}
	defer rows.Close()

	var tags []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan tag failed: %w", err)
		}
		tags = append(tags, name)
	}
	return tags, rows.Err()
}

// SetBookmarkTags replaces all tags on a bookmark with the given list.
// Removes old associations, creates new tags as needed, then syncs tags_text.
func SetBookmarkTags(database *sql.DB, bookmarkID string, tagNames []string) error {
	tx, err := database.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction failed: %w", err)
	}
	defer tx.Rollback()

	// soft delete 现有关联
	if _, err := tx.Exec(
		`UPDATE bookmark_tags SET deleted_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now'),
		                          updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
		 WHERE bookmark_id = ? AND deleted_at IS NULL`, bookmarkID); err != nil {
		return fmt.Errorf("clear tags failed: %w", err)
	}

	// 添加新标签关联（先尝试恢复已 soft delete 的，不存在再新建）
	for _, name := range tagNames {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		tagID, err := getOrCreateTag(tx, name)
		if err != nil {
			return err
		}
		// 尝试恢复已 soft delete 的关联
		result, err := tx.Exec(
			`UPDATE bookmark_tags SET deleted_at = NULL,
			                          updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
			 WHERE bookmark_id = ? AND tag_id = ? AND deleted_at IS NOT NULL`,
			bookmarkID, tagID)
		if err != nil {
			return fmt.Errorf("restore bookmark_tag: %w", err)
		}
		affected, _ := result.RowsAffected()
		if affected == 0 {
			// 不存在，新建关联
			_, err = tx.Exec(
				`INSERT OR IGNORE INTO bookmark_tags (bookmark_id, tag_id, updated_at) VALUES (?, ?, strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))`,
				bookmarkID, tagID)
			if err != nil {
				return fmt.Errorf("insert bookmark_tag: %w", err)
			}
		}
	}

	if err := syncTagsText(tx, bookmarkID); err != nil {
		return err
	}

	// 标签变更也要刷新 bookmarks.updated_at，确保 sync 能推送
	if _, err := tx.Exec(
		`UPDATE bookmarks SET updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now') WHERE id = ?`,
		bookmarkID); err != nil {
		return fmt.Errorf("touch bookmark updated_at: %w", err)
	}

	return tx.Commit()
}

// AddTagsToBookmark 给书签添加标签。
// tagNames 是标签名列表（如 ["design", "color palette"]）。
// 标签不存在时自动创建，已关联的标签不会重复关联。
func AddTagsToBookmark(db *sql.DB, bookmarkID string, tagNames []string) error {
	// Go 的事务处理：Begin → 操作 → Commit，出错则 Rollback
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction failed: %w", err)
	}
	// defer + Rollback：如果 Commit 前函数返回（出错），自动回滚
	// 如果已经 Commit 了，Rollback 是 no-op（安全）
	defer tx.Rollback()

	for _, name := range tagNames {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}

		// 获取或创建标签
		tagID, err := getOrCreateTag(tx, name)
		if err != nil {
			return err
		}

		// 尝试恢复已 soft delete 的关联
		result, err := tx.Exec(
			`UPDATE bookmark_tags SET deleted_at = NULL,
			                          updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
			 WHERE bookmark_id = ? AND tag_id = ? AND deleted_at IS NOT NULL`,
			bookmarkID, tagID)
		if err != nil {
			return fmt.Errorf("restore bookmark_tag: %w", err)
		}
		affected, _ := result.RowsAffected()
		if affected == 0 {
			// 不存在，新建关联（INSERT OR IGNORE 避免重复关联报错）
			_, err = tx.Exec(
				`INSERT OR IGNORE INTO bookmark_tags (bookmark_id, tag_id, updated_at) VALUES (?, ?, strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))`,
				bookmarkID, tagID)
			if err != nil {
				return fmt.Errorf("insert bookmark_tag: %w", err)
			}
		}
	}

	// 同步 tags_text 冗余字段
	if err := syncTagsText(tx, bookmarkID); err != nil {
		return err
	}

	// 标签变更也要刷新 bookmarks.updated_at，确保 sync 能推送
	if _, err := tx.Exec(
		`UPDATE bookmarks SET updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now') WHERE id = ?`,
		bookmarkID); err != nil {
		return fmt.Errorf("touch bookmark updated_at: %w", err)
	}

	return tx.Commit()
}

// getOrCreateTag 查找已有标签或创建新标签，返回 tag ID。
// 使用事务参数 *sql.Tx 确保在同一个事务中执行。
func getOrCreateTag(tx *sql.Tx, name string) (string, error) {
	var id string
	err := tx.QueryRow(`SELECT id FROM tags WHERE name = ?`, name).Scan(&id)
	if err == nil {
		// 已存在，直接返回
		return id, nil
	}
	if err != sql.ErrNoRows {
		return "", fmt.Errorf("query tag failed: %w", err)
	}

	// 不存在，创建新标签
	id = newULID()
	_, err = tx.Exec(`INSERT INTO tags (id, name) VALUES (?, ?)`, id, name)
	if err != nil {
		return "", fmt.Errorf("create tag failed: %w", err)
	}
	return id, nil
}

// syncTagsText 更新 bookmarks.tags_text 冗余字段。
// 从 bookmark_tags + tags 联表查询，拼成逗号分隔的字符串。
// 参考：docs/cli/tsumu-cli-schema.md "tags_text 同步" 段落。
func syncTagsText(tx *sql.Tx, bookmarkID string) error {
	_, err := tx.Exec(
		`UPDATE bookmarks
		 SET tags_text = (
		     SELECT COALESCE(GROUP_CONCAT(t.name, ','), '')
		     FROM bookmark_tags bt
		     JOIN tags t ON bt.tag_id = t.id
		     WHERE bt.bookmark_id = ? AND bt.deleted_at IS NULL
		 ),
		 updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
		 WHERE id = ?`,
		bookmarkID, bookmarkID,
	)
	if err != nil {
		return fmt.Errorf("sync tags_text failed: %w", err)
	}
	return nil
}
