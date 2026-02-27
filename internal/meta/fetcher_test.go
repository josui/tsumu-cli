// cli/internal/meta/fetcher_test.go
package meta

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetch_OGPTags(t *testing.T) {
	// httptest.NewServer 创建本地 HTTP 服务器，用于测试（不发真实网络请求）
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<!DOCTYPE html>
<html>
<head>
    <meta property="og:title" content="Test Page Title">
    <meta property="og:description" content="Test description here">
    <meta property="og:site_name" content="TestSite">
    <title>Fallback Title</title>
</head>
<body></body>
</html>`))
	}))
	defer server.Close()

	meta, err := Fetch(server.URL)
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}

	if meta.Title != "Test Page Title" {
		t.Errorf("expected title 'Test Page Title', got '%s'", meta.Title)
	}
	if meta.Description != "Test description here" {
		t.Errorf("expected description 'Test description here', got '%s'", meta.Description)
	}
	// SiteName is always domain, not og:site_name
	if meta.SiteName == "" {
		t.Error("site_name should be the domain")
	}
}

func TestFetch_FallbackToHTMLTags(t *testing.T) {
	// 没有 OGP 标签时，应 fallback 到普通 HTML 标签
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<!DOCTYPE html>
<html>
<head>
    <title>Fallback Title</title>
    <meta name="description" content="Fallback description">
</head>
<body></body>
</html>`))
	}))
	defer server.Close()

	meta, err := Fetch(server.URL)
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}

	if meta.Title != "Fallback Title" {
		t.Errorf("expected title 'Fallback Title', got '%s'", meta.Title)
	}
	if meta.Description != "Fallback description" {
		t.Errorf("expected description 'Fallback description', got '%s'", meta.Description)
	}
	// site_name 没有 OGP 也没有其他标签时，应从 URL 提取域名
	if meta.SiteName == "" {
		t.Error("site_name should fallback to domain")
	}
}

func TestIsTwitterURL(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"https://x.com/user/status/123456", true},
		{"https://twitter.com/user/status/789", true},
		{"https://www.x.com/user/status/123", true},
		{"https://mobile.twitter.com/user/status/123", true},
		{"https://github.com/repo", false},
		{"https://example.com", false},
	}
	for _, tt := range tests {
		if got := isTwitterURL(tt.url); got != tt.want {
			t.Errorf("isTwitterURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestExtractTweetID(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://x.com/user/status/123456789", "123456789"},
		{"https://twitter.com/user/status/987654321?s=20", "987654321"},
		{"https://x.com/user/status/111/photo/1", "111"},
		{"https://x.com/user", ""},
		{"https://example.com", ""},
	}
	for _, tt := range tests {
		if got := extractTweetID(tt.url); got != tt.want {
			t.Errorf("extractTweetID(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func TestExtractDomain(t *testing.T) {
	tests := []struct {
		url      string
		expected string
	}{
		{"https://coolors.co/generate", "coolors.co"},
		{"https://www.example.com/path", "example.com"},
		{"http://sub.domain.co.jp/page", "sub.domain.co.jp"},
	}

	for _, tt := range tests {
		got := extractDomain(tt.url)
		if got != tt.expected {
			t.Errorf("extractDomain(%q) = %q, want %q", tt.url, got, tt.expected)
		}
	}
}

func TestFetch_TwitterURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/i/status/") {
			json.NewEncoder(w).Encode(map[string]any{
				"tweet": map[string]any{
					"text": "Test tweet content",
					"author": map[string]any{
						"name":        "Author",
						"screen_name": "author",
					},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	origBase := fixTweetBase
	fixTweetBase = server.URL
	defer func() { fixTweetBase = origBase }()

	meta, err := Fetch("https://x.com/author/status/12345")
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}
	if !strings.Contains(meta.Title, "Author") {
		t.Errorf("expected title to contain author, got %q", meta.Title)
	}
	if meta.SiteName != "x.com" {
		t.Errorf("expected site_name '@author', got %q", meta.SiteName)
	}
}

func TestFetchTwitterMeta_FixTweet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/i/status/123" {
			json.NewEncoder(w).Encode(map[string]any{
				"tweet": map[string]any{
					"text": "Hello world, this is a test tweet",
					"author": map[string]any{
						"name":        "Test User",
						"screen_name": "testuser",
					},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	meta, err := fetchFixTweet(server.URL, "123")
	if err != nil {
		t.Fatalf("fetchFixTweet failed: %v", err)
	}
	if meta.Title != "Test User: Hello world, this is a test tweet" {
		t.Errorf("unexpected title: %q", meta.Title)
	}
	if meta.SiteName != "x.com" {
		t.Errorf("unexpected site_name: %q", meta.SiteName)
	}
	if meta.Description != "Hello world, this is a test tweet" {
		t.Errorf("unexpected description: %q", meta.Description)
	}
}

func TestFetchTwitterMeta_WhitespaceCollapse(t *testing.T) {
	multiLineText := "Line one\nLine two\n\nLine three"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"tweet": map[string]any{
				"text": multiLineText,
				"author": map[string]any{
					"name":        "User",
					"screen_name": "user",
				},
			},
		})
	}))
	defer server.Close()

	meta, err := fetchFixTweet(server.URL, "1")
	if err != nil {
		t.Fatalf("fetchFixTweet failed: %v", err)
	}
	if strings.Contains(meta.Title, "\n") {
		t.Errorf("title should not contain newlines, got %q", meta.Title)
	}
	expected := "User: Line one Line two Line three"
	if meta.Title != expected {
		t.Errorf("title = %q, want %q", meta.Title, expected)
	}
	if meta.Description != multiLineText {
		t.Errorf("description should preserve original text")
	}
}

func TestFetchTwitterMeta_OEmbed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"author_name": "Test Author",
			"html":        "<blockquote><p>Tweet content here</p></blockquote>",
		})
	}))
	defer server.Close()

	meta, err := fetchOEmbed(server.URL, "https://x.com/user/status/123")
	if err != nil {
		t.Fatalf("fetchOEmbed failed: %v", err)
	}
	if meta.SiteName != "x.com" {
		t.Errorf("unexpected site_name: %q", meta.SiteName)
	}
	if meta.Title == "" {
		t.Error("title should be extracted from html")
	}
}
