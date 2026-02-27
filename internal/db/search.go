// cli/internal/db/search.go

// search.go 负责 FTS5 全文搜索查询。
// 始终 LEFT JOIN tags 表，返回完整的书签信息（含标签）。

package db

import (
	"database/sql"
	"fmt"
	"strings"
)

// searchSQL 是 FTS5 搜索查询（含 tags）。
const searchSQL = `
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

// listSQL 列出全部书签（含 tags），按创建时间倒序。
const listSQL = `
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
// since 非空时按创建时间筛选，favOnly 为 true 时只返回收藏。
// 返回值：结果列表、总条数、错误。
func Search(db *sql.DB, query string, limit, offset int, since string, favOnly bool, tag string) ([]Bookmark, int, error) {
	var total int
	var rows *sql.Rows
	var err error

	if query == "" {
		// 无搜索词：列出全部
		var wheres []string
		var args []any
		if since != "" {
			wheres = append(wheres, "b.created_at >= ?")
			args = append(args, since)
		}
		if favOnly {
			wheres = append(wheres, "b.is_favorite = 1")
		}
		if tag != "" {
			wheres = append(wheres, "b.id IN (SELECT bt.bookmark_id FROM bookmark_tags bt JOIN tags t ON bt.tag_id = t.id WHERE t.name = ?)")
			args = append(args, tag)
		}

		whereClause := ""
		if len(wheres) > 0 {
			whereClause = "WHERE " + strings.Join(wheres, " AND ")
		}

		// Count query
		countQ := "SELECT COUNT(*) FROM bookmarks b " + whereClause
		if err := db.QueryRow(countQ, args...).Scan(&total); err != nil {
			return nil, 0, fmt.Errorf("count query failed: %w", err)
		}

		var querySQL string
		if whereClause != "" {
			querySQL = fmt.Sprintf(`
SELECT b.id, b.url, b.title, b.site_name,
       b.click_count, b.is_favorite, b.note, b.description,
       b.source, b.created_at,
       COALESCE(GROUP_CONCAT(t.name, ', '), '') AS tags
FROM bookmarks b
LEFT JOIN bookmark_tags bt ON b.id = bt.bookmark_id
LEFT JOIN tags t ON bt.tag_id = t.id
%s
GROUP BY b.id
ORDER BY b.created_at DESC
LIMIT ? OFFSET ?`, whereClause)
		} else {
			querySQL = listSQL
		}

		queryArgs := append(args, limit, offset)
		rows, err = db.Query(querySQL, queryArgs...)
	} else {
		// FTS5 全文搜索
		var extraConds string
		var extraArgs []any
		if since != "" {
			extraConds += " AND b.created_at >= ?"
			extraArgs = append(extraArgs, since)
		}
		if favOnly {
			extraConds += " AND b.is_favorite = 1"
		}
		if tag != "" {
			extraConds += " AND b.id IN (SELECT bt.bookmark_id FROM bookmark_tags bt JOIN tags t ON bt.tag_id = t.id WHERE t.name = ?)"
			extraArgs = append(extraArgs, tag)
		}

		// Count query
		if extraConds != "" {
			countQ := fmt.Sprintf(`
SELECT COUNT(*)
FROM bookmarks_fts fts
JOIN bookmarks b ON b.rowid = fts.rowid
WHERE bookmarks_fts MATCH ?%s`, extraConds)
			countArgs := append([]any{query}, extraArgs...)
			if err := db.QueryRow(countQ, countArgs...).Scan(&total); err != nil {
				return nil, 0, fmt.Errorf("count query failed: %w", err)
			}
		} else {
			if err := db.QueryRow(countSQL, query).Scan(&total); err != nil {
				return nil, 0, fmt.Errorf("count query failed: %w", err)
			}
		}

		querySQL := searchSQL
		if extraConds != "" {
			querySQL = fmt.Sprintf(`
SELECT b.id, b.url, b.title, b.site_name,
       b.click_count, b.is_favorite, b.note, b.description,
       b.source, b.created_at,
       COALESCE(GROUP_CONCAT(t.name, ', '), '') AS tags
FROM bookmarks_fts fts
JOIN bookmarks b ON b.rowid = fts.rowid
LEFT JOIN bookmark_tags bt ON b.id = bt.bookmark_id
LEFT JOIN tags t ON bt.tag_id = t.id
WHERE bookmarks_fts MATCH ?%s
GROUP BY b.id
ORDER BY rank
LIMIT ? OFFSET ?`, extraConds)
		}

		searchArgs := append([]any{query}, extraArgs...)
		searchArgs = append(searchArgs, limit, offset)
		rows, err = db.Query(querySQL, searchArgs...)
	}
	if err != nil {
		return nil, 0, fmt.Errorf("search query failed: %w", err)
	}
	defer rows.Close()

	var results []Bookmark
	for rows.Next() {
		var bm Bookmark
		err = rows.Scan(
			&bm.ID, &bm.URL, &bm.Title, &bm.SiteName,
			&bm.ClickCount, &bm.IsFavorite, &bm.Note, &bm.Description,
			&bm.Source, &bm.CreatedAt, &bm.Tags,
		)
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

// RandomBookmark returns a random bookmark matching the given filters.
func RandomBookmark(database *sql.DB, since string, favOnly bool, tag string) (*Bookmark, error) {
	var wheres []string
	var args []any
	if since != "" {
		wheres = append(wheres, "b.created_at >= ?")
		args = append(args, since)
	}
	if favOnly {
		wheres = append(wheres, "b.is_favorite = 1")
	}
	if tag != "" {
		wheres = append(wheres, "b.id IN (SELECT bt.bookmark_id FROM bookmark_tags bt JOIN tags t ON bt.tag_id = t.id WHERE t.name = ?)")
		args = append(args, tag)
	}

	whereClause := ""
	if len(wheres) > 0 {
		whereClause = "WHERE " + strings.Join(wheres, " AND ")
	}

	q := fmt.Sprintf(`
SELECT b.id, b.url, b.title, b.site_name,
       b.click_count, b.is_favorite, b.note, b.description,
       b.source, b.created_at,
       COALESCE(GROUP_CONCAT(t.name, ', '), '') AS tags
FROM bookmarks b
LEFT JOIN bookmark_tags bt ON b.id = bt.bookmark_id
LEFT JOIN tags t ON bt.tag_id = t.id
%s
GROUP BY b.id
ORDER BY RANDOM()
LIMIT 1`, whereClause)

	var bm Bookmark
	err := database.QueryRow(q, args...).Scan(
		&bm.ID, &bm.URL, &bm.Title, &bm.SiteName,
		&bm.ClickCount, &bm.IsFavorite, &bm.Note, &bm.Description,
		&bm.Source, &bm.CreatedAt, &bm.Tags,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("random query failed: %w", err)
	}
	return &bm, nil
}
