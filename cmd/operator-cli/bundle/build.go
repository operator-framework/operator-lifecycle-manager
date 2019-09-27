package bundle

import (
	"fmt"
	"os"
	"os/exec"
	"path"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var (
	dirBuildArgs     string
	tagBuildArgs     string
	imageBuilderArgs string
)

// newBundleBuildCmd returns a command that will build operator bundle image.
func newBundleBuildCmd() *cobra.Command {
	bundleBuildCmd := &cobra.Command{
		Use:   "build",
		Short: "Build operator bundle image",
		Long: `The operator-cli bundle build command will generate operator
        bundle metadata if needed and build bundle image with operator manifest
        and metadata.

        For example: The command will generate annotations.yaml metadata plus
        Dockerfile for bundle image and then build a container image from
        provided operator bundle manifests generated metadata
        e.g. "quay.io/example/operator:v0.0.1".

        After the build process is completed, a container image would be built
        locally in docker and available to push to a container registry.

        $ operator-cli bundle build -dir /test/0.0.1/ -t quay.io/example/operator:v0.0.1

        Note: Bundle image is not runnable.
        `,
		RunE: buildFunc,
	}

	bundleBuildCmd.Flags().StringVarP(&dirBuildArgs, "directory", "d", "", "The directory where bundle manifests are located.")
	if err := bundleBuildCmd.MarkFlagRequired("directory"); err != nil {
		log.Fatalf("Failed to mark `directory` flag for `build` subcommand as required")
	}

	bundleBuildCmd.Flags().StringVarP(&tagBuildArgs, "tag", "t", "", "The name of the bundle image will be built.")
	if err := bundleBuildCmd.MarkFlagRequired("tag"); err != nil {
		log.Fatalf("Failed to mark `tag` flag for `build` subcommand as required")
	}

	bundleBuildCmd.Flags().StringVarP(&imageBuilderArgs, "image-builder", "b", "docker", "Tool to build container images. One of: [docker, podman, buildah]")

	return bundleBuildCmd
}

// Create build command to build bundle manifests image
func buildBundleImage(directory, imageTag, imageBuilder string) (*exec.Cmd, error) {
	var args []string

	dockerfilePath := path.Join(directory, dockerFile)

	switch imageBuilder {
	case "docker", "podman":
		args = append(args, "build", "-f", dockerfilePath, "-t", imageTag, ".")
	case "buildah":
		args = append(args, "bud", "--format=docker", "-f", dockerfilePath, "-t", imageTag, ".")
	default:
		return nil, fmt.Errorf("%s is not supported image builder", imageBuilder)
	}

	return exec.Command(imageBuilder, args...), nil
}

func executeCommand(cmd *exec.Cmd) error {
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	log.Debugf("Running %#v", cmd.Args)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Failed to exec %#v: %v", cmd.Args, err)
	}

	return nil
}

func buildFunc(cmd *cobra.Command, args []string) error {
	// Generate annotations.yaml and Dockerfile
	err := generateFunc(cmd, args)
	if err != nil {
		return err
	}

	// Build bundle image
	log.Info("Building bundle image")
	buildCmd, err := buildBundleImage(path.Dir(path.Clean(dirBuildArgs)), tagBuildArgs, imageBuilderArgs)
	if err != nil {
		return err
	}

	if err := executeCommand(buildCmd); err != nil {
		return err
	}

	return nil
}
