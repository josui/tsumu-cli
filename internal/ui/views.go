package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/josui/tsumu-cli/internal/db"
	"github.com/josui/tsumu-cli/internal/sync"
)

// ============================================================
// View 渲染
// ============================================================

func (m Model) View() string {
	var b strings.Builder

	// 顶部：搜索词 + 页码
	var header string
	if m.query == "" {
		if m.favOnly {
			header = "tsumu ★ favorites"
		} else {
			header = "tsumu"
		}
	} else {
		header = fmt.Sprintf("tsumu %s", m.query)
		if m.aiExpanded {
			header += "  AI expanded, [x] to mark irrelevant"
		} else if len(m.results) > 0 {
			hint := ""
			if m.cfg.AI.IsConfigured() {
				hint = "[⇥ AI]  "
			}
			header += "  " + hint + "[x] irrelevant"
		} else if m.cfg.AI.IsConfigured() {
			header += "  [⇥ AI]"
		}
	}
	// 右侧状态：bgTaskLabel 优先于 syncStatus
	rightStatus := m.syncStatus
	if m.bgTaskLabel != "" {
		rightStatus = m.bgTaskLabel
	}
	rightParts := fmt.Sprintf("Page %d/%d", m.page()+1, m.totalPages())
	if rightStatus != "" {
		rightParts += "  " + rightStatus
	}
	cw := m.contentWidth()
	gap := cw - stringWidth(header) - stringWidth(rightParts)
	if gap < 2 {
		gap = 2
	}
	b.WriteString(headerStyle.Render(header + strings.Repeat(" ", gap) + rightParts))
	b.WriteString("\n\n")

	// 无结果
	if len(m.results) == 0 {
		b.WriteString("  No matching bookmarks found\n")
	} else {
		start := m.pageStart()
		end := m.pageEnd()
		pageResults := m.results[start:end]

		for i, bm := range pageResults {
			globalIdx := start + i
			isFocused := globalIdx == m.cursor

			b.WriteString(m.renderDefault(globalIdx+1, &bm, isFocused))

			if i < len(pageResults)-1 {
				b.WriteString(dividerStyle.Render("  " + strings.Repeat("─", cw)))
				b.WriteString("\n")
			}
		}
	}

	b.WriteString("\n")

	// 底部消息
	if m.message != "" {
		if m.isError {
			b.WriteString("  " + errorMessageStyle.Render(m.message))
		} else {
			b.WriteString("  " + messageStyle.Render(m.message))
		}
		b.WriteString("\n")
	}

	// 输入行（tags / note）
	if m.mode == modeTag || m.mode == modeNote {
		label := "tags"
		if m.mode == modeNote {
			label = "note"
		}
		prefix := fmt.Sprintf("  %s> ", label)
		// 输入部分的可用宽度 = 终端宽度 - prefix 宽度
		inputMaxW := m.width - stringWidth(prefix)
		if inputMaxW < 10 {
			inputMaxW = 10
		}
		inputContent := m.renderFieldWithCursor(m.input, inputMaxW)
		b.WriteString(prefix + inputContent + "\n")

		// Render suggestion dropdown (only in tag mode)
		if m.mode == modeTag && len(m.suggestions) > 0 {
			token := currentTagToken(m.input)
			// tokenCol = "  tags> " 前缀宽度 + 输入文字到 token 起始的宽度
			tokenCol := 8 + stringWidth(m.input) - stringWidth(token)
			b.WriteString(renderDropdown(m.suggestions, m.selectedSug, tokenCol))
			b.WriteString("\n")
		}
	}

	// 操作提示
	if m.mode == modeNormal && m.overlay == overlayNone {
		b.WriteString(helpStyle.Render("  [↵] open  [/] cmd  [t] tag  [f] fav  [d] del  [n] note  [c] copy  [r] refetch  [q] quit"))
		b.WriteString("\n")
	}

	base := b.String()

	// overlay 渲染：居中叠加在列表内容之上
	if m.overlay != overlayNone {
		overlay := m.renderOverlay()
		if overlay != "" {
			return lipgloss.Place(
				m.width, lipgloss.Height(base),
				lipgloss.Center, lipgloss.Center,
				overlay,
				lipgloss.WithWhitespaceChars(" "),
			)
		}
	}

	return base
}

