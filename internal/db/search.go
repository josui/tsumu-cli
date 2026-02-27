// cli/internal/db/search.go

// search.go 负责 FTS5 全文搜索查询。
// 支持默认模式（快速）和详细模式（含 tags、完整信息）。

package db

import (
	"database/sql"
	"fmt"
)

// searchDefaultSQL 是默认搜索查询。
// JOIN bookmarks_fts 做全文匹配，按 FTS5 的 rank 排序（相关度）。
const searchDefaultSQL = `
SELECT b.id, b.url, b.title, b.site_name,
       b.click_count, b.is_favorite, b.note, b.description,
       b.source, b.created_at
FROM bookmarks_fts fts
JOIN bookmarks b ON b.rowid = fts.rowid
WHERE bookmarks_fts MATCH ?
ORDER BY rank
LIMIT ? OFFSET ?
`

// searchDetailedSQL 是详细搜索查询。
// 额外 LEFT JOIN tags 表获取标签名，用 GROUP_CONCAT 拼成字符串。
const searchDetailedSQL = `
SELECT b.id, b.url, b.title, b.site_name,
       b.click_count, b.is_favorite, b.note, b.description,
       b.source, b.created_at,
       COALESCE(GROUP_CONCAT(t.name, ', '), '') AS tags
FROM bookmarks_fts fts
JOIN bookmarks b ON b.rowid = fts.rowid
LEFT JOIN bookmark_tags bt ON b.id = bt.bookmark_id
LEFT JOIN tags t ON bt.tag_id = t.id
WHERE bookmarks_fts MATCH ?
GROUP BY b.id
ORDER BY rank
LIMIT ? OFFSET ?
`

// countSQL 统计搜索结果总数（用于分页）。
const countSQL = `
SELECT COUNT(*)
FROM bookmarks_fts
WHERE bookmarks_fts MATCH ?
`

// listDefaultSQL 列出全部书签（无搜索词时使用），按创建时间倒序。
const listDefaultSQL = `
SELECT id, url, title, site_name,
       click_count, is_favorite, note, description,
       source, created_at
FROM bookmarks
ORDER BY created_at DESC
LIMIT ? OFFSET ?
`

// listDetailedSQL 列出全部书签（详细模式），额外返回 tags。
const listDetailedSQL = `
SELECT b.id, b.url, b.title, b.site_name,
       b.click_count, b.is_favorite, b.note, b.description,
       b.source, b.created_at,
       COALESCE(GROUP_CONCAT(t.name, ', '), '') AS tags
FROM bookmarks b
LEFT JOIN bookmark_tags bt ON b.id = bt.bookmark_id
LEFT JOIN tags t ON bt.tag_id = t.id
GROUP BY b.id
ORDER BY b.created_at DESC
LIMIT ? OFFSET ?
`

// Search 执行搜索或列出全部书签。
// query 为空时列出全部，否则使用 FTS5 全文搜索。
// detailed=true 时额外返回 tags 信息。
// 返回值：结果列表、总条数、错误。
func Search(db *sql.DB, query string, detailed bool, limit, offset int) ([]Bookmark, int, error) {
	var total int
	var rows *sql.Rows
	var err error

	if query == "" {
		// 无搜索词：列出全部
		if err := db.QueryRow(`SELECT COUNT(*) FROM bookmarks`).Scan(&total); err != nil {
			return nil, 0, fmt.Errorf("count query failed: %w", err)
		}
		querySQL := listDefaultSQL
		if detailed {
			querySQL = listDetailedSQL
		}
		rows, err = db.Query(querySQL, limit, offset)
	} else {
		// FTS5 全文搜索
		if err := db.QueryRow(countSQL, query).Scan(&total); err != nil {
			return nil, 0, fmt.Errorf("count query failed: %w", err)
		}
		querySQL := searchDefaultSQL
		if detailed {
			querySQL = searchDetailedSQL
		}
		rows, err = db.Query(querySQL, query, limit, offset)
	}
	if err != nil {
		return nil, 0, fmt.Errorf("search query failed: %w", err)
	}
	defer rows.Close()

	var results []Bookmark
	for rows.Next() {
		var bm Bookmark
		if detailed {
			err = rows.Scan(
				&bm.ID, &bm.URL, &bm.Title, &bm.SiteName,
				&bm.ClickCount, &bm.IsFavorite, &bm.Note, &bm.Description,
				&bm.Source, &bm.CreatedAt, &bm.Tags,
			)
		} else {
			err = rows.Scan(
				&bm.ID, &bm.URL, &bm.Title, &bm.SiteName,
				&bm.ClickCount, &bm.IsFavorite, &bm.Note, &bm.Description,
				&bm.Source, &bm.CreatedAt,
			)
		}
		if err != nil {
			return nil, 0, fmt.Errorf("scan row failed: %w", err)
		}
		results = append(results, bm)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("rows iteration failed: %w", err)
	}

	return results, total, nil
}
