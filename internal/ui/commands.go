package ui

import (
	"context"
	"fmt"
	neturl "net/url"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/josui/tsumu-cli/internal/ai"
	"github.com/josui/tsumu-cli/internal/db"
	"github.com/josui/tsumu-cli/internal/meta"
	"github.com/josui/tsumu-cli/internal/sync"
)

// ============================================================
// 异步命令（tea.Cmd）
// ============================================================

func (m Model) doSearch() tea.Cmd {
	return func() tea.Msg {
		results, total, err := db.Search(m.db, m.query, 50000, 0, m.since, m.favOnly, m.tag)
		return searchResultMsg{results: results, total: total, err: err}
	}
}

func (m Model) doOpen() tea.Cmd {
	return func() tea.Msg {
		bm := m.focusedBookmark()
		if bm == nil {
			return openResultMsg{err: fmt.Errorf("no bookmark selected")}
		}

		if err := OpenBrowser(bm.URL); err != nil {
			return openResultMsg{err: err}
		}

		count, err := db.IncrementClickCount(m.db, bm.ID)
		if err != nil {
			return openResultMsg{err: err}
		}

		displayName := bm.SiteName
		if displayName == "" {
			displayName = bm.Title
		}
		return openResultMsg{title: displayName, count: count}
	}
}

func (m Model) doToggleFavorite() tea.Cmd {
	// 捕获当前书签信息（闭包在异步执行时 model 可能已变）
	bm := m.focusedBookmark()
	if bm == nil {
		return nil
	}
	id := bm.ID
	title := bm.Title

	return func() tea.Msg {
		isFav, err := db.ToggleFavorite(m.db, id)
		if err != nil {
			return actionResultMsg{message: fmt.Sprintf("Action failed: %v", err), isError: true}
		}
		if isFav {
			return actionResultMsg{message: fmt.Sprintf("✓ ★ Favorited %s", title)}
		}
		return actionResultMsg{message: fmt.Sprintf("✓ ☆ Unfavorited %s", title)}
	}
}

func (m Model) doSetTags(tags []string) tea.Cmd {
	bm := m.focusedBookmark()
	if bm == nil {
		return nil
	}
	id := bm.ID

	return func() tea.Msg {
		if err := db.SetBookmarkTags(m.db, id, tags); err != nil {
			return actionResultMsg{message: fmt.Sprintf("Tag failed: %v", err), isError: true}
		}
		return actionResultMsg{message: fmt.Sprintf("✓ Tagged: %s", strings.Join(tags, ", "))}
	}
}

func (m Model) doUpdateNote(note string) tea.Cmd {
	bm := m.focusedBookmark()
	if bm == nil {
		return nil
	}
	return m.doUpdateNoteByID(bm.ID, note)
}

func (m Model) doUpdateNoteByID(id string, note string) tea.Cmd {
	return func() tea.Msg {
		if err := db.UpdateNote(m.db, id, note); err != nil {
			return actionResultMsg{message: fmt.Sprintf("Note update failed: %v", err), isError: true}
		}
		if note == "" {
			return actionResultMsg{message: "✓ Note cleared"}
		}
		return actionResultMsg{message: "✓ Note updated"}
	}
}

// resolveEditor returns the editor command and arguments from $EDITOR, with fallbacks.
// Supports $EDITOR values like "code --wait" or "vim".
func resolveEditor() (string, []string) {
	if editor := os.Getenv("EDITOR"); editor != "" {
		parts := strings.Fields(editor)
		return parts[0], parts[1:]
	}
	for _, name := range []string{"vim", "vi", "nano"} {
		if _, err := exec.LookPath(name); err == nil {
			return name, nil
		}
	}
	return "vi", nil
}

