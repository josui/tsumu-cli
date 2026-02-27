// cli/cmd/search.go

// search.go 实现 tsumu -s <query> 搜索命令。
// 查询数据库后启动 bubbletea TUI 展示交互式搜索结果。

package cmd

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/josui/tsumu-cli/internal/ui"
)

// runSearch 启动搜索 TUI。
func runSearch(query string, detailed bool) error {
	// 创建 bubbletea Model
	model := ui.NewModel(Store.DB, query, detailed)

	// tea.NewProgram 创建 TUI 程序
	// tea.WithAltScreen: 使用备用屏幕缓冲区（退出时恢复原终端内容）
	p := tea.NewProgram(model, tea.WithAltScreen())

	// Run 启动事件循环，阻塞直到程序退出（用户按 q 或 Ctrl+C）
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("TUI failed to start: %w", err)
	}

	return nil
}