// renderOverlay 根据当前 overlay 类型渲染对应的浮层内容
func (m Model) renderOverlay() string {
	switch m.overlay {
	case overlayCommand:
		return m.renderCommandPalette()
	case overlayAddForm:
		return m.renderAddForm()
	case overlayConfigAI:
		return m.renderConfigAI()
	case overlayConfigSync:
		return m.renderConfigSync()
	case overlaySyncStatus:
		return m.renderSyncStatus()
	}
	return ""
}

// renderAddForm 渲染 Add Bookmark 表单
func (m Model) renderAddForm() string {
	var b strings.Builder
	w := m.overlayWidth()

	// 标题行（手动 pad 到 w 宽度）
	title := overlayTitleStyle.Render("Add Bookmark")
	hint := overlayHintStyle.Render("esc")
	gap := w - lipgloss.Width(title) - lipgloss.Width(hint)
	if gap < 2 {
		gap = 2
	}
	b.WriteString(title + strings.Repeat(" ", gap) + hint + "\n\n")

	labels := [3]string{"URL", "Tags (comma separated, optional)", "Note (optional)"}
	placeholders := [3]string{"Paste URL here...", "", ""}
	// field 内容宽度 = overlay 内容宽度 - field 自身 border(2) + padding(2)
	fieldW := w - 4

	// renderField 渲染单个表单字段（label + input box）
	renderField := func(i int) {
		b.WriteString(overlayLabelStyle.Render(labels[i]) + "\n")

		var content string
		if i == m.addFocus && !m.addSubmitting {
			content = m.renderFieldWithCursor(m.addFields[i], fieldW)
		} else if m.addFields[i] == "" && placeholders[i] != "" && !m.addSubmitting {
			content = overlayHintStyle.Render(placeholders[i])
		} else {
			content = truncateField(m.addFields[i], fieldW)
		}

		fieldStyle := overlayFieldStyle
		if i == m.addFocus && !m.addSubmitting {
			fieldStyle = overlayFieldFocusStyle
		}

		visW := lipgloss.Width(content)
		if visW < fieldW {
			content += strings.Repeat(" ", fieldW-visW)
		}
		b.WriteString(fieldStyle.Render(content) + "\n")
	}

	// URL 字段
	renderField(0)
	b.WriteString("\n")

	// Tags 字段
	renderField(1)

	// Tags 字段后：dropdown 替换 Note 区域，或正常渲染 Note
	showDropdown := m.addFocus == 1 && len(m.suggestions) > 0 && !m.addSubmitting
	if showDropdown {
		token := currentTagToken(m.addFields[1])
		// field border(1) + padding(1) = 2 列偏移
		tokenCol := 2 + stringWidth(m.addFields[1]) - stringWidth(token)
		b.WriteString(renderDropdown(m.suggestions, m.selectedSug, tokenCol))
		b.WriteString("\n")
	} else {
		b.WriteString("\n") // Tags 和 Note 之间的空行分隔
		renderField(2)
	}

	if m.addSubmitting {
		b.WriteString("\n" + messageStyle.Render("⟳ Fetching metadata..."))
	} else if showDropdown {
		b.WriteString("\n" + overlayHintStyle.Render("tab complete · ↑↓ · enter submit"))
	} else {
		b.WriteString("\n" + overlayHintStyle.Render("tab next · shift+tab prev · enter submit"))
	}

	return overlayBorderStyle.Render(b.String())
}

