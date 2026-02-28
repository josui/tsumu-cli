// sync.go 是同步流程的入口。
// 顺序：Pull（远端 → 本地）→ Push（本地 → 远端）→ 调用方更新 last_synced。

package sync

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Result 是同步结果统计。
type Result struct {
	PulledNew     int    // pull 阶段新增的记录数
	PulledUpdated int    // pull 阶段更新的记录数
	PushedNew     int    // push 阶段新增的记录数
	PushedUpdated int    // push 阶段更新的记录数
	Warning       string // 同步后校验警告（计数不一致等）
}

// SyncAll 执行完整的双向同步。
// lastSynced 为空字符串时表示首次同步，拉取/推送全量。
// forceUpdate=true 时 Push 阶段忽略 LWW 比较，强制覆盖远端。
// onProgress 可选回调，用于报告进度（可为 nil）。
func SyncAll(ctx context.Context, localDB *sql.DB, client *Client, lastSynced string, forceUpdate bool, onProgress func(string)) Result {
	var result Result

	if onProgress != nil {
		onProgress("Pulling from remote...")
	}

	pullResult := Pull(ctx, localDB, client, lastSynced)
	result.PulledNew = pullResult.New
	result.PulledUpdated = pullResult.Updated

	if onProgress != nil {
		onProgress(fmt.Sprintf("Pulled: %d new, %d updated", pullResult.New, pullResult.Updated))
	}

	if onProgress != nil {
		onProgress("Pushing to remote...")
	}

	pushResult := Push(ctx, localDB, client, lastSynced, forceUpdate)
	result.PushedNew = pushResult.New
	result.PushedUpdated = pushResult.Updated

	if onProgress != nil {
		onProgress(fmt.Sprintf("Pushed: %d new, %d updated", pushResult.New, pushResult.Updated))
	}

	// 同步后校验：比较本地和远端的活跃书签数。
	// 不一致时设置 Warning，由调用方决定是否显示。
	result.Warning = verifyCounts(ctx, localDB, client)

	return result
}

// verifyCounts 比较本地与远端的活跃书签数量。
// 不一致时返回警告信息，一致时返回空字符串。
func verifyCounts(ctx context.Context, localDB *sql.DB, client *Client) string {
	var localCount int
	err := localDB.QueryRow(`SELECT COUNT(*) FROM bookmarks WHERE deleted_at IS NULL`).Scan(&localCount)
	if err != nil {
		return ""
	}

	res, err := client.ExecuteOne(ctx, `SELECT COUNT(*) FROM bookmarks WHERE deleted_at IS NULL`)
	if err != nil || len(res.Rows) == 0 {
		return ""
	}

	remoteCount := 0
	fmt.Sscanf(res.Rows[0][0].Value, "%d", &remoteCount)

	if localCount != remoteCount {
		return fmt.Sprintf("count mismatch: local %d, remote %d — run `tsumu sync --force` to fix", localCount, remoteCount)
	}
	return ""
}

// NowUTC 返回当前 UTC 时间的 RFC3339 格式字符串。
func NowUTC() string {
	return time.Now().UTC().Format(time.RFC3339)
}
