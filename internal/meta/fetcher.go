// cli/internal/meta/fetcher.go

// Package meta 负责从 URL 抓取网页元数据（标题、描述、站点名）。
// 解析策略和 macOS App 的 MetadataFetcher 保持一致：OGP 标签优先，HTML 标签 fallback。
package meta

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

var tweetIDRegex = regexp.MustCompile(`/status/(\d+)`)

// isTwitterURL checks if the URL belongs to X/Twitter.
func isTwitterURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(host, "www.")
	host = strings.TrimPrefix(host, "mobile.")
	return host == "x.com" || host == "twitter.com"
}

// extractTweetID extracts the tweet ID from an X/Twitter URL.
// Returns empty string if no valid ID found.
func extractTweetID(rawURL string) string {
	matches := tweetIDRegex.FindStringSubmatch(rawURL)
	if len(matches) < 2 {
		return ""
	}
	return matches[1]
}

// Metadata 是从网页中提取的元数据。
type Metadata struct {
	Title       string // 页面标题
	Description string // 页面描述
	SiteName    string // 站点名称（如 "GitHub"）
}

var (
	fixTweetBase = "https://api.fxtwitter.com"
	oEmbedBase   = "https://publish.twitter.com"
)

// collapseWhitespace replaces newlines and consecutive spaces with a single space.
func collapseWhitespace(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	// collapse consecutive spaces
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return strings.TrimSpace(s)
}

// fetchFixTweet fetches tweet metadata from FixTweet API.
func fetchFixTweet(baseURL string, tweetID string) (*Metadata, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(baseURL + "/i/status/" + tweetID)
	if err != nil {
		return nil, fmt.Errorf("fixtweet request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fixtweet HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("fixtweet read body: %w", err)
	}

	var result struct {
		Tweet struct {
			Text   string `json:"text"`
			Author struct {
				Name       string `json:"name"`
				ScreenName string `json:"screen_name"`
			} `json:"author"`
			Article *struct {
				Title       string `json:"title"`
				PreviewText string `json:"preview_text"`
			} `json:"article"`
		} `json:"tweet"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("fixtweet parse JSON: %w", err)
	}

	tweet := result.Tweet

	// X Article: 用文章标题和预览文本
	if tweet.Article != nil && tweet.Article.Title != "" {
		return &Metadata{
			Title:       tweet.Article.Title,
			Description: tweet.Article.PreviewText,
			SiteName:    "x.com",
		}, nil
	}

	title := tweet.Author.Name + ": " + collapseWhitespace(tweet.Text)

	return &Metadata{
		Title:       title,
		Description: tweet.Text,
		SiteName:    "x.com",
	}, nil
}

// fetchOEmbed fetches tweet metadata from Twitter's oEmbed API.
func fetchOEmbed(baseURL string, tweetURL string) (*Metadata, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	endpoint := baseURL + "/oembed?url=" + url.QueryEscape(tweetURL)
	resp, err := client.Get(endpoint)
	if err != nil {
		return nil, fmt.Errorf("oembed request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oembed HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("oembed read body: %w", err)
	}

	var result struct {
		AuthorName string `json:"author_name"`
		HTML       string `json:"html"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("oembed parse JSON: %w", err)
	}

	title := result.AuthorName
	if result.HTML != "" {
		if start := strings.Index(result.HTML, "<p>"); start != -1 {
			start += 3
			if end := strings.Index(result.HTML[start:], "</p>"); end != -1 {
				text := result.HTML[start : start+end]
				title = result.AuthorName + ": " + collapseWhitespace(text)
			}
		}
	}

	return &Metadata{
		Title:    title,
		SiteName: "x.com",
	}, nil
}

// fetchTwitterMeta tries FixTweet first, falls back to oEmbed.
func fetchTwitterMeta(rawURL string, tweetID string) (*Metadata, error) {
	meta, err := fetchFixTweet(fixTweetBase, tweetID)
	if err == nil {
		return meta, nil
	}
	meta, err2 := fetchOEmbed(oEmbedBase, rawURL)
	if err2 == nil {
		return meta, nil
	}
	return nil, fmt.Errorf("all twitter fetchers failed: fixtweet=%v, oembed=%v", err, err2)
}

// Fetch 抓取指定 URL 的元数据。
// 优先使用 OGP (Open Graph Protocol) 标签，fallback 到普通 HTML 标签。
// X/Twitter URL 使用 FixTweet + oEmbed 专用路径。
func Fetch(rawURL string) (*Metadata, error) {
	// Twitter/X special handling
	if isTwitterURL(rawURL) {
		tweetID := extractTweetID(rawURL)
		if tweetID != "" {
			return fetchTwitterMeta(rawURL, tweetID)
		}
		// No tweet ID (profile page etc.) — fall through to generic fetch
	}

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
	// 统一使用域名，保证 TUI 显示一致
	meta.SiteName = extractDomain(rawURL)

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

// ExtractDomain extracts and returns the domain from a URL, stripping www. prefix.
func ExtractDomain(rawURL string) string {
	return extractDomain(rawURL)
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
