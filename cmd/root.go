// cli/cmd/root.go

// Package cmd 定义 tsumu 的 CLI 命令。
// 使用 cobra 框架：rootCmd 是根命令，add / find 是子命令。
// 同时保留 -a / -s flag 作为快捷方式。
package cmd

import (
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/josui/tsumu-cli/config"
	"github.com/josui/tsumu-cli/internal/db"
)

// root flag 变量（快捷方式，兼容旧写法）
var (
	flagAdd    string // -a <url>
	flagSearch string // -s <query>
)

// Store 是全局数据库 Store，由 main.go 注入。
var Store *db.Store

// Cfg 是全局配置，由 main.go 注入。sync 命令需要读写配置。
var Cfg *config.Config

// rootCmd 是 cobra 的根命令。
var rootCmd = &cobra.Command{
	Use:   "tsumu",
	Short: "tsumu — local-first CLI bookmark manager",
	Long: `tsumu — local-first CLI bookmark manager.
Save links fast, find them faster.

Usage:
  tsumu                          list all bookmarks
  tsumu add <url> [note...]      add bookmark
  tsumu find <query>             search bookmarks
  tsumu find -d <query>          search (detailed)

Shortcuts:
  tsumu -a <url> [note...]       add bookmark
  tsumu -s <query>               search bookmarks`,

	SilenceUsage: true,

	RunE: func(cmd *cobra.Command, args []string) error {
		// -a 快捷方式
		if flagAdd != "" {
			note := strings.Join(args, " ")
			return runAdd(flagAdd, note)
		}

		// -s 快捷方式
		if flagSearch != "" {
			return runSearch(flagSearch, false)
		}

		// 无参数：列出全部书签
		return runSearch("", false)
	},
}

// addCmd 是 tsumu add <url> [note...] 子命令。
var addCmd = &cobra.Command{
	Use:   "add <url> [note...]",
	Short: "Add a bookmark",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		url := args[0]
		note := strings.Join(args[1:], " ")
		return runAdd(url, note)
	},
}

// find flag
var findDetailed bool

// findCmd 是 tsumu find <query> 子命令。
var findCmd = &cobra.Command{
	Use:   "find <query>",
	Short: "Search bookmarks",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runSearch(args[0], findDetailed)
	},
}

// Execute 是 CLI 的入口点，由 main.go 调用。
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	// root 快捷 flag
	rootCmd.Flags().StringVarP(&flagAdd, "add", "a", "", "add bookmark: tsumu -a <url>")
	rootCmd.Flags().StringVarP(&flagSearch, "search", "s", "", "search bookmarks: tsumu -s <query>")

	// find 子命令的 flag
	findCmd.Flags().BoolVarP(&findDetailed, "detailed", "d", false, "detailed mode")

	// 注册子命令
	rootCmd.AddCommand(addCmd)
	rootCmd.AddCommand(findCmd)
	rootCmd.AddCommand(syncCmd)
}
