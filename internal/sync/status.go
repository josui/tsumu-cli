// status.go 提供同步状态查询函数。

package sync

import "database/sql"

// PendingCount 返回待同步的书签数量。
// 排除已 soft delete 的记录，确保与 sync --status 和 TUI header 口径一致。
func PendingCount(db *sql.DB, lastSynced string) int {
	var count int
	if lastSynced == "" {
		db.QueryRow("SELECT COUNT(*) FROM bookmarks WHERE deleted_at IS NULL").Scan(&count)
	} else {
		db.QueryRow(
			"SELECT COUNT(*) FROM bookmarks WHERE updated_at > ? AND deleted_at IS NULL",
			lastSynced).Scan(&count)
	}
	return count
}
