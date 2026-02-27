package cmd

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update tsumu to the latest version",
	RunE: func(cmd *cobra.Command, args []string) error {
		brewPath, err := exec.LookPath("brew")
		if err != nil {
			fmt.Println("  brew not found. Please update manually:")
			fmt.Println("  go install github.com/josui/tsumu-cli@latest")
			return nil
		}

		fmt.Println("  Updating tsumu via Homebrew...")
		c := exec.Command(brewPath, "upgrade", "josui/tap/tsumu")
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		if err := c.Run(); err != nil {
			return fmt.Errorf("brew upgrade failed: %w", err)
		}
		fmt.Println("  ✓ Updated successfully")
		return nil
	},
}
