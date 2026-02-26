// cli/cmd/add.go

// add.go 实现 tsumu -a <url> 添加书签的命令逻辑。

package cmd

import (
	"fmt"

	"github.com/user/tsumu-cli/internal/db"
	"github.com/user/tsumu-cli/internal/meta"
)

// runAdd 执行添加书签流程：抓元数据 → 写数据库 → 打印结果。
func runAdd(url string) error {
	fmt.Print("  ⠋ 抓取页面信息...\r") // \r 回到行首，下一行输出会覆盖这行（简易 spinner）

	// 抓取网页元数据
	metadata, err := meta.Fetch(url)
	if err != nil {
		// 元数据抓取失败不算致命错误，用 URL 域名兜底
		fmt.Printf("  ⚠ 元数据抓取失败: %v\n", err)
		metadata = &meta.Metadata{
			Title:    url,
			SiteName: url,
		}
	}

	// 写入数据库
	bm, err := db.CreateBookmark(DB, url, metadata.Title, metadata.Description, metadata.SiteName)
	if err != nil {
		return fmt.Errorf("保存失败: %w", err)
	}

	// 打印成功消息
	displayName := bm.Title
	if displayName == "" {
		displayName = bm.URL
	}
	fmt.Printf("  ✓ 已保存: %s (%s)\n", displayName, bm.SiteName)
	return nil
}
