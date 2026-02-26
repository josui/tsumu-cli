// cli/internal/db/tags.go

// tags.go 负责 tags 和 bookmark_tags 表的操作。
// 包括创建标签、关联书签、同步 tags_text 冗余字段。

package db

import (
	"database/sql"
	"fmt"
	"strings"
)

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

		// 关联书签和标签（INSERT OR IGNORE 避免重复关联报错）
		_, err = tx.Exec(
			`INSERT OR IGNORE INTO bookmark_tags (bookmark_id, tag_id) VALUES (?, ?)`,
			bookmarkID, tagID,
		)
		if err != nil {
			return fmt.Errorf("link tag to bookmark failed: %w", err)
		}
	}

	// 同步 tags_text 冗余字段
	if err := syncTagsText(tx, bookmarkID); err != nil {
		return err
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
		     WHERE bt.bookmark_id = ?
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
