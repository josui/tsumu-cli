// cli/config/config.go

// Package config 管理 tsumu 的配置目录和配置文件。
// 数据目录默认为 ~/.tsumu/，包含 SQLite 数据库和 config.toml 配置文件。
package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Config 保存 tsumu 的运行配置。
// Dir 是数据目录路径（默认 ~/.tsumu/）。
type Config struct {
	Dir string // 数据目录路径（例如 ~/.tsumu/）

	// TUI 设置
	PageSize int `toml:"page_size,omitempty"` // 每页显示条数，默认 5

	// [sync] 段落 — Phase 4 才用，现在预留结构
	Sync SyncConfig `toml:"sync"`

	// [ai] 段落 — Phase 3 才用，现在预留结构
	AI AIConfig `toml:"ai"`
}

// GetPageSize 返回每页条数，0 或未设置时返回默认值 5。
func (c *Config) GetPageSize() int {
	if c.PageSize <= 0 {
		return 5
	}
	return c.PageSize
}

// SyncConfig 是 Turso 云端同步配置（Phase 4）。
type SyncConfig struct {
	Enabled    bool   `toml:"enabled"`
	URL        string `toml:"url"`
	AuthToken  string `toml:"auth_token"`
	Interval   string `toml:"interval,omitempty"`
	LastSynced string `toml:"last_synced,omitempty"`
}

// ParseInterval 将 interval 字符串（例如 "24h", "7d"）解析为 time.Duration。
// 空字符串或无法解析时返回默认值 24h。
func (s *SyncConfig) ParseInterval() time.Duration {
	const defaultInterval = 24 * time.Hour
	raw := strings.TrimSpace(s.Interval)
	if raw == "" {
		return defaultInterval
	}

	// 支持 "Nd" 格式（天数）
	if strings.HasSuffix(raw, "d") {
		days, err := strconv.Atoi(raw[:len(raw)-1])
		if err != nil || days <= 0 {
			return defaultInterval
		}
		return time.Duration(days) * 24 * time.Hour
	}

	// 其他情况用标准 time.ParseDuration（支持 "1h", "12h" 等）
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return defaultInterval
	}
	return d
}

// NeedsSync 判断是否需要同步：enabled 且（从未同步 或 距上次同步已超过 interval）。
func (s *SyncConfig) NeedsSync() bool {
	if !s.Enabled {
		return false
	}
	if s.LastSynced == "" {
		return true
	}
	last, err := time.Parse(time.RFC3339, s.LastSynced)
	if err != nil {
		return true
	}
	return time.Since(last) >= s.ParseInterval()
}

// AIConfig 是 AI embedding 配置（Phase 3）。
type AIConfig struct {
	Provider string `toml:"embedding_provider"` // "gemini" | "jina" | "openai" | "ollama"
	APIKey   string `toml:"api_key"`
}

// Default 返回默认配置，数据目录为 ~/.tsumu/。
func Default() *Config {
	home, err := os.UserHomeDir()
	if err != nil {
		// 极端情况：无法获取 home 目录，用当前目录兜底
		home = "."
	}
	return &Config{
		Dir: filepath.Join(home, ".tsumu"),
	}
}

// DBPath 返回 SQLite 数据库文件的完整路径。
func (c *Config) DBPath() string {
	return filepath.Join(c.Dir, "tsumu.db")
}

// ConfigPath 返回 config.toml 的完整路径。
func (c *Config) ConfigPath() string {
	return filepath.Join(c.Dir, "config.toml")
}

// IsFirstRun 检测是否首次运行（config.toml 不存在）。
func (c *Config) IsFirstRun() bool {
	_, err := os.Stat(c.ConfigPath())
	return os.IsNotExist(err)
}

// EnsureDir 确保数据目录存在，不存在则创建。
// os.MkdirAll 是幂等的：目录已存在时不会报错。
func (c *Config) EnsureDir() error {
	return os.MkdirAll(c.Dir, 0755)
}

// Load 从 config.toml 读取配置。文件不存在时返回默认配置（不报错）。
func (c *Config) Load() error {
	path := c.ConfigPath()

	// os.ReadFile 一次性读取整个文件到内存
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// 配置文件不存在是正常的（首次运行），返回默认值
			return nil
		}
		return err
	}

	// toml.Unmarshal 将 TOML 文本解析到结构体
	return toml.Unmarshal(data, c)
}

// Save 将当前配置写入 config.toml。
func (c *Config) Save() error {
	// 确保目录存在
	if err := c.EnsureDir(); err != nil {
		return err
	}

	f, err := os.Create(c.ConfigPath())
	if err != nil {
		return err
	}
	defer f.Close() // defer: 函数返回前自动关闭文件

	// toml.NewEncoder 将结构体序列化为 TOML 格式写入文件
	encoder := toml.NewEncoder(f)
	return encoder.Encode(c)
}
