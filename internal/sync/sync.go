// sync.go 是同步流程的入口。
// 顺序：Pull（远端 → 本地）→ Push（本地 → 远端）→ 调用方更新 last_synced。

package sync

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// SyncMode 定义同步模式。
type SyncMode int

const (
	// SyncIncremental 增量同步（默认）。
	// 仅同步 lastSynced 之后的变更。
	SyncIncremental SyncMode = iota

	// SyncFull 全量同步（--force）。
	// Pull 全量 + Push 全量 + 三表孤儿清理。安全的多设备全量重同步。
	SyncFull

	// SyncOverwrite 本地覆盖远端（--overwrite）。
	// 跳过 Pull，Push 全量 + 三表孤儿清理。危险操作，需确认。
	SyncOverwrite
)

// Result 是同步结果统计。
type Result struct {
	PulledNew     int    // pull 阶段新增的记录数
	PulledUpdated int    // pull 阶段更新的记录数
	PushedNew     int    // push 阶段新增的记录数
	PushedUpdated int    // push 阶段更新的记录数
	Warning       string // 同步后校验警告（计数不一致等）
}

// SyncAll 执行同步。
// mode 决定同步方式：SyncIncremental（增量）、SyncFull（全量双向）、SyncOverwrite（本地覆盖远端）。
// 返回 error 时调用方不应更新 last_synced。
func SyncAll(ctx context.Context, localDB *sql.DB, client *Client, lastSynced string, mode SyncMode, onProgress func(string)) (Result, error) {
	var result Result

	// SyncFull / SyncOverwrite 时清空 lastSynced，触发全量处理
	effectiveLastSynced := lastSynced
	if mode == SyncFull || mode == SyncOverwrite {
		effectiveLastSynced = ""
	}

	// Pull 阶段（SyncOverwrite 跳过）
	if mode != SyncOverwrite {
		if onProgress != nil {
			onProgress("Pulling from remote...")
		}

		pullResult, err := Pull(ctx, localDB, client, effectiveLastSynced)
		if err != nil {
			return result, fmt.Errorf("pull failed: %w", err)
		}
		result.PulledNew = pullResult.New
		result.PulledUpdated = pullResult.Updated

		if onProgress != nil {
			onProgress(fmt.Sprintf("Pulled: %d new, %d updated", pullResult.New, pullResult.Updated))
		}
	}

	// Push 阶段
	if onProgress != nil {
		onProgress("Pushing to remote...")
	}

	forceMode := mode == SyncFull || mode == SyncOverwrite
	pushResult, err := Push(ctx, localDB, client, effectiveLastSynced, forceMode)
	if err != nil {
		return result, fmt.Errorf("push failed: %w", err)
	}
	result.PushedNew = pushResult.New
	result.PushedUpdated = pushResult.Updated

	if onProgress != nil {
		onProgress(fmt.Sprintf("Pushed: %d new, %d updated", pushResult.New, pushResult.Updated))
	}

	// 同步后校验
	result.Warning = verifyCounts(ctx, localDB, client)

	return result, nil
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
