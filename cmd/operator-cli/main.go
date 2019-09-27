package main

import (
	"os"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/operator-framework/operator-lifecycle-manager/cmd/operator-cli/bundle"
)

func main() {
	var rootCmd = &cobra.Command{
		Short: "operator-cli",
		Long:  `A CLI tool to perform operator-related tasks.`,

		PreRunE: func(cmd *cobra.Command, args []string) error {
			if debug, _ := cmd.Flags().GetBool("debug"); debug {
				log.SetLevel(log.DebugLevel)
			}
			return nil
		},
	}

	rootCmd.AddCommand(bundle.NewCmd())

	rootCmd.Flags().Bool("debug", false, "enable debug logging")
	if err := rootCmd.Flags().MarkHidden("debug"); err != nil {
		log.Panic(err.Error())
	}

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
