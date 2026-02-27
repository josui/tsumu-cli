package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	importChrome  bool
	importFirefox bool
)

var importCmd = &cobra.Command{
	Use:   "import [file]",
	Short: "Import bookmarks",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("coming soon")
		return nil
	},
}

func init() {
	importCmd.Flags().BoolVar(&importChrome, "chrome", false, "import from Chrome")
	importCmd.Flags().BoolVar(&importFirefox, "firefox", false, "import from Firefox")
}
