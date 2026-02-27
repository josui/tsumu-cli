# tsumu CLI

Local-first CLI bookmark manager. Save links fast, find them faster.

[中文](README.md)

## Install

```bash
make install
```

Builds the binary and installs both `tsumu` and `tm` (short alias) to `/usr/local/bin/`.

Build only:

```bash
make build
```

## Usage

`tm` is a short alias for `tsumu`. All commands work with either name.

```
tsumu                          list all bookmarks (TUI)
tsumu add <url> [note...]      add bookmark
tsumu find <query>             search bookmarks
tsumu find -d <query>          search (detailed mode)
```

### Add a bookmark

```bash
tsumu add https://example.com
```

Automatically fetches page title, description, and site name via OGP tags.

### Search bookmarks

```bash
tsumu find design
```

Opens an interactive TUI with search results.

**TUI keybindings:**

| Key | Action |
|-----|--------|
| `{n}` + Enter | Open bookmark #n in browser |
| `t{n} tag1,tag2` + Enter | Add tags to bookmark #n |
| `f{n}` + Enter | Toggle favorite on bookmark #n |
| `d{n}` + Enter | Delete bookmark #n (with confirmation) |
| `j` | Next page |
| `k` | Previous page |
| `Esc` | Clear input |
| `q` | Quit |

### Detailed mode

```bash
tsumu find -d design
```

Shows URL, description, tags, creation date, click count, and source.

### Cloud sync

```bash
tsumu sync --setup       # configure Turso sync
tsumu sync --status      # show sync status
tsumu sync               # manual sync
tsumu sync --off         # disable sync
```

## Data

Data is stored in `~/.tsumu/`:

- `tsumu.db` — SQLite database (WAL mode, FTS5 full-text search)
- `config.toml` — Configuration file (auto-created)

The database schema is shared with [tsumu macOS app](https://github.com/josui/tsumu) via Turso cloud sync.

## Tech Stack

- [cobra](https://github.com/spf13/cobra) — CLI framework
- [bubbletea](https://github.com/charmbracelet/bubbletea) — TUI (Elm architecture)
- [lipgloss](https://github.com/charmbracelet/lipgloss) — Terminal styling
- [go-libsql](https://github.com/tursodatabase/go-libsql) — SQLite / Turso driver
- [goquery](https://github.com/PuerkitoBio/goquery) — HTML parsing for metadata
- [toml](https://github.com/BurntSushi/toml) — Config file format
