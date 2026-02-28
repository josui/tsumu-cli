// cli/internal/ai/expand.go

// expand.go 实现 AI 搜索查询展开。
// 将自然语言查询拆解为 2-5 个搜索关键词，用于 FTS5/LIKE 多次搜索后合并结果。

package ai

import (
	"context"
	"encoding/json"
	"fmt"
)

// ExpandQuery 将自然语言查询展开为搜索关键词列表。
// 返回 2-5 个关键词，用于分别搜索后合并去重。
func (c *Client) ExpandQuery(ctx context.Context, query string) ([]string, error) {
	prompt := fmt.Sprintf(`你是一个书签搜索助手。将以下搜索查询拆解为 2-5 个搜索关键词，用于在书签标题、描述、标签中搜索。关键词应该覆盖查询的不同方面和同义词。返回 JSON 数组格式，例如 ["keyword1", "keyword2"]。每个关键词尽量简短（1-3个词）。

查询: %s`, query)

	result, err := c.GenerateJSON(ctx, prompt)
	if err != nil {
		return nil, err
	}

	var keywords []string
	if err := json.Unmarshal([]byte(result), &keywords); err != nil {
		return nil, fmt.Errorf("parse keywords JSON: %w", err)
	}

	// 限制最多 5 个
	if len(keywords) > 5 {
		keywords = keywords[:5]
	}
	return keywords, nil
}
