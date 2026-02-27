// cli/internal/ui/model.go

// model.go 是 bubbletea TUI 的核心。
// 实现 Elm 架构的三个部分：
// - Model: 应用状态（搜索结果、光标位置、输入模式等）
// - Update: 事件处理（按键、命令执行结果）
// - View: 渲染终端输出（根据状态生成字符串）
//
// 交互模型：cursor-based 导航
// j/k 移动光标，Enter 打开，d 删除，f 收藏，t 打标签

package ui

import (
	"database/sql"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/user/tsumu-cli/internal/db"
)

// ============================================================
// 输入模式
// ============================================================

// inputMode 表示当前的输入模式
type inputMode int

const (
	modeNormal  inputMode = iota // 普通模式：j/k/Enter/d/f/t/q 即时操作
	modeTag                      // 标签输入模式：输入标签文本，Enter 提交
	modeConfirm                  // 删除确认模式：y 确认，其他键取消
)

// ============================================================
// Model 定义
// ============================================================

// Model 是 TUI 的完整状态。bubbletea 要求实现 Init/Update/View 三个方法。
type Model struct {
	db       *sql.DB       // 数据库连接
	query    string        // 搜索关键词（空 = 列出全部）
	detailed bool          // 是否详细模式
	results  []db.Bookmark // 搜索结果
	total    int           // 结果总数
	pageSize int           // 每页条数

	cursor int       // 当前光标位置（全局索引，0-based）
	mode   inputMode // 当前输入模式
	input  string    // 标签输入缓冲区（仅 modeTag 时使用）

	message string // 底部反馈消息
	isError bool   // message 是否为错误消息

	width int // 终端宽度
}

// NewModel 创建并初始化 Model。
func NewModel(database *sql.DB, query string, detailed bool) Model {
	return Model{
		db:       database,
		query:    query,
		detailed: detailed,
		pageSize: 5,
		mode:     modeNormal,
	}
}

// ============================================================
// bubbletea 消息类型
// ============================================================

// searchResultMsg 搜索完成后的消息
type searchResultMsg struct {
	results []db.Bookmark
	total   int
	err     error
}

// openResultMsg 打开书签后的消息
type openResultMsg struct {
	title string
	count int
	err   error
}

// actionResultMsg 操作完成后的消息
type actionResultMsg struct {
	message string
	isError bool
}

// ============================================================
// 光标与分页辅助
// ============================================================

// page 根据光标位置计算当前页码（0-based）
func (m Model) page() int {
	if m.pageSize == 0 {
		return 0
	}
	return m.cursor / m.pageSize
}

// totalPages 计算总页数
func (m Model) totalPages() int {
	if len(m.results) == 0 {
		return 1
	}
	return (len(m.results) + m.pageSize - 1) / m.pageSize
}

// pageStart 当前页的起始索引
func (m Model) pageStart() int {
	return m.page() * m.pageSize
}

// pageEnd 当前页的结束索引（不含）
func (m Model) pageEnd() int {
	end := m.pageStart() + m.pageSize
	if end > len(m.results) {
		end = len(m.results)
	}
	return end
}

// focusedBookmark 返回当前光标指向的书签，无结果时返回 nil
func (m Model) focusedBookmark() *db.Bookmark {
	if m.cursor < 0 || m.cursor >= len(m.results) {
		return nil
	}
	return &m.results[m.cursor]
}

// ============================================================
// Init / Update / View
// ============================================================

// Init 返回初始命令。bubbletea 启动时自动调用一次。
func (m Model) Init() tea.Cmd {
	return m.doSearch()
}

