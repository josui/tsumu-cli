package db

import (
	"database/sql"
	"testing"
)

func TestAppendAINote(t *testing.T) {
	database := openTestDB(t)

	// 创建测试书签
	bm, err := CreateBookmark(database, "https://example.com", "Test", "", "", "")
	if err != nil {
		t.Fatal(err)
	}

	// 追加到空 ai_note
	if err := AppendAINote(database, bm.ID, "文本提取"); err != nil {
		t.Fatal(err)
	}
	got := getAINote(t, database, bm.ID)
	if got != "文本提取" {
		t.Errorf("got %q, want %q", got, "文本提取")
	}

	// 追加不同关键词
	if err := AppendAINote(database, bm.ID, "parser"); err != nil {
		t.Fatal(err)
	}
	got = getAINote(t, database, bm.ID)
	if got != "文本提取 parser" {
		t.Errorf("got %q, want %q", got, "文本提取 parser")
	}

	// 去重：相同关键词不重复追加
	if err := AppendAINote(database, bm.ID, "文本提取"); err != nil {
		t.Fatal(err)
	}
	got = getAINote(t, database, bm.ID)
	if got != "文本提取 parser" {
		t.Errorf("got %q, want %q", got, "文本提取 parser")
	}
}

func TestRemoveFromAINote(t *testing.T) {
	database := openTestDB(t)

	bm, err := CreateBookmark(database, "https://example.com", "Test", "", "", "")
	if err != nil {
		t.Fatal(err)
	}

	// 写入初始内容
	if err := UpdateAiNote(database, bm.ID, "existing content 文本提取 parser"); err != nil {
		t.Fatal(err)
	}

	// 移除中间关键词
	if err := RemoveFromAINote(database, bm.ID, "文本提取"); err != nil {
		t.Fatal(err)
	}
	got := getAINote(t, database, bm.ID)
	if got != "existing content parser" {
		t.Errorf("got %q, want %q", got, "existing content parser")
	}

	// 移除不存在的关键词：no-op
	if err := RemoveFromAINote(database, bm.ID, "不存在"); err != nil {
		t.Fatal(err)
	}
	got = getAINote(t, database, bm.ID)
	if got != "existing content parser" {
		t.Errorf("got %q, want %q", got, "existing content parser")
	}
}

func TestAppendAINote_PreservesExistingContent(t *testing.T) {
	database := openTestDB(t)

	bm, err := CreateBookmark(database, "https://example.com", "Test", "", "", "")
	if err != nil {
		t.Fatal(err)
	}

	// 先用 AI enhance 写入摘要
	if err := UpdateAiNote(database, bm.ID, "A tool for extracting text from documents"); err != nil {
		t.Fatal(err)
	}

	// 追加搜索关键词
	if err := AppendAINote(database, bm.ID, "文本提取"); err != nil {
		t.Fatal(err)
	}
	got := getAINote(t, database, bm.ID)
	if got != "A tool for extracting text from documents 文本提取" {
		t.Errorf("got %q, want %q", got, "A tool for extracting text from documents 文本提取")
	}
}

// getAINote 测试辅助：读取指定书签的 ai_note
func getAINote(t *testing.T, database *sql.DB, id string) string {
	t.Helper()
	var note sql.NullString
	err := database.QueryRow("SELECT ai_note FROM bookmarks WHERE id = ?", id).Scan(&note)
	if err != nil {
		t.Fatal(err)
	}
	return note.String
}
