package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var tagsCmd = &cobra.Command{
	Use:   "tags",
	Short: "List all tags",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("coming soon")
		return nil
	},
}
