// search.go contains runSearch which starts the bubbletea TUI.

package cmd

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/josui/tsumu-cli/internal/sync"
	"github.com/josui/tsumu-cli/internal/ui"
)

// syncStatusText 返回 TUI header 用的 sync 状态文本。
func syncStatusText() string {
	if !Cfg.Sync.CanSync() {
		return ""
	}
	if Cfg.Sync.LastSynced == "" {
		return "not synced"
	}
	pending := sync.PendingCount(Store.DB, Cfg.Sync.LastSynced)
	if pending > 0 {
		return fmt.Sprintf("%d pending", pending)
	}
	return "synced ✓"
}

// runSearch 启动搜索 TUI。
func runSearch(query string, favOnly bool, since string, tag string) error {
	model := ui.NewModel(Store.DB, Cfg, query, favOnly, since, tag, syncStatusText(), Cfg.Sync.LastSynced)
	p := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("TUI failed to start: %w", err)
	}
	return nil
}
