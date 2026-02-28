// cli/cmd/ai.go

// ai.go 实现 tsumu ai 批量 AI 增强命令。
// 对已有书签补充缺失的 ai_note 和推荐标签。
// 使用 EnhanceBookmark 合并 prompt（1 次 API 调用 = 描述 + 标签），
// 并发 3 个 goroutine 同时处理，大幅缩短批量处理时间。

package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"

	"github.com/spf13/cobra"

	"github.com/josui/tsumu-cli/internal/ai"
	"github.com/josui/tsumu-cli/internal/db"
)

// 并发数量：Gemini 免费 tier 的 rate limit 约 15 RPM，
// 3 并发 × ~3s/请求 ≈ 1 RPM，留足余量。
const aiConcurrency = 3

var aiEmpty bool

var aiCmd = &cobra.Command{
	Use:   "ai",
	Short: "Enhance existing bookmarks with AI (description + tags)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAI()
	},
}

func init() {
	aiCmd.Flags().BoolVar(&aiEmpty, "empty", false, "only process bookmarks without ai_note")
}

// aiResult 是单个书签增强的结果，用于 channel 传递。
type aiResult struct {
	index     int
	title     string
	descAdded bool
	tagsAdded int
	err       error
}

func runAI() error {
	if Cfg == nil || !Cfg.AI.IsConfigured() {
		return fmt.Errorf("AI not configured. Run `tsumu config --ai` first")
	}

	client := ai.NewClient(Cfg.AI.GetAPIKey(), Cfg.AI.GetGenModel())

	// 获取需要增强的书签（--empty 时只跑 ai_note 为空的）
	bookmarks, err := db.ListBookmarksForAI(Store.DB, aiEmpty)
	if err != nil {
		return fmt.Errorf("list bookmarks failed: %w", err)
	}

	if len(bookmarks) == 0 {
		fmt.Println("  No bookmarks found.")
		return nil
	}

	// 获取已有 tag 库
	allTags, _ := db.ListAllTags(Store.DB)

	fmt.Printf("  Enhancing %d bookmarks (%d concurrent)...\n\n", len(bookmarks), aiConcurrency)

	// 支持 Ctrl+C 中断
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		cancel()
	}()

	// 统计（atomic 保证并发安全）
	var enhanced, skipped, descsGenerated, tagsAdded atomic.Int32

	// semaphore 控制并发数
	sem := make(chan struct{}, aiConcurrency)
	var wg sync.WaitGroup
	// 结果 channel，用于按完成顺序打印
	results := make(chan aiResult, len(bookmarks))

	for i, bm := range bookmarks {
		// 检查中断
		if ctx.Err() != nil {
			break
		}

		wg.Add(1)
		go func(idx int, bm db.BookmarkBrief) {
			defer wg.Done()

			// 获取 semaphore slot
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}

			if ctx.Err() != nil {
				return
			}

			res := aiResult{index: idx, title: bm.Title}
			if res.title == "" {
				res.title = bm.URL
			}

			// 一次 API 调用：描述 + 标签
			result, err := client.EnhanceBookmark(ctx, bm.Title, bm.URL, bm.SiteName, allTags)
			if err != nil {
				res.err = err
				skipped.Add(1)
				results <- res
				return
			}

			did := false

			// 写入 ai_note
			if bm.AiNote == "" && result.Description != "" {
				if err := db.UpdateAiNote(Store.DB, bm.ID, result.Description); err == nil {
					descsGenerated.Add(1)
					did = true
				}
			}

			// 写入标签
			if len(result.Tags) > 0 {
				if err := db.AddTagsToBookmark(Store.DB, bm.ID, result.Tags); err == nil {
					tagsAdded.Add(1)
					res.tagsAdded = len(result.Tags)
					did = true
				}
			}

			if did {
				enhanced.Add(1)
				res.descAdded = bm.AiNote == "" && result.Description != ""
			}

			results <- res
		}(i, bm)
	}

	// 关闭 results channel
	go func() {
		wg.Wait()
		close(results)
	}()

	// 按完成顺序打印结果
	total := len(bookmarks)
	printed := 0
	for res := range results {
		printed++
		if res.err != nil {
			fmt.Printf("  [%d/%d] ✗ %s (%v)\n", printed, total, truncateStr(res.title, 50), res.err)
		} else if res.descAdded || res.tagsAdded > 0 {
			fmt.Printf("  [%d/%d] ✓ %s\n", printed, total, truncateStr(res.title, 50))
		} else {
			fmt.Printf("  [%d/%d] - %s (no changes)\n", printed, total, truncateStr(res.title, 50))
		}
	}

	if ctx.Err() != nil {
		remaining := total - printed
		if remaining > 0 {
			fmt.Printf("\n  Interrupted. %d remaining.\n", remaining)
			fmt.Println("  Run `tsumu ai` again to continue.")
		}
	}

	fmt.Printf("\n  Done: %d enhanced, %d skipped\n", enhanced.Load(), skipped.Load())
	fmt.Printf("  - AI notes generated: %d\n", descsGenerated.Load())
	fmt.Printf("  - Tags added: %d\n", tagsAdded.Load())

	return nil
}

// truncateStr 截断字符串到指定长度。
func truncateStr(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-2]) + ".."
}
