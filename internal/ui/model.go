// cli/internal/ui/model.go

// model.go 是 bubbletea TUI 的核心。
// 实现 Elm 架构的三个部分：
// - Model: 应用状态（搜索结果、当前页、输入缓冲等）
// - Update: 事件处理（按键、命令执行结果）
// - View: 渲染终端输出（根据状态生成字符串）

package ui

import (
	"database/sql"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/user/tsumu-cli/internal/db"
)

// ============================================================
// Model 定义
// ============================================================

// Model 是 TUI 的完整状态。bubbletea 要求实现 Init/Update/View 三个方法。
type Model struct {
	db       *sql.DB       // 数据库连接
	query    string        // 搜索关键词
	detailed bool          // 是否详细模式 (-sd)
	results  []db.Bookmark // 搜索结果
	total    int           // 结果总数（用于分页）
	page     int           // 当前页码（从 0 开始）
	pageSize int           // 每页条数

	input         string // 用户输入缓冲区（数字、t2 tag1, f3, d3 等）
	confirmDelete int    // 正在确认删除的序号，-1 表示不在确认状态
	message       string // 底部反馈消息
	isError       bool   // message 是否为错误消息

	width int // 终端宽度
}

// NewModel 创建并初始化 Model。
func NewModel(database *sql.DB, query string, detailed bool) Model {
	return Model{
		db:            database,
		query:         query,
		detailed:      detailed,
		pageSize:      5,
		confirmDelete: -1,
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
			m.message = fmt.Sprintf("搜索失败: %v", msg.err)
			m.isError = true
			return m, nil
		}
		m.results = msg.results
		m.total = msg.total
		m.message = ""
		return m, nil

	case openResultMsg:
		if msg.err != nil {
			m.message = fmt.Sprintf("打开失败: %v", msg.err)
			m.isError = true
		} else {
			m.message = fmt.Sprintf("✓ 已打开 %s (×%d)", msg.title, msg.count)
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

// handleKey 处理按键输入。
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Ctrl+C 始终退出
	if key == "ctrl+c" {
		return m, tea.Quit
	}

	// --- 删除确认状态 ---
	if m.confirmDelete >= 0 {
		switch key {
		case "y":
			idx := m.confirmDelete
			m.confirmDelete = -1
			return m, m.doDelete(idx)
		default:
			m.confirmDelete = -1
			m.message = "已取消删除"
			m.isError = false
			return m, nil
		}
	}

	// --- 即时按键（不需要 Enter） ---
	switch key {
	case keyQuit:
		if m.input == "" {
			return m, tea.Quit
		}
	case keyEsc:
		m.input = ""
		m.message = ""
		return m, nil
	case keyNextPage:
		if m.input == "" {
			maxPage := 0
			if m.total > 0 {
				maxPage = (m.total - 1) / m.pageSize
			}
			if m.page < maxPage {
				m.page++
			}
			return m, nil
		}
	case keyPrevPage:
		if m.input == "" {
			if m.page > 0 {
				m.page--
			}
			return m, nil
		}
	}

	// --- 需要 Enter 的输入 ---
	if key == keyEnter {
		return m.handleInputSubmit()
	}

	// 退格键
	if key == "backspace" {
		if len(m.input) > 0 {
			m.input = m.input[:len(m.input)-1]
		}
		return m, nil
	}

	// 累积输入字符
	if len(key) == 1 {
		m.input += key
	}

	return m, nil
}

// handleInputSubmit 处理 Enter 提交的输入。
func (m Model) handleInputSubmit() (tea.Model, tea.Cmd) {
	input := strings.TrimSpace(m.input)
	m.input = ""

	if input == "" {
		return m, nil
	}

	// t{n} tag1,tag2 — 打标签
	if strings.HasPrefix(input, "t") {
		return m.handleTagInput(input[1:])
	}

	// f{n} — 收藏
	if strings.HasPrefix(input, "f") {
		return m.handleFavoriteInput(input[1:])
	}

	// d{n} — 删除
	if strings.HasPrefix(input, "d") {
		return m.handleDeleteInput(input[1:])
	}

	// 纯数字 — 打开
	if num, err := strconv.Atoi(input); err == nil {
		return m, m.doOpen(num)
	}

	m.message = fmt.Sprintf("无法识别的输入: %s", input)
	m.isError = true
	return m, nil
}

func (m Model) handleTagInput(rest string) (tea.Model, tea.Cmd) {
	parts := strings.SplitN(rest, " ", 2)
	if len(parts) < 2 {
		m.message = "格式: t{序号} tag1,tag2"
		m.isError = true
		return m, nil
	}

	num, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		m.message = "无效序号"
		m.isError = true
		return m, nil
	}

	tagStr := strings.TrimSpace(parts[1])
	if tagStr == "" {
		m.message = "标签不能为空"
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

	return m, m.doAddTags(num, tags)
}

func (m Model) handleFavoriteInput(rest string) (tea.Model, tea.Cmd) {
	num, err := strconv.Atoi(strings.TrimSpace(rest))
	if err != nil {
		m.message = "无效序号"
		m.isError = true
		return m, nil
	}
	return m, m.doToggleFavorite(num)
}

func (m Model) handleDeleteInput(rest string) (tea.Model, tea.Cmd) {
	num, err := strconv.Atoi(strings.TrimSpace(rest))
	if err != nil {
		m.message = "无效序号"
		m.isError = true
		return m, nil
	}

	bm := m.getBookmarkByIndex(num)
	if bm == nil {
		m.message = fmt.Sprintf("序号 %d 超出范围", num)
		m.isError = true
		return m, nil
	}

	m.confirmDelete = num
	m.message = fmt.Sprintf("确认删除 %s? [y/其他键取消]", bm.Title)
	m.isError = false
	return m, nil
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

func (m Model) doOpen(index int) tea.Cmd {
	return func() tea.Msg {
		bm := m.getBookmarkByIndex(index)
		if bm == nil {
			return openResultMsg{err: fmt.Errorf("序号 %d 超出范围", index)}
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

func (m Model) doToggleFavorite(index int) tea.Cmd {
	return func() tea.Msg {
		bm := m.getBookmarkByIndex(index)
		if bm == nil {
			return actionResultMsg{message: fmt.Sprintf("序号 %d 超出范围", index), isError: true}
		}

		isFav, err := db.ToggleFavorite(m.db, bm.ID)
		if err != nil {
			return actionResultMsg{message: fmt.Sprintf("操作失败: %v", err), isError: true}
		}

		if isFav {
			return actionResultMsg{message: fmt.Sprintf("✓ ★ 已收藏 %s", bm.Title)}
		}
		return actionResultMsg{message: fmt.Sprintf("✓ ☆ 已取消收藏 %s", bm.Title)}
	}
}

func (m Model) doAddTags(index int, tags []string) tea.Cmd {
	return func() tea.Msg {
		bm := m.getBookmarkByIndex(index)
		if bm == nil {
			return actionResultMsg{message: fmt.Sprintf("序号 %d 超出范围", index), isError: true}
		}

		if err := db.AddTagsToBookmark(m.db, bm.ID, tags); err != nil {
			return actionResultMsg{message: fmt.Sprintf("打标签失败: %v", err), isError: true}
		}

		return actionResultMsg{message: fmt.Sprintf("✓ 已添加标签: %s", strings.Join(tags, ", "))}
	}
}

func (m Model) doDelete(index int) tea.Cmd {
	return func() tea.Msg {
		bm := m.getBookmarkByIndex(index)
		if bm == nil {
			return actionResultMsg{message: fmt.Sprintf("序号 %d 超出范围", index), isError: true}
		}

		if err := db.DeleteBookmark(m.db, bm.ID); err != nil {
			return actionResultMsg{message: fmt.Sprintf("删除失败: %v", err), isError: true}
		}

		return actionResultMsg{message: "✓ 已删除"}
	}
}

// ============================================================
// View 渲染
// ============================================================

func (m Model) View() string {
	var b strings.Builder

	// 顶部：搜索词 + 页码
	totalPages := (m.total + m.pageSize - 1) / m.pageSize
	if totalPages == 0 {
		totalPages = 1
	}
	header := fmt.Sprintf("tsumu -s %s", m.query)
	if m.detailed {
		header = fmt.Sprintf("tsumu -sd %s", m.query)
	}
	pageInfo := fmt.Sprintf("%d/%d 页", m.page+1, totalPages)
	gap := 60 - len(header) - len(pageInfo)
	if gap < 2 {
		gap = 2
	}
	b.WriteString(headerStyle.Render(header + strings.Repeat(" ", gap) + pageInfo))
	b.WriteString("\n\n")

	// 无结果
	if len(m.results) == 0 {
		b.WriteString("  没有找到匹配的书签\n")
	} else {
		start := m.page * m.pageSize
		end := start + m.pageSize
		if end > len(m.results) {
			end = len(m.results)
		}
		pageResults := m.results[start:end]

		for i, bm := range pageResults {
			globalIdx := start + i + 1

			if m.detailed {
				b.WriteString(m.renderDetailed(globalIdx, &bm))
			} else {
				b.WriteString(m.renderDefault(globalIdx, &bm))
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

	// 操作提示
	if m.confirmDelete < 0 {
		b.WriteString(helpStyle.Render("  [n] 打开  [t+n] 标签  [f+n] 收藏  [d+n] 删除"))
		b.WriteString("\n")
		b.WriteString(helpStyle.Render("  [j] 下一页  [k] 上一页  [q] 退出"))
		b.WriteString("\n")
	}

	// 输入缓冲区
	if m.input != "" {
		b.WriteString(fmt.Sprintf("\n  > %s", m.input))
	}

	return b.String()
}

func (m Model) renderDefault(idx int, bm *db.Bookmark) string {
	var b strings.Builder

	idxStr := indexStyle.Render(fmt.Sprintf("%d", idx))
	star := "  "
	if bm.IsFavorite {
		star = starStyle.Render("★ ")
	}
	title := titleStyle.Render(truncate(bm.Title, 35))
	domain := domainStyle.Render(truncate(bm.SiteName, 20))
	clicks := clickStyle.Render(fmt.Sprintf("×%d", bm.ClickCount))

	b.WriteString(fmt.Sprintf("  %s %s%s  %s  %s\n", idxStr, star, title, domain, clicks))

	if bm.Note != "" {
		b.WriteString(fmt.Sprintf("       %s\n", noteStyle.Render(truncate(bm.Note, 55))))
	}

	return b.String()
}

func (m Model) renderDetailed(idx int, bm *db.Bookmark) string {
	var b strings.Builder

	idxStr := indexStyle.Render(fmt.Sprintf("%d", idx))
	star := "  "
	if bm.IsFavorite {
		star = starStyle.Render("★ ")
	}
	title := titleStyle.Render(bm.Title)
	b.WriteString(fmt.Sprintf("  %s %s%s\n", idxStr, star, title))

	b.WriteString(fmt.Sprintf("       %s\n", domainStyle.Render(bm.URL)))

	if bm.Description != "" {
		b.WriteString(fmt.Sprintf("       %s\n", truncate(bm.Description, 55)))
	}

	if bm.Note != "" {
		b.WriteString(fmt.Sprintf("       %s\n", noteStyle.Render("📝 "+bm.Note)))
	}

	if bm.Tags != "" {
		b.WriteString(fmt.Sprintf("       %s\n", tagStyle.Render("tags: "+bm.Tags)))
	}

	date := ""
	if len(bm.CreatedAt) >= 10 {
		date = bm.CreatedAt[:10]
	}
	metaLine := fmt.Sprintf("added: %s    clicks: ×%d    source: %s", date, bm.ClickCount, bm.Source)
	b.WriteString(fmt.Sprintf("       %s\n", metaStyle.Render(metaLine)))

	return b.String()
}

// ============================================================
// 辅助函数
// ============================================================

func (m Model) getBookmarkByIndex(index int) *db.Bookmark {
	arrayIdx := index - 1
	if arrayIdx < 0 || arrayIdx >= len(m.results) {
		return nil
	}
	return &m.results[arrayIdx]
}

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
