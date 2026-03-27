// Package cmd defines tsumu CLI commands using cobra framework.
package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/josui/tsumu-cli/config"
	"github.com/josui/tsumu-cli/internal/db"
	"github.com/josui/tsumu-cli/internal/ui"
)

var (
	flagFav   bool   // -f
	flagDay   bool   // -d
	flagWeek  bool   // -w
	flagMonth bool   // -m
	flagTag   string // -t <tag>
	flagRand  bool   // -r
)

var Store *db.Store
var Cfg *config.Config

var rootCmd = &cobra.Command{
	Use:     "tsumu [query]",
	Short:   "tsumu — local-first CLI bookmark manager",
	Long: `tsumu — local-first CLI bookmark manager.
Save links fast, find them faster.

Browse:
  tsumu                    list all bookmarks
  tsumu <query>            search bookmarks
  tsumu -f                 favorites only
  tsumu -d / -w / -m       today / this week / this month
  tsumu -t <tag>           filter by tag
  tsumu -r                 open a random bookmark

Manage:
  tsumu add <url>          add bookmark
  tsumu sync               sync with Turso cloud
  tsumu update             update to latest version`,

	SilenceUsage: true,
	Args:         cobra.ArbitraryArgs,

	RunE: func(cmd *cobra.Command, args []string) error {
		query := strings.Join(args, " ")

		var since string
		now := time.Now()
		switch {
		case flagDay:
			since = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).UTC().Format(time.RFC3339)
		case flagWeek:
			since = now.AddDate(0, 0, -7).UTC().Format(time.RFC3339)
		case flagMonth:
			since = now.AddDate(0, -1, 0).UTC().Format(time.RFC3339)
		}

		if flagRand {
			bm, err := db.RandomBookmark(Store.DB, since, flagFav, flagTag)
			if err != nil {
				return err
			}
			if bm == nil {
				fmt.Println("No bookmarks found")
				return nil
			}
			if err := ui.OpenBrowser(bm.URL); err != nil {
				return err
			}
			name := bm.SiteName
			if name == "" {
				name = bm.Title
			}
			fmt.Printf("✓ Opened %s\n", name)
			return nil
		}

		return runSearch(query, flagFav, since, flagTag)
	},
}

// addCmd is defined in add.go

func Execute() {
	rootCmd.Version = Version
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.Flags().BoolVarP(&flagFav, "fav", "f", false, "show favorites only")
	rootCmd.Flags().BoolVarP(&flagDay, "day", "d", false, "show bookmarks added today")
	rootCmd.Flags().BoolVarP(&flagWeek, "week", "w", false, "show bookmarks added this week")
	rootCmd.Flags().BoolVarP(&flagMonth, "month", "m", false, "show bookmarks added this month")
	rootCmd.Flags().StringVarP(&flagTag, "tag", "t", "", "filter by tag")
	rootCmd.Flags().BoolVarP(&flagRand, "random", "r", false, "open a random bookmark")

	rootCmd.MarkFlagsMutuallyExclusive("day", "week", "month")

	rootCmd.AddCommand(addCmd)
	rootCmd.AddCommand(aiCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(syncCmd)
	rootCmd.AddCommand(updateCmd)
	rootCmd.AddCommand(completionCmd)
}
