// search.go contains runSearch which starts the bubbletea TUI.

package cmd

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/josui/tsumu-cli/internal/ui"
)

// syncStatusText 返回 TUI header 用的 sync 状态文本。
func syncStatusText() string {
	if !Cfg.Sync.IsEnabled() || Cfg.Sync.GetURL() == "" {
		return ""
	}
	if Cfg.Sync.LastSynced == "" {
		return "not synced"
	}
	var pending int
	Store.DB.QueryRow("SELECT COUNT(*) FROM bookmarks WHERE updated_at > ?", Cfg.Sync.LastSynced).Scan(&pending)
	if pending > 0 {
		return fmt.Sprintf("%d pending", pending)
	}
	return "synced ✓"
}

// runSearch 启动搜索 TUI。
func runSearch(query string, favOnly bool, since string, tag string) error {
	// 从 Cfg 提取 AI 配置
	var aiKey, aiModel string
	if Cfg != nil && Cfg.AI.IsConfigured() {
		aiKey = Cfg.AI.GetAPIKey()
		aiModel = Cfg.AI.GetGenModel()
	}

	// 创建 bubbletea Model
	model := ui.NewModel(Store.DB, query, favOnly, since, tag, Cfg.GetPageSize(), syncStatusText(), Cfg.Sync.LastSynced, aiKey, aiModel)

	// tea.NewProgram 创建 TUI 程序
	// tea.WithAltScreen: 使用备用屏幕缓冲区（退出时恢复原终端内容）
	p := tea.NewProgram(model, tea.WithAltScreen())

	// Run 启动事件循环，阻塞直到程序退出（用户按 q 或 Ctrl+C）
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("TUI failed to start: %w", err)
	}

	return nil
}
