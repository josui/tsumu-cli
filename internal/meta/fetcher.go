// cli/internal/meta/fetcher.go

// Package meta 负责从 URL 抓取网页元数据（标题、描述、站点名）。
// 解析策略和 macOS App 的 MetadataFetcher 保持一致：OGP 标签优先，HTML 标签 fallback。
package meta

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// Metadata 是从网页中提取的元数据。
type Metadata struct {
	Title       string // 页面标题
	Description string // 页面描述
	SiteName    string // 站点名称（如 "GitHub"）
}

// Fetch 抓取指定 URL 的元数据。
// 优先使用 OGP (Open Graph Protocol) 标签，fallback 到普通 HTML 标签。
func Fetch(rawURL string) (*Metadata, error) {
	// 创建带超时的 HTTP 客户端（默认 Client 没有超时，可能永远阻塞）
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	resp, err := client.Get(rawURL)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close() // 必须关闭 body，否则 HTTP 连接不会归还连接池

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	// goquery 从 io.Reader 解析 HTML DOM
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("HTML parse failed: %w", err)
	}

	meta := &Metadata{}

	// --- 提取 title ---
	// 优先级：og:title > <title>
	meta.Title = getMetaProperty(doc, "og:title")
	if meta.Title == "" {
		meta.Title = strings.TrimSpace(doc.Find("title").First().Text())
	}

	// --- 提取 description ---
	// 优先级：og:description > <meta name="description">
	meta.Description = getMetaProperty(doc, "og:description")
	if meta.Description == "" {
		meta.Description = getMetaName(doc, "description")
	}

	// --- 提取 site_name ---
	// 优先级：og:site_name > 从 URL 提取域名
	meta.SiteName = getMetaProperty(doc, "og:site_name")
	if meta.SiteName == "" {
		meta.SiteName = extractDomain(rawURL)
	}

	return meta, nil
}

// getMetaProperty 获取 <meta property="xxx" content="..."> 的 content 值。
// 用于 OGP 标签（property 属性，不是 name 属性）。
func getMetaProperty(doc *goquery.Document, property string) string {
	var content string
	// CSS 属性选择器：[property='og:title']
	selector := fmt.Sprintf("meta[property='%s']", property)
	doc.Find(selector).First().Each(func(_ int, s *goquery.Selection) {
		content, _ = s.Attr("content")
	})
	return strings.TrimSpace(content)
}

// getMetaName 获取 <meta name="xxx" content="..."> 的 content 值。
// 用于普通 HTML meta 标签。
func getMetaName(doc *goquery.Document, name string) string {
	var content string
	selector := fmt.Sprintf("meta[name='%s']", name)
	doc.Find(selector).First().Each(func(_ int, s *goquery.Selection) {
		content, _ = s.Attr("content")
	})
	return strings.TrimSpace(content)
}

// extractDomain 从 URL 中提取域名，去掉 www. 前缀。
// 例如 "https://www.example.com/path" → "example.com"
func extractDomain(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	host := u.Hostname() // 去掉端口号
	// 去掉 www. 前缀
	host = strings.TrimPrefix(host, "www.")
	return host
}
