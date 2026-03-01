package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// ============================================================
// 按键处理
// ============================================================

// handleKey 根据当前模式分发按键处理
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Ctrl+C 始终退出
	if key == "ctrl+c" {
		return m, tea.Quit
	}

	switch m.mode {
	case modeConfirm:
		return m.handleConfirmKey(key)
	case modeTag:
		return m.handleTagKey(msg)
	case modeNote:
		return m.handleNoteKey(msg)
	default:
		return m.handleNormalKey(key)
	}
}

// handleNormalKey 处理普通模式的按键
func (m Model) handleNormalKey(key string) (tea.Model, tea.Cmd) {
	switch key {

	// 退出
	case keyQuit:
		return m, tea.Quit

	// esc：有 query 时清空 query 刷新全列表，无 query 时清除消息
	case keyEsc:
		if m.query != "" {
			m.query = ""
			m.cursor = 0
			return m, m.doSearch()
		}
		m.message = ""
		return m, nil

	// 光标下移
	case keyDown, "down":
		if m.cursor < len(m.results)-1 {
			m.cursor++
			m.message = ""
		}
		return m, nil

	// 光标上移
	case keyUp, "up":
		if m.cursor > 0 {
			m.cursor--
			m.message = ""
		}
		return m, nil

	// 打开选中项
	case keyEnter:
		if bm := m.focusedBookmark(); bm != nil {
			return m, m.doOpen()
		}
		return m, nil

	// 删除选中项（进入确认模式）
	case keyDel:
		if bm := m.focusedBookmark(); bm != nil {
			m.mode = modeConfirm
			m.message = fmt.Sprintf("Delete %s? [y/any key to cancel]", bm.Title)
			m.isError = false
		}
		return m, nil

	// 收藏/取消收藏选中项
	case keyFav:
		if bm := m.focusedBookmark(); bm != nil {
			return m, m.doToggleFavorite()
		}
		return m, nil

	// 进入标签输入模式（预填充已有 tag + 异步加载全部 tag 用于 autocomplete）
	case keyTag:
		if bm := m.focusedBookmark(); bm != nil {
			m.mode = modeTag
			m.input = bm.Tags // 预加载已有 tag（逗号分隔）
			if m.input != "" {
				m.input += ", "
			}
			m.inputPos = len([]rune(m.input))
			m.message = ""
			m.suggestions = nil
			m.selectedSug = 0
			return m, tea.Batch(m.resetBlink(), m.doLoadAllTags())
		}
		return m, nil

	// 进入 note 编辑模式（内联）
	case keyNote:
		if bm := m.focusedBookmark(); bm != nil {
			m.mode = modeNote
			m.input = bm.Note // 预填充已有 note
			m.inputPos = len([]rune(bm.Note))
			m.message = ""
			return m, m.resetBlink()
		}
		return m, nil

	// 用外部编辑器编辑 note
	case keyNoteEditor:
		if bm := m.focusedBookmark(); bm != nil {
			return m, m.doEditNoteExternal(bm.ID, bm.Note)
		}
		return m, nil

	// 复制选中项 URL 到剪贴板
	case keyCopy:
		if bm := m.focusedBookmark(); bm != nil {
			return m, m.doCopyURL()
		}
		return m, nil

	// 重新抓取选中项元数据 + AI note
	case keyRefetch:
		if bm := m.focusedBookmark(); bm != nil {
			m.message = "⟳ Refetching..."
			m.isError = false
			return m, m.doRefetch()
		}
		return m, nil

	// 标记搜索结果不相关（toggle）
	case keyIrrelevant:
		if m.query != "" && m.focusedBookmark() != nil {
			bm := m.focusedBookmark()
			if m.irrelevantSet == nil {
				m.irrelevantSet = make(map[string]bool)
			}
			m.irrelevantSet[bm.ID] = !m.irrelevantSet[bm.ID]
			return m, m.doToggleIrrelevant()
		}
		return m, nil

	// 打开命令面板
	case keyCommand:
		m.overlay = overlayCommand
		m.cmdFilter = ""
		m.inputPos = 0
		m.cmdFiltered = fuzzyMatch("", commands)
		m.cmdCursor = 0
		m.cmdScrollOffset = 0
		return m, m.resetBlink()

	// AI query expansion：query 非空且 AI 已配置时触发
	case keyAIExpand:
		if m.query != "" && m.cfg.AI.IsConfigured() && !m.aiExpanding {
			m.aiExpanding = true
			m.message = "AI expanding..."
			m.isError = false
			return m, m.doAIExpand()
		}
		return m, nil
	}

	return m, nil
}

// handleConfirmKey 处理删除确认模式的按键
func (m Model) handleConfirmKey(key string) (tea.Model, tea.Cmd) {
	m.mode = modeNormal
	if key == "y" {
		return m, m.doDelete()
	}
	m.message = "Delete cancelled"
	m.isError = false
	return m, nil
}

