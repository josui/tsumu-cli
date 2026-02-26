// cli/main.go

// tsumu CLI — 本地命令行书签管理工具
// 入口文件。初始化顺序：配置 → 数据库 → 执行命令。
package main

import (
	"fmt"
	"os"

	"github.com/user/tsumu-cli/cmd"
	"github.com/user/tsumu-cli/config"
	"github.com/user/tsumu-cli/internal/db"
)

func main() {
	// 1. 加载配置
	cfg := config.Default()
	if err := cfg.EnsureDir(); err != nil {
		fmt.Fprintf(os.Stderr, "创建数据目录失败: %v\n", err)
		os.Exit(1)
	}
	if err := cfg.Load(); err != nil {
		fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err)
		os.Exit(1)
	}

	// 2. 打开数据库（自动执行 migration）
	database, err := db.Open(cfg.DBPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "数据库初始化失败: %v\n", err)
		os.Exit(1)
	}
	defer database.Close()

	// 3. 注入数据库连接到 cmd 包，然后执行命令
	cmd.DB = database
	cmd.Execute()
}
