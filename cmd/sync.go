// cli/cmd/sync.go

// sync.go 实现 tsumu sync 子命令。
// 管理 Turso 云端同步的配置和手动触发。

package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/josui/tsumu-cli/internal/sync"
)

// sync flag
var (
	syncSetup  bool
	syncStatus bool
	syncOff    bool
)

// syncCmd 是 tsumu sync 子命令。
var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Manage cloud sync",
	Long: `Manage Turso cloud sync.

  tsumu sync              manual sync (pull latest changes)
  tsumu sync --setup      configure Turso sync
  tsumu sync --status     show sync status
  tsumu sync --off        disable sync`,

	RunE: func(cmd *cobra.Command, args []string) error {
		if syncSetup {
			return runSyncSetup()
		}
		if syncStatus {
			return runSyncStatus()
		}
		if syncOff {
			return runSyncOff()
		}
		// 无 flag：手动触发 sync
		return runSyncManual()
	},
}

func init() {
	syncCmd.Flags().BoolVar(&syncSetup, "setup", false, "configure Turso sync")
	syncCmd.Flags().BoolVar(&syncStatus, "status", false, "show sync status")
	syncCmd.Flags().BoolVar(&syncOff, "off", false, "disable sync")
}

// runSyncManual 手动触发双向同步。
// 流程：Pull（远端 → 本地）→ Push（本地 → 远端）→ 更新 last_synced。
func runSyncManual() error {
	if !Cfg.Sync.IsEnabled() || Cfg.Sync.GetURL() == "" {
		fmt.Println("  Sync not configured. Run: tsumu sync --setup")
		return nil
	}

	client := sync.NewClient(Cfg.Sync.GetURL(), Cfg.Sync.GetAuthToken())
	ctx := context.Background()

	result := sync.SyncAll(ctx, Store.DB, client, Cfg.Sync.LastSynced, func(msg string) {
		fmt.Printf("\r  ⠋ %s", msg)
	})

	Cfg.Sync.LastSynced = sync.NowUTC()
	Cfg.Save()

	// 清除进度行，显示结果
	fmt.Print("\r")
	pulled := result.PulledNew + result.PulledUpdated
	pushed := result.PushedNew + result.PushedUpdated
	if pulled > 0 || pushed > 0 {
		fmt.Printf("  ✓ Pulled: %d new, %d updated\n", result.PulledNew, result.PulledUpdated)
		fmt.Printf("  ✓ Pushed: %d new, %d updated\n", result.PushedNew, result.PushedUpdated)
	} else {
		fmt.Println("  ✓ Already up to date")
	}

	return nil
}

// runSyncSetup 交互式配置同步（URL/token 输入 → 首次全量同步）
func runSyncSetup() error {
	reader := bufio.NewReader(os.Stdin)

	url := Cfg.Sync.URL
	token := Cfg.Sync.AuthToken

	if url != "" {
		fmt.Printf("  Existing config found: %s\n", url)
		fmt.Print("  Use existing config? [Y/n]: ")
		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "" && answer != "y" && answer != "yes" {
			url = ""
			token = ""
		}
	}

	if url == "" {
		fmt.Print("  Turso database URL: ")
		input, _ := reader.ReadString('\n')
		url = strings.TrimSpace(input)
		if url == "" {
			return fmt.Errorf("URL is required")
		}

		fmt.Print("  Auth token: ")
		input, _ = reader.ReadString('\n')
		token = strings.TrimSpace(input)
		if token == "" {
			return fmt.Errorf("auth token is required")
		}
	}

	Cfg.Sync.URL = url
	Cfg.Sync.AuthToken = token
	Cfg.Sync.Enabled = true
	Cfg.Sync.Interval = "24h"
	if err := Cfg.Save(); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	// 首次同步：全量 pull + push
	fmt.Println("  Syncing...")
	client := sync.NewClient(url, token)
	ctx := context.Background()
	result := sync.SyncAll(ctx, Store.DB, client, "", nil)

	Cfg.Sync.LastSynced = sync.NowUTC()
	Cfg.Save()

	pulled := result.PulledNew + result.PulledUpdated
	pushed := result.PushedNew + result.PushedUpdated
	if pulled > 0 || pushed > 0 {
		fmt.Printf("  ✓ Synced: pulled %d, pushed %d\n", pulled, pushed)
	} else {
		fmt.Println("  ✓ Sync configured")
	}

	return nil
}

// runSyncStatus 显示同步状态
func runSyncStatus() error {
	if !Cfg.Sync.IsEnabled() || Cfg.Sync.GetURL() == "" {
		fmt.Println("  Status:  disabled")
		return nil
	}

	interval := Cfg.Sync.Interval
	if interval == "" {
		interval = "24h"
	}

	fmt.Printf("  Status:  enabled\n")
	fmt.Printf("  Remote:  %s\n", Cfg.Sync.GetURL())
	fmt.Printf("  Mode:    lazy (every %s)\n", interval)

	if Cfg.Sync.LastSynced != "" {
		last, err := time.Parse(time.RFC3339, Cfg.Sync.LastSynced)
		if err == nil {
			fmt.Printf("  Last:    %s (%s)\n", formatDuration(time.Since(last)), last.Local().Format("2006-01-02 15:04"))
		}

		// 本地变更检测（排除已 soft delete 的记录）
		var total, pending int
		Store.DB.QueryRow("SELECT COUNT(*) FROM bookmarks WHERE deleted_at IS NULL").Scan(&total)
		Store.DB.QueryRow("SELECT COUNT(*) FROM bookmarks WHERE updated_at > ? AND deleted_at IS NULL", Cfg.Sync.LastSynced).Scan(&pending)
		if pending > 0 {
			fmt.Printf("  Data:    %d bookmarks pending sync\n", pending)
		} else {
			fmt.Printf("  Data:    all synced (%d bookmarks)\n", total)
		}
	} else {
		fmt.Printf("  Last:    never\n")
		var total int
		Store.DB.QueryRow("SELECT COUNT(*) FROM bookmarks WHERE deleted_at IS NULL").Scan(&total)
		fmt.Printf("  Data:    never synced (%d bookmarks)\n", total)
	}
	return nil
}

// formatDuration 将 Duration 格式化为人类可读的相对时间。
func formatDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		return fmt.Sprintf("%dm ago", m)
	case d < 24*time.Hour:
		h := int(d.Hours())
		return fmt.Sprintf("%dh ago", h)
	default:
		days := int(d.Hours() / 24)
		return fmt.Sprintf("%dd ago", days)
	}
}

// runSyncOff 关闭同步
func runSyncOff() error {
	Cfg.Sync.Enabled = false
	if err := Cfg.Save(); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}
	fmt.Println("  Sync disabled. Local data preserved.")
	fmt.Println("  Restart tsumu to switch to local-only mode.")
	return nil
}

// RunSyncSetupFromOnboarding 是 onboarding 调用的 sync setup 入口。
func RunSyncSetupFromOnboarding() error {
	return runSyncSetup()
}
