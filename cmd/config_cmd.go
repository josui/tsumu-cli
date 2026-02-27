package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	configAI   bool
	configShow bool
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Configure tsumu",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("coming soon")
		return nil
	},
}

func init() {
	configCmd.Flags().BoolVar(&configAI, "ai", false, "configure AI settings")
	configCmd.Flags().BoolVar(&configShow, "show", false, "show current config")
}
