// cli/internal/ai/enhance.go

// enhance.go 实现 AI 书签增强功能。
// EnhanceBookmark: 一次 API 调用同时生成描述 + 推荐标签（主要入口）。
// GenerateDescription / SuggestTags: 单独调用，保留给特定场景使用。

package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// EnhanceResult 是 EnhanceBookmark 的返回结果。
type EnhanceResult struct {
	Description string   `json:"description"` // AI 生成的摘要（≤100 字）
	Keywords    []string `json:"keywords"`    // 多语言搜索关键词
	Tags        []string `json:"tags"`        // 从已有 tag 库中推荐的标签（1-3 个）
}

// buildLangInstruction 根据 lang 配置生成 prompt 中的语言指令部分。
// 返回 description 使用的语言名 和 keywords 使用的语言列表文本。
func buildLangInstruction(lang string) (descLang string, keywordLang string) {
	langs := strings.Split(lang, ",")
	langNames := map[string]string{
		"en": "English", "zh": "中文", "ja": "日本語", "ko": "한국어",
	}

	// 第一个语言用于 description
	if name, ok := langNames[strings.TrimSpace(langs[0])]; ok {
		descLang = name
	} else {
		descLang = "English"
	}

	// 所有语言用于 keywords
	var kwLangs []string
	for _, l := range langs {
		l = strings.TrimSpace(l)
		if name, ok := langNames[l]; ok {
			kwLangs = append(kwLangs, name)
		}
	}
	if len(kwLangs) == 0 {
		kwLangs = []string{"English"}
	}
	keywordLang = strings.Join(kwLangs, " and ")
	return
}

// FormatAiNote 将 description 和 keywords 拼接为 ai_note 存储格式。
// 格式：description\n\nkeyword1, keyword2, keyword3
func FormatAiNote(description string, keywords []string) string {
	if description == "" {
		return ""
	}
	if len(keywords) == 0 {
		return description
	}
	return description + "\n\n" + strings.Join(keywords, ", ")
}

// EnhanceBookmark 一次 API 调用同时生成描述、关键词和推荐标签。
// existingTags 为空时只生成描述和关键词，不推荐标签。
// lang 控制描述和关键词的语言（如 "en", "zh,en"）。
func (c *Client) EnhanceBookmark(ctx context.Context, title, url, siteName string, existingTags []string, lang string) (*EnhanceResult, error) {
	descLang, keywordLang := buildLangInstruction(lang)

	var tagInstruction string
	if len(existingTags) > 0 {
		tagList := strings.Join(existingTags, ", ")
		tagInstruction = fmt.Sprintf(`
3. 从以下已有标签列表中，选择 1-3 个最相关的标签放入 "tags" 数组。只能从列表中选择，不要创造新标签。如果没有相关标签，返回空数组。
已有标签: %s`, tagList)
	} else {
		tagInstruction = `
3. "tags" 返回空数组 []。`
	}

	prompt := fmt.Sprintf(`你是一个书签管理助手。根据以下网页信息完成任务，以 JSON 格式返回结果 {"description": "...", "keywords": ["..."], "tags": ["..."]}:

1. 用%s写一句简短描述（不超过100字），说明这个网页的内容和用途，放入 "description" 字段。不要加引号或其他格式。

2. 生成 3-5 个%s搜索关键词，放入 "keywords" 数组。关键词应覆盖网页的核心主题，方便用户用不同语言搜索到这个书签。
%s

网页信息:
标题: %s
URL: %s
网站: %s`, descLang, keywordLang, tagInstruction, title, url, siteName)

	result, err := c.GenerateJSON(ctx, prompt)
	if err != nil {
		return nil, err
	}

	var resp EnhanceResult
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		return nil, fmt.Errorf("parse enhance JSON: %w", err)
	}

	// 清理描述
	resp.Description = strings.TrimSpace(resp.Description)
	resp.Description = strings.Trim(resp.Description, "\"'")

	// 验证返回的 tag 确实在已有列表中
	if len(existingTags) > 0 && len(resp.Tags) > 0 {
		tagSet := make(map[string]bool, len(existingTags))
		for _, t := range existingTags {
			tagSet[t] = true
		}
		var valid []string
		for _, t := range resp.Tags {
			if tagSet[t] {
				valid = append(valid, t)
			}
		}
		resp.Tags = valid
	} else {
		resp.Tags = nil
	}

	return &resp, nil
}

// GenerateDescription 用 AI 生成书签描述。
// 输入：title、URL、siteName。输出：1-2 句中文摘要（≤100 字）。
func (c *Client) GenerateDescription(ctx context.Context, title, url, siteName string) (string, error) {
	prompt := fmt.Sprintf(`你是一个书签管理助手。根据以下网页信息，用中文写一句简短描述（不超过100字），说明这个网页的内容和用途。只输出描述文本，不要加引号或其他格式。

标题: %s
URL: %s
网站: %s`, title, url, siteName)

	result, err := c.Generate(ctx, prompt)
	if err != nil {
		return "", err
	}

	// 清理可能的引号和换行
	result = strings.TrimSpace(result)
	result = strings.Trim(result, "\"'")
	return result, nil
}

// SuggestTags 从已有 tag 库中推荐相关标签。
// existingTags 是数据库中所有已有的 tag 名称列表。
// 返回推荐的 tag 名称列表（1-3 个）。
func (c *Client) SuggestTags(ctx context.Context, title, url, description string, existingTags []string) ([]string, error) {
	if len(existingTags) == 0 {
		return nil, nil
	}

	tagList := strings.Join(existingTags, ", ")
	prompt := fmt.Sprintf(`你是一个书签管理助手。从以下已有标签列表中，选择 1-3 个最相关的标签。只能从列表中选择，不要创造新标签。返回 JSON 数组格式，例如 ["tag1", "tag2"]。如果没有相关标签，返回空数组 []。

已有标签: %s

网页信息:
标题: %s
URL: %s
描述: %s`, tagList, title, url, description)

	result, err := c.GenerateJSON(ctx, prompt)
	if err != nil {
		return nil, err
	}

	var tags []string
	if err := json.Unmarshal([]byte(result), &tags); err != nil {
		return nil, fmt.Errorf("parse tags JSON: %w", err)
	}

	// 验证返回的 tag 确实在已有列表中
	tagSet := make(map[string]bool, len(existingTags))
	for _, t := range existingTags {
		tagSet[t] = true
	}

	var valid []string
	for _, t := range tags {
		if tagSet[t] {
			valid = append(valid, t)
		}
	}
	return valid, nil
}
