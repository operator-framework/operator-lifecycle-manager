package generate

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/operator-framework/operator-registry/alpha/action"
	"github.com/operator-framework/operator-registry/pkg/containertools"
)

func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate various artifacts for declarative config indexes",
	}
	cmd.AddCommand(
		newDockerfileCmd(),
	)
	return cmd
}

func newDockerfileCmd() *cobra.Command {
	var (
		baseImage      string
		extraLabelStrs []string
	)
	cmd := &cobra.Command{
		Use:   "dockerfile <dcRootDir>",
		Args:  cobra.ExactArgs(1),
		Short: "Generate a Dockerfile for a declarative config index",
		Long: `Generate a Dockerfile for a declarative config index.

This command creates a Dockerfile in the same directory as the <dcRootDir>
(named <dcDirName>.Dockerfile) that can be used to build the index. If a
Dockerfile with the same name already exists, this command will fail.

When specifying extra labels, note that if duplicate keys exist, only the last
value of each duplicate key will be added to the generated Dockerfile.
`,
		RunE: func(_ *cobra.Command, args []string) error {
			fromDir := filepath.Clean(args[0])

			extraLabels, err := parseLabels(extraLabelStrs)
			if err != nil {
				return err
			}

			dir, indexName := filepath.Split(fromDir)
			dockerfilePath := filepath.Join(dir, fmt.Sprintf("%s.Dockerfile", indexName))

			if err := ensureNotExist(dockerfilePath); err != nil {
				logrus.Fatal(err)
			}

			if s, err := os.Stat(fromDir); err != nil {
				return err
			} else if !s.IsDir() {
				return fmt.Errorf("provided root path %q is not a directory", fromDir)
			}

			f, err := os.OpenFile(dockerfilePath, os.O_CREATE|os.O_WRONLY, 0666)
			if err != nil {
				logrus.Fatal(err)
			}
			defer f.Close()

			gen := action.GenerateDockerfile{
				BaseImage:   baseImage,
				IndexDir:    indexName,
				ExtraLabels: extraLabels,
				Writer:      f,
			}
			if err := gen.Run(); err != nil {
				log.Fatal(err)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&baseImage, "binary-image", "i", containertools.DefaultBinarySourceImage, "Image in which to build catalog.")
	cmd.Flags().StringSliceVarP(&extraLabelStrs, "extra-labels", "l", []string{}, "Extra labels to include in the generated Dockerfile. Labels should be of the form 'key=value'.")
	return cmd
}

func parseLabels(labelStrs []string) (map[string]string, error) {
	labels := map[string]string{}
	for _, l := range labelStrs {
		spl := strings.SplitN(l, "=", 2)
		if len(spl) != 2 {
			return nil, fmt.Errorf("invalid label %q", l)
		}
		labels[spl[0]] = spl[1]
	}
	return labels, nil
}

func ensureNotExist(path string) error {
	_, err := os.Stat(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		return fmt.Errorf("path %q: %w", path, os.ErrExist)
	}
	return nil
}