// Update 处理所有事件（按键、消息）。
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil

	case searchResultMsg:
		if msg.err != nil {
			m.message = fmt.Sprintf("Search failed: %v", msg.err)
			m.isError = true
			return m, nil
		}
		m.results = msg.results
		m.total = msg.total
		m.message = ""
		// 删除后光标可能越界，修正
		if m.cursor >= len(m.results) && len(m.results) > 0 {
			m.cursor = len(m.results) - 1
		}
		return m, nil

	case openResultMsg:
		if msg.err != nil {
			m.message = fmt.Sprintf("Open failed: %v", msg.err)
			m.isError = true
		} else {
			m.message = fmt.Sprintf("✓ Opened %s (×%d)", msg.title, msg.count)
			m.isError = false
		}
		return m, nil

	case actionResultMsg:
		m.message = msg.message
		m.isError = msg.isError
		if !msg.isError {
			return m, m.doSearch()
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

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
		return m.handleTagKey(key)
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

	// 清除消息
	case keyEsc:
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

	// 进入标签输入模式
	case keyTag:
		if bm := m.focusedBookmark(); bm != nil {
			m.mode = modeTag
			m.input = ""
			m.message = ""
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

// handleTagKey 处理标签输入模式的按键
func (m Model) handleTagKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case keyEsc:
		// 取消标签输入
		m.mode = modeNormal
		m.input = ""
		m.message = ""
		return m, nil

	case keyEnter:
		// 提交标签
		m.mode = modeNormal
		tagStr := strings.TrimSpace(m.input)
		m.input = ""

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

		return m, m.doAddTags(tags)

	case "backspace":
		if len(m.input) > 0 {
			// 处理多字节字符（中文等）
			runes := []rune(m.input)
			m.input = string(runes[:len(runes)-1])
		}
		return m, nil

	default:
		// 累积输入字符（过滤控制键）
		if len(key) == 1 || key == " " {
			m.input += key
		}
		return m, nil
	}
}

// ============================================================
// 异步命令（tea.Cmd）
// ============================================================

func (m Model) doSearch() tea.Cmd {
	return func() tea.Msg {
		results, total, err := db.Search(m.db, m.query, m.detailed, 50000, 0)
		return searchResultMsg{results: results, total: total, err: err}
	}
}

func (m Model) doOpen() tea.Cmd {
	return func() tea.Msg {
		bm := m.focusedBookmark()
		if bm == nil {
			return openResultMsg{err: fmt.Errorf("no bookmark selected")}
		}

		if err := openBrowser(bm.URL); err != nil {
			return openResultMsg{err: err}
		}

		count, err := db.IncrementClickCount(m.db, bm.ID)
		if err != nil {
			return openResultMsg{err: err}
		}

		displayName := bm.SiteName
		if displayName == "" {
			displayName = bm.Title
		}
		return openResultMsg{title: displayName, count: count}
	}
}

func (m Model) doToggleFavorite() tea.Cmd {
	// 捕获当前书签信息（闭包在异步执行时 model 可能已变）
	bm := m.focusedBookmark()
	if bm == nil {
		return nil
	}
	id := bm.ID
	title := bm.Title

	return func() tea.Msg {
		isFav, err := db.ToggleFavorite(m.db, id)
		if err != nil {
			return actionResultMsg{message: fmt.Sprintf("Action failed: %v", err), isError: true}
		}
		if isFav {
			return actionResultMsg{message: fmt.Sprintf("✓ ★ Favorited %s", title)}
		}
		return actionResultMsg{message: fmt.Sprintf("✓ ☆ Unfavorited %s", title)}
	}
}

func (m Model) doAddTags(tags []string) tea.Cmd {
	bm := m.focusedBookmark()
	if bm == nil {
		return nil
	}
	id := bm.ID

	return func() tea.Msg {
		if err := db.AddTagsToBookmark(m.db, id, tags); err != nil {
			return actionResultMsg{message: fmt.Sprintf("Tag failed: %v", err), isError: true}
		}
		return actionResultMsg{message: fmt.Sprintf("✓ Tagged: %s", strings.Join(tags, ", "))}
	}
}

func (m Model) doDelete() tea.Cmd {
	bm := m.focusedBookmark()
	if bm == nil {
		return nil
	}
	id := bm.ID

	return func() tea.Msg {
		if err := db.DeleteBookmark(m.db, id); err != nil {
			return actionResultMsg{message: fmt.Sprintf("Delete failed: %v", err), isError: true}
		}
		return actionResultMsg{message: "✓ Deleted"}
	}
}

// ============================================================
// View 渲染
// ============================================================

