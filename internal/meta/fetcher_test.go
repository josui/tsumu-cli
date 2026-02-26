// cli/internal/meta/fetcher_test.go
package meta

import (
	"net/http"
	"net/http/httptest"
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
	if meta.SiteName != "TestSite" {
		t.Errorf("expected site_name 'TestSite', got '%s'", meta.SiteName)
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
