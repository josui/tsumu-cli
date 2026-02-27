# tsumu CLI

本地优先的命令行书签管理工具。快速保存链接，更快找到它们。

[English](README.en.md)

## 安装

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

```
tsumu                          列出所有书签（TUI）
tsumu add <url> [备注...]      添加书签
tsumu find <query>             搜索书签
tsumu find -d <query>          搜索（详细模式）
```

### 添加书签

```bash
tsumu add https://example.com
```

自动通过 OGP 标签抓取页面标题、描述和站点名。

### 搜索书签

```bash
tsumu find design
```

打开交互式 TUI 搜索结果界面。

**TUI 快捷键：**

| 按键 | 操作 |
|------|------|
| `{n}` + Enter | 在浏览器中打开第 n 个书签 |
| `t{n} tag1,tag2` + Enter | 给第 n 个书签添加标签 |
| `f{n}` + Enter | 切换第 n 个书签的收藏状态 |
| `d{n}` + Enter | 删除第 n 个书签（需确认） |
| `j` | 下一页 |
| `k` | 上一页 |
| `Esc` | 清空输入 |
| `q` | 退出 |

### 详细模式

```bash
tsumu find -d design
```

显示 URL、描述、标签、创建时间、点击次数和来源。

### 云端同步

```bash
tsumu sync --setup       # 配置 Turso 同步
tsumu sync --status      # 查看同步状态
tsumu sync               # 手动同步
tsumu sync --off         # 关闭同步
```

## 数据

数据存储在 `~/.tsumu/`：

- `tsumu.db` — SQLite 数据库（WAL 模式，FTS5 全文搜索）
- `config.toml` — 配置文件（自动创建）

数据库 schema 通过 Turso 云端同步与 [tsumu macOS app](https://github.com/josui/tsumu) 共享。

## 技术栈

- [cobra](https://github.com/spf13/cobra) — CLI 框架
- [bubbletea](https://github.com/charmbracelet/bubbletea) — TUI（Elm 架构）
- [lipgloss](https://github.com/charmbracelet/lipgloss) — 终端样式
- [go-libsql](https://github.com/tursodatabase/go-libsql) — SQLite / Turso 驱动
- [goquery](https://github.com/PuerkitoBio/goquery) — HTML 解析
- [toml](https://github.com/BurntSushi/toml) — 配置文件格式