func (m Model) View() string {
	var b strings.Builder

	// 顶部：搜索词 + 页码
	var header string
	if m.query == "" {
		header = "tsumu"
	} else if m.detailed {
		header = fmt.Sprintf("tsumu find -d %s", m.query)
	} else {
		header = fmt.Sprintf("tsumu find %s", m.query)
	}
	pageInfo := fmt.Sprintf("Page %d/%d", m.page()+1, m.totalPages())
	gap := 60 - len(header) - len(pageInfo)
	if gap < 2 {
		gap = 2
	}
	b.WriteString(headerStyle.Render(header + strings.Repeat(" ", gap) + pageInfo))
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

			if m.detailed {
				b.WriteString(m.renderDetailed(globalIdx+1, &bm, isFocused))
			} else {
				b.WriteString(m.renderDefault(globalIdx+1, &bm, isFocused))
			}

			if i < len(pageResults)-1 {
				b.WriteString(dividerStyle.Render("  " + strings.Repeat("─", 60)))
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

	// 标签输入提示
	if m.mode == modeTag {
		b.WriteString(fmt.Sprintf("  tags> %s\n", m.input))
	}

	// 操作提示
	if m.mode == modeNormal {
		b.WriteString(helpStyle.Render("  [enter] open  [t] tag  [f] fav  [d] delete"))
		b.WriteString("\n")
		b.WriteString(helpStyle.Render("  [j] ↓  [k] ↑  [q] quit"))
		b.WriteString("\n")
	}

	return b.String()
}

// renderDefault 渲染默认模式的单条书签。isFocused 控制高亮。
func (m Model) renderDefault(idx int, bm *db.Bookmark, isFocused bool) string {
	var b strings.Builder

	// 光标指示符
	cursor := "  "
	if isFocused {
		cursor = cursorStyle.Render("▸ ")
	}

	idxStr := indexStyle.Render(fmt.Sprintf("%d", idx))
	star := "  "
	if bm.IsFavorite {
		star = starStyle.Render("★ ")
	}

	// 选中时标题高亮
	var title string
	if isFocused {
		title = focusTitleStyle.Render(truncate(bm.Title, 35))
	} else {
		title = titleStyle.Render(truncate(bm.Title, 35))
	}
	domain := domainStyle.Render(truncate(bm.SiteName, 20))
	clicks := clickStyle.Render(fmt.Sprintf("×%d", bm.ClickCount))

	fmt.Fprintf(&b, "%s%s %s%s  %s  %s\n", cursor, idxStr, star, title, domain, clicks)

	if bm.Note != "" {
		fmt.Fprintf(&b, "       %s\n", noteStyle.Render(truncate(bm.Note, 55)))
	}

	return b.String()
}

// renderDetailed 渲染详细模式的单条书签。
func (m Model) renderDetailed(idx int, bm *db.Bookmark, isFocused bool) string {
	var b strings.Builder

	cursor := "  "
	if isFocused {
		cursor = cursorStyle.Render("▸ ")
	}

	idxStr := indexStyle.Render(fmt.Sprintf("%d", idx))
	star := "  "
	if bm.IsFavorite {
		star = starStyle.Render("★ ")
	}

	var title string
	if isFocused {
		title = focusTitleStyle.Render(bm.Title)
	} else {
		title = titleStyle.Render(bm.Title)
	}
	fmt.Fprintf(&b, "%s%s %s%s\n", cursor, idxStr, star, title)

	fmt.Fprintf(&b, "       %s\n", domainStyle.Render(bm.URL))

	if bm.Description != "" {
		fmt.Fprintf(&b, "       %s\n", truncate(bm.Description, 55))
	}

	if bm.Note != "" {
		fmt.Fprintf(&b, "       %s\n", noteStyle.Render("📝 "+bm.Note))
	}

	if bm.Tags != "" {
		fmt.Fprintf(&b, "       %s\n", tagStyle.Render("tags: "+bm.Tags))
	}

	date := ""
	if len(bm.CreatedAt) >= 10 {
		date = bm.CreatedAt[:10]
	}
	metaLine := fmt.Sprintf("added: %s    clicks: ×%d    source: %s", date, bm.ClickCount, bm.Source)
	fmt.Fprintf(&b, "       %s\n", metaStyle.Render(metaLine))

	return b.String()
}

// ============================================================
// 辅助函数
// ============================================================

func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen-2]) + ".."
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
	return cmd.Start()
}
