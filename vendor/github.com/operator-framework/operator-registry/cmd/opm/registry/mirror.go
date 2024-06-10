package registry

import (
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/operator-framework/operator-registry/pkg/mirror"
	"github.com/operator-framework/operator-registry/pkg/sqlite"
)

func MirrorCmd() *cobra.Command {
	// TODO(joelanford): MirrorCmd is unused. Delete it and any other code used only by it.
	o := mirror.DefaultImageIndexMirrorerOptions()
	cmd := &cobra.Command{
		Hidden: true,
		Use:    "mirror [src image] [dest image]",
		Short:  "mirror an operator-registry catalog",
		Long: `mirror an operator-registry catalog image from one registry to another

` + sqlite.DeprecationMessage,

		PreRunE: func(cmd *cobra.Command, args []string) error {
			if debug, _ := cmd.Flags().GetBool("debug"); debug {
				logrus.SetLevel(logrus.DebugLevel)
			}
			return nil
		},

		RunE: func(cmd *cobra.Command, args []string) error {
			src := args[0]
			dest := args[1]

			mirrorer, err := mirror.NewIndexImageMirror(o.ToOption(), mirror.WithSource(src), mirror.WithDest(dest))
			if err != nil {
				return err
			}
			_, err = mirrorer.Mirror()
			if err != nil {
				return err
			}
			return nil
		},
		Args: cobra.ExactArgs(2),
	}
	flags := cmd.Flags()

	cmd.Flags().Bool("debug", false, "Enable debug logging.")
	flags.StringVar(&o.ManifestDir, "--to-manifests", "manifests", "Local path to store manifests.")

	return cmd
}
