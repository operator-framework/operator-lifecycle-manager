package bundle

import (
	"fmt"
	"os"
	"path/filepath"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/operator-framework/operator-registry/pkg/containertools"
	"github.com/operator-framework/operator-registry/pkg/image"
	"github.com/operator-framework/operator-registry/pkg/image/containerdregistry"
	"github.com/operator-framework/operator-registry/pkg/image/execregistry"
	"github.com/operator-framework/operator-registry/pkg/lib/bundle"
)

var (
	optional string
)

func newBundleValidateCmd() *cobra.Command {
	bundleValidateCmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate bundle image",
		Long: `The "opm alpha bundle validate" command will validate a bundle image
from a remote source to determine if its format and content information are accurate. 
Required validators. These validators will run by default on every invocation of the command. 
 * CSV validator - validates the CSV name and replaces fields.
 * CRD validator - validates the CRDs OpenAPI V3 schema. 
 * Bundle validator - validates the bundle format and annotations.yaml file as well as the optional dependencies.yaml file. 

Optional validators. These validators are disabled by default and can be enabled via the --optional-validators flag. 
 * Operatorhub validator - performs operatorhub.io validation. To validate a bundle using custom categories use with the OPERATOR_BUNDLE_CATEGORIES environmental variable to point to a json-encoded categories file.
 * Bundle objects validator - performs validation on resources like PodDisruptionBudgets and PriorityClasses. 

See https://olm.operatorframework.io/docs/tasks/validate-package/#validation for more info.

Note that this subcommand is deprecated and will be removed in a future release. Migrate to operator-sdk bundle validate.`,
		Example:    `$ opm alpha bundle validate --tag quay.io/test/test-operator:latest --image-builder docker`,
		RunE:       validateFunc,
		Args:       cobra.NoArgs,
		Deprecated: "This subcommand is deprecated and will be removed in a future release. Migrate to operator-sdk bundle validate",
	}

	bundleValidateCmd.Flags().StringVarP(&tag, "tag", "t", "",
		"The path of a registry to pull from, image name and its tag that present the bundle image (e.g. quay.io/test/test-operator:latest)")
	if err := bundleValidateCmd.MarkFlagRequired("tag"); err != nil {
		log.Fatalf("Failed to mark `tag` flag for `validate` subcommand as required")
	}

	bundleValidateCmd.Flags().StringVarP(&containerTool, "image-builder", "b", "docker", "Tool used to pull and unpack bundle images. One of: [none, docker, podman]")
	bundleValidateCmd.Flags().StringVarP(&optional, "optional-validators", "o", "", "Specifies optional validations to be run. One or more of: [operatorhub, bundle-objects]")

	return bundleValidateCmd
}

func validateFunc(cmd *cobra.Command, _ []string) error {
	logger := log.WithFields(log.Fields{"container-tool": containerTool})
	log.SetLevel(log.DebugLevel)

	var (
		registry image.Registry
		err      error
	)

	tool := containertools.NewContainerTool(containerTool, containertools.NoneTool)
	switch tool {
	case containertools.PodmanTool, containertools.DockerTool:
		registry, err = execregistry.NewRegistry(tool, logger)
	case containertools.NoneTool:
		registry, err = containerdregistry.NewRegistry(containerdregistry.WithLog(logger))
	default:
		err = fmt.Errorf("unrecognized container-tool option: %s", containerTool)
	}

	if err != nil {
		return err
	}
	imageValidator := bundle.NewImageValidator(registry, logger, optional)

	dir, err := os.MkdirTemp("", "bundle-")
	logger.Infof("Create a temp directory at %s", dir)
	if err != nil {
		return err
	}
	defer func() {
		err := os.RemoveAll(dir)
		if err != nil {
			logger.Error(err.Error())
		}
	}()

	err = imageValidator.PullBundleImage(tag, dir)
	if err != nil {
		return err
	}

	logger.Info("Unpacked image layers, validating bundle image format & contents")

	err = imageValidator.ValidateBundleFormat(dir)
	if err != nil {
		return err
	}

	err = imageValidator.ValidateBundleContent(filepath.Join(dir, bundle.ManifestsDir))
	if err != nil {
		return err
	}

	logger.Info("All validation tests have been completed successfully")

	return nil
}
