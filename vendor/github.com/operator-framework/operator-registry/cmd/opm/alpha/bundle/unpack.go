package bundle

import (
	"context"
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"

	dircopy "github.com/otiai10/copy"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/operator-framework/operator-registry/cmd/opm/internal/util"
	"github.com/operator-framework/operator-registry/pkg/image"
	"github.com/operator-framework/operator-registry/pkg/image/containerdregistry"
	"github.com/operator-framework/operator-registry/pkg/lib/bundle"
)

func newBundleUnpackCmd() *cobra.Command {
	unpack := &cobra.Command{
		Use:   "unpack BUNDLE_NAME[:TAG|@DIGEST]",
		Short: "Unpacks the content of an operator bundle",
		Long:  "Unpacks the content of an operator bundle into a directory",
		Args: func(cmd *cobra.Command, args []string) error {
			return cobra.ExactArgs(1)(cmd, args)
		},
		RunE: unpackBundle,
	}
	unpack.Flags().BoolP("debug", "d", false, "enable debug log output")
	unpack.Flags().BoolP("skip-tls", "s", false, "use plain HTTP")
	unpack.Flags().Bool("skip-tls-verify", false, "disable TLS verification")
	unpack.Flags().Bool("use-http", false, "use plain HTTP")
	unpack.Flags().BoolP("skip-validation", "v", false, "disable bundle validation")
	unpack.Flags().StringP("root-ca", "c", "", "file path of a root CA to use when communicating with image registries")
	unpack.Flags().StringP("out", "o", "./", "directory in which to unpack operator bundle content")

	if err := unpack.Flags().MarkDeprecated("skip-tls", "use --use-http and --skip-tls-verify instead"); err != nil {
		logrus.Panic(err.Error())
	}
	return unpack
}

func unpackBundle(cmd *cobra.Command, args []string) error {
	debug, err := cmd.Flags().GetBool("debug")
	if err != nil {
		return err
	}

	logger := logrus.WithField("cmd", "unpack")
	if debug {
		logger.Logger.SetLevel(logrus.DebugLevel)
	}

	var out string
	out, err = cmd.Flags().GetString("out")
	if err != nil {
		return err
	}

	if info, err := os.Stat(out); err != nil {
		if os.IsNotExist(err) {
			err = os.MkdirAll(out, 0755)
		}
		if err != nil {
			return err
		}
	} else {
		if info == nil {
			return fmt.Errorf("failed to get output directory info")
		}
		if !info.IsDir() {
			return fmt.Errorf("out %s is not a directory", out)
		}
	}

	var (
		registryOpts []containerdregistry.RegistryOption
	)

	skipTLSVerify, useHTTP, err := util.GetTLSOptions(cmd)
	if err != nil {
		return err
	}

	registryOpts = append(registryOpts, containerdregistry.SkipTLSVerify(skipTLSVerify), containerdregistry.WithPlainHTTP(useHTTP))

	var skipValidation bool
	skipValidation, err = cmd.Flags().GetBool("skip-validation")
	if err != nil {
		return err
	}

	var rootCA string
	rootCA, err = cmd.Flags().GetString("root-ca")
	if err != nil {
		return err
	}
	if rootCA != "" {
		rootCAs := x509.NewCertPool()
		certs, err := os.ReadFile(rootCA)
		if err != nil {
			return err
		}

		if !rootCAs.AppendCertsFromPEM(certs) {
			return fmt.Errorf("failed to fetch root CA from %s", rootCA)
		}

		registryOpts = append(registryOpts, containerdregistry.WithRootCAs(rootCAs))
	}

	registry, err := containerdregistry.NewRegistry(registryOpts...)
	if err != nil {
		return err
	}
	defer func() {
		if err := registry.Destroy(); err != nil {
			logger.Error(err)
		}
	}()

	var (
		ref = image.SimpleReference(args[0])
		ctx = context.Background()
	)
	if err := registry.Pull(ctx, ref); err != nil {
		return err
	}

	dir, err := os.MkdirTemp("", "bundle-")
	if err != nil {
		return err
	}

	defer func() {
		err := os.RemoveAll(dir)
		if err != nil {
			logger.Error(err.Error())
		}
	}()
	if err := registry.Unpack(ctx, ref, dir); err != nil {
		return err
	}

	if err := registry.Destroy(); err != nil {
		return err
	}

	if skipValidation {
		logger.Info("skipping bundle validation")
	} else {
		validator := bundle.NewImageValidator(registry, logger)
		if err := validator.ValidateBundleFormat(dir); err != nil {
			return fmt.Errorf("bundle format validation failed: %s", err)
		}
		if err := validator.ValidateBundleContent(filepath.Join(dir, bundle.ManifestsDir)); err != nil {
			return fmt.Errorf("bundle content validation failed: %s", err)
		}
	}

	if err := dircopy.Copy(dir, out); err != nil {
		return fmt.Errorf("failed to copy unpacked content to output directory: %s", err)
	}

	return nil
}
