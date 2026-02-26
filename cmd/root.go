// cli/cmd/root.go

// Package cmd 定义 tsumu 的 CLI 命令。
// 使用 cobra 框架：rootCmd 是根命令，子命令和 flag 挂载在其上。
package cmd

import (
	"database/sql"
	"os"

	"github.com/spf13/cobra"
)

// 命令行 flag 变量
// cobra 会把用户输入的 flag 值绑定到这些变量上
var (
	flagAdd      string // -a <url>: 要添加的 URL
	flagSearch   string // -s <query>: 搜索关键词
	flagDetailed bool   // -d: 详细模式
)

// DB 是全局数据库连接，由 main.go 注入。
// 大写开头 = 导出（exported），其他包可以访问。
var DB *sql.DB

// rootCmd 是 cobra 的根命令。
var rootCmd = &cobra.Command{
	Use:   "tsumu",
	Short: "tsumu — 本地命令行书签管理工具",
	Long: `tsumu（積む）— 本地优先的命令行书签管理工具。
快速存链接，快速找到它。

用法:
  tsumu -a <url>         添加书签
  tsumu -s <query>       搜索书签
  tsumu -s -d <query>    搜索书签（详细模式）`,

	// SilenceUsage: 命令出错时不自动打印 usage（避免干扰错误信息）
	SilenceUsage: true,

	// RunE 比 Run 多一个 error 返回值，cobra 会自动打印错误
	RunE: func(cmd *cobra.Command, args []string) error {
		// -a 模式：添加书签
		if flagAdd != "" {
			return runAdd(flagAdd)
		}

		// -s 模式：搜索
		if flagSearch != "" {
			return runSearch(flagSearch, flagDetailed)
		}

		// 无参数：打印帮助
		return cmd.Help()
	},
}

// Execute 是 CLI 的入口点，由 main.go 调用。
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		// cobra 已经打印了错误信息，这里只需要设置退出码
		os.Exit(1)
	}
}

// init 函数在包被导入时自动执行（Go 的初始化机制）。
// 这里用来注册 flag。
func init() {
	// StringVarP: 绑定字符串 flag，P 后缀支持短写（-a）和长写（--add）
	rootCmd.Flags().StringVarP(&flagAdd, "add", "a", "", "添加书签: tsumu -a <url>")
	rootCmd.Flags().StringVarP(&flagSearch, "search", "s", "", "搜索书签: tsumu -s <query>")
	rootCmd.Flags().BoolVarP(&flagDetailed, "detailed", "d", false, "详细模式（配合 -s 使用）")
}
