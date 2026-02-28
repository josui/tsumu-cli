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

Builds and installs both `tsumu` and `tm` (short alias) to `/usr/local/bin/`.

Build only:

```bash
make build
```

## Usage

`tm` is a short alias for `tsumu`. All commands work with either name.

### Browse & Search

```
tsumu                          list all bookmarks (TUI)
tsumu <query>                  search bookmarks
tsumu -f                       favorites only
tsumu -d / -w / -m             today / this week / this month
tsumu -t <tag>                 filter by tag
tsumu -r                       open a random bookmark
```

### Add bookmarks

```bash
tsumu add https://example.com
tsumu add https://example.com -t design,tool -n "great palette tool"
tsumu add https://example.com this is a note
```

Automatically fetches page title, description, and site name via OGP tags. Text after the URL is saved as a note.

X/Twitter links fetch tweet content via FixTweet API. URLs are automatically cleaned (query params and fragments stripped). Domain-based auto-tagging via `[domain_tags]` in config.toml.

### Cloud sync

```bash
tsumu sync                     manual sync
tsumu sync --setup             configure Turso sync
tsumu sync --status            show sync status
tsumu sync --force             full resync
tsumu sync --overwrite         overwrite remote with local (dangerous)
tsumu sync --off               disable sync
```

Data lives in local SQLite, synced via Turso HTTP API on demand. Auto-checks on startup based on `sync.interval`. Reads always hit local DB — zero network latency.

### AI enhancement (optional)

```bash
tsumu config --ai              configure Gemini API
tsumu ai                       batch-generate AI summaries
tsumu ai --empty               only process bookmarks without AI notes
```

Everything works without AI configured. With AI enabled:

- **Enhancement on add** — AI auto-generates descriptions, tags, and multilingual search keywords (runs in background, non-blocking)
- **Query expansion** — Press `Tab` in TUI to expand natural language into keywords (synonyms, cross-language translation)
- **Batch processing** — `tsumu ai` processes all bookmarks concurrently

### Other commands

```bash
tsumu config                   view/edit configuration
tsumu update                   update to latest version
tsumu completion [bash|zsh|fish]  generate shell completion script
```

## TUI keybindings

| Key | Action |
|-----|--------|
| `j` / `↓` | Move cursor down |
| `k` / `↑` | Move cursor up |
| `Enter` | Open in browser |
| `t` | Edit tags (with autocomplete) |
| `f` | Toggle favorite |
| `n` | Edit note inline |
| `N` | Edit note with `$EDITOR` |
| `c` | Copy URL to clipboard |
| `r` | Refetch metadata + AI enhancement |
| `Tab` | AI query expansion (requires AI config) |
| `d` | Delete (with confirmation) |
| `Esc` | Clear message / exit input |
| `q` | Quit |

## Data

Data is stored in `~/.tsumu/`:

- `tsumu.db` — SQLite database (WAL mode, FTS5 full-text search)
- `config.toml` — Configuration file (auto-created on first run)

## Tech stack

- [cobra](https://github.com/spf13/cobra) — CLI framework
- [bubbletea](https://github.com/charmbracelet/bubbletea) — TUI (Elm architecture)
- [lipgloss](https://github.com/charmbracelet/lipgloss) — Terminal styling
- [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) — Pure Go SQLite driver
- [goquery](https://github.com/PuerkitoBio/goquery) — HTML parsing
- [toml](https://github.com/BurntSushi/toml) — Config file format