// renderSyncStatus 渲染 Sync 状态卡片
func (m Model) renderSyncStatus() string {
	var b strings.Builder
	w := m.overlayWidth()

	title := overlayTitleStyle.Render("Sync Status")
	hint := overlayHintStyle.Render("esc")
	gap := w - lipgloss.Width(title) - lipgloss.Width(hint)
	if gap < 2 {
		gap = 2
	}
	b.WriteString(title + strings.Repeat(" ", gap) + hint + "\n\n")

	if !m.cfg.Sync.CanSync() {
		b.WriteString("  Status     " + errorMessageStyle.Render("● Not configured") + "\n")
		b.WriteString("\n" + overlayHintStyle.Render("Use /config sync to set up"))
	} else {
		b.WriteString("  Status     " + messageStyle.Render("● Connected") + "\n")
		b.WriteString("  Database   " + m.cfg.Sync.GetURL() + "\n")

		if m.cfg.Sync.LastSynced != "" {
			last, err := time.Parse(time.RFC3339, m.cfg.Sync.LastSynced)
			if err == nil {
				b.WriteString(fmt.Sprintf("  Last sync  %s\n", last.Local().Format("2006-01-02 15:04:05")))
			}
		} else {
			b.WriteString("  Last sync  never\n")
		}

		pending := sync.PendingCount(m.db, m.cfg.Sync.LastSynced)
		if pending > 0 {
			b.WriteString(fmt.Sprintf("  Pending    %d bookmarks\n", pending))
		} else {
			b.WriteString("  Pending    all synced\n")
		}

		interval := m.cfg.Sync.Interval
		if interval == "" {
			interval = "24h"
		}
		autoSync := "Enabled"
		if !m.cfg.Sync.IsEnabled() {
			autoSync = "Disabled"
		}
		b.WriteString(fmt.Sprintf("  Auto sync  %s (%s interval)\n", autoSync, interval))
	}

	b.WriteString("\n" + overlayHintStyle.Render("esc close"))
	return overlayBorderStyle.Render(b.String())
}

// renderConfigAI 渲染 AI 配置表单
func (m Model) renderConfigAI() string {
	var b strings.Builder
	w := m.overlayWidth()

	// 标题行
	title := overlayTitleStyle.Render("Config AI")
	hint := overlayHintStyle.Render("esc")
	gap := w - lipgloss.Width(title) - lipgloss.Width(hint)
	if gap < 2 {
		gap = 2
	}
	b.WriteString(title + strings.Repeat(" ", gap) + hint + "\n\n")

	labels := [4]string{"Provider", "API Key", "Gen Model", "Lang (e.g. en, zh,en)"}
	placeholders := [4]string{"gemini", "", "gemini-flash-latest", "en"}
	fieldW := w - 4

	for i := 0; i < 4; i++ {
		b.WriteString(overlayLabelStyle.Render(labels[i]) + "\n")

		var content string
		if i == m.aiConfigFocus {
			// 聚焦字段：显示光标（带视口滚动）
			content = m.renderFieldWithCursor(m.aiConfigFields[i], fieldW)
		} else if m.aiConfigFields[i] == "" && placeholders[i] != "" {
			content = overlayHintStyle.Render(placeholders[i])
		} else {
			// 非聚焦字段：左对齐截断
			content = truncateField(m.aiConfigFields[i], fieldW)
		}

		fieldStyle := overlayFieldStyle
		if i == m.aiConfigFocus {
			fieldStyle = overlayFieldFocusStyle
		}

		visW := lipgloss.Width(content)
		if visW < fieldW {
			content += strings.Repeat(" ", fieldW-visW)
		}
		b.WriteString(fieldStyle.Render(content) + "\n")

		if i < 3 {
			b.WriteString("\n")
		}
	}

	b.WriteString("\n" + overlayHintStyle.Render("tab next · shift+tab prev · enter save"))

	return overlayBorderStyle.Render(b.String())
}

