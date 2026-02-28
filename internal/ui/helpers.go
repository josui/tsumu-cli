package ui

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/mattn/go-runewidth"
)

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

// renderFieldWithCursor 渲染带光标的输入字段内容（带视口滚动）。
// maxWidth 为字段可见宽度（display columns）。当文字超长时，
// 只显示光标附近的文字，模拟 web input 的水平滚动效果。
// 光标处字符用反色样式，末尾时显示反色空格。闪烁周期由 cursorVisible 控制。
func (m Model) renderFieldWithCursor(value string, maxWidth int) string {
	runes := []rune(value)
	pos := m.inputPos
	// 防御：clamp 到合法范围
	if pos < 0 {
		pos = 0
	}
	if pos > len(runes) {
		pos = len(runes)
	}

	// 光标处字符（末尾时为空格）
	cursorChar := " "
	cursorW := 1
	if pos < len(runes) {
		cursorChar = string(runes[pos])
		cursorW = runewidth.StringWidth(cursorChar)
	}

	// 计算视口：确保光标字符完整可见
	// 策略：从光标处往左尽量填满 maxWidth
	// beforeW = 光标左侧可显示的最大宽度
	beforeW := maxWidth - cursorW
	if beforeW < 0 {
		beforeW = 0
	}

	// 从 pos 往左扫描，找到视口起始 rune 索引
	viewStart := pos
	usedW := 0
	for viewStart > 0 {
		cw := runewidth.RuneWidth(runes[viewStart-1])
		if usedW+cw > beforeW {
			break
		}
		usedW += cw
		viewStart--
	}

	// 从 pos+1 往右扫描，填满剩余宽度
	afterBudget := maxWidth - usedW - cursorW
	viewEnd := pos + 1
	for viewEnd < len(runes) && afterBudget > 0 {
		cw := runewidth.RuneWidth(runes[viewEnd])
		if cw > afterBudget {
			break
		}
		afterBudget -= cw
		viewEnd++
	}

	// 构建可见部分
	before := string(runes[viewStart:pos])
	after := ""
	if pos+1 < viewEnd {
		after = string(runes[pos+1 : viewEnd])
	}

	var cursor string
	if m.cursorVisible {
		cursor = inputCursorStyle.Render(cursorChar)
	} else {
		cursor = cursorChar
	}
	return before + cursor + after
}

// truncateField 截断非聚焦字段的文字（左对齐，右侧截断）。
// 与 truncate() 不同，不加 ".." 后缀，保持 input 的简洁外观。
func truncateField(s string, maxWidth int) string {
	if runewidth.StringWidth(s) <= maxWidth {
		return s
	}
	return runewidth.Truncate(s, maxWidth, "")
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