// handleTagKey 处理标签输入模式的按键（含 autocomplete 逻辑）
func (m Model) handleTagKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		m.mode = modeNormal
		m.input = ""
		m.inputPos = 0
		m.message = ""
		m.suggestions = nil
		m.selectedSug = 0
		return m, nil

	case tea.KeyEnter:
		m.mode = modeNormal
		tagStr := strings.TrimSpace(m.input)
		m.input = ""
		m.inputPos = 0
		m.suggestions = nil
		m.selectedSug = 0

		if tagStr == "" {
			m.message = "Tags cannot be empty"
			m.isError = true
			return m, nil
		}

		var tags []string
		for _, t := range strings.Split(tagStr, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				tags = append(tags, t)
			}
		}
		if len(tags) == 0 {
			m.message = "Tags cannot be empty"
			m.isError = true
			return m, nil
		}

		return m, m.doSetTags(tags)

	case tea.KeyTab:
		// Accept selected suggestion
		if len(m.suggestions) > 0 && m.selectedSug < len(m.suggestions) {
			selected := m.suggestions[m.selectedSug]
			parts := strings.Split(m.input, ",")
			if len(parts) == 1 {
				parts[0] = selected
			} else {
				parts[len(parts)-1] = " " + selected
			}
			m.input = strings.Join(parts, ",") + ", "
			m.inputPos = len([]rune(m.input))
			m.suggestions = nil
			m.selectedSug = 0
			return m, m.resetBlink()
		}
		return m, nil

	case tea.KeyUp:
		if len(m.suggestions) > 0 {
			m.selectedSug--
			if m.selectedSug < 0 {
				m.selectedSug = len(m.suggestions) - 1
			}
		}
		return m, nil

	case tea.KeyDown:
		if len(m.suggestions) > 0 {
			m.selectedSug++
			if m.selectedSug >= len(m.suggestions) {
				m.selectedSug = 0
			}
		}
		return m, nil

	default:
		model, cmd := m.handleTextInput(msg)
		m2 := model.(Model)
		token := currentTagToken(m2.input)
		m2.suggestions = computeSuggestions(m2.allTags, token)
		m2.selectedSug = 0
		return m2, cmd
	}
}

// handleNoteKey 处理 note 输入模式的按键
func (m Model) handleNoteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		m.mode = modeNormal
		m.input = ""
		m.inputPos = 0
		m.message = ""
		return m, nil

	case tea.KeyEnter:
		m.mode = modeNormal
		note := strings.TrimSpace(m.input)
		m.input = ""
		m.inputPos = 0
		return m, m.doUpdateNote(note)

	default:
		return m.handleTextInput(msg)
	}
}

// handleTextInput 处理文本输入的通用逻辑（光标移动、删除、字符插入）
// 任何操作都重置光标为可见并重启闪烁计时器，避免操作瞬间光标消失。
func (m Model) handleTextInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	runes := []rune(m.input)

	switch msg.Type {
	case tea.KeyBackspace:
		if m.inputPos > 0 {
			m.input = string(runes[:m.inputPos-1]) + string(runes[m.inputPos:])
			m.inputPos--
		}

	case tea.KeyDelete:
		if m.inputPos < len(runes) {
			m.input = string(runes[:m.inputPos]) + string(runes[m.inputPos+1:])
		}

	case tea.KeyLeft:
		if m.inputPos > 0 {
			m.inputPos--
		}

	case tea.KeyRight:
		if m.inputPos < len(runes) {
			m.inputPos++
		}

	case tea.KeyHome, tea.KeyCtrlA:
		m.inputPos = 0

	case tea.KeyEnd, tea.KeyCtrlE:
		m.inputPos = len(runes)

	case tea.KeyCtrlK:
		m.input = string(runes[:m.inputPos])

	case tea.KeyCtrlU:
		m.input = string(runes[m.inputPos:])
		m.inputPos = 0

	case tea.KeySpace:
		// 空格作为普通字符插入
		newRunes := make([]rune, 0, len(runes)+1)
		newRunes = append(newRunes, runes[:m.inputPos]...)
		newRunes = append(newRunes, ' ')
		newRunes = append(newRunes, runes[m.inputPos:]...)
		m.input = string(newRunes)
		m.inputPos++

	case tea.KeyRunes:
		newRunes := make([]rune, 0, len(runes)+len(msg.Runes))
		newRunes = append(newRunes, runes[:m.inputPos]...)
		newRunes = append(newRunes, msg.Runes...)
		newRunes = append(newRunes, runes[m.inputPos:]...)
		m.input = string(newRunes)
		m.inputPos += len(msg.Runes)
	}

	// 每次操作都重置光标可见 + 重启闪烁（旧定时器自动失效）
	return m, m.resetBlink()
}
