package list

import (
	"os"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/operator-framework/operator-registry/alpha/action"
	"github.com/operator-framework/operator-registry/cmd/opm/internal/util"
)

const humanReadabilityOnlyNote = `NOTE: This is meant to be used for convenience and human-readability only. The
CLI and output format are subject to change, so it is not recommended to depend
on the output in any programs or scripts. Use the "render" subcommand to do
more complex processing and automation.`

func NewCmd() *cobra.Command {
	list := &cobra.Command{
		Use:   "list",
		Short: "List contents of an index",
		Long: `The list subcommands print the contents of an index.

` + humanReadabilityOnlyNote,
	}

	list.AddCommand(newPackagesCmd(), newChannelsCmd(), newBundlesCmd())
	return list
}

func newPackagesCmd() *cobra.Command {
	logger := logrus.New()

	return &cobra.Command{
		Use:   "packages <indexRef>",
		Short: "List packages in an index",
		Long: `The "channels" command lists the channels from the specified index.

` + humanReadabilityOnlyNote,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, err := util.CreateCLIRegistry(cmd)
			if err != nil {
				logger.Fatal(err)
			}
			defer reg.Destroy()
			lp := action.ListPackages{IndexReference: args[0], Registry: reg}
			res, err := lp.Run(cmd.Context())
			if err != nil {
				logger.Fatal(err)
			}
			if err := res.WriteColumns(os.Stdout); err != nil {
				logger.Fatal(err)
			}
			return nil
		},
	}
}

func newChannelsCmd() *cobra.Command {
	logger := logrus.New()

	return &cobra.Command{
		Use:   "channels <indexRef> [packageName]",
		Short: "List package channels in an index",
		Long: `The "channels" command lists the channels from the specified index and package.

` + humanReadabilityOnlyNote,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, err := util.CreateCLIRegistry(cmd)
			if err != nil {
				logger.Fatal(err)
			}
			defer reg.Destroy()
			lc := action.ListChannels{IndexReference: args[0], Registry: reg}
			if len(args) > 1 {
				lc.PackageName = args[1]
			}
			res, err := lc.Run(cmd.Context())
			if err != nil {
				logger.Fatal(err)
			}
			if err := res.WriteColumns(os.Stdout); err != nil {
				logger.Fatal(err)
			}
			return nil
		},
	}
}

func newBundlesCmd() *cobra.Command {
	logger := logrus.New()

	return &cobra.Command{
		Use:   "bundles <indexRef> <packageName>",
		Short: "List package bundles in an index",
		Long: `The "bundles" command lists the bundles from the specified index and package.
Bundles that exist in multiple channels are duplicated in the output (one
for each channel in which the bundle is present).

` + humanReadabilityOnlyNote,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, err := util.CreateCLIRegistry(cmd)
			if err != nil {
				logger.Fatal(err)
			}
			defer reg.Destroy()
			lb := action.ListBundles{IndexReference: args[0], Registry: reg}
			if len(args) > 1 {
				lb.PackageName = args[1]
			}
			res, err := lb.Run(cmd.Context())
			if err != nil {
				logger.Fatal(err)
			}
			if err := res.WriteColumns(os.Stdout); err != nil {
				logger.Fatal(err)
			}
			return nil
		},
	}
}
