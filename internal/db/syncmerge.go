// cli/internal/db/syncmerge.go

// syncmerge.go 处理启用同步时的数据合并。
// 当本地已有数据，连接远端后远端数据会覆盖本地，
// 需要把本地独有的书签重新导入。

package db

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"time"
)

// BackupDB 备份数据库文件，返回备份路径。
func BackupDB(dbPath string) (string, error) {
	backupPath := dbPath + ".backup." + time.Now().Format("20060102")

	src, err := os.Open(dbPath)
	if err != nil {
		return "", fmt.Errorf("open source db: %w", err)
	}
	defer src.Close()

	dst, err := os.Create(backupPath)
	if err != nil {
		return "", fmt.Errorf("create backup: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return "", fmt.Errorf("copy db: %w", err)
	}

	return backupPath, nil
}

// LocalBookmark 是从备份中读取的简化书签数据
type LocalBookmark struct {
	ID          string
	URL         string
	Title       string
	Description string
	Note        string
	SiteName    string
	TagsText    string
	ClickCount  int
	IsFavorite  int
	Source      string
	CreatedAt   string
	UpdatedAt   string
}

// ReadAllBookmarks 从指定数据库文件读取所有书签
func ReadAllBookmarks(dbPath string) ([]LocalBookmark, error) {
	backup, err := sql.Open("libsql", "file:"+dbPath)
	if err != nil {
		return nil, fmt.Errorf("open backup db: %w", err)
	}
	defer backup.Close()

	rows, err := backup.Query(`
		SELECT id, url, title, description, note, site_name, tags_text,
		       click_count, is_favorite, source, created_at, updated_at
		FROM bookmarks
	`)
	if err != nil {
		return nil, fmt.Errorf("query bookmarks: %w", err)
	}
	defer rows.Close()

	var bookmarks []LocalBookmark
	for rows.Next() {
		var bm LocalBookmark
		if err := rows.Scan(
			&bm.ID, &bm.URL, &bm.Title, &bm.Description, &bm.Note,
			&bm.SiteName, &bm.TagsText, &bm.ClickCount, &bm.IsFavorite,
			&bm.Source, &bm.CreatedAt, &bm.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan bookmark: %w", err)
		}
		bookmarks = append(bookmarks, bm)
	}
	return bookmarks, rows.Err()
}

// LocalTag 是从备份中读取的标签数据
type LocalTag struct {
	ID   string
	Name string
}

// ReadAllTags 从指定数据库文件读取所有标签
func ReadAllTags(dbPath string) ([]LocalTag, error) {
	backup, err := sql.Open("libsql", "file:"+dbPath)
	if err != nil {
		return nil, fmt.Errorf("open backup db: %w", err)
	}
	defer backup.Close()

	rows, err := backup.Query(`SELECT id, name FROM tags`)
	if err != nil {
		return nil, fmt.Errorf("query tags: %w", err)
	}
	defer rows.Close()

	var tags []LocalTag
	for rows.Next() {
		var t LocalTag
		if err := rows.Scan(&t.ID, &t.Name); err != nil {
			return nil, fmt.Errorf("scan tag: %w", err)
		}
		tags = append(tags, t)
	}
	return tags, rows.Err()
}

// BookmarkTagLink 是书签-标签关联
type BookmarkTagLink struct {
	BookmarkID string
	TagID      string
}

// ReadAllBookmarkTags 从指定数据库文件读取所有书签-标签关联
func ReadAllBookmarkTags(dbPath string) ([]BookmarkTagLink, error) {
	backup, err := sql.Open("libsql", "file:"+dbPath)
	if err != nil {
		return nil, fmt.Errorf("open backup db: %w", err)
	}
	defer backup.Close()

	rows, err := backup.Query(`SELECT bookmark_id, tag_id FROM bookmark_tags`)
	if err != nil {
		return nil, fmt.Errorf("query bookmark_tags: %w", err)
	}
	defer rows.Close()

	var links []BookmarkTagLink
	for rows.Next() {
		var l BookmarkTagLink
		if err := rows.Scan(&l.BookmarkID, &l.TagID); err != nil {
			return nil, fmt.Errorf("scan bookmark_tag: %w", err)
		}
		links = append(links, l)
	}
	return links, rows.Err()
}

// ReadAllBookmarksFromDB 从已打开的 *sql.DB 读取所有书签（内存读取，不走文件）
func ReadAllBookmarksFromDB(database *sql.DB) ([]LocalBookmark, error) {
	rows, err := database.Query(`
		SELECT id, url, title, description, note, site_name, tags_text,
		       click_count, is_favorite, source, created_at, updated_at
		FROM bookmarks
	`)
	if err != nil {
		return nil, fmt.Errorf("query bookmarks: %w", err)
	}
	defer rows.Close()

	var bookmarks []LocalBookmark
	for rows.Next() {
		var bm LocalBookmark
		if err := rows.Scan(
			&bm.ID, &bm.URL, &bm.Title, &bm.Description, &bm.Note,
			&bm.SiteName, &bm.TagsText, &bm.ClickCount, &bm.IsFavorite,
			&bm.Source, &bm.CreatedAt, &bm.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan bookmark: %w", err)
		}
		bookmarks = append(bookmarks, bm)
	}
	return bookmarks, rows.Err()
}

// ReadAllTagsFromDB 从已打开的 *sql.DB 读取所有标签
func ReadAllTagsFromDB(database *sql.DB) ([]LocalTag, error) {
	rows, err := database.Query(`SELECT id, name FROM tags`)
	if err != nil {
		return nil, fmt.Errorf("query tags: %w", err)
	}
	defer rows.Close()

	var tags []LocalTag
	for rows.Next() {
		var t LocalTag
		if err := rows.Scan(&t.ID, &t.Name); err != nil {
			return nil, fmt.Errorf("scan tag: %w", err)
		}
		tags = append(tags, t)
	}
	return tags, rows.Err()
}

// ReadAllBookmarkTagsFromDB 从已打开的 *sql.DB 读取所有书签-标签关联
func ReadAllBookmarkTagsFromDB(database *sql.DB) ([]BookmarkTagLink, error) {
	rows, err := database.Query(`SELECT bookmark_id, tag_id FROM bookmark_tags`)
	if err != nil {
		return nil, fmt.Errorf("query bookmark_tags: %w", err)
	}
	defer rows.Close()

	var links []BookmarkTagLink
	for rows.Next() {
		var l BookmarkTagLink
		if err := rows.Scan(&l.BookmarkID, &l.TagID); err != nil {
			return nil, fmt.Errorf("scan bookmark_tag: %w", err)
		}
		links = append(links, l)
	}
	return links, rows.Err()
}

// MergeFromBackup 将备份中远端没有的数据导入到当前数据库。
// 使用 INSERT OR IGNORE 避免 URL 冲突（远端已有的跳过）。
// 返回导入的书签数。
func MergeFromBackup(targetDB *sql.DB, bookmarks []LocalBookmark, tags []LocalTag, links []BookmarkTagLink) (int, error) {
	// 导入标签（按 name 去重）
	for _, t := range tags {
		_, err := targetDB.Exec(
			`INSERT OR IGNORE INTO tags (id, name) VALUES (?, ?)`,
			t.ID, t.Name,
		)
		if err != nil {
			return 0, fmt.Errorf("import tag %s: %w", t.Name, err)
		}
	}

	// 导入书签（按 URL 去重）
	imported := 0
	for _, bm := range bookmarks {
		result, err := targetDB.Exec(
			`INSERT OR IGNORE INTO bookmarks
			 (id, url, title, description, note, site_name, tags_text,
			  click_count, is_favorite, source, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			bm.ID, bm.URL, bm.Title, bm.Description, bm.Note,
			bm.SiteName, bm.TagsText, bm.ClickCount, bm.IsFavorite,
			bm.Source, bm.CreatedAt, bm.UpdatedAt,
		)
		if err != nil {
			return imported, fmt.Errorf("import bookmark %s: %w", bm.URL, err)
		}
		affected, _ := result.RowsAffected()
		if affected > 0 {
			imported++
		}
	}

	// 导入书签-标签关联
	for _, l := range links {
		_, err := targetDB.Exec(
			`INSERT OR IGNORE INTO bookmark_tags (bookmark_id, tag_id) VALUES (?, ?)`,
			l.BookmarkID, l.TagID,
		)
		if err != nil {
			// 书签或标签可能不存在（被去重跳过），忽略外键错误
			continue
		}
	}

	return imported, nil
}
