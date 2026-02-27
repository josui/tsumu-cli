package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Check for dead links",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("coming soon")
		return nil
	},
}
