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

	rep, err := Store.Sync()
	if err != nil {
		return fmt.Errorf("sync failed: %w", err)
	}
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

	// 检测本地书签数量
	var localCount int
	err := Store.DB.QueryRow("SELECT COUNT(*) FROM bookmarks").Scan(&localCount)
	if err != nil {
		localCount = 0
	}

	// 如果本地有数据，备份并读取
	dbPath := Cfg.DBPath()
	var backupBookmarks []db.LocalBookmark
	var backupTags []db.LocalTag
	var backupLinks []db.BookmarkTagLink

	if localCount > 0 {
		fmt.Printf("  Found %d local bookmarks.\n", localCount)

		// 先关闭当前数据库连接，才能备份文件
		Store.Close()

		backupPath, backupErr := db.BackupDB(dbPath)
		if backupErr != nil {
			return fmt.Errorf("backup failed: %w", backupErr)
		}
		fmt.Printf("  Backed up to %s\n", backupPath)

		// 从备份读取数据
		backupBookmarks, err = db.ReadAllBookmarks(backupPath)
		if err != nil {
			return fmt.Errorf("read backup bookmarks: %w", err)
		}
		backupTags, err = db.ReadAllTags(backupPath)
		if err != nil {
			return fmt.Errorf("read backup tags: %w", err)
		}
		backupLinks, err = db.ReadAllBookmarkTags(backupPath)
		if err != nil {
			return fmt.Errorf("read backup links: %w", err)
		}
	} else {
		// 无本地数据时也需要关闭连接，因为要用 embedded replica 重新打开
		Store.Close()
	}

	// 用 embedded replica 重新打开（连接远端）
	fmt.Println("  Connecting to remote...")
	syncOpts := &db.SyncOpts{
		PrimaryURL: url,
		AuthToken:  token,
		Interval:   60 * time.Second,
	}
	newStore, err := db.OpenStore(dbPath, syncOpts)
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

	// 如果有备份数据，合并导入
	if len(backupBookmarks) > 0 {
		fmt.Println("  Merging local data...")
		imported, mergeErr := db.MergeFromBackup(Store.DB, backupBookmarks, backupTags, backupLinks)
		if mergeErr != nil {
			return fmt.Errorf("merge failed: %w", mergeErr)
		}

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

	fmt.Printf("  Status:  enabled\n")
	fmt.Printf("  Remote:  %s\n", Cfg.Sync.URL)
	if Store.IsSynced() {
		fmt.Printf("  Mode:    auto (every 60s)\n")
	} else {
		fmt.Printf("  Mode:    configured but not active (restart to activate)\n")
	}
	return nil
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
