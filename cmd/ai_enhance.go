// cli/cmd/ai_enhance.go

// ai_enhance.go 实现单条书签 AI 增强的隐藏命令。
// 由 add 命令通过子进程调用，不暴露在 help 中。

package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/josui/tsumu-cli/internal/ai"
	"github.com/josui/tsumu-cli/internal/db"
)

var enhanceID string

var aiEnhanceCmd = &cobra.Command{
	Use:    "ai-enhance",
	Short:  "Enhance a single bookmark with AI (internal)",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if enhanceID == "" {
			return fmt.Errorf("--id is required")
		}
		return runAiEnhance(enhanceID)
	},
}

func init() {
	aiEnhanceCmd.Flags().StringVar(&enhanceID, "id", "", "bookmark ID to enhance")
	rootCmd.AddCommand(aiEnhanceCmd)
}

// runAiEnhance 对单条书签执行 AI 增强（描述 + 关键词 + 标签推荐）。
// AI 未配置或书签不存在时静默退出（exit 0），不影响调用方。
func runAiEnhance(id string) error {
	if Cfg == nil || !Cfg.AI.IsConfigured() {
		return nil // AI 未配置，静默退出
	}

	// 查询书签信息
	bm, err := db.GetBookmarkByID(Store.DB, id)
	if err != nil {
		return fmt.Errorf("bookmark not found: %w", err)
	}
	if bm == nil {
		return nil // 书签不存在（已删除等），静默退出
	}

	client := ai.NewClient(Cfg.AI.GetAPIKey(), Cfg.AI.GetGenModel())
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	allTags, _ := db.ListAllTags(Store.DB)

	result, err := client.EnhanceBookmark(ctx, bm.Title, bm.URL, bm.SiteName, allTags, Cfg.AI.GetLang())
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠ AI enhance failed: %v\n", err)
		return nil // 不返回 error，避免子进程 exit code 非 0
	}

	// 写入 ai_note（description + keywords 拼接）
	if result.Description != "" {
		note := ai.FormatAiNote(result.Description, result.Keywords)
		_ = db.UpdateAiNote(Store.DB, bm.ID, note)
	}

	// AI 标签推荐
	if len(result.Tags) > 0 {
		_ = db.AddTagsToBookmark(Store.DB, bm.ID, result.Tags)
	}

	return nil
}
