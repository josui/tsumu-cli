// cli/internal/ui/keys.go

// keys.go 定义 TUI 的键位绑定常量。
// bubbletea 通过 tea.KeyMsg 传递按键事件，这里集中定义键名。

package ui

// 按键常量
// bubbletea 的 KeyMsg.String() 返回这些字符串
const (
	keyQuit     = "q"
	keyEsc      = "esc"
	keyNextPage = "j"
	keyPrevPage = "k"
	keyEnter    = "enter"
)
