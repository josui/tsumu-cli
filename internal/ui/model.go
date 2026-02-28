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
	"context"
	"database/sql"
	"fmt"
	neturl "net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"github.com/josui/tsumu-cli/config"
	"github.com/josui/tsumu-cli/internal/ai"
	"github.com/josui/tsumu-cli/internal/db"
	"github.com/josui/tsumu-cli/internal/meta"
	"github.com/josui/tsumu-cli/internal/sync"
)

// ============================================================
// 输入模式
// ============================================================

// inputMode 表示当前的输入模式
type inputMode int

const (
	modeNormal  inputMode = iota // 普通模式：j/k/Enter/d/f/t/q 即时操作
	modeTag                      // 标签输入模式：输入标签文本，Enter 提交
	modeNote                     // Note 编辑模式：输入 note 文本，Enter 提交
	modeConfirm                  // 删除确认模式：y 确认，其他键取消
)

// overlayType 表示当前显示的 overlay 类型
type overlayType int

const (
	overlayNone       overlayType = iota // 无 overlay
	overlayCommand                       // 命令面板
	overlayAddForm                       // 添加书签表单
	overlayConfigAI                      // AI 配置表单
	overlayConfigSync                    // Sync 配置表单
	overlaySyncStatus                    // Sync 状态卡片
)

// command 是命令面板中的一条命令
type command struct {
	name     string // 显示名，如 "add"
	desc     string // 描述，如 "Add new bookmark"
	category string // 分类，如 "Bookmarks"
}

// commands 是所有可用命令（按分类排列）
var commands = []command{
	{name: "add", desc: "Add new bookmark", category: "Bookmarks"},
	{name: "sync", desc: "Pull & push changes", category: "Sync"},
	{name: "sync force", desc: "Full resync", category: "Sync"},
	{name: "sync status", desc: "View sync status", category: "Sync"},
	{name: "ai", desc: "Enhance all bookmarks", category: "AI"},
	{name: "ai empty", desc: "Enhance empty only", category: "AI"},
	{name: "config ai", desc: "Configure AI provider", category: "Config"},
	{name: "config sync", desc: "Configure Turso sync", category: "Config"},
}

// ============================================================
// Model 定义
// ============================================================

// Model 是 TUI 的完整状态。bubbletea 要求实现 Init/Update/View 三个方法。
type Model struct {
	db      *sql.DB        // 数据库连接
	cfg     *config.Config // 配置（AI、Sync、DomainTags 等）
	query   string         // 搜索关键词（空 = 列出全部）
	results []db.Bookmark  // 搜索结果
	total    int           // 结果总数
	pageSize int           // 每页条数

	cursor int       // 当前光标位置（全局索引，0-based）
	mode   inputMode // 当前输入模式
	input  string    // 输入缓冲区（modeTag / modeNote 时使用）
	inputPos int     // 输入光标位置（rune 单位，0 = 最左端）
	blinkID  int     // 闪烁代次，递增后旧定时器自动失效

	favOnly bool   // 仅显示收藏
	since   string // 时间筛选（ISO 时间字符串）
	tag     string // 按标签筛选

	message       string // 底部反馈消息
	isError       bool   // message 是否为错误消息
	cursorVisible bool   // 输入模式光标闪烁状态

	syncStatus string // sync 状态文本（header 显示用）
	lastSynced string // 上次 sync 时间（用于动态刷新 syncStatus）
	width      int    // 终端宽度

	// Tag autocomplete
	allTags     []string // all existing tags (loaded on modeTag entry)
	suggestions []string // current matching suggestions
	selectedSug int      // selected suggestion index

	// AI query expansion
	aiExpanding bool // 是否正在 AI 展开中

	// Overlay 状态
	overlay     overlayType // 当前显示的 overlay
	cmdFilter   string      // 命令面板搜索框输入
	cmdFiltered []int       // 过滤后的 commands 索引
	cmdCursor   int         // 命令面板中的光标位置

	// Add 表单
	addFields     [3]string // 0=URL, 1=Tags, 2=Note
	addFocus      int       // 当前聚焦的字段 (0-2)
	addSubmitting bool      // 正在提交中

	// 后台任务
	bgTaskLabel string // header 右侧显示的任务进度文本（空 = 无任务）

	// Config AI 表单
	aiConfigFields [4]string // 0=Provider, 1=API Key, 2=Gen Model, 3=Lang
	aiConfigFocus  int       // 当前聚焦的字段 (0-3)

	// Config Sync 表单
	syncConfigFields [4]string // 0=URL, 1=Auth Token, 2=Interval, 3=Enabled
	syncConfigFocus  int       // 当前聚焦的字段 (0-3)
}

