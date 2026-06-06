// cli/config/config_test.go
package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEnsureDir_CreatesDirectory(t *testing.T) {
	// 使用临时目录，避免影响用户真实的 ~/.tsumu
	tmpDir := filepath.Join(t.TempDir(), ".tsumu")

	cfg := &Config{Dir: tmpDir}
	if err := cfg.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir failed: %v", err)
	}

	// 验证目录已创建
	info, err := os.Stat(tmpDir)
	if err != nil {
		t.Fatalf("directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("path is not a directory")
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := Default()
	if cfg.Dir == "" {
		t.Fatal("Dir should not be empty")
	}
	if cfg.DBPath() == "" {
		t.Fatal("DBPath should not be empty")
	}
}

func TestParseInterval(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
	}{
		{"1h", 1 * time.Hour},
		{"12h", 12 * time.Hour},
		{"24h", 24 * time.Hour},
		{"7d", 7 * 24 * time.Hour},
		{"30d", 30 * 24 * time.Hour},
		{"", 24 * time.Hour},
		{"invalid", 24 * time.Hour},
	}
	for _, tt := range tests {
		cfg := &Config{Sync: SyncConfig{Interval: tt.input}}
		got := cfg.Sync.ParseInterval()
		if got != tt.expected {
			t.Errorf("ParseInterval(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

func TestNeedsSync(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name       string
		lastSynced string
		interval   string
		want       bool
	}{
		{"no last_synced", "", "24h", true},
		{"recently synced", now.Add(-1 * time.Hour).UTC().Format(time.RFC3339), "24h", false},
		{"overdue", now.Add(-25 * time.Hour).UTC().Format(time.RFC3339), "24h", true},
		{"exactly at boundary", now.Add(-24 * time.Hour).UTC().Format(time.RFC3339), "24h", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := SyncConfig{
				Enabled:    true,
				LastSynced: tt.lastSynced,
				Interval:   tt.interval,
			}
			if got := cfg.NeedsSync(); got != tt.want {
				t.Errorf("NeedsSync() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDomainTags_DefaultValues(t *testing.T) {
	cfg := Default()
	if cfg.DomainTags == nil {
		t.Fatal("DomainTags should have default values")
	}
	if cfg.DomainTags["x.com"] != "x" {
		t.Errorf("expected x.com → x, got %q", cfg.DomainTags["x.com"])
	}
	if cfg.DomainTags["github.com"] != "github" {
		t.Errorf("expected github.com → github, got %q", cfg.DomainTags["github.com"])
	}
}

func TestDomainTags_SaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{
		Dir: dir,
		DomainTags: map[string]string{
			"example.com": "example",
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded := &Config{Dir: dir}
	if err := loaded.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.DomainTags["example.com"] != "example" {
		t.Errorf("expected example.com → example after round-trip, got %q", loaded.DomainTags["example.com"])
	}
}

func TestAIConfig_IsConfigured(t *testing.T) {
	tests := []struct {
		cfg  AIConfig
		want bool
	}{
		{AIConfig{}, false},
		{AIConfig{Provider: "gemini", APIKey: "key"}, true},
		{AIConfig{Provider: "ollama"}, true},
	}
	for _, tt := range tests {
		if got := tt.cfg.IsConfigured(); got != tt.want {
			t.Errorf("IsConfigured() = %v, want %v for %+v", got, tt.want, tt.cfg)
		}
	}
}

func TestAIConfig_GetDimension(t *testing.T) {
	tests := []struct {
		dim  int
		want int
	}{
		{0, 768},
		{-1, 768},
		{1024, 1024},
	}
	for _, tt := range tests {
		cfg := AIConfig{Dimension: tt.dim}
		if got := cfg.GetDimension(); got != tt.want {
			t.Errorf("GetDimension() = %d, want %d", got, tt.want)
		}
	}
}

func TestAIConfig_GetModel(t *testing.T) {
	tests := []struct {
		provider string
		model    string
		want     string
	}{
		{"gemini", "", "gemini-embedding-001"},
		{"ollama", "", "nomic-embed-text"},
		{"ollama", "custom-model", "custom-model"},
		{"", "", ""},
	}
	for _, tt := range tests {
		cfg := AIConfig{Provider: tt.provider, Model: tt.model}
		if got := cfg.GetModel(); got != tt.want {
			t.Errorf("GetModel() = %q, want %q", got, tt.want)
		}
	}
}

func TestLoadEnv(t *testing.T) {
	dir := t.TempDir()
	envContent := `# tsumu environment variables
TSUMU_SYNC_URL="libsql://test.turso.io"
TSUMU_SYNC_AUTH_TOKEN="token-123"
TSUMU_AI_API_KEY='my-api-key'

# 空行和注释应被忽略
INVALID_LINE_NO_EQUALS
`
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(envContent), 0600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	cfg := &Config{Dir: dir}
	if err := cfg.LoadEnv(); err != nil {
		t.Fatalf("LoadEnv failed: %v", err)
	}

	if cfg.Sync.URL != "libsql://test.turso.io" {
		t.Errorf("Sync.URL = %q, want %q", cfg.Sync.URL, "libsql://test.turso.io")
	}
	if cfg.Sync.AuthToken != "token-123" {
		t.Errorf("Sync.AuthToken = %q, want %q", cfg.Sync.AuthToken, "token-123")
	}
	if cfg.AI.APIKey != "my-api-key" {
		t.Errorf("AI.APIKey = %q, want %q", cfg.AI.APIKey, "my-api-key")
	}
}

func TestLoadEnv_OverridesToml(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("TSUMU_AI_API_KEY=\"from-dotenv\"\n"), 0600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	cfg := &Config{Dir: dir, AI: AIConfig{APIKey: "from-toml"}}
	if err := cfg.LoadEnv(); err != nil {
		t.Fatalf("LoadEnv failed: %v", err)
	}

	if cfg.AI.APIKey != "from-dotenv" {
		t.Errorf("AI.APIKey = %q, want %q (.env should override toml)", cfg.AI.APIKey, "from-dotenv")
	}
}

func TestSaveEnvRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{
		Dir: dir,
		Sync: SyncConfig{
			URL:       "libsql://my-db.turso.io",
			AuthToken: "eyJhbGciOiJFZERTQSIsInR5cCI6IkpXVCJ9.test-token",
		},
		AI: AIConfig{APIKey: "AIzaSy-test-key_123"},
	}
	if err := cfg.SaveEnv(); err != nil {
		t.Fatalf("SaveEnv failed: %v", err)
	}

	// 重新加载，验证 round-trip
	loaded := &Config{Dir: dir}
	if err := loaded.LoadEnv(); err != nil {
		t.Fatalf("LoadEnv failed: %v", err)
	}

	if loaded.Sync.URL != cfg.Sync.URL {
		t.Errorf("Sync.URL = %q, want %q", loaded.Sync.URL, cfg.Sync.URL)
	}
	if loaded.Sync.AuthToken != cfg.Sync.AuthToken {
		t.Errorf("Sync.AuthToken = %q, want %q", loaded.Sync.AuthToken, cfg.Sync.AuthToken)
	}
	if loaded.AI.APIKey != cfg.AI.APIKey {
		t.Errorf("AI.APIKey = %q, want %q", loaded.AI.APIKey, cfg.AI.APIKey)
	}

	// 验证文件权限 0600
	info, err := os.Stat(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatalf("stat .env: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf(".env permissions = %o, want 0600", perm)
	}
}

func TestLoadEnv_MigrateFromToml(t *testing.T) {
	dir := t.TempDir()
	// 模拟旧版 config.toml 含敏感字段
	oldToml := `[sync]
enabled = true
url = "libsql://old.turso.io"
auth_token = "old-token"
interval = "24h"

[ai]
provider = "gemini"
api_key = "old-api-key"
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(oldToml), 0644); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}

	// .env 不存在，LoadEnv 应自动迁移
	cfg := &Config{Dir: dir}
	if err := cfg.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if err := cfg.LoadEnv(); err != nil {
		t.Fatalf("LoadEnv migration failed: %v", err)
	}

	// 验证 .env 已自动创建
	if _, err := os.Stat(filepath.Join(dir, ".env")); err != nil {
		t.Fatalf(".env should be created by migration: %v", err)
	}

	// 验证敏感字段已迁移到 Config
	if cfg.Sync.URL != "libsql://old.turso.io" {
		t.Errorf("migrated URL = %q, want %q", cfg.Sync.URL, "libsql://old.turso.io")
	}
	if cfg.Sync.AuthToken != "old-token" {
		t.Errorf("migrated AuthToken = %q, want %q", cfg.Sync.AuthToken, "old-token")
	}
	if cfg.AI.APIKey != "old-api-key" {
		t.Errorf("migrated APIKey = %q, want %q", cfg.AI.APIKey, "old-api-key")
	}

	// 验证 round-trip: 新实例 Load + LoadEnv 能读到
	loaded := &Config{Dir: dir}
	loaded.Load()
	loaded.LoadEnv()
	if loaded.Sync.URL != "libsql://old.turso.io" {
		t.Errorf("after round-trip URL = %q, want %q", loaded.Sync.URL, "libsql://old.turso.io")
	}
}

func TestLoadEnv_FileNotExist(t *testing.T) {
	cfg := &Config{Dir: t.TempDir()}
	if err := cfg.LoadEnv(); err != nil {
		t.Fatalf("LoadEnv should not error on missing .env: %v", err)
	}
}

func TestSaveLoadWithNewFields(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &Config{
		Dir: tmpDir,
		Sync: SyncConfig{
			Enabled:    true,
			URL:        "libsql://test.turso.io",
			AuthToken:  "token123",
			Interval:   "12h",
			LastSynced: "2026-02-27T10:00:00Z",
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	loaded := &Config{Dir: tmpDir}
	if err := loaded.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.Sync.Interval != "12h" {
		t.Errorf("Interval = %q, want %q", loaded.Sync.Interval, "12h")
	}
	if loaded.Sync.LastSynced != "2026-02-27T10:00:00Z" {
		t.Errorf("LastSynced = %q, want %q", loaded.Sync.LastSynced, "2026-02-27T10:00:00Z")
	}
}

func TestConfig_PullCursorRoundTrip(t *testing.T) {
	dir := t.TempDir()
	c := &Config{Dir: dir}
	c.Sync.Enabled = true
	c.Sync.LastSynced = "2026-06-06T12:05:26Z"
	c.Sync.PullCursor = "2026-05-27T03:07:31Z"

	if err := c.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded := &Config{Dir: dir}
	if err := loaded.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.Sync.PullCursor != "2026-05-27T03:07:31Z" {
		t.Errorf("PullCursor = %q, want '2026-05-27T03:07:31Z'", loaded.Sync.PullCursor)
	}
}
