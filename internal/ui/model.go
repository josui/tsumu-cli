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
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"github.com/josui/tsumu-cli/config"
	"github.com/josui/tsumu-cli/internal/ai"
	"github.com/josui/tsumu-cli/internal/db"
	"github.com/josui/tsumu-cli/internal/meta"
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

	case cursorBlinkMsg:
		// 只处理当前代次的定时器，旧的自动丢弃
		if msg.id != m.blinkID {
			return m, nil
		}
		if m.mode == modeTag || m.mode == modeNote {
			m.cursorVisible = !m.cursorVisible
			return m, cursorBlink(m.blinkID)
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
	rightParts := fmt.Sprintf("Page %d/%d", m.page()+1, m.totalPages())
	if m.syncStatus != "" {
		rightParts += "  " + m.syncStatus
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
	if m.mode == modeNormal {
		b.WriteString(helpStyle.Render("  [↵] open  [t] tag  [f] fav  [d] del  [n] note  [c] copy  [r] refetch  [q] quit"))
		b.WriteString("\n")
	}

	return b.String()
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