// NewModel 创建并初始化 Model。
// cfg 注入完整配置，TUI 内可直接读取 AI/Sync/DomainTags 等设置。
func NewModel(database *sql.DB, cfg *config.Config, query string, favOnly bool, since string, tag string, syncStatus string, lastSynced string) Model {
	return Model{
		db:         database,
		cfg:        cfg,
		query:      query,
		favOnly:    favOnly,
		since:      since,
		tag:        tag,
		pageSize:   cfg.GetPageSize(),
		syncStatus: syncStatus,
		lastSynced: lastSynced,
		mode:       modeNormal,
		width:      80, // 默认值，WindowSizeMsg 到达后会更新
	}
}

// contentWidth 返回可用内容宽度（留 2 字符左右边距）
func (m Model) contentWidth() int {
	w := m.width - 2
	if w < 40 {
		w = 40
	}
	return w
}

// overlayWidth 返回 overlay 的内容宽度（总宽度的 70%，最小 44，最大 80，再减去边框+padding 的 6 字符）
// 用法：各 render 函数中 w := m.overlayWidth()，最终 overlayBorderStyle.Width(w).Render(...)
func (m Model) overlayWidth() int {
	total := m.width * 70 / 100
	if total < 44 {
		total = 44
	}
	if total > 80 {
		total = 80
	}
	// 减去 overlayBorderStyle 的水平装饰：Border(2) + Padding(1,2) 即 2+4=6
	w := total - 6
	if w < 38 {
		w = 38
	}
	return w
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

// editorFinishedMsg is sent when the external editor process exits
type editorFinishedMsg struct {
	bookmarkID string // 编辑器启动前捕获，避免返回后光标漂移
	note       string
	err        error
}

// cursorBlinkMsg 触发光标闪烁，携带 blinkID 用于过滤过期定时器
type cursorBlinkMsg struct{ id int }

func cursorBlink(id int) tea.Cmd {
	return tea.Tick(530*time.Millisecond, func(time.Time) tea.Msg {
		return cursorBlinkMsg{id: id}
	})
}

// tagsLoadedMsg is sent when all tags have been loaded from the database
type tagsLoadedMsg struct {
	tags []string
	err  error
}

func (m Model) doLoadAllTags() tea.Cmd {
	return func() tea.Msg {
		tags, err := db.ListAllTags(m.db)
		return tagsLoadedMsg{tags: tags, err: err}
	}
}

// aiExpandMsg 是 AI query expansion 完成后的消息
type aiExpandMsg struct {
	keywords []string
	err      error
}

// copyResultMsg 是复制完成后的消息（不触发 doSearch，避免消息被清掉）
type copyResultMsg struct {
	message string
	isError bool
}

// refetchResultMsg 是 refetch 完成后的消息
type refetchResultMsg struct {
	title string // 更新后的标题（显示用）
	hasAI bool   // 是否执行了 AI 增强
	err   error
}

// addBookmarkMsg 是添加书签完成后的消息
type addBookmarkMsg struct {
	title    string
	siteName string
	bmID     string // 用于后台 AI 增强
	err      error
}

// aiEnhanceDoneMsg 是单个书签后台 AI 增强完成的消息
type aiEnhanceDoneMsg struct {
	title string
	err   error
}

// syncDoneMsg 是 sync 完成后的消息
type syncDoneMsg struct {
	result  sync.Result
	warning string
	err     error
}

// aiBatchDoneMsg 是批量 AI 增强完成后的消息
type aiBatchDoneMsg struct {
	total    int // 处理目标总数
	enhanced int // 成功增强数
	err      error
}

func (m Model) doAIExpand() tea.Cmd {
	query := m.query
	apiKey := m.cfg.AI.GetAPIKey()
	genModel := m.cfg.AI.GetGenModel()
	return func() tea.Msg {
		client := ai.NewClient(apiKey, genModel)
		keywords, err := client.ExpandQuery(context.Background(), query)
		return aiExpandMsg{keywords: keywords, err: err}
	}
}

func (m Model) doExpandedSearch(keywords []string) tea.Cmd {
	return func() tea.Msg {
		seen := make(map[string]bool)
		var allResults []db.Bookmark

		for _, kw := range keywords {
			results, _, err := db.Search(m.db, kw, 50000, 0, m.since, m.favOnly, m.tag)
			if err != nil {
				continue
			}
			for _, bm := range results {
				if !seen[bm.ID] {
					seen[bm.ID] = true
					allResults = append(allResults, bm)
				}
			}
		}

		return searchResultMsg{results: allResults, total: len(allResults), err: nil}
	}
}

// computeSuggestions filters allTags by prefix match on the current token.
// Returns up to 5 matches.
func computeSuggestions(allTags []string, token string) []string {
	if token == "" {
		return nil
	}
	token = strings.ToLower(token)

	var result []string
	for _, tag := range allTags {
		if strings.HasPrefix(strings.ToLower(tag), token) {
			result = append(result, tag)
			if len(result) >= 5 {
				break
			}
		}
	}
	return result
}

// currentTagToken extracts the tag token currently being typed (after last comma).
func currentTagToken(input string) string {
	parts := strings.Split(input, ",")
	return strings.TrimSpace(parts[len(parts)-1])
}

// resetBlink 递增 blinkID 使旧定时器失效，重置光标可见，启动新闪烁周期
func (m *Model) resetBlink() tea.Cmd {
	m.blinkID++
	m.cursorVisible = true
	return cursorBlink(m.blinkID)
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

	case copyResultMsg:
		m.message = msg.message
		m.isError = msg.isError
		return m, nil

	case actionResultMsg:
		m.message = msg.message
		m.isError = msg.isError
		if !msg.isError {
			return m, m.doSearch()
		}
		return m, nil

	case editorFinishedMsg:
		if msg.err != nil {
			m.message = fmt.Sprintf("Editor failed: %v", msg.err)
			m.isError = true
			return m, nil
		}
		return m, m.doUpdateNoteByID(msg.bookmarkID, msg.note)

	case tagsLoadedMsg:
		if msg.err == nil {
			m.allTags = msg.tags
		}
		return m, nil

	case aiExpandMsg:
		m.aiExpanding = false
		if msg.err != nil {
			m.message = "AI expand failed"
			m.isError = true
			return m, nil
		}
		if len(msg.keywords) == 0 {
			m.message = ""
			return m, nil
		}
		// 用展开的关键词搜索，合并去重
		m.message = fmt.Sprintf("AI: %s", strings.Join(msg.keywords, ", "))
		m.isError = false
		return m, m.doExpandedSearch(msg.keywords)

	case refetchResultMsg:
		if msg.err != nil {
			m.message = fmt.Sprintf("Refetch failed: %v", msg.err)
			m.isError = true
			return m, nil
		}
		if msg.hasAI {
			m.message = fmt.Sprintf("✓ Refetched: %s", msg.title)
		} else {
			m.message = fmt.Sprintf("✓ Refetched (no AI): %s", msg.title)
		}
		m.isError = false
		refreshSyncStatus(&m)
		return m, m.doSearch()

	case addBookmarkMsg:
		m.addSubmitting = false
		if msg.err != nil {
			m.message = fmt.Sprintf("Add failed: %v", msg.err)
			m.isError = true
			m.overlay = overlayNone
			return m, nil
		}
		m.overlay = overlayNone
		displayName := msg.title
		if displayName == "" {
			displayName = msg.siteName
		}
		m.message = fmt.Sprintf("✓ Added: %s", displayName)
		m.isError = false

		// 后台 AI 增强
		var aiCmd tea.Cmd
		if m.cfg.AI.IsConfigured() && msg.bmID != "" {
			aiCmd = m.doBackgroundAIEnhance(msg.bmID)
		}
		return m, tea.Batch(m.doSearch(), aiCmd)

	case aiEnhanceDoneMsg:
		if msg.err == nil {
			m.message = fmt.Sprintf("✦ AI enhanced: %s", msg.title)
			m.isError = false
			return m, m.doSearch()
		}
		return m, nil

	case syncDoneMsg:
		m.bgTaskLabel = ""
		if msg.err != nil {
			m.message = fmt.Sprintf("⚠ Sync failed: %v", msg.err)
			m.isError = true
			return m, nil
		}
		pulled := msg.result.PulledNew + msg.result.PulledUpdated
		pushed := msg.result.PushedNew + msg.result.PushedUpdated
		if pulled > 0 || pushed > 0 {
			m.message = fmt.Sprintf("✓ Synced: ↓%d ↑%d", pulled, pushed)
		} else {
			m.message = "✓ Already up to date"
		}
		if msg.warning != "" {
			m.message += " ⚠ " + msg.warning
		}
		m.isError = false
		m.lastSynced = m.cfg.Sync.LastSynced
		refreshSyncStatus(&m)
		return m, m.doSearch()

	case aiBatchDoneMsg:
		m.bgTaskLabel = ""
		if msg.err != nil {
			m.message = fmt.Sprintf("⚠ AI batch failed: %v", msg.err)
			m.isError = true
			return m, nil
		}
		if msg.total == 0 {
			m.message = "✓ No bookmarks to enhance"
		} else {
			m.message = fmt.Sprintf("✓ AI enhanced %d/%d bookmarks", msg.enhanced, msg.total)
		}
		m.isError = false
		return m, m.doSearch()

	case cursorBlinkMsg:
		// 只处理当前代次的定时器，旧的自动丢弃
		if msg.id != m.blinkID {
			return m, nil
		}
		// 内联输入模式或 overlay 输入表单中都需要闪烁
		if m.mode == modeTag || m.mode == modeNote ||
			m.overlay == overlayCommand || m.overlay == overlayAddForm ||
			m.overlay == overlayConfigAI || m.overlay == overlayConfigSync {
			m.cursorVisible = !m.cursorVisible
			return m, cursorBlink(m.blinkID)
		}
		return m, nil

	case tea.KeyMsg:
		// overlay 激活时，所有按键路由到 overlay
		if m.overlay != overlayNone {
			return m.handleOverlayKey(msg)
		}
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

	// 打开命令面板
	case keyCommand:
		m.overlay = overlayCommand
		m.cmdFilter = ""
		m.inputPos = 0
		m.cmdFiltered = fuzzyMatch("", commands)
		m.cmdCursor = 0
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
// 统一搜索/命令栏：Enter=搜索书签，Tab=执行命令，↑/↓/j/k=移动光标
func (m Model) handleCommandKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		// 有输入时清空回全列表，无输入时关闭
		if m.cmdFilter != "" {
			m.cmdFilter = ""
			m.inputPos = 0
			m.cmdFiltered = fuzzyMatch("", commands)
			m.cmdCursor = 0
			return m, m.resetBlink()
		}
		m.overlay = overlayNone
		return m, nil

	case tea.KeyEnter:
		// 用输入内容搜索书签
		m.overlay = overlayNone
		m.query = strings.TrimSpace(m.cmdFilter)
		m.cmdFilter = ""
		m.inputPos = 0
		m.cursor = 0
		return m, m.doSearch()

	case tea.KeyTab:
		// 执行命令列表中当前高亮的命令
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
		}
		return m, nil

	case tea.KeyDown:
		if m.cmdCursor < len(m.cmdFiltered)-1 {
			m.cmdCursor++
		}
		return m, nil

	default:
		// j/k 等同于 ↑/↓，与主列表体验统一
		if msg.Type == tea.KeyRunes {
			ch := string(msg.Runes)
			if ch == "j" {
				if m.cmdCursor < len(m.cmdFiltered)-1 {
					m.cmdCursor++
				}
				return m, nil
			}
			if ch == "k" {
				if m.cmdCursor > 0 {
					m.cmdCursor--
				}
				return m, nil
			}
		}
		// 其余按键交给通用文本输入处理（光标移动、删除、字符插入）
		m.input = m.cmdFilter
		result, cmd := m.handleTextInput(msg)
		m2 := result.(Model)
		m2.cmdFilter = m2.input
		m2.input = ""
		m2.cmdFiltered = fuzzyMatch(m2.cmdFilter, commands)
		m2.cmdCursor = 0
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
		return m, m.resetBlink()

	case tea.KeyTab:
		m.addFocus = (m.addFocus + 1) % 3
		m.inputPos = len([]rune(m.addFields[m.addFocus]))
		return m, m.resetBlink()

	case tea.KeyShiftTab:
		m.addFocus = (m.addFocus + 2) % 3
		m.inputPos = len([]rune(m.addFields[m.addFocus]))
		return m, m.resetBlink()

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
	return m, m.resetBlink()
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

// ============================================================
// 异步命令（tea.Cmd）
// ============================================================

func (m Model) doSearch() tea.Cmd {
	return func() tea.Msg {
		results, total, err := db.Search(m.db, m.query, 50000, 0, m.since, m.favOnly, m.tag)
		return searchResultMsg{results: results, total: total, err: err}
	}
}

func (m Model) doOpen() tea.Cmd {
	return func() tea.Msg {
		bm := m.focusedBookmark()
		if bm == nil {
			return openResultMsg{err: fmt.Errorf("no bookmark selected")}
		}

		if err := OpenBrowser(bm.URL); err != nil {
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

func (m Model) doSetTags(tags []string) tea.Cmd {
	bm := m.focusedBookmark()
	if bm == nil {
		return nil
	}
	id := bm.ID

	return func() tea.Msg {
		if err := db.SetBookmarkTags(m.db, id, tags); err != nil {
			return actionResultMsg{message: fmt.Sprintf("Tag failed: %v", err), isError: true}
		}
		return actionResultMsg{message: fmt.Sprintf("✓ Tagged: %s", strings.Join(tags, ", "))}
	}
}

func (m Model) doUpdateNote(note string) tea.Cmd {
	bm := m.focusedBookmark()
	if bm == nil {
		return nil
	}
	return m.doUpdateNoteByID(bm.ID, note)
}

func (m Model) doUpdateNoteByID(id string, note string) tea.Cmd {
	return func() tea.Msg {
		if err := db.UpdateNote(m.db, id, note); err != nil {
			return actionResultMsg{message: fmt.Sprintf("Note update failed: %v", err), isError: true}
		}
		if note == "" {
			return actionResultMsg{message: "✓ Note cleared"}
		}
		return actionResultMsg{message: "✓ Note updated"}
	}
}

// resolveEditor returns the editor command and arguments from $EDITOR, with fallbacks.
// Supports $EDITOR values like "code --wait" or "vim".
func resolveEditor() (string, []string) {
	if editor := os.Getenv("EDITOR"); editor != "" {
		parts := strings.Fields(editor)
		return parts[0], parts[1:]
	}
	for _, name := range []string{"vim", "vi", "nano"} {
		if _, err := exec.LookPath(name); err == nil {
			return name, nil
		}
	}
	return "vi", nil
}

func (m Model) doEditNoteExternal(bookmarkID string, currentNote string) tea.Cmd {
	f, err := os.CreateTemp("", "tsumu-note-*.txt")
	if err != nil {
		return func() tea.Msg {
			return editorFinishedMsg{err: fmt.Errorf("create temp file: %w", err)}
		}
	}
	tmpFile := f.Name()

	if _, err := f.WriteString(currentNote); err != nil {
		f.Close()
		os.Remove(tmpFile)
		return func() tea.Msg {
			return editorFinishedMsg{err: fmt.Errorf("write temp file: %w", err)}
		}
	}
	f.Close()

	editorCmd, editorArgs := resolveEditor()
	args := append(editorArgs, tmpFile)
	c := exec.Command(editorCmd, args...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr

	return tea.ExecProcess(c, func(err error) tea.Msg {
		defer os.Remove(tmpFile)

		if err != nil {
			return editorFinishedMsg{err: err}
		}

		content, readErr := os.ReadFile(tmpFile)
		if readErr != nil {
			return editorFinishedMsg{err: fmt.Errorf("read temp file: %w", readErr)}
		}

		return editorFinishedMsg{bookmarkID: bookmarkID, note: strings.TrimSpace(string(content))}
	})
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

// doCopyURL 复制选中书签的 URL 到系统剪贴板（macOS pbcopy）
func (m Model) doCopyURL() tea.Cmd {
	bm := m.focusedBookmark()
	if bm == nil {
		return nil
	}
	url := bm.URL

	return func() tea.Msg {
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(url)
		if err := cmd.Run(); err != nil {
			return copyResultMsg{message: fmt.Sprintf("Copy failed: %v", err), isError: true}
		}
		return copyResultMsg{message: fmt.Sprintf("✓ Copied: %s", url)}
	}
}

// doRefetch 重新抓取选中书签的元数据并重新生成 AI note。
// 保留用户的 note 和已有 tags 不变。
func (m Model) doRefetch() tea.Cmd {
	bm := m.focusedBookmark()
	if bm == nil {
		return nil
	}
	id := bm.ID
	url := bm.URL
	database := m.db
	apiKey := m.cfg.AI.GetAPIKey()
	genModel := m.cfg.AI.GetGenModel()
	lang := m.cfg.AI.GetLang()

	return func() tea.Msg {
		// 1. 重新抓取网页元数据
		fetched, err := meta.Fetch(url)
		if err != nil {
			return refetchResultMsg{err: fmt.Errorf("fetch failed: %w", err)}
		}

		// 2. 更新元数据（title, description, site_name）
		if err := db.UpdateBookmarkMeta(database, id, fetched.Title, fetched.Description, fetched.SiteName); err != nil {
			return refetchResultMsg{err: fmt.Errorf("update meta failed: %w", err)}
		}

		// 3. AI 增强（如果已配置 API key）
		if apiKey == "" {
			return refetchResultMsg{title: fetched.Title, hasAI: false}
		}

		// 加载所有已有标签（用于 AI 标签推荐）
		allTags, _ := db.ListAllTags(database)

		client := ai.NewClient(apiKey, genModel)
		result, err := client.EnhanceBookmark(context.Background(), fetched.Title, url, fetched.SiteName, allTags, lang)
		if err != nil {
			// AI 失败不阻断，元数据已更新成功
			return refetchResultMsg{title: fetched.Title, hasAI: false}
		}

		// 4. 更新 ai_note（description + keywords 拼接）
		if result.Description != "" {
			note := ai.FormatAiNote(result.Description, result.Keywords)
			_ = db.UpdateAiNote(database, id, note)
		}

		// 5. 追加 AI 推荐标签（不删除用户已有标签）
		if len(result.Tags) > 0 {
			_ = db.AddTagsToBookmark(database, id, result.Tags)
		}

		return refetchResultMsg{title: fetched.Title, hasAI: true}
	}
}

// cleanURL 去除 URL 中的 query 和 fragment 参数
func cleanURL(raw string) string {
	u, err := neturl.Parse(raw)
	if err != nil {
		return raw
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// doAddBookmark 异步添加书签：抓取元数据 → 写数据库 → 自动打 domain tag
func (m Model) doAddBookmark(rawURL, tagsStr, note string) tea.Cmd {
	database := m.db
	cfg := m.cfg
	return func() tea.Msg {
		cleanedURL := cleanURL(rawURL)

		// 抓取元数据
		metadata, err := meta.Fetch(rawURL)
		if err != nil {
			metadata = &meta.Metadata{
				Title:    cleanedURL,
				SiteName: cleanedURL,
			}
		}

		// 写数据库
		bm, err := db.CreateBookmark(database, cleanedURL, metadata.Title, metadata.Description, metadata.SiteName, strings.TrimSpace(note))
		if err != nil {
			return addBookmarkMsg{err: err}
		}

		// 解析 tags
		var tagList []string
		if tagsStr != "" {
			for _, t := range strings.Split(tagsStr, ",") {
				t = strings.TrimSpace(t)
				if t != "" {
					tagList = append(tagList, t)
				}
			}
		}
		// Domain auto-tag
		if cfg != nil && len(cfg.DomainTags) > 0 {
			domain := meta.ExtractDomain(rawURL)
			if autoTag, ok := cfg.DomainTags[domain]; ok && autoTag != "" {
				found := false
				for _, t := range tagList {
					if t == autoTag {
						found = true
						break
					}
				}
				if !found {
					tagList = append(tagList, autoTag)
				}
			}
		}
		if len(tagList) > 0 {
			_ = db.AddTagsToBookmark(database, bm.ID, tagList)
		}

		return addBookmarkMsg{title: bm.Title, siteName: bm.SiteName, bmID: bm.ID}
	}
}

// doBackgroundAIEnhance 异步增强单个书签的 AI note 和 tags
func (m Model) doBackgroundAIEnhance(bookmarkID string) tea.Cmd {
	database := m.db
	apiKey := m.cfg.AI.GetAPIKey()
	genModel := m.cfg.AI.GetGenModel()
	lang := m.cfg.AI.GetLang()
	return func() tea.Msg {
		bm, err := db.GetBookmarkByID(database, bookmarkID)
		if err != nil || bm == nil {
			return aiEnhanceDoneMsg{err: fmt.Errorf("bookmark not found")}
		}

		allTags, _ := db.ListAllTags(database)
		client := ai.NewClient(apiKey, genModel)
		result, err := client.EnhanceBookmark(context.Background(), bm.Title, bm.URL, bm.SiteName, allTags, lang)
		if err != nil {
			return aiEnhanceDoneMsg{title: bm.Title, err: err}
		}

		if result.Description != "" {
			note := ai.FormatAiNote(result.Description, result.Keywords)
			_ = db.UpdateAiNote(database, bookmarkID, note)
		}
		if len(result.Tags) > 0 {
			_ = db.AddTagsToBookmark(database, bookmarkID, result.Tags)
		}

		return aiEnhanceDoneMsg{title: bm.Title}
	}
}

// doAIBatch 异步批量 AI 增强书签
func (m Model) doAIBatch(emptyOnly bool) tea.Cmd {
	database := m.db
	cfg := m.cfg
	return func() tea.Msg {
		bookmarks, err := db.ListBookmarksForAI(database, emptyOnly)
		if err != nil {
			return aiBatchDoneMsg{err: err}
		}
		if len(bookmarks) == 0 {
			return aiBatchDoneMsg{total: 0, enhanced: 0}
		}

		allTags, _ := db.ListAllTags(database)
		apiKey := cfg.AI.GetAPIKey()
		genModel := cfg.AI.GetGenModel()
		lang := cfg.AI.GetLang()
		client := ai.NewClient(apiKey, genModel)

		enhanced := 0
		for _, bm := range bookmarks {
			result, err := client.EnhanceBookmark(context.Background(), bm.Title, bm.URL, bm.SiteName, allTags, lang)
			if err != nil {
				continue
			}
			if result.Description != "" {
				note := ai.FormatAiNote(result.Description, result.Keywords)
				_ = db.UpdateAiNote(database, bm.ID, note)
			}
			if len(result.Tags) > 0 {
				_ = db.AddTagsToBookmark(database, bm.ID, result.Tags)
			}
			enhanced++
		}

		return aiBatchDoneMsg{total: len(bookmarks), enhanced: enhanced}
	}
}

// doSync 异步执行同步操作
func (m Model) doSync(force bool) tea.Cmd {
	database := m.db
	cfg := m.cfg
	return func() tea.Msg {
		client := sync.NewClient(cfg.Sync.GetURL(), cfg.Sync.GetAuthToken())
		ctx := context.Background()

		mode := sync.SyncIncremental
		if force {
			mode = sync.SyncFull
		}

		result, err := sync.SyncAll(ctx, database, client, cfg.Sync.LastSynced, mode, nil)
		if err != nil {
			return syncDoneMsg{err: err}
		}

		// 更新 config（last_synced）
		cfg.Sync.LastSynced = sync.NowUTC()
		cfg.Save()

		return syncDoneMsg{result: result, warning: result.Warning}
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
		if m.favOnly {
			header = "tsumu ★ favorites"
		} else {
			header = "tsumu"
		}
	} else {
		header = fmt.Sprintf("tsumu %s", m.query)
		// AI expand 提示紧跟搜索词，比放在 help 栏更醒目
		if m.cfg.AI.IsConfigured() {
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
		runes := []rune(m.input)
		before := string(runes[:m.inputPos])
		after := ""
		// 光标位置的字符用反色渲染，末尾时显示空格
		cursorChar := " "
		if m.inputPos < len(runes) {
			cursorChar = string(runes[m.inputPos])
			after = string(runes[m.inputPos+1:])
		}
		var cursor string
		if m.cursorVisible {
			cursor = inputCursorStyle.Render(cursorChar)
		} else {
			cursor = cursorChar
		}
		label := "tags"
		if m.mode == modeNote {
			label = "note"
		}
		b.WriteString(fmt.Sprintf("  %s> %s%s%s\n", label, before, cursor, after))

		// Render suggestion dropdown (only in tag mode)
		// Align suggestion text with the current token start position
		if m.mode == modeTag && len(m.suggestions) > 0 {
			token := currentTagToken(m.input)
			tokenCol := 8 + stringWidth(m.input) - stringWidth(token) // 8 = len("  tags> ")
			selPad := strings.Repeat(" ", max(tokenCol-2, 0))         // "→ " is 2 cols, place before token
			normPad := strings.Repeat(" ", tokenCol)
			for i, sug := range m.suggestions {
				if i == m.selectedSug {
					b.WriteString(selPad + suggestSelStyle.Render("→ "+sug) + "\n")
				} else {
					b.WriteString(normPad + suggestStyle.Render(sug) + "\n")
				}
			}
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

	for i := 0; i < 3; i++ {
		b.WriteString(overlayLabelStyle.Render(labels[i]) + "\n")

		var content string
		if i == m.addFocus && !m.addSubmitting {
			// 聚焦字段：显示光标
			content = m.renderFieldWithCursor(m.addFields[i])
		} else if m.addFields[i] == "" && placeholders[i] != "" && !m.addSubmitting {
			content = overlayHintStyle.Render(placeholders[i])
		} else {
			content = m.addFields[i]
		}

		fieldStyle := overlayFieldStyle
		if i == m.addFocus && !m.addSubmitting {
			fieldStyle = overlayFieldFocusStyle
		}

		// 用 lipgloss.Width() 测量可见宽度（ANSI-safe），手动 pad 到 fieldW
		visW := lipgloss.Width(content)
		if visW < fieldW {
			content += strings.Repeat(" ", fieldW-visW)
		}
		b.WriteString(fieldStyle.Render(content) + "\n")

		if i < 2 {
			b.WriteString("\n")
		}
	}

	if m.addSubmitting {
		b.WriteString("\n" + messageStyle.Render("⟳ Fetching metadata..."))
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
			// 聚焦字段：显示光标
			content = m.renderFieldWithCursor(m.aiConfigFields[i])
		} else if m.aiConfigFields[i] == "" && placeholders[i] != "" {
			content = overlayHintStyle.Render(placeholders[i])
		} else {
			content = m.aiConfigFields[i]
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
			// 聚焦字段：显示光标
			content = m.renderFieldWithCursor(m.syncConfigFields[i])
		} else {
			content = m.syncConfigFields[i]
			if content == "" && placeholders[i] != "" {
				content = overlayHintStyle.Render(placeholders[i])
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
		searchContent = m.renderFieldWithCursor(m.cmdFilter)
	}
	b.WriteString(searchContent + "\n\n")

	// 命令列表（按分类分组）
	lastCategory := ""
	displayIdx := 0
	for _, idx := range m.cmdFiltered {
		cmd := commands[idx]
		if cmd.category != lastCategory {
			if lastCategory != "" {
				b.WriteString("\n")
			}
			b.WriteString(overlayCatStyle.Render(cmd.category) + "\n")
			lastCategory = cmd.category
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
		displayIdx++
	}

	// 底部提示
	b.WriteString("\n" + overlayHintStyle.Render("enter search · tab command · esc close"))

	return overlayBorderStyle.Render(b.String())
}

// renderDefault 渲染默认模式的单条书签。isFocused 控制高亮。
//
// 布局：▸ N ★ Title............  domain  ×clicks
//          Note text aligned with title...
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
	if isFocused {
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

// ============================================================
// 辅助函数
// ============================================================

// stringWidth returns the display width of a string, accounting for CJK double-width characters.
func stringWidth(s string) int {
	return runewidth.StringWidth(s)
}

// truncate truncates a string to fit within maxWidth display columns.
// Uses display width (CJK chars = 2 columns) instead of rune count.
func truncate(s string, maxWidth int) string {
	if runewidth.StringWidth(s) <= maxWidth {
		return s
	}
	return runewidth.Truncate(s, maxWidth, "..")
}

// renderFieldWithCursor 渲染带光标的输入字段内容。
// 光标处字符用反色样式，末尾时显示反色空格。闪烁周期由 cursorVisible 控制。
func (m Model) renderFieldWithCursor(value string) string {
	runes := []rune(value)
	pos := m.inputPos
	// 防御：clamp 到合法范围
	if pos < 0 {
		pos = 0
	}
	if pos > len(runes) {
		pos = len(runes)
	}
	before := string(runes[:pos])
	cursorChar := " "
	after := ""
	if pos < len(runes) {
		cursorChar = string(runes[pos])
		after = string(runes[pos+1:])
	}
	var cursor string
	if m.cursorVisible {
		cursor = inputCursorStyle.Render(cursorChar)
	} else {
		cursor = cursorChar
	}
	return before + cursor + after
}

// fuzzyMatch 简单的子串匹配（name 和 desc 都参与）
func fuzzyMatch(query string, cmds []command) []int {
	if query == "" {
		result := make([]int, len(cmds))
		for i := range cmds {
			result[i] = i
		}
		return result
	}
	query = strings.ToLower(query)
	var result []int
	for i, cmd := range cmds {
		if strings.Contains(strings.ToLower(cmd.name), query) ||
			strings.Contains(strings.ToLower(cmd.desc), query) {
			result = append(result, i)
		}
	}
	return result
}

func OpenBrowser(url string) error {
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
