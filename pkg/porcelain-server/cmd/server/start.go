package server

import (
	"context"
	"fmt"
	"io"
	"net"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/sync/errgroup"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/endpoints/openapi"
	"k8s.io/apiserver/pkg/features"
	genericapiserver "k8s.io/apiserver/pkg/server"
	genericoptions "k8s.io/apiserver/pkg/server/options"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog"

	clientset "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	operatorsinformers "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/informers/externalversions"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/porcelain-server/apis/porcelain/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/porcelain-server/apiserver"
	porcelainopenapi "github.com/operator-framework/operator-lifecycle-manager/pkg/porcelain-server/generated/openapi"
)

const defaultEtcdPathPrefix = "/registry/porcelain.operators.coreos.com"

// PorcelainServerOptions contains state for master/api server.
type PorcelainServerOptions struct {
	RecommendedOptions *genericoptions.RecommendedOptions

	// UseEmbeddedEtcd starts an embedded Etcd server and configures the apiserver to use it.
	UseEmbeddedEtcd                bool
	Client                         kubernetes.Interface
	SharedInformerFactory          informers.SharedInformerFactory
	OperatorsSharedInformerFactory operatorsinformers.SharedInformerFactory
	StdOut                         io.Writer
	StdErr                         io.Writer
}

func NewPorcelainServerOptions(out, errOut io.Writer) *PorcelainServerOptions {
	o := &PorcelainServerOptions{
		RecommendedOptions: genericoptions.NewRecommendedOptions(
			defaultEtcdPathPrefix,
			apiserver.Codecs.LegacyCodec(v1alpha1.SchemeGroupVersion),
			genericoptions.NewProcessInfo("porcelain-server", "runtime"),
		),

		StdOut: out,
		StdErr: errOut,
	}
	o.RecommendedOptions.Etcd.StorageConfig.EncodeVersioner = runtime.NewMultiGroupVersioner(v1alpha1.SchemeGroupVersion, schema.GroupKind{Group: v1alpha1.GroupName})
	return o
}

func (o *PorcelainServerOptions) AddFlags(fs *pflag.FlagSet) {
	fs.BoolVar(&o.UseEmbeddedEtcd, "use-embedded-etcd", o.UseEmbeddedEtcd, ""+
		"Starts an embedded etcd server and configures the apiserver to use it."+
		"If true, this option overrides all other etcd options")
	o.RecommendedOptions.AddFlags(fs)
	utilfeature.DefaultMutableFeatureGate.AddFlag(fs)
}

// NewCommandStartPorcelainServer provides a CLI handler for 'start master' command
// with a default PorcelainServerOptions.
func NewCommandStartPorcelainServer(ctx context.Context, defaults *PorcelainServerOptions) *cobra.Command {
	o := *defaults
	cmd := &cobra.Command{
		Short: "Launch an operator runtime API server",
		Long:  "Launch an operator runtime API server",
		RunE: func(c *cobra.Command, args []string) error {
			if err := o.Complete(); err != nil {
				return err
			}
			if err := o.Validate(args); err != nil {
				return err
			}
			if err := o.RunPorcelainServer(ctx); err != nil {
				return err
			}
			return nil
		},
	}

	flags := cmd.Flags()
	o.AddFlags(flags)

	return cmd
}

func (o PorcelainServerOptions) Validate(args []string) error {
	errors := []error{}
	errors = append(errors, o.RecommendedOptions.Validate()...)
	return utilerrors.NewAggregate(errors)
}

func (o *PorcelainServerOptions) Complete() error {
	// register admission plugins
	// banflunder.Register(o.RecommendedOptions.Admission.Plugins)
	// add admisison plugins to the RecommendedPluginOrder
	// o.RecommendedOptions.Admission.RecommendedPluginOrder = append(o.RecommendedOptions.Admission.RecommendedPluginOrder, "BanFlunder")

	// Override Etcd options if embedded Etcd is enabled
	if o.UseEmbeddedEtcd {
		klog.Info("Embedded etcd option set")
		o.RecommendedOptions.Etcd.StorageConfig.Transport.ServerList = []string{"http://localhost:2379"}
		// TODO: Set TLS settings to default
	}

	return nil
}

