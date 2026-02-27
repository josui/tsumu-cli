// cli/main.go

// tsumu CLI — 本地命令行书签管理工具
// 入口文件。初始化顺序：配置 → 数据库 → 执行命令。
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/josui/tsumu-cli/cmd"
	"github.com/josui/tsumu-cli/config"
	"github.com/josui/tsumu-cli/internal/db"
)

func main() {
	// 1. 加载配置
	cfg := config.Default()
	if err := cfg.EnsureDir(); err != nil {
		fmt.Fprintf(os.Stderr, "创建数据目录失败: %v\n", err)
		os.Exit(1)
	}
	// 首次运行检测（在 Load 之前，因为 Load 不会报错如果文件不存在）
	firstRun := cfg.IsFirstRun()

	if err := cfg.Load(); err != nil {
		fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err)
		os.Exit(1)
	}

	if firstRun {
		// 写入默认配置（标记为非首次运行）
		if err := cfg.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "保存配置失败: %v\n", err)
			os.Exit(1)
		}

		wantSync := cmd.RunOnboarding()
		if wantSync {
			// 先打开本地 DB（sync setup 需要），然后走 sync setup 流程
			store, err := db.OpenStore(cfg.DBPath(), nil)
			if err != nil {
				fmt.Fprintf(os.Stderr, "数据库初始化失败: %v\n", err)
				os.Exit(1)
			}
			cmd.Store = store
			cmd.Cfg = cfg
			if err := cmd.RunSyncSetupFromOnboarding(); err != nil {
				fmt.Fprintf(os.Stderr, "Sync setup failed: %v\n", err)
				fmt.Println("  Continuing in local mode. Run tsumu sync --setup to try again.")
			}
			store.Close()
			// setup 完成后重新加载配置（可能已切换到 sync 模式）
			if err := cfg.Load(); err != nil {
				fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err)
				os.Exit(1)
			}
		}
	}

	// 2. 打开数据库（自动执行 migration）
	// 根据 config 中的 sync 配置决定使用本地模式还是云端同步模式
	var syncOpts *db.SyncOpts
	if cfg.Sync.Enabled && cfg.Sync.URL != "" {
		syncOpts = &db.SyncOpts{
			PrimaryURL: cfg.Sync.URL,
			AuthToken:  cfg.Sync.AuthToken,
			Interval:   0,
		}
	}

	store, err := db.OpenStore(cfg.DBPath(), syncOpts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "数据库初始化失败: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	// 3. 懒同步：启动时检查 interval，需要时后台同步
	if store.IsSynced() && cfg.Sync.NeedsSync() {
		go func() {
			if _, err := store.Sync(); err != nil {
				return
			}
			cfg.Sync.LastSynced = time.Now().UTC().Format(time.RFC3339)
			cfg.Save()
		}()
	}

	// 4. 注入 Store 到 cmd 包，然后执行命令
	cmd.Store = store
	cmd.Cfg = cfg
	cmd.Execute()
}
