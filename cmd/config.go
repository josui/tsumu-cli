package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/josui/tsumu-cli/internal/db"
)

var flagAI bool

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Configure tsumu settings",
	RunE: func(cmd *cobra.Command, args []string) error {
		if flagAI {
			return RunConfigAI()
		}
		return cmd.Help()
	},
}

func init() {
	configCmd.Flags().BoolVar(&flagAI, "ai", false, "configure AI enhancement provider")
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
	fmt.Println("  推荐使用环境变量配置 API key:")
	fmt.Println("    export TSUMU_AI_API_KEY=\"your-api-key\"")
	fmt.Println()

	// 检查环境变量是否已设置
	envKey := os.Getenv("TSUMU_AI_API_KEY")
	var key string
	if envKey != "" {
		fmt.Printf("  ✓ TSUMU_AI_API_KEY detected (***%s)\n", envKey[max(0, len(envKey)-4):])
		fmt.Print("  Save to config.toml too? (leave blank to skip): ")
		key, _ = reader.ReadString('\n')
		key = strings.TrimSpace(key)
		// 即使用户跳过，环境变量也够用
	} else {
		fmt.Print("  Gemini API Key: ")
		key, _ = reader.ReadString('\n')
		key = strings.TrimSpace(key)
		if key == "" {
			return fmt.Errorf("API key is required. Set TSUMU_AI_API_KEY or enter here")
		}
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
