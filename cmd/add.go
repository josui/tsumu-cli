// cli/cmd/add.go

// add.go 实现 tsumu -a <url> 添加书签的命令逻辑。

package cmd

import (
	"fmt"
	neturl "net/url"
	"strings"

	"github.com/josui/tsumu-cli/internal/db"
	"github.com/josui/tsumu-cli/internal/meta"
)

var (
	addTags string
	addNote string
)

func init() {
	addCmd.Flags().StringVarP(&addTags, "tags", "t", "", "comma-separated tags")
	addCmd.Flags().StringVarP(&addNote, "note", "n", "", "note for the bookmark")
}

// cleanURL 去除 URL 中的 query 参数和 fragment。
func cleanURL(raw string) string {
	u, err := neturl.Parse(raw)
	if err != nil {
		return raw
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// runAdd 执行添加书签流程：抓元数据 → 写数据库 → 打印结果。
func runAdd(rawURL string, note string, tags string) error {
	cleanedURL := cleanURL(rawURL)

	fmt.Print("  ⠋ Fetching page info...\r") // \r 回到行首，下一行输出会覆盖这行（简易 spinner）

	// 抓取网页元数据（用原始 URL 抓取，保证页面能正确加载）
	metadata, err := meta.Fetch(rawURL)
	if err != nil {
		// 元数据抓取失败不算致命错误，用 URL 域名兜底
		fmt.Printf("  ⚠ Metadata fetch failed: %v\n", err)
		metadata = &meta.Metadata{
			Title:    cleanedURL,
			SiteName: cleanedURL,
		}
	}

	// 写入数据库（存储清理后的 URL）
	bm, err := db.CreateBookmark(Store.DB, cleanedURL, metadata.Title, metadata.Description, metadata.SiteName, note)
	if err != nil {
		return fmt.Errorf("save failed: %w", err)
	}

	// 打印成功消息
	displayName := bm.Title
	if displayName == "" {
		displayName = bm.URL
	}
	fmt.Printf("  ✓ Saved: %s (%s)\n", displayName, bm.SiteName)

	// 添加标签
	if tags != "" {
		var tagList []string
		for _, t := range strings.Split(tags, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				tagList = append(tagList, t)
			}
		}
		if len(tagList) > 0 {
			if err := db.AddTagsToBookmark(Store.DB, bm.ID, tagList); err != nil {
				return fmt.Errorf("tag failed: %w", err)
			}
			fmt.Printf("  ✓ Tagged: %s\n", strings.Join(tagList, ", "))
		}
	}

	return nil
}
