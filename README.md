# tsumu CLI

本地优先的命令行书签管理工具。快速保存链接，更快找到它们。

[English](README.en.md)

## 安装

### Homebrew

```bash
brew install josui/tap/tsumu
```

### 源码编译

```bash
make install
```

编译并安装 `tsumu` 和 `tm`（短别名）到 `/usr/local/bin/`。

仅编译：

```bash
make build
```

## 使用

`tm` 是 `tsumu` 的短别名，以下命令两者通用。

### 浏览与搜索

```
tsumu                          列出所有书签（TUI）
tsumu <query>                  搜索书签
tsumu -f                       仅显示收藏
tsumu -d / -w / -m             今天 / 本周 / 本月
tsumu -t <tag>                 按标签筛选
tsumu -r                       随机打开一个书签
```

### 添加书签

```bash
tsumu add https://example.com
tsumu add https://example.com -t design,tool -n "好用的配色工具"
tsumu add https://example.com 这是一个备注
```

自动抓取页面标题、描述和站点名（OGP 标签）。URL 后直接跟文字会自动作为备注。

X/Twitter 链接通过 FixTweet API 获取 tweet 内容。WAF 保护站点（Vercel Bot Protection 等）自动通过 Wayback Machine 缓存抓取。URL 存储时自动去除 query 参数和 fragment。根据 config.toml `[domain_tags]` 自动按域名打标签。

### 云端同步

```bash
tsumu sync                     手动同步
tsumu sync --setup             配置 Turso 同步
tsumu sync --status            查看同步状态
tsumu sync --force             全量重同步
tsumu sync --overwrite         本地覆盖远端（危险）
tsumu sync --off               关闭同步
```

数据存本地 SQLite，通过 Turso HTTP API 按需同步。启动时按 `sync.interval` 自动检查，读操作始终走本地，零网络延迟。

### AI 增强（可选）

```bash
tsumu config --ai              配置 Gemini API
tsumu ai                       批量生成 AI 摘要
tsumu ai --empty               仅处理无 AI 摘要的书签
```

未配置 AI 时所有功能正常工作。配置后自动启用：

- **添加时增强** — AI 自动补充描述、标签、多语言搜索关键词（后台执行，不阻塞）
- **搜索扩展** — TUI 中按 `Tab` 将自然语言展开为关键词（含同义词、中英互译）
- **批量处理** — `tsumu ai` 并发处理所有书签

### 其他

```bash
tsumu config                   查看/修改配置
tsumu update                   更新到最新版本
tsumu completion [bash|zsh|fish]  生成 Shell 补全脚本
```

## TUI 快捷键

| 按键 | 操作 |
|------|------|
| `j` / `↓` | 下移光标 |
| `k` / `↑` | 上移光标 |
| `Enter` | 在浏览器中打开 |
| `t` | 打标签（支持自动补全） |
| `f` | 切换收藏状态 |
| `n` | 内联编辑备注 |
| `N` | 用 `$EDITOR` 编辑备注 |
| `c` | 复制 URL 到剪贴板 |
| `r` | 重新抓取元数据 + AI 增强 |
| `Tab` | AI 搜索扩展（需配置 AI） |
| `d` | 删除（需确认） |
| `Esc` | 清除消息 / 退出输入 |
| `q` | 退出 |

## 数据

数据存储在 `~/.tsumu/`：

- `tsumu.db` — SQLite 数据库（WAL 模式，FTS5 全文搜索）
- `config.toml` — 配置文件（首次运行自动创建）

## 技术栈

- [cobra](https://github.com/spf13/cobra) — CLI 框架
- [bubbletea](https://github.com/charmbracelet/bubbletea) — TUI（Elm 架构）
- [lipgloss](https://github.com/charmbracelet/lipgloss) — 终端样式
- [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) — 纯 Go SQLite 驱动
- [goquery](https://github.com/PuerkitoBio/goquery) — HTML 解析
- [toml](https://github.com/BurntSushi/toml) — 配置文件格式
