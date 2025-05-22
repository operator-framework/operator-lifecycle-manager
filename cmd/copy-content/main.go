package main

import (
	"fmt"
	"os"

	"github.com/otiai10/copy"
	"github.com/spf13/cobra"
)

func main() {
	cmd := newCmd()
	cmd.Execute()
}

func newCmd() *cobra.Command {
	var (
		catalogFrom string
		catalogTo   string
		cacheFrom   string
		cacheTo     string
	)
	cmd := &cobra.Command{
		Use:   "copy-content",
		Short: "Copy catalog and cache content",
		Long:  `Copy catalog and cache content`,
		Run: func(cmd *cobra.Command, args []string) {
			var contentMap = make(map[string]string, 2)
			contentMap[catalogFrom] = catalogTo
			if cmd.Flags().Changed("cache.from") {
				contentMap[cacheFrom] = cacheTo
			}

			for from, to := range contentMap {
				if err := os.RemoveAll(to); err != nil {
					fmt.Printf("failed to remove %s: %s", to, err)
					os.Exit(1)
				}
				if err := copy.Copy(from, to); err != nil {
					fmt.Printf("failed to copy %s to %s: %s\n", from, to, err)
					os.Exit(1)
				}
			}
		},
	}

	cmd.Flags().StringVar(&catalogFrom, "catalog.from", "", "Path to catalog contents to copy")
	cmd.Flags().StringVar(&catalogTo, "catalog.to", "", "Path to where catalog contents should be copied")
	cmd.Flags().StringVar(&cacheFrom, "cache.from", "", "Path to cache contents to copy (required if cache.to is set)")              // optional
	cmd.Flags().StringVar(&cacheTo, "cache.to", "", "Path to where cache contents should be copied (required if cache.from is set)") // optional
	cmd.MarkFlagRequired("catalog.from")
	cmd.MarkFlagRequired("catalog.to")
	cmd.MarkFlagsRequiredTogether("cache.from", "cache.to")
	return cmd
}
