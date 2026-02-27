// cli/cmd/sync.go

// sync.go 实现 tsumu sync 子命令。
// 管理 Turso 云端同步的配置和手动触发。

package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/josui/tsumu-cli/internal/db"
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

// runSyncManual 手动触发同步
func runSyncManual() error {
	if !Store.IsSynced() {
		fmt.Println("  Sync not configured. Run: tsumu sync --setup")
		return nil
	}

	rep, err := Store.SyncNow()
	if err != nil {
		return fmt.Errorf("sync failed: %w", err)
	}

	Cfg.Sync.LastSynced = time.Now().UTC().Format(time.RFC3339)
	Cfg.Save()

	fmt.Printf("  Synced (%d frames)\n", rep.FramesSynced)
	return nil
}

// runSyncSetup 交互式配置 Turso 同步
func runSyncSetup() error {
	reader := bufio.NewReader(os.Stdin)

	// 如果已有配置，询问是否使用已有的
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

	// 直接从当前连接读取本地数据到内存（在关闭连接前）
	dbPath := Cfg.DBPath()
	localBookmarks, err := db.ReadAllBookmarksFromDB(Store.DB)
	if err != nil {
		localBookmarks = nil
	}
	var localTags []db.LocalTag
	var localLinks []db.BookmarkTagLink
	if len(localBookmarks) > 0 {
		fmt.Printf("  Found %d local bookmarks.\n", len(localBookmarks))
		localTags, _ = db.ReadAllTagsFromDB(Store.DB)
		localLinks, _ = db.ReadAllBookmarkTagsFromDB(Store.DB)
	}

	// 关闭连接
	Store.Close()

	// 删除旧的本地 db 文件：embedded replica 需要从零创建，
	// 已有的纯本地 db 没有 metadata 文件会导致 sync 失败。
	// 备份已保存，数据会在 merge 阶段导回。
	for _, suffix := range []string{"", "-wal", "-shm", "-info"} {
		os.Remove(dbPath + suffix)
	}

	// 用 embedded replica 重新打开（连接远端）
	fmt.Println("  Connecting to remote...")
	syncOpts := &db.SyncOpts{
		PrimaryURL: url,
		AuthToken:  token,
	}
	newStore, err := db.OpenStoreWithConnector(dbPath, syncOpts)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}

	// 替换全局 Store
	*Store = *newStore

	// 首次 sync — 拉取远端数据
	_, err = Store.Sync()
	if err != nil {
		return fmt.Errorf("initial sync failed: %w", err)
	}

	// 如果有本地数据，合并导入
	if len(localBookmarks) > 0 {
		fmt.Println("  Merging local data...")
		imported, mergeErr := db.MergeFromBackup(Store.DB, localBookmarks, localTags, localLinks)
		if mergeErr != nil {
			return fmt.Errorf("merge failed: %w", mergeErr)
		}

		// 合并后 sync 一次，把数据推到远端
		Store.Sync()

		var remoteCount int
		Store.DB.QueryRow("SELECT COUNT(*) FROM bookmarks").Scan(&remoteCount)
		fmt.Printf("  Merged: %d imported, %d total\n", imported, remoteCount)
	} else {
		var count int
		Store.DB.QueryRow("SELECT COUNT(*) FROM bookmarks").Scan(&count)
		if count > 0 {
			fmt.Printf("  Downloaded %d bookmarks from cloud.\n", count)
		}
	}

	// 保存配置
	Cfg.Sync.URL = url
	Cfg.Sync.AuthToken = token
	Cfg.Sync.Enabled = true
	Cfg.Sync.Interval = "24h"
	Cfg.Sync.LastSynced = time.Now().UTC().Format(time.RFC3339)
	if err := Cfg.Save(); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Println("  Sync enabled.")
	return nil
}

// runSyncStatus 显示同步状态
func runSyncStatus() error {
	if !Cfg.Sync.Enabled || Cfg.Sync.URL == "" {
		fmt.Println("  Status:  disabled")
		return nil
	}

	interval := Cfg.Sync.Interval
	if interval == "" {
		interval = "24h"
	}

	fmt.Printf("  Status:  enabled\n")
	fmt.Printf("  Remote:  %s\n", Cfg.Sync.URL)
	fmt.Printf("  Mode:    lazy (every %s)\n", interval)

	if Cfg.Sync.LastSynced != "" {
		last, err := time.Parse(time.RFC3339, Cfg.Sync.LastSynced)
		if err == nil {
			fmt.Printf("  Last:    %s (%s)\n", formatDuration(time.Since(last)), last.Local().Format("2006-01-02 15:04"))
		}

		// 本地变更检测
		var total, pending int
		Store.DB.QueryRow("SELECT COUNT(*) FROM bookmarks").Scan(&total)
		Store.DB.QueryRow("SELECT COUNT(*) FROM bookmarks WHERE updated_at > ?", Cfg.Sync.LastSynced).Scan(&pending)
		if pending > 0 {
			fmt.Printf("  Data:    %d bookmarks pending sync\n", pending)
		} else {
			fmt.Printf("  Data:    all synced (%d bookmarks)\n", total)
		}
	} else {
		fmt.Printf("  Last:    never\n")
		var total int
		Store.DB.QueryRow("SELECT COUNT(*) FROM bookmarks").Scan(&total)
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
