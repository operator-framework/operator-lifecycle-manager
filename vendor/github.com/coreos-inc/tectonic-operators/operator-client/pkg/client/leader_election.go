package client

import (
	"time"

	"github.com/golang/glog"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/client-go/tools/record"
)

// LeaderElectionConfig abstracts the various configs
// needed to set up leader election into a single config.
type LeaderElectionConfig struct {
	// Kubeconfig is an optional path to kubeconfig
	// to use to setup leader election client.
	Kubeconfig string
	// ComponentName is the name of the component that is running for
	// leader election, e.g. `kube-version-operator`.
	ComponentName string

	// Name of configmap for leader election.
	ConfigMapName string
	// Namespace of configmap for leader election.
	ConfigMapNamespace string

	// LeaseDuration is the duration that non-leader candidates will
	// wait to force acquire leadership. This is measured against time of
	// last observed ack.
	LeaseDuration time.Duration
	// RenewDeadline is the duration that the acting master will retry
	// refreshing leadership before giving up.
	RenewDeadline time.Duration
	// RetryPeriod is the duration the LeaderElector clients should wait
	// between tries of actions.
	RetryPeriod time.Duration

	// LockIdentity specifies the holder of the lock. Should be unique per pod.
	LockIdentity string

	// OnStartedLeading is a function callback for when the component
	// has become the leader.
	OnStartedLeading func(stop <-chan struct{})
	// OnStoppedLeading is a function callback for when the component
	// has stopped to be the leader.
	OnStoppedLeading func()
}

// RunLeaderElection will setup leader election for the consumer.
// It abstracts away various initialization and configuration.
// This method will fatally log if leader election cannot be setup.
func (c *Client) RunLeaderElection(opts LeaderElectionConfig) {
	glog.V(4).Info("[LEADER ELECTION]")

	recorder := record.
		NewBroadcaster().
		NewRecorder(runtime.NewScheme(), v1.EventSource{Component: opts.ComponentName})

	config, err := loadRESTClientConfig(opts.Kubeconfig)
	if err != nil {
		glog.Fatalf("Cannot load config for REST client: %v", err)
	}

	leaderElectionClient, err := kubernetes.NewForConfig(restclient.AddUserAgent(config, "leader-election"))
	if err != nil {
		glog.Fatalf("Failed to create leader-election client: %v", err)
	}

	rl := &resourcelock.ConfigMapLock{
		ConfigMapMeta: metav1.ObjectMeta{
			Namespace: opts.ConfigMapNamespace,
			Name:      opts.ConfigMapName,
		},
		Client: leaderElectionClient.CoreV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity:      opts.LockIdentity,
			EventRecorder: recorder,
		},
	}

	leaderelection.RunOrDie(leaderelection.LeaderElectionConfig{
		Lock:          rl,
		LeaseDuration: opts.LeaseDuration,
		RenewDeadline: opts.RenewDeadline,
		RetryPeriod:   opts.RetryPeriod,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: opts.OnStartedLeading,
			OnStoppedLeading: opts.OnStoppedLeading,
		},
	})
}

func loadRESTClientConfig(kubeconfig string) (*restclient.Config, error) {
	if kubeconfig != "" {
		glog.V(4).Infof("Loading kube client config from path %q", kubeconfig)
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	glog.V(4).Infof("Using in-cluster kube client config")
	return restclient.InClusterConfig()
}
