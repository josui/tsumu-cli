// cli/main.go

// tsumu CLI — 本地命令行书签管理工具
// 入口文件。初始化顺序：配置 → 数据库 → 执行命令。
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/josui/tsumu-cli/cmd"
	"github.com/josui/tsumu-cli/config"
	"github.com/josui/tsumu-cli/internal/db"
	"github.com/josui/tsumu-cli/internal/sync"
)

func main() {
	// 1. 加载配置
	cfg := config.Default()
	if err := cfg.EnsureDir(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create data directory: %v\n", err)
		os.Exit(1)
	}
	// 首次运行检测（在 Load 之前，因为 Load 不会报错如果文件不存在）
	firstRun := cfg.IsFirstRun()

	if err := cfg.Load(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// .env 在 Load 之后加载，覆盖 config.toml 中的敏感字段
	if err := cfg.LoadEnv(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to load .env: %v\n", err)
	}

	if firstRun {
		// 写入默认配置（标记为非首次运行）
		if err := cfg.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to save config: %v\n", err)
			os.Exit(1)
		}

		wantSync := cmd.RunOnboarding()
		if wantSync {
			// 先打开本地 DB（sync setup 需要），然后走 sync setup 流程
			store, err := db.OpenStore(cfg.DBPath())
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to initialize database: %v\n", err)
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
				fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
				os.Exit(1)
			}
			if err := cfg.LoadEnv(); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to load .env: %v\n", err)
			}
		}
	}

	// 2. 打开数据库（本地模式）
	store, err := db.OpenStore(cfg.DBPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize database: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	// 2.5 Lazy sync：启用同步且距上次同步超过 interval 时，自动拉取/推送变更。
	// 静默执行，失败不阻塞正常使用（仅输出警告）。
	if cfg.Sync.CanSync() && cfg.Sync.NeedsSync() {
		client := sync.NewClient(cfg.Sync.GetURL(), cfg.Sync.GetAuthToken())
		result, err := sync.SyncAll(context.Background(), store.DB, client, cfg.Sync.LastSynced, cfg.Sync.PullCursor, sync.SyncIncremental, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  ⚠ Auto-sync failed: %v\n", err)
		} else {
			cfg.Sync.LastSynced = sync.NowUTC()
			cfg.Sync.PullCursor = result.NewPullCursor
			cfg.Save()
			pulled := result.PulledNew + result.PulledUpdated
			pushed := result.PushedNew + result.PushedUpdated
			if pulled > 0 || pushed > 0 {
				fmt.Printf("  ⟳ Auto-synced: pulled %d, pushed %d\n", pulled, pushed)
			}
		}
	}

	// 3. 注入 Store 到 cmd 包，然后执行命令
	cmd.Store = store
	cmd.Cfg = cfg
	cmd.Execute()
}
