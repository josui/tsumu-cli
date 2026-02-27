// cli/cmd/onboarding.go

// onboarding.go 处理首次运行的引导流程。
// 当 config.toml 不存在时触发，询问用户是否启用云端同步。

package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// RunOnboarding 执行首次运行引导。返回用户是否选择了启用 sync。
func RunOnboarding() bool {
	fmt.Println()
	fmt.Println("  Welcome to tsumu — local-first bookmark manager.")
	fmt.Println()
	fmt.Println("  Would you like to enable cloud sync?")
	fmt.Println("  Sync lets you access bookmarks from multiple machines via Turso.")
	fmt.Println()
	fmt.Println("  1. Enable sync now (need Turso URL + token)")
	fmt.Println("  2. Skip — use local only")
	fmt.Println()
	fmt.Println("  You can enable sync later with: tsumu sync --setup")
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)
	fmt.Print("> ")
	input, _ := reader.ReadString('\n')
	choice := strings.TrimSpace(input)

	if choice == "1" {
		return true
	}

	fmt.Println("  Ready. Local mode.")
	fmt.Println()
	fmt.Println("  Try: tsumu add https://example.com this is an example")
	fmt.Println()
	return false
}
