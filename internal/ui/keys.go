// cli/internal/ui/keys.go

// keys.go 定义 TUI 的键位绑定常量。
// bubbletea 通过 tea.KeyMsg 传递按键事件，这里集中定义键名。

package ui

// 按键常量
// bubbletea 的 KeyMsg.String() 返回这些字符串
const (
	keyQuit  = "q"
	keyEsc   = "esc"
	keyDown  = "j" // 光标下移
	keyUp    = "k" // 光标上移
	keyEnter = "enter"
	keyTag   = "t" // 给选中项打标签
	keyFav   = "f" // 收藏/取消收藏选中项
	keyDel        = "d" // 删除选中项
	keyNote       = "n" // 编辑选中项的 note（内联）
	keyNoteEditor = "N" // 用外部编辑器编辑 note
)
