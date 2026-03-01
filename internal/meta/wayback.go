// cli/internal/meta/wayback.go

// wayback 通过 Wayback Machine（Internet Archive）的缓存页面抓取标题。
// 作为第三级 fallback：HTTP fetch 和 headless browser 都失败时使用。
// 典型场景：Vercel Bot Protection、Cloudflare 等 WAF 拦截非浏览器请求的站点。
package meta

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// waybackTimeout 是 Wayback Machine 请求的超时时间。
// 两次请求（API 查询 + 缓存页面获取），每次 5 秒足够。
const waybackTimeout = 5 * time.Second

// waybackAvailableAPI 查询 Wayback Machine 是否有指定 URL 的快照。
// 文档：https://archive.org/help/wayback_api.php
const waybackAvailableAPI = "https://archive.org/wayback/available"

// waybackResponse 是 Wayback Machine availability API 的响应结构。
type waybackResponse struct {
	ArchivedSnapshots struct {
		Closest *struct {
			URL       string `json:"url"`
			Status    string `json:"status"`
			Available bool   `json:"available"`
		} `json:"closest"`
	} `json:"archived_snapshots"`
}

// fetchWaybackTitle 从 Wayback Machine 缓存页面提取标题。
// 流程：1) 查询是否有快照 → 2) 获取缓存页面 → 3) 解析 OGP/HTML title。
// 无快照或解析失败时返回 error，由调用方继续 fallback。
func fetchWaybackTitle(rawURL string) (string, error) {
	client := &http.Client{Timeout: waybackTimeout}

	// 1. 查询快照可用性
	apiURL := waybackAvailableAPI + "?url=" + rawURL
	resp, err := client.Get(apiURL)
	if err != nil {
		return "", fmt.Errorf("wayback API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("wayback API HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("wayback API read body: %w", err)
	}

	var result waybackResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("wayback API parse JSON: %w", err)
	}

	snapshot := result.ArchivedSnapshots.Closest
	if snapshot == nil || !snapshot.Available || snapshot.URL == "" {
		return "", fmt.Errorf("no wayback snapshot available for %s", rawURL)
	}

	// 2. 获取缓存页面
	cacheResp, err := client.Get(snapshot.URL)
	if err != nil {
		return "", fmt.Errorf("wayback cache fetch failed: %w", err)
	}
	defer cacheResp.Body.Close()

	if cacheResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("wayback cache HTTP %d", cacheResp.StatusCode)
	}

	// 3. 解析 HTML 提取标题（OGP 优先，HTML <title> fallback）
	doc, err := goquery.NewDocumentFromReader(cacheResp.Body)
	if err != nil {
		return "", fmt.Errorf("wayback HTML parse failed: %w", err)
	}

	title := getMetaProperty(doc, "og:title")
	if title == "" {
		title = strings.TrimSpace(doc.Find("title").First().Text())
	}

	if title == "" {
		return "", fmt.Errorf("wayback cache has no title for %s", rawURL)
	}

	return title, nil
}