// renderConfigSync 渲染 Sync 配置表单
func (m Model) renderConfigSync() string {
	var b strings.Builder
	w := m.overlayWidth()

	// 标题行
	title := overlayTitleStyle.Render("Config Sync")
	hint := overlayHintStyle.Render("esc")
	gap := w - lipgloss.Width(title) - lipgloss.Width(hint)
	if gap < 2 {
		gap = 2
	}
	b.WriteString(title + strings.Repeat(" ", gap) + hint + "\n\n")

	labels := [4]string{"URL", "Auth Token", "Interval (e.g. 24h, 7d)", "Enabled"}
	placeholders := [4]string{"libsql://your-db.turso.io", "", "24h", ""}
	fieldW := w - 4

	for i := 0; i < 4; i++ {
		b.WriteString(overlayLabelStyle.Render(labels[i]) + "\n")

		fieldStyle := overlayFieldStyle
		if i == m.syncConfigFocus {
			fieldStyle = overlayFieldFocusStyle
		}

		var content string
		if i == 3 {
			// Enabled 字段：toggle 显示（无光标）
			enabled := m.syncConfigFields[3] == "true"
			if enabled {
				content = messageStyle.Render("● Enabled")
			} else {
				content = overlayHintStyle.Render("○ Disabled")
			}
		} else if i == m.syncConfigFocus {
			// 聚焦字段：显示光标（带视口滚动）
			content = m.renderFieldWithCursor(m.syncConfigFields[i], fieldW)
		} else {
			content = m.syncConfigFields[i]
			if content == "" && placeholders[i] != "" {
				content = overlayHintStyle.Render(placeholders[i])
			} else {
				// 非聚焦字段：左对齐截断
				content = truncateField(content, fieldW)
			}
		}

		visW := lipgloss.Width(content)
		if visW < fieldW {
			content += strings.Repeat(" ", fieldW-visW)
		}
		b.WriteString(fieldStyle.Render(content) + "\n")

		if i < 3 {
			b.WriteString("\n")
		}
	}

	b.WriteString("\n" + overlayHintStyle.Render("tab next · space toggle · enter save"))

	return overlayBorderStyle.Render(b.String())
}

// renderCommandPalette 渲染命令面板：搜索框 + 分类命令列表
func (m Model) renderCommandPalette() string {
	var b strings.Builder
	w := m.overlayWidth()

	// 标题行
	title := overlayTitleStyle.Render("Commands")
	hint := overlayHintStyle.Render("esc")
	gap := w - lipgloss.Width(title) - lipgloss.Width(hint)
	if gap < 2 {
		gap = 2
	}
	b.WriteString(title + strings.Repeat(" ", gap) + hint + "\n\n")

	// 搜索框（带光标）
	var searchContent string
	if m.cmdFilter == "" && m.inputPos == 0 {
		// 空输入时：光标 + placeholder
		var cursor string
		if m.cursorVisible {
			cursor = inputCursorStyle.Render(" ")
		} else {
			cursor = " "
		}
		searchContent = cursor + overlayHintStyle.Render("Search or type command...")
	} else {
		searchContent = m.renderFieldWithCursor(m.cmdFilter, w)
	}
	b.WriteString(searchContent + "\n\n")

	// 命令列表（按分类分组，支持滚动）
	maxVisible := m.cmdMaxVisible()
	totalItems := len(m.cmdFiltered)

	// 顶部截断提示
	if m.cmdScrollOffset > 0 {
		b.WriteString(overlayHintStyle.Render("  ↑ more") + "\n")
	}

	// 构建可见区域内的行（需要考虑分类标题也占行数）
	// 先计算每个条目的 displayIdx，再只渲染可见范围内的
	lastCategory := ""
	displayIdx := 0
	visibleStart := m.cmdScrollOffset
	visibleEnd := m.cmdScrollOffset + maxVisible
	if visibleEnd > totalItems {
		visibleEnd = totalItems
	}

	for _, idx := range m.cmdFiltered {
		cmd := commands[idx]

		if displayIdx >= visibleStart && displayIdx < visibleEnd {
			// 分类标题：只在可见范围内且分类变化时显示
			if cmd.category != lastCategory {
				if lastCategory != "" {
					b.WriteString("\n")
				}
				b.WriteString(overlayCatStyle.Render(cmd.category) + "\n")
			}

			prefix := "  "
			nameStyle := overlayCmdStyle
			if displayIdx == m.cmdCursor {
				prefix = "→ "
				nameStyle = overlayCmdSelStyle
			}
			// name 固定 14 列宽，desc 跟在后面
			namePad := 14 - stringWidth(cmd.name)
			if namePad < 2 {
				namePad = 2
			}
			b.WriteString(prefix + nameStyle.Render(cmd.name) + strings.Repeat(" ", namePad) + overlayHintStyle.Render(cmd.desc) + "\n")
		}

		lastCategory = cmd.category
		displayIdx++
	}

	// 底部截断提示
	if visibleEnd < totalItems {
		b.WriteString(overlayHintStyle.Render("  ↓ more") + "\n")
	}

	// 底部提示
	b.WriteString("\n" + overlayHintStyle.Render("tab command · ↑↓ navigate · esc close"))

	return overlayBorderStyle.Render(b.String())
}

