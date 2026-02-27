// cli/config/config.go

// Package config 管理 tsumu 的配置目录和配置文件。
// 数据目录默认为 ~/.tsumu/，包含 SQLite 数据库和 config.toml 配置文件。
package config

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config 保存 tsumu 的运行配置。
// Dir 是数据目录路径（默认 ~/.tsumu/）。
type Config struct {
	Dir string // 数据目录路径（例如 ~/.tsumu/）

	// [sync] 段落 — Phase 4 才用，现在预留结构
	Sync SyncConfig `toml:"sync"`

	// [ai] 段落 — Phase 3 才用，现在预留结构
	AI AIConfig `toml:"ai"`
}

// SyncConfig 是 Turso 云端同步配置（Phase 4）。
type SyncConfig struct {
	Enabled   bool   `toml:"enabled"`
	URL       string `toml:"url"`
	AuthToken string `toml:"auth_token"`
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
