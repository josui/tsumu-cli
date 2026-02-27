package cmd

import "github.com/spf13/cobra"

var favCmd = &cobra.Command{
	Use:   "fav",
	Short: "List favorite bookmarks",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runSearch("", true, "", "")
	},
}