// renderDefault 渲染默认模式的单条书签。isFocused 控制高亮。
//
// 布局：▸ N ★ Title............  domain  ×clicks
//
//	Note text aligned with title...
//
// 左侧固定: cursor(1+1) + idx(2) + space(1) + star(1) + space(1) = 7
// 右侧固定: "  " + domain + "  " + clicks
// Title 填满中间剩余空间，右侧右对齐
func (m Model) renderDefault(idx int, bm *db.Bookmark, isFocused bool) string {
	var b strings.Builder
	cw := m.contentWidth()
	const prefixWidth = 7 // cursor(2) + idx(2) + " " + star(1) + " "

	// 光标指示符（1 字符 + 1 空格）
	cursor := "  "
	if isFocused {
		cursor = cursorStyle.Render("→ ")
	}

	// 索引（2 字符宽度右对齐）
	idxStr := indexStyle.Render(fmt.Sprintf("%d", idx))

	// 星标（固定 1 字符宽度）
	star := " "
	if bm.IsFavorite {
		star = starStyle.Render("★")
	}

	// 右侧内容（domain + clicks），计算可见宽度
	domainText := truncate(bm.SiteName, 15)
	clicksText := fmt.Sprintf("×%d", bm.ClickCount)
	rightWidth := stringWidth(domainText) + 2 + len(clicksText) // domain + "  " + clicks

	// Title 填满剩余空间
	titleMax := cw - prefixWidth - rightWidth - 2 // 2 = title 与右侧的间距
	if titleMax < 10 {
		titleMax = 10
	}

	titleText := truncate(bm.Title, titleMax)
	var title string
	if m.irrelevantSet[bm.ID] {
		title = dimTitleStyle.Render(titleText)
	} else if isFocused {
		title = focusTitleStyle.Render(titleText)
	} else {
		title = titleStyle.Render(titleText)
	}

	// 用空格填充 title 到固定宽度，使右侧对齐
	titlePad := titleMax - stringWidth(titleText)
	if titlePad < 0 {
		titlePad = 0
	}

	domain := domainStyle.Render(domainText)
	clicks := clickStyle.Render(clicksText)

	fmt.Fprintf(&b, "%s%s %s %s%s  %s  %s\n",
		cursor, idxStr, star, title, strings.Repeat(" ", titlePad), domain, clicks)

	// Note 行，与 title 列对齐
	if bm.Note != "" {
		noteMax := cw - prefixWidth
		fmt.Fprintf(&b, "%s%s\n", strings.Repeat(" ", prefixWidth), noteStyle.Render(truncate(bm.Note, noteMax)))
	}

	// Tags 行：#[tag1] #[tag2] 格式
	if bm.Tags != "" {
		var tags []string
		for _, t := range strings.Split(bm.Tags, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				tags = append(tags, tagStyle.Render("#"+t))
			}
		}
		fmt.Fprintf(&b, "%s%s\n", strings.Repeat(" ", prefixWidth), strings.Join(tags, " "))
	}

	return b.String()
}

// refreshSyncStatus 重新查询 pending count 并更新 syncStatus 文本。
// 在数据变更（refetch 等）后调用，使 header 状态及时反映变化。
func refreshSyncStatus(m *Model) {
	if m.lastSynced == "" {
		return // sync 未配置或从未同步过，保持原状态
	}
	var pending int
	m.db.QueryRow("SELECT COUNT(*) FROM bookmarks WHERE updated_at > ?", m.lastSynced).Scan(&pending)
	if pending > 0 {
		m.syncStatus = fmt.Sprintf("%d pending", pending)
	} else {
		m.syncStatus = "synced ✓"
	}
}
