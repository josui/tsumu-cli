// cli/internal/db/bookmarks.go

// bookmarks.go 负责 bookmarks 表的 CRUD 操作。

package db

import (
	"crypto/rand"
	"database/sql"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
)

// Bookmark 对应 bookmarks 表的一行数据。
type Bookmark struct {
	ID          string
	URL         string
	Title       string
	Description string
	Note        string
	SiteName    string
	TagsText    string // 逗号分隔的标签文本（冗余字段，给 FTS 用）
	ClickCount  int
	IsFavorite  bool
	Source      string
	CreatedAt   string
	UpdatedAt   string

	// Tags 是 JOIN 查询时填充的标签列表（不存在 bookmarks 表中）
	Tags string
}

// newULID 生成一个新的 ULID。
// ULID 是有序的唯一 ID，比 UUID 更适合按时间排序。
func newULID() string {
	return ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
}

// CreateBookmark 创建或更新书签（URL 相同时覆盖 metadata/note），返回 Bookmark。
func CreateBookmark(db *sql.DB, url, title, description, siteName, note string) (*Bookmark, error) {
	// 先查是否已存在
	var existingID string
	err := db.QueryRow(`SELECT id FROM bookmarks WHERE url = ?`, url).Scan(&existingID)
	if err == nil {
		// 已存在，更新 metadata/note 并刷新时间（排到最上面）
		_, err := db.Exec(
			`UPDATE bookmarks
			 SET title = ?, description = ?, site_name = ?, note = ?,
			     created_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now'),
			     updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
			 WHERE id = ?`,
			title, description, siteName, note, existingID,
		)
		if err != nil {
			return nil, fmt.Errorf("update bookmark failed: %w", err)
		}
		return GetBookmarkByID(db, existingID)
	}

	// 新增
	id := newULID()
	_, err = db.Exec(
		`INSERT INTO bookmarks (id, url, title, description, site_name, note, source)
		 VALUES (?, ?, ?, ?, ?, ?, 'cli')`,
		id, url, title, description, siteName, note,
	)
	if err != nil {
		return nil, fmt.Errorf("insert bookmark failed: %w", err)
	}
	return GetBookmarkByID(db, id)
}

// GetBookmarkByID 根据 ID 查询单条书签。不存在时返回 (nil, nil)。
func GetBookmarkByID(db *sql.DB, id string) (*Bookmark, error) {
	bm := &Bookmark{}
	err := db.QueryRow(
		`SELECT id, url, title, description, note, site_name, tags_text,
		        click_count, is_favorite, source, created_at, updated_at
		 FROM bookmarks WHERE id = ?`, id,
	).Scan(
		&bm.ID, &bm.URL, &bm.Title, &bm.Description, &bm.Note,
		&bm.SiteName, &bm.TagsText, &bm.ClickCount, &bm.IsFavorite,
		&bm.Source, &bm.CreatedAt, &bm.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		// sql.ErrNoRows 表示查询无结果，这不是真正的错误
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get bookmark failed: %w", err)
	}
	return bm, nil
}

// IncrementClickCount 将书签的点击次数 +1，返回更新后的次数。
func IncrementClickCount(db *sql.DB, id string) (int, error) {
	// 注意：go-libsql 不支持 RETURNING 子句，所以先 UPDATE 再 SELECT。
	_, err := db.Exec(
		`UPDATE bookmarks
		 SET click_count = click_count + 1,
		     updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
		 WHERE id = ?`, id,
	)
	if err != nil {
		return 0, fmt.Errorf("increment click_count failed: %w", err)
	}

	var count int
	err = db.QueryRow(`SELECT click_count FROM bookmarks WHERE id = ?`, id).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("read click_count failed: %w", err)
	}
	return count, nil
}

// ToggleFavorite 切换书签的收藏状态，返回切换后是否收藏。
func ToggleFavorite(db *sql.DB, id string) (bool, error) {
	// 先 UPDATE 再 SELECT（避免 RETURNING 兼容性问题）
	_, err := db.Exec(
		`UPDATE bookmarks
		 SET is_favorite = CASE WHEN is_favorite = 1 THEN 0 ELSE 1 END,
		     updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
		 WHERE id = ?`, id,
	)
	if err != nil {
		return false, fmt.Errorf("toggle favorite failed: %w", err)
	}

	var isFav bool
	err = db.QueryRow(`SELECT is_favorite FROM bookmarks WHERE id = ?`, id).Scan(&isFav)
	if err != nil {
		return false, fmt.Errorf("read is_favorite failed: %w", err)
	}
	return isFav, nil
}

// UpdateNote updates a bookmark's note text.
func UpdateNote(db *sql.DB, id string, note string) error {
	result, err := db.Exec(
		`UPDATE bookmarks
		 SET note = ?,
		     updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
		 WHERE id = ?`, note, id,
	)
	if err != nil {
		return fmt.Errorf("update note failed: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check affected rows failed: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("bookmark not found: %s", id)
	}
	return nil
}

// DeleteBookmark 删除指定书签。bookmark_tags 通过 ON DELETE CASCADE 自动清理。
func DeleteBookmark(db *sql.DB, id string) error {
	result, err := db.Exec(`DELETE FROM bookmarks WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete bookmark failed: %w", err)
	}

	// RowsAffected 返回受影响的行数，0 表示没有匹配的记录
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check affected rows failed: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("bookmark not found: %s", id)
	}
	return nil
}
