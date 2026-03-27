package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/josui/tsumu-cli/internal/db"
)

var (
	flagAI   bool
	flagSync bool
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Configure tsumu settings",
	RunE: func(cmd *cobra.Command, args []string) error {
		if flagSync {
			return runSyncSetup()
		}
		if flagAI {
			return RunConfigAI()
		}
		return cmd.Help()
	},
}

func init() {
	configCmd.Flags().BoolVar(&flagAI, "ai", false, "configure AI enhancement provider")
	configCmd.Flags().BoolVar(&flagSync, "sync", false, "configure cloud sync (Turso)")
}

// RunConfigAI runs the interactive AI configuration flow.
// Exported so onboarding can reuse it.
func RunConfigAI() error {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println()
	fmt.Println("  AI Enhancement Setup")
	fmt.Println()
	fmt.Println("  Provider: Gemini (Google AI)")
	fmt.Println("  Features: description generation, tag suggestion, query expansion")
	fmt.Println()
	// 已有 key 时显示掩码
	if existing := Cfg.AI.APIKey; existing != "" {
		fmt.Printf("  Current API key: ***%s\n", existing[max(0, len(existing)-4):])
	}
	fmt.Print("  Gemini API Key (leave blank to keep current): ")
	key, _ := reader.ReadString('\n')
	key = strings.TrimSpace(key)
	if key == "" && Cfg.AI.APIKey == "" {
		return fmt.Errorf("API key is required")
	}
	Cfg.AI.Provider = "gemini"
	if key != "" {
		Cfg.AI.APIKey = key
	}

	// Generation model
	fmt.Printf("  Generation model [%s]: ", Cfg.AI.GetGenModel())
	model, _ := reader.ReadString('\n')
	model = strings.TrimSpace(model)
	if model != "" {
		Cfg.AI.GenModel = model
	}

	// Language for AI notes
	fmt.Println("  AI note language: \"en\", \"zh,en\", \"zh,en,ja\" etc.")
	fmt.Printf("  [%s]: ", Cfg.AI.GetLang())
	lang, _ := reader.ReadString('\n')
	lang = strings.TrimSpace(lang)
	if lang != "" {
		Cfg.AI.Lang = lang
	}

	// 敏感信息（API key）写入 .env，其余写入 config.toml
	if err := Cfg.SaveEnv(); err != nil {
		return fmt.Errorf("save .env: %w", err)
	}
	if err := Cfg.Save(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Println()
	fmt.Printf("  ✓ AI configured: %s (model: %s)\n", Cfg.AI.Provider, Cfg.AI.GetGenModel())

	// 检查空描述书签数，提示批量补缺
	count, err := db.CountEmptyAiNote(Store.DB)
	if err == nil && count > 0 {
		fmt.Printf("\n  Found %d bookmarks without AI notes.\n", count)
		fmt.Println("  Run `tsumu ai` to enhance existing bookmarks.")
	}
	fmt.Println()

	return nil
}
