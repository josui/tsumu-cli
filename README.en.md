# tsumu CLI

Local-first CLI bookmark manager. Save links fast, find them faster.

[中文](README.md)

## Install

### Homebrew

```bash
brew install josui/tap/tsumu
```

### From source

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
tsumu <query>                  search bookmarks
tsumu -f                       favorites only
tsumu -d / -w / -m             today / this week / this month
tsumu -t <tag>                 filter by tag
tsumu -r                       open a random bookmark
tsumu add <url>                add bookmark
tsumu add <url> -t a,b -n "x"  add bookmark with tags and note
tsumu sync                     sync with Turso cloud
tsumu update                   update to latest version
```

### Add a bookmark

```bash
tsumu add https://example.com
```

Automatically fetches page title, description, and site name via OGP tags.

### Search bookmarks

```bash
tsumu design
```

Opens an interactive TUI with search results. Combine with flags: `tsumu design -f` to search favorites.

**TUI keybindings:**

| Key | Action |
|-----|--------|
| `j` / `↓` | Move cursor down |
| `k` / `↑` | Move cursor up |
| `Enter` | Open selected bookmark in browser |
| `t` | Add tags to selected bookmark |
| `f` | Toggle favorite on selected bookmark |
| `d` | Delete selected bookmark (with confirmation) |
| `Esc` | Clear message |
| `q` | Quit |

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

The database uses SQLite (WAL mode, FTS5 full-text search) with optional Turso cloud sync across devices.

## Tech Stack

- [cobra](https://github.com/spf13/cobra) — CLI framework
- [bubbletea](https://github.com/charmbracelet/bubbletea) — TUI (Elm architecture)
- [lipgloss](https://github.com/charmbracelet/lipgloss) — Terminal styling
- [go-libsql](https://github.com/tursodatabase/go-libsql) — SQLite / Turso driver
- [goquery](https://github.com/PuerkitoBio/goquery) — HTML parsing for metadata
- [toml](https://github.com/BurntSushi/toml) — Config file format
