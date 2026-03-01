package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// ============================================================
// Overlay 按键处理
// ============================================================

// handleOverlayKey 根据当前 overlay 类型分发按键处理
func (m Model) handleOverlayKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.overlay {
	case overlayCommand:
		return m.handleCommandKey(msg)
	case overlayAddForm:
		return m.handleAddFormKey(msg)
	case overlayConfigAI:
		return m.handleConfigAIKey(msg)
	case overlayConfigSync:
		return m.handleConfigSyncKey(msg)
	case overlaySyncStatus:
		return m.handleSyncStatusKey(msg)
	}
	return m, nil
}

// handleCommandKey 处理命令面板中的按键
// 统一搜索/命令栏：Enter=搜索书签，Tab=执行命令，↑/↓=移动光标
// cmdMaxVisible 计算命令列表区域最大可见行数
// 窗口高度的 50% 减去固定行数（标题 2 行 + 搜索框 2 行 + 底部提示 2 行 + 边框 2 行 = 8 行）
func (m Model) cmdMaxVisible() int {
	max := m.height/2 - 8
	if max < 3 {
		max = 3
	}
	return max
}

// cmdEnsureVisible 确保 cmdCursor 在可见范围内，自动调整 cmdScrollOffset
func (m *Model) cmdEnsureVisible() {
	maxVisible := m.cmdMaxVisible()
	if m.cmdCursor < m.cmdScrollOffset {
		m.cmdScrollOffset = m.cmdCursor
	}
	if m.cmdCursor >= m.cmdScrollOffset+maxVisible {
		m.cmdScrollOffset = m.cmdCursor - maxVisible + 1
	}
}

func (m Model) handleCommandKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		// 有输入时清空回全列表，无输入时关闭
		if m.cmdFilter != "" {
			m.cmdFilter = ""
			m.inputPos = 0
			m.cmdFiltered = fuzzyMatch("", commands)
			m.cmdCursor = 0
			m.cmdScrollOffset = 0
			return m, m.resetBlink()
		}
		m.overlay = overlayNone
		return m, nil

	case tea.KeyEnter:
		// Enter: 用输入内容搜索书签
		m.overlay = overlayNone
		m.query = strings.TrimSpace(m.cmdFilter)
		m.cmdFilter = ""
		m.inputPos = 0
		m.cursor = 0
		return m, m.doSearch()

	case tea.KeyTab:
		// Tab: 执行命令列表中当前高亮的命令
		if len(m.cmdFiltered) == 0 {
			return m, nil
		}
		selected := commands[m.cmdFiltered[m.cmdCursor]]
		m.overlay = overlayNone
		m.cmdFilter = ""
		m.inputPos = 0
		return m.executeCommand(selected.name)

	case tea.KeyUp:
		if m.cmdCursor > 0 {
			m.cmdCursor--
			m.cmdEnsureVisible()
		}
		return m, nil

	case tea.KeyDown:
		if m.cmdCursor < len(m.cmdFiltered)-1 {
			m.cmdCursor++
			m.cmdEnsureVisible()
		}
		return m, nil

	default:
		// 按键交给通用文本输入处理（光标移动、删除、字符插入）
		m.input = m.cmdFilter
		result, cmd := m.handleTextInput(msg)
		m2 := result.(Model)
		m2.cmdFilter = m2.input
		m2.input = ""
		m2.cmdFiltered = fuzzyMatch(m2.cmdFilter, commands)
		m2.cmdCursor = 0
		m2.cmdScrollOffset = 0
		return m2, cmd
	}
}

// executeCommand 执行命令面板中选中的命令
func (m Model) executeCommand(name string) (tea.Model, tea.Cmd) {
	switch name {
	case "add":
		return m.openAddForm()
	case "sync":
		return m.startSync(false)
	case "sync force":
		return m.startSync(true)
	case "sync status":
		return m.openSyncStatus()
	case "ai":
		return m.startAIBatch(false)
	case "ai empty":
		return m.startAIBatch(true)
	case "config ai":
		return m.openConfigAI()
	case "config sync":
		return m.openConfigSync()
	case "find":
		// 关闭 overlay，用 cmdFilter 搜索书签（和 Enter 行为一致）
		m.overlay = overlayNone
		m.query = strings.TrimSpace(m.cmdFilter)
		m.cmdFilter = ""
		m.inputPos = 0
		m.cursor = 0
		return m, m.doSearch()
	}
	return m, nil
}

