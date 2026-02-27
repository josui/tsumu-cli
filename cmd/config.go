package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
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
	configCmd.Flags().BoolVar(&flagAI, "ai", false, "configure AI embedding provider")
}

// RunConfigAI runs the interactive AI configuration flow.
// Exported so onboarding can reuse it.
func RunConfigAI() error {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println()
	fmt.Println("  AI Semantic Search Setup")
	fmt.Println()
	fmt.Println("  Select embedding provider:")
	fmt.Println("  1. gemini  (Google, needs API key, free tier: 1500 req/min)")
	fmt.Println("  2. ollama  (local, no API key needed, needs Ollama running)")
	fmt.Println()
	fmt.Print("> ")
	input, _ := reader.ReadString('\n')
	choice := strings.TrimSpace(input)

	switch choice {
	case "1", "gemini":
		Cfg.AI.Provider = "gemini"
		fmt.Print("  API Key: ")
		key, _ := reader.ReadString('\n')
		key = strings.TrimSpace(key)
		if key == "" {
			return fmt.Errorf("API key is required for Gemini")
		}
		Cfg.AI.APIKey = key

	case "2", "ollama":
		Cfg.AI.Provider = "ollama"
		fmt.Printf("  Model name (default: nomic-embed-text): ")
		model, _ := reader.ReadString('\n')
		model = strings.TrimSpace(model)
		if model != "" {
			Cfg.AI.Model = model
		}

	default:
		return fmt.Errorf("invalid choice: %q", choice)
	}

	// Dimension
	fmt.Printf("  Embedding dimension (default: 768): ")
	dimInput, _ := reader.ReadString('\n')
	dimInput = strings.TrimSpace(dimInput)
	if dimInput != "" {
		var dim int
		if _, err := fmt.Sscanf(dimInput, "%d", &dim); err == nil && dim > 0 {
			Cfg.AI.Dimension = dim
		}
	}

	if err := Cfg.Save(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Println()
	fmt.Printf("  ✓ AI configured: %s (%dd)\n", Cfg.AI.Provider, Cfg.AI.GetDimension())
	fmt.Println("  Run `tsumu embed` to generate embeddings for existing bookmarks.")
	fmt.Println()

	return nil
}