func (m Model) doEditNoteExternal(bookmarkID string, currentNote string) tea.Cmd {
	f, err := os.CreateTemp("", "tsumu-note-*.txt")
	if err != nil {
		return func() tea.Msg {
			return editorFinishedMsg{err: fmt.Errorf("create temp file: %w", err)}
		}
	}
	tmpFile := f.Name()

	if _, err := f.WriteString(currentNote); err != nil {
		f.Close()
		os.Remove(tmpFile)
		return func() tea.Msg {
			return editorFinishedMsg{err: fmt.Errorf("write temp file: %w", err)}
		}
	}
	f.Close()

	editorCmd, editorArgs := resolveEditor()
	args := append(editorArgs, tmpFile)
	c := exec.Command(editorCmd, args...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr

	return tea.ExecProcess(c, func(err error) tea.Msg {
		defer os.Remove(tmpFile)

		if err != nil {
			return editorFinishedMsg{err: err}
		}

		content, readErr := os.ReadFile(tmpFile)
		if readErr != nil {
			return editorFinishedMsg{err: fmt.Errorf("read temp file: %w", readErr)}
		}

		return editorFinishedMsg{bookmarkID: bookmarkID, note: strings.TrimSpace(string(content))}
	})
}

func (m Model) doDelete() tea.Cmd {
	bm := m.focusedBookmark()
	if bm == nil {
		return nil
	}
	id := bm.ID

	return func() tea.Msg {
		if err := db.DeleteBookmark(m.db, id); err != nil {
			return actionResultMsg{message: fmt.Sprintf("Delete failed: %v", err), isError: true}
		}
		return actionResultMsg{message: "✓ Deleted"}
	}
}

// doCopyURL 复制选中书签的 URL 到系统剪贴板（macOS pbcopy）
func (m Model) doCopyURL() tea.Cmd {
	bm := m.focusedBookmark()
	if bm == nil {
		return nil
	}
	url := bm.URL

	return func() tea.Msg {
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(url)
		if err := cmd.Run(); err != nil {
			return copyResultMsg{message: fmt.Sprintf("Copy failed: %v", err), isError: true}
		}
		return copyResultMsg{message: fmt.Sprintf("✓ Copied: %s", url)}
	}
}

// doRefetch 重新抓取选中书签的元数据并重新生成 AI note。
// 保留用户的 note 和已有 tags 不变。
func (m Model) doRefetch() tea.Cmd {
	bm := m.focusedBookmark()
	if bm == nil {
		return nil
	}
	id := bm.ID
	url := bm.URL
	database := m.db
	apiKey := m.cfg.AI.GetAPIKey()
	genModel := m.cfg.AI.GetGenModel()
	lang := m.cfg.AI.GetLang()

	return func() tea.Msg {
		// 1. 重新抓取网页元数据
		fetched, err := meta.Fetch(url)
		if err != nil {
			return refetchResultMsg{err: fmt.Errorf("fetch failed: %w", err)}
		}

		// 2. 更新元数据（title, description, site_name）
		if err := db.UpdateBookmarkMeta(database, id, fetched.Title, fetched.Description, fetched.SiteName); err != nil {
			return refetchResultMsg{err: fmt.Errorf("update meta failed: %w", err)}
		}

		// 3. AI 增强（如果已配置 API key）
		if apiKey == "" {
			return refetchResultMsg{title: fetched.Title, hasAI: false}
		}

		// 加载所有已有标签（用于 AI 标签推荐）
		allTags, _ := db.ListAllTags(database)

		client := ai.NewClient(apiKey, genModel)
		result, err := client.EnhanceBookmark(context.Background(), fetched.Title, url, fetched.SiteName, allTags, lang)
		if err != nil {
			// AI 失败不阻断，元数据已更新成功
			return refetchResultMsg{title: fetched.Title, hasAI: false}
		}

		// 4. 更新 ai_note（description + keywords 拼接）
		if result.Description != "" {
			note := ai.FormatAiNote(result.Description, result.Keywords)
			_ = db.UpdateAiNote(database, id, note)
		}

		// 5. 追加 AI 推荐标签（不删除用户已有标签）
		if len(result.Tags) > 0 {
			_ = db.AddTagsToBookmark(database, id, result.Tags)
		}

		return refetchResultMsg{title: fetched.Title, hasAI: true}
	}
}

// cleanURL 去除 URL 中的 query 和 fragment 参数
func cleanURL(raw string) string {
	u, err := neturl.Parse(raw)
	if err != nil {
		return raw
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// doAddBookmark 异步添加书签：抓取元数据 → 写数据库 → 自动打 domain tag
func (m Model) doAddBookmark(rawURL, tagsStr, note string) tea.Cmd {
	database := m.db
	cfg := m.cfg
	return func() tea.Msg {
		cleanedURL := cleanURL(rawURL)

		// 抓取元数据
		metadata, err := meta.Fetch(rawURL)
		if err != nil {
			metadata = &meta.Metadata{
				Title:    cleanedURL,
				SiteName: cleanedURL,
			}
		}

		// 写数据库
		bm, err := db.CreateBookmark(database, cleanedURL, metadata.Title, metadata.Description, metadata.SiteName, strings.TrimSpace(note))
		if err != nil {
			return addBookmarkMsg{err: err}
		}

		// 解析 tags
		var tagList []string
		if tagsStr != "" {
			for _, t := range strings.Split(tagsStr, ",") {
				t = strings.TrimSpace(t)
				if t != "" {
					tagList = append(tagList, t)
				}
			}
		}
		// Domain auto-tag
		if cfg != nil && len(cfg.DomainTags) > 0 {
			domain := meta.ExtractDomain(rawURL)
			if autoTag, ok := cfg.DomainTags[domain]; ok && autoTag != "" {
				found := false
				for _, t := range tagList {
					if t == autoTag {
						found = true
						break
					}
				}
				if !found {
					tagList = append(tagList, autoTag)
				}
			}
		}
		if len(tagList) > 0 {
			_ = db.AddTagsToBookmark(database, bm.ID, tagList)
		}

		return addBookmarkMsg{title: bm.Title, siteName: bm.SiteName, bmID: bm.ID}
	}
}

// doBackgroundAIEnhance 异步增强单个书签的 AI note 和 tags
func (m Model) doBackgroundAIEnhance(bookmarkID string) tea.Cmd {
	database := m.db
	apiKey := m.cfg.AI.GetAPIKey()
	genModel := m.cfg.AI.GetGenModel()
	lang := m.cfg.AI.GetLang()
	return func() tea.Msg {
		bm, err := db.GetBookmarkByID(database, bookmarkID)
		if err != nil || bm == nil {
			return aiEnhanceDoneMsg{err: fmt.Errorf("bookmark not found")}
		}

		allTags, _ := db.ListAllTags(database)
		client := ai.NewClient(apiKey, genModel)
		result, err := client.EnhanceBookmark(context.Background(), bm.Title, bm.URL, bm.SiteName, allTags, lang)
		if err != nil {
			return aiEnhanceDoneMsg{title: bm.Title, err: err}
		}

		if result.Description != "" {
			note := ai.FormatAiNote(result.Description, result.Keywords)
			_ = db.UpdateAiNote(database, bookmarkID, note)
		}
		if len(result.Tags) > 0 {
			_ = db.AddTagsToBookmark(database, bookmarkID, result.Tags)
		}

		return aiEnhanceDoneMsg{title: bm.Title}
	}
}

// doAIBatch 异步批量 AI 增强书签
func (m Model) doAIBatch(emptyOnly bool) tea.Cmd {
	database := m.db
	cfg := m.cfg
	return func() tea.Msg {
		bookmarks, err := db.ListBookmarksForAI(database, emptyOnly)
		if err != nil {
			return aiBatchDoneMsg{err: err}
		}
		if len(bookmarks) == 0 {
			return aiBatchDoneMsg{total: 0, enhanced: 0}
		}

		allTags, _ := db.ListAllTags(database)
		apiKey := cfg.AI.GetAPIKey()
		genModel := cfg.AI.GetGenModel()
		lang := cfg.AI.GetLang()
		client := ai.NewClient(apiKey, genModel)

		enhanced := 0
		for _, bm := range bookmarks {
			result, err := client.EnhanceBookmark(context.Background(), bm.Title, bm.URL, bm.SiteName, allTags, lang)
			if err != nil {
				continue
			}
			if result.Description != "" {
				note := ai.FormatAiNote(result.Description, result.Keywords)
				_ = db.UpdateAiNote(database, bm.ID, note)
			}
			if len(result.Tags) > 0 {
				_ = db.AddTagsToBookmark(database, bm.ID, result.Tags)
			}
			enhanced++
		}

		return aiBatchDoneMsg{total: len(bookmarks), enhanced: enhanced}
	}
}

// doSync 异步执行同步操作
func (m Model) doSync(force bool) tea.Cmd {
	database := m.db
	cfg := m.cfg
	return func() tea.Msg {
		client := sync.NewClient(cfg.Sync.GetURL(), cfg.Sync.GetAuthToken())
		ctx := context.Background()

		mode := sync.SyncIncremental
		if force {
			mode = sync.SyncFull
		}

		result, err := sync.SyncAll(ctx, database, client, cfg.Sync.LastSynced, cfg.Sync.PullCursor, mode, nil)
		if err != nil {
			return syncDoneMsg{err: err}
		}

		// 更新 config（last_synced + pull_cursor）
		cfg.Sync.LastSynced = sync.NowUTC()
		cfg.Sync.PullCursor = result.NewPullCursor
		cfg.Save()

		return syncDoneMsg{result: result, warning: result.Warning}
	}
}

// doToggleIrrelevant 切换当前书签的不相关状态。
// 标记为不相关时从 ai_note 移除查询词，取消标记时重新追加。
func (m Model) doToggleIrrelevant() tea.Cmd {
	bm := m.focusedBookmark()
	if bm == nil {
		return nil
	}
	id := bm.ID
	query := m.query
	wasIrrelevant := m.irrelevantSet[id]

	return func() tea.Msg {
		if wasIrrelevant {
			// 取消标记：重新追加查询词
			_ = db.AppendAINote(m.db, id, query)
		} else {
			// 标记不相关：从 ai_note 移除查询词
			_ = db.RemoveFromAINote(m.db, id, query)
		}
		return nil
	}
}
