# tsumu CLI

Local-first CLI bookmark manager. Save links fast, find them faster.

## Install

```bash
cd cli
go build -o tsumu .
```

Move the binary to your PATH:

```bash
mv tsumu /usr/local/bin/
```

## Usage

```
tsumu -a <url>         Add a bookmark
tsumu -s <query>       Search bookmarks
tsumu -s -d <query>    Search bookmarks (detailed mode)
```

### Add a bookmark

```bash
tsumu -a https://example.com
```

Automatically fetches page title, description, and site name via OGP tags.

### Search bookmarks

```bash
tsumu -s design
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
tsumu -s -d design
```

Shows URL, description, tags, creation date, click count, and source.

## Data

Data is stored in `~/.tsumu/`:

- `tsumu.db` — SQLite database (WAL mode, FTS5 full-text search)
- `config.toml` — Configuration file (auto-created)

## Tech Stack

- [cobra](https://github.com/spf13/cobra) — CLI framework
- [bubbletea](https://github.com/charmbracelet/bubbletea) — TUI (Elm architecture)
- [lipgloss](https://github.com/charmbracelet/lipgloss) — Terminal styling
- [go-libsql](https://github.com/tursodatabase/go-libsql) — SQLite driver
- [goquery](https://github.com/PuerkitoBio/goquery) — HTML parsing for metadata
- [toml](https://github.com/BurntSushi/toml) — Config file format
