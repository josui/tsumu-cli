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
	PulledNew     int // pull 阶段新增的记录数
	PulledUpdated int // pull 阶段更新的记录数
	PushedNew     int // push 阶段新增的记录数
	PushedUpdated int // push 阶段更新的记录数
}

// SyncAll 执行完整的双向同步。
// lastSynced 为空字符串时表示首次同步，拉取/推送全量。
// onProgress 可选回调，用于报告进度（可为 nil）。
func SyncAll(ctx context.Context, localDB *sql.DB, client *Client, lastSynced string, onProgress func(string)) Result {
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

	pushResult := Push(ctx, localDB, client, lastSynced)
	result.PushedNew = pushResult.New
	result.PushedUpdated = pushResult.Updated

	if onProgress != nil {
		onProgress(fmt.Sprintf("Pushed: %d new, %d updated", pushResult.New, pushResult.Updated))
	}

	return result
}

// NowUTC 返回当前 UTC 时间的 RFC3339 格式字符串。
func NowUTC() string {
	return time.Now().UTC().Format(time.RFC3339)
}
