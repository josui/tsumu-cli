// cli/internal/ui/model.go

// model.go 是 bubbletea TUI 的核心状态和事件循环。
// 按功能拆分为多个文件：
// - model.go: Model 定义、消息类型、Init/Update
// - handlers.go: 按键处理（普通模式、标签、note、文本输入）
// - overlays.go: Overlay 按键处理（命令面板、表单、配置）
// - commands.go: 异步命令（tea.Cmd）
// - views.go: View 渲染
// - helpers.go: 辅助函数

package ui

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/josui/tsumu-cli/config"
	"github.com/josui/tsumu-cli/internal/ai"
	"github.com/josui/tsumu-cli/internal/db"
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
	{name: "find", desc: "Search bookmarks", category: "Navigation"},
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
	height     int    // 终端高度

	// Tag autocomplete
	allTags     []string // all existing tags (loaded on modeTag entry)
	suggestions []string // current matching suggestions
	selectedSug int      // selected suggestion index

	// AI query expansion
	aiExpanding bool // 是否正在 AI 展开中

	// Overlay 状态
	overlay         overlayType // 当前显示的 overlay
	cmdFilter       string      // 命令面板搜索框输入
	cmdFiltered     []int       // 过滤后的 commands 索引
	cmdCursor       int         // 命令面板中的光标位置
	cmdScrollOffset int         // 命令列表的滚动偏移（第一个可见条目的索引）

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
		height:     24, // 默认值，WindowSizeMsg 到达后会更新
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
// Init / Update
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
		m.height = msg.Height
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
