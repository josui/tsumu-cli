// cli/config/config_test.go
package config

import (
	"os"
	"path/filepath"
	"testing"
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
