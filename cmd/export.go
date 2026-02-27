package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	exportJSON bool
	exportMD   bool
)

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export bookmarks",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("coming soon")
		return nil
	},
}

func init() {
	exportCmd.Flags().BoolVar(&exportJSON, "json", false, "export as JSON")
	exportCmd.Flags().BoolVar(&exportMD, "md", false, "export as Markdown")
}
