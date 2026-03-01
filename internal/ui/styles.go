// cli/internal/ui/styles.go

// Package ui 实现 tsumu 的终端交互界面（TUI）。
// 使用 bubbletea 做交互循环，lipgloss 做样式渲染。
package ui

import "github.com/charmbracelet/lipgloss"

// 终端颜色定义
// lipgloss.Color() 接受 hex 或 ANSI 颜色码
var (
	colorPrimary = lipgloss.Color("#D4A574") // 暖色主调，呼应 macOS App 的 Warm Paper 主题
	colorDim     = lipgloss.Color("#888888") // 次要信息
	colorSuccess = lipgloss.Color("#A8C97F") // 成功提示
	colorStar    = lipgloss.Color("#E6B450") // 收藏星标
	colorError   = lipgloss.Color("#D45B5B") // 错误
)

// 样式定义
// lipgloss.NewStyle() 返回一个不可变的样式对象，链式调用设置属性
var (
	titleStyle        = lipgloss.NewStyle().Bold(true)
	focusTitleStyle   = lipgloss.NewStyle().Bold(true).Foreground(colorPrimary) // 选中项标题高亮
	cursorStyle       = lipgloss.NewStyle().Foreground(colorPrimary)            // 光标指示符 ▸
	domainStyle       = lipgloss.NewStyle().Foreground(colorDim)
	clickStyle        = lipgloss.NewStyle().Foreground(colorDim)
	noteStyle         = lipgloss.NewStyle().Foreground(colorDim).Italic(true)
	starStyle         = lipgloss.NewStyle().Foreground(colorStar)
	indexStyle        = lipgloss.NewStyle().Width(2).Align(lipgloss.Right)
	dividerStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#444444"))
	messageStyle      = lipgloss.NewStyle().Foreground(colorSuccess)
	errorMessageStyle = lipgloss.NewStyle().Foreground(colorError)
	helpStyle         = lipgloss.NewStyle().Foreground(colorDim)
	headerStyle       = lipgloss.NewStyle().Foreground(colorPrimary).Bold(true)
	tagStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("#7DAEA3"))
	dimTitleStyle     = lipgloss.NewStyle().Foreground(colorDim) // 不相关标记：灰掉 title
	inputCursorStyle  = lipgloss.NewStyle().Reverse(true) // 输入光标：反色显示
	// Dropdown 样式（tag autocomplete）
	dropdownBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.NormalBorder()).
				BorderForeground(colorDim).
				Padding(0, 1)
	dropdownSelStyle = lipgloss.NewStyle().Background(lipgloss.Color("#444444")) // 选中项：灰底，避免纯白过亮
)

// Overlay 样式
var (
	overlayBorderStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorDim).
		Padding(1, 2)
	overlayTitleStyle  = lipgloss.NewStyle().Bold(true).Foreground(colorPrimary)
	overlayHintStyle   = lipgloss.NewStyle().Foreground(colorDim)
	overlayCmdStyle    = lipgloss.NewStyle()
	overlayCmdSelStyle = lipgloss.NewStyle().Foreground(colorPrimary).Bold(true)
	overlayCatStyle    = lipgloss.NewStyle().Foreground(colorPrimary).Bold(true)
	overlayInputStyle  = lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(colorDim).
		Padding(0, 1)
	overlayLabelStyle = lipgloss.NewStyle().Foreground(colorDim)
	overlayFieldStyle = lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(colorDim).
		Padding(0, 1)
	overlayFieldFocusStyle = lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(colorPrimary).
		Padding(0, 1)
	overlaySelectedStyle = lipgloss.NewStyle().Foreground(colorPrimary).Bold(true)
)