// handleAddFormKey 处理 Add 表单的按键
func (m Model) handleAddFormKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.addSubmitting {
		return m, nil // 提交中忽略按键
	}

	switch msg.Type {
	case tea.KeyEscape:
		// 回到命令面板而不是直接关闭 overlay
		m.overlay = overlayCommand
		m.cmdFilter = ""
		m.inputPos = 0
		m.cmdFiltered = fuzzyMatch("", commands)
		m.cmdCursor = 0
		m.cmdScrollOffset = 0
		return m, m.resetBlink()

	case tea.KeyTab:
		// Tags 字段有 suggestions 时 Tab 补全
		if m.addFocus == 1 && len(m.suggestions) > 0 && m.selectedSug < len(m.suggestions) {
			selected := m.suggestions[m.selectedSug]
			parts := strings.Split(m.addFields[1], ",")
			if len(parts) == 1 {
				parts[0] = selected
			} else {
				parts[len(parts)-1] = " " + selected
			}
			m.addFields[1] = strings.Join(parts, ",") + ", "
			m.inputPos = len([]rune(m.addFields[1]))
			m.suggestions = nil
			m.selectedSug = 0
			return m, m.resetBlink()
		}
		// 切换字段时清空 suggestions
		m.suggestions = nil
		m.selectedSug = 0
		m.addFocus = (m.addFocus + 1) % 3
		m.inputPos = len([]rune(m.addFields[m.addFocus]))
		return m, m.resetBlink()

	case tea.KeyShiftTab:
		m.suggestions = nil
		m.selectedSug = 0
		m.addFocus = (m.addFocus + 2) % 3
		m.inputPos = len([]rune(m.addFields[m.addFocus]))
		return m, m.resetBlink()

	case tea.KeyUp:
		if m.addFocus == 1 && len(m.suggestions) > 0 {
			m.selectedSug--
			if m.selectedSug < 0 {
				m.selectedSug = len(m.suggestions) - 1
			}
		}
		return m, nil

	case tea.KeyDown:
		if m.addFocus == 1 && len(m.suggestions) > 0 {
			m.selectedSug++
			if m.selectedSug >= len(m.suggestions) {
				m.selectedSug = 0
			}
		}
		return m, nil

	case tea.KeyEnter:
		url := strings.TrimSpace(m.addFields[0])
		if url == "" {
			m.message = "URL is required"
			m.isError = true
			return m, nil
		}
		m.addSubmitting = true
		return m, m.doAddBookmark(url, m.addFields[1], m.addFields[2])

	default:
		// 交给通用文本输入处理（光标移动、删除、字符插入）
		m.input = m.addFields[m.addFocus]
		result, cmd := m.handleTextInput(msg)
		m2 := result.(Model)
		m2.addFields[m2.addFocus] = m2.input
		m2.input = ""
		// Tags 字段：计算 autocomplete suggestions
		if m2.addFocus == 1 {
			token := currentTagToken(m2.addFields[1])
			m2.suggestions = computeSuggestions(m2.allTags, token)
			m2.selectedSug = 0
		}
		return m2, cmd
	}
}

// handleConfigAIKey 处理 AI 配置表单的按键
func (m Model) handleConfigAIKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		// 回到命令面板
		m.overlay = overlayCommand
		m.cmdFilter = ""
		m.inputPos = 0
		m.cmdFiltered = fuzzyMatch("", commands)
		m.cmdCursor = 0
		m.cmdScrollOffset = 0
		return m, m.resetBlink()

	case tea.KeyTab:
		m.aiConfigFocus = (m.aiConfigFocus + 1) % 4
		m.inputPos = len([]rune(m.aiConfigFields[m.aiConfigFocus]))
		return m, m.resetBlink()

	case tea.KeyShiftTab:
		m.aiConfigFocus = (m.aiConfigFocus + 3) % 4
		m.inputPos = len([]rune(m.aiConfigFields[m.aiConfigFocus]))
		return m, m.resetBlink()

	case tea.KeyEnter:
		// 保存 AI 配置
		m.cfg.AI.Provider = strings.TrimSpace(m.aiConfigFields[0])
		m.cfg.AI.APIKey = strings.TrimSpace(m.aiConfigFields[1])
		m.cfg.AI.GenModel = strings.TrimSpace(m.aiConfigFields[2])
		m.cfg.AI.Lang = strings.TrimSpace(m.aiConfigFields[3])
		if err := m.cfg.Save(); err != nil {
			m.message = fmt.Sprintf("Save failed: %v", err)
			m.isError = true
		} else {
			m.message = "✓ AI config saved"
			m.isError = false
		}
		m.overlay = overlayNone
		return m, nil

	default:
		// 交给通用文本输入处理
		m.input = m.aiConfigFields[m.aiConfigFocus]
		result, cmd := m.handleTextInput(msg)
		m2 := result.(Model)
		m2.aiConfigFields[m2.aiConfigFocus] = m2.input
		m2.input = ""
		return m2, cmd
	}
}

