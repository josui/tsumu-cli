// cli/internal/meta/headless.go

// headless 提供基于 go-rod 的无头浏览器标题抓取。
// 仅在普通 HTTP fetch 拿不到标题时调用（典型场景：SPA / CSR 站点）。
// 使用系统已安装的 Chrome，若无可用浏览器则直接返回错误，不自动下载。
package meta

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

// headlessTimeout 是无头浏览器整体超时时间。
// 包含浏览器启动 + 页面加载 + JS 渲染，10 秒对绝大多数 SPA 足够。
const headlessTimeout = 10 * time.Second

// fetchHeadlessTitle 用无头浏览器加载页面并获取 document.title。
// 仅在系统有可用浏览器时工作；无浏览器时返回 error，由调用方 fallback。
func fetchHeadlessTitle(rawURL string) (string, error) {
	// 查找系统中已安装的浏览器（Chrome / Chromium / Edge）
	path, found := launcher.LookPath()
	if !found {
		return "", fmt.Errorf("no browser found on system")
	}

	// 启动无头浏览器，用完即关
	u, err := launcher.New().Bin(path).Headless(true).Launch()
	if err != nil {
		return "", fmt.Errorf("browser launch failed: %w", err)
	}

	browser := rod.New().ControlURL(u)
	if err := browser.Connect(); err != nil {
		return "", fmt.Errorf("browser connect failed: %w", err)
	}
	defer browser.MustClose()

	// 设置整体超时
	ctx, cancel := context.WithTimeout(context.Background(), headlessTimeout)
	defer cancel()
	browser = browser.Context(ctx)

	// 导航到目标页面，等待页面稳定（网络空闲 + DOM 不再变化）
	page, err := browser.Page(proto.TargetCreateTarget{URL: rawURL})
	if err != nil {
		return "", fmt.Errorf("create page failed: %w", err)
	}

	// WaitStable 等待页面 DOM 稳定（默认 1s 内无变化视为稳定）
	if err := page.WaitStable(time.Second); err != nil {
		return "", fmt.Errorf("wait stable failed: %w", err)
	}

	// 获取 JS 渲染后的 document.title
	titleObj, err := page.Eval(`() => document.title`)
	if err != nil {
		return "", fmt.Errorf("eval title failed: %w", err)
	}

	title := strings.TrimSpace(titleObj.Value.String())
	if title == "" {
		return "", fmt.Errorf("document.title is empty after JS rendering")
	}

	return title, nil
}
