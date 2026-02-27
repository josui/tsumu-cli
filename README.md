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

```
tsumu                          列出所有书签（TUI）
tsumu <query>                  搜索书签
tsumu -f                       仅显示收藏
tsumu -d / -w / -m             今天 / 本周 / 本月
tsumu -t <tag>                 按标签筛选
tsumu -r                       随机打开一个书签
tsumu add <url>                添加书签
tsumu add <url> -t a,b -n "x"  添加书签（含标签和备注）
tsumu sync                     云端同步
tsumu update                   更新到最新版本
```

### 添加书签

```bash
tsumu add https://example.com
```

自动通过 OGP 标签抓取页面标题、描述和站点名。

### 搜索书签

```bash
tsumu design
```

打开交互式 TUI 搜索结果界面。支持组合筛选：`tsumu design -f` 搜索收藏中的书签。

**TUI 快捷键：**

| 按键 | 操作 |
|------|------|
| `j` / `↓` | 下移光标 |
| `k` / `↑` | 上移光标 |
| `Enter` | 在浏览器中打开选中书签 |
| `t` | 给选中书签添加标签 |
| `f` | 切换选中书签的收藏状态 |
| `d` | 删除选中书签（需确认） |
| `Esc` | 清除消息 |
| `q` | 退出 |

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

数据库使用 SQLite（WAL 模式，FTS5 全文搜索），通过 Turso 支持多设备云端同步。

## 技术栈

- [cobra](https://github.com/spf13/cobra) — CLI 框架
- [bubbletea](https://github.com/charmbracelet/bubbletea) — TUI（Elm 架构）
- [lipgloss](https://github.com/charmbracelet/lipgloss) — 终端样式
- [go-libsql](https://github.com/tursodatabase/go-libsql) — SQLite / Turso 驱动
- [goquery](https://github.com/PuerkitoBio/goquery) — HTML 解析
- [toml](https://github.com/BurntSushi/toml) — 配置文件格式