// handleConfigSyncKey 处理 Sync 配置表单的按键
func (m Model) handleConfigSyncKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		// 回到命令面板
		m.overlay = overlayCommand
		m.cmdFilter = ""
		m.inputPos = 0
		m.cmdFiltered = fuzzyMatch("", commands)
		m.cmdCursor = 0
		m.cmdScrollOffset = 0
		return m, m.resetBlink()

	case tea.KeyTab:
		m.syncConfigFocus = (m.syncConfigFocus + 1) % 4
		if m.syncConfigFocus == 3 {
			m.inputPos = 0 // Enabled toggle 不需要光标位置
		} else {
			m.inputPos = len([]rune(m.syncConfigFields[m.syncConfigFocus]))
		}
		return m, m.resetBlink()

	case tea.KeyShiftTab:
		m.syncConfigFocus = (m.syncConfigFocus + 3) % 4
		if m.syncConfigFocus == 3 {
			m.inputPos = 0
		} else {
			m.inputPos = len([]rune(m.syncConfigFields[m.syncConfigFocus]))
		}
		return m, m.resetBlink()

	case tea.KeyEnter:
		// 保存 Sync 配置
		m.cfg.Sync.URL = strings.TrimSpace(m.syncConfigFields[0])
		m.cfg.Sync.AuthToken = strings.TrimSpace(m.syncConfigFields[1])
		m.cfg.Sync.Interval = strings.TrimSpace(m.syncConfigFields[2])
		m.cfg.Sync.Enabled = m.syncConfigFields[3] == "true"
		if err := m.cfg.Save(); err != nil {
			m.message = fmt.Sprintf("Save failed: %v", err)
			m.isError = true
		} else {
			m.message = "✓ Sync config saved"
			m.isError = false
		}
		m.overlay = overlayNone
		refreshSyncStatus(&m)
		return m, nil

	default:
		// Enabled 字段：只支持 space 切换 true/false
		if m.syncConfigFocus == 3 {
			if msg.Type == tea.KeySpace {
				if m.syncConfigFields[3] == "true" {
					m.syncConfigFields[3] = "false"
				} else {
					m.syncConfigFields[3] = "true"
				}
			}
			return m, nil
		}
		// 其余字段交给通用文本输入处理
		m.input = m.syncConfigFields[m.syncConfigFocus]
		result, cmd := m.handleTextInput(msg)
		m2 := result.(Model)
		m2.syncConfigFields[m2.syncConfigFocus] = m2.input
		m2.input = ""
		return m2, cmd
	}
}

// handleSyncStatusKey 处理 Sync Status 卡片的按键（esc 回命令面板，q 关闭）
func (m Model) handleSyncStatusKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyEscape {
		// 回到命令面板
		m.overlay = overlayCommand
		m.cmdFilter = ""
		m.inputPos = 0
		m.cmdFiltered = fuzzyMatch("", commands)
		m.cmdCursor = 0
		m.cmdScrollOffset = 0
		return m, m.resetBlink()
	} else if msg.String() == "q" {
		m.overlay = overlayNone
	}
	return m, nil
}

// openAddForm 打开添加书签表单
func (m Model) openAddForm() (tea.Model, tea.Cmd) {
	m.overlay = overlayAddForm
	m.addFields = [3]string{"", "", ""}
	m.addFocus = 0
	m.addSubmitting = false
	m.inputPos = 0
	m.suggestions = nil
	m.selectedSug = 0
	return m, tea.Batch(m.resetBlink(), m.doLoadAllTags())
}

// startSync 启动后台同步
func (m Model) startSync(force bool) (tea.Model, tea.Cmd) {
	if !m.cfg.Sync.CanSync() {
		m.message = "Sync not configured. Use /config sync"
		m.isError = true
		return m, nil
	}
	m.bgTaskLabel = "⟳ syncing..."
	return m, m.doSync(force)
}

// openSyncStatus 打开 Sync 状态卡片
func (m Model) openSyncStatus() (tea.Model, tea.Cmd) {
	m.overlay = overlaySyncStatus
	return m, nil
}

// startAIBatch 启动批量 AI 增强（后台执行）
func (m Model) startAIBatch(emptyOnly bool) (tea.Model, tea.Cmd) {
	if !m.cfg.AI.IsConfigured() {
		m.message = "AI not configured. Use /config ai"
		m.isError = true
		return m, nil
	}
	label := "✦ AI enhancing all..."
	if emptyOnly {
		label = "✦ AI enhancing empty..."
	}
	m.bgTaskLabel = label
	return m, m.doAIBatch(emptyOnly)
}

// openConfigAI 打开 AI 配置表单，预填充当前配置
func (m Model) openConfigAI() (tea.Model, tea.Cmd) {
	m.overlay = overlayConfigAI
	m.aiConfigFields = [4]string{
		m.cfg.AI.GetProvider(),
		m.cfg.AI.APIKey, // 显示 config 中的值（非 env），让用户知道实际配置
		m.cfg.AI.GenModel,
		m.cfg.AI.GetLang(),
	}
	m.aiConfigFocus = 0
	m.inputPos = len([]rune(m.aiConfigFields[0]))
	return m, m.resetBlink()
}

// openConfigSync 打开 Sync 配置表单，预填充当前配置
func (m Model) openConfigSync() (tea.Model, tea.Cmd) {
	m.overlay = overlayConfigSync
	m.syncConfigFields = [4]string{
		m.cfg.Sync.URL,
		m.cfg.Sync.AuthToken,
		m.cfg.Sync.Interval,
		fmt.Sprintf("%t", m.cfg.Sync.Enabled),
	}
	m.syncConfigFocus = 0
	m.inputPos = len([]rune(m.syncConfigFields[0]))
	return m, m.resetBlink()
}
