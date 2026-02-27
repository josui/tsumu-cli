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