func (o *PorcelainServerOptions) Config() (*apiserver.Config, error) {
	// TODO have a "real" external address
	if err := o.RecommendedOptions.SecureServing.MaybeDefaultWithSelfSignedCerts("localhost", nil, []net.IP{net.ParseIP("127.0.0.1")}); err != nil {
		return nil, fmt.Errorf("error creating self-signed certificates: %v", err)
	}

	o.RecommendedOptions.Etcd.StorageConfig.Paging = utilfeature.DefaultFeatureGate.Enabled(features.APIListChunking)

	o.RecommendedOptions.ExtraAdmissionInitializers = func(c *genericapiserver.RecommendedConfig) ([]admission.PluginInitializer, error) {
		client, err := clientset.NewForConfig(c.ClientConfig)
		if err != nil {
			return nil, err
		}
		o.OperatorsSharedInformerFactory = operatorsinformers.NewSharedInformerFactory(client, c.ClientConfig.Timeout)

		o.Client, err = kubernetes.NewForConfig(c.ClientConfig)
		if err != nil {
			return nil, err
		}
		o.SharedInformerFactory = informers.NewSharedInformerFactory(o.Client, c.ClientConfig.Timeout)
		return []admission.PluginInitializer{}, nil
	}

	serverConfig := genericapiserver.NewRecommendedConfig(apiserver.Codecs)

	serverConfig.OpenAPIConfig = genericapiserver.DefaultOpenAPIConfig(porcelainopenapi.GetOpenAPIDefinitions, openapi.NewDefinitionNamer(apiserver.Scheme))
	serverConfig.OpenAPIConfig.Info.Title = "Runtime"
	serverConfig.OpenAPIConfig.Info.Version = "0.1"

	if err := o.RecommendedOptions.ApplyTo(serverConfig); err != nil {
		return nil, err
	}

	config := &apiserver.Config{
		GenericConfig: serverConfig,
		ExtraConfig: apiserver.ExtraConfig{
			Client:                         o.Client,
			SharedInformerFactory:          o.SharedInformerFactory,
			OperatorsSharedInformerFactory: o.OperatorsSharedInformerFactory,
		},
	}
	return config, nil
}

func (o PorcelainServerOptions) RunPorcelainServer(ctx context.Context) error {
	config, err := o.Config()
	if err != nil {
		return err
	}

	// Start the embedded Etcd server is enabled
	if o.UseEmbeddedEtcd {
		klog.Info("Starting embedded etcd")
		// Starts Etcd and blocks until the server is ready, the context is canceled, or an error occurs
		// TODO: Pull etcd data dir from PorcelainServerOptions
		if err := o.startEmbeddedEtcd(ctx, "porcelain.etcd"); err != nil {
			return err
		}
	}

	server, err := config.Complete().New()
	if err != nil {
		return err
	}

	// Start informers before starting the apiserver
	klog.Infoln("Starting informers")
	o.SharedInformerFactory.Start(ctx.Done())
	o.OperatorsSharedInformerFactory.Start(ctx.Done())

	klog.Infoln("Waiting for cache sync")
	o.SharedInformerFactory.WaitForCacheSync(ctx.Done())
	o.OperatorsSharedInformerFactory.WaitForCacheSync(ctx.Done())

	// Start the controller
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		return server.Controller.Run(gctx, 1)
	})

	// Wait for the controller to be ready before serving the porcelain API
	select {
	case <-server.Controller.Ready():
	case <-ctx.Done():
		return ctx.Err()
	}
	<-server.Controller.Ready()

	// Start the apiserver
	g.Go(func() error {
		return server.GenericAPIServer.PrepareRun().Run(gctx.Done())
	})

	// Block until the controller and apiserver exit
	return g.Wait()
}
