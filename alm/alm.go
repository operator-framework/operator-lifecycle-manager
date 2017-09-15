package alm

import (
	"net"
	"os"
	"time"

	log "github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/workqueue"
)

const (
	defaultQPS   = 100
	defaultBurst = 100
)

type Operator struct {
	kclient  *kubernetes.Clientset
	queue    workqueue.RateLimitingInterface
	informer cache.SharedIndexInformer
}

func NewClusterConfig(kubeconfig string) (*rest.Config, error) {
	cfg := &rest.Config{
		QPS:   defaultQPS,
		Burst: defaultBurst,
	}
	var err error
	if len(kubeconfig) == 0 {
		// Work around https://github.com/kubernetes/kubernetes/issues/40973
		// See https://github.com/coreos/etcd-operator/issues/731#issuecomment-283804819
		if len(os.Getenv("KUBERNETES_SERVICE_HOST")) == 0 {
			addrs, err := net.LookupHost("kubernetes.default.svc")
			if err != nil {
				panic(err)
			}

			os.Setenv("KUBERNETES_SERVICE_HOST", addrs[0])
		}

		if len(os.Getenv("KUBERNETES_SERVICE_PORT")) == 0 {
			os.Setenv("KUBERNETES_SERVICE_PORT", "443")
		}

		cfg, err = rest.InClusterConfig()
		if err != nil {
			return nil, err
		}

	} else {
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, err
		}

	}

	return cfg, nil
}

func New(kubeconfig string) (*Operator, error) {
	cfg, err := NewClusterConfig(kubeconfig)
	if err != nil {
		return nil, err
	}

	kclient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}

	operator := &Operator{
		kclient: kclient,
	}
	operator.queue = workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "labelle")
	operatorVersionWatcher := cache.NewListWatchFromClient(
		kclient.CoreV1().RESTClient(),
		"operatorversions",
		metav1.NamespaceAll,
		fields.Everything(),
	)
	operator.informer = cache.NewSharedIndexInformer(
		operatorVersionWatcher,
		&OperatorVersionResource{},
		15*time.Minute,
		cache.Indexers{},
	)
	operator.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: operator.handleAddOperatorVersion,
	})
	return operator, nil
}

func (o *Operator) keyFunc(obj interface{}) (string, bool) {
	k, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		log.Info("msg", "creating key failed", "err", err)
		return k, false
	}

	return k, true
}

func (o *Operator) handleAddOperatorVersion(obj interface{}) {
	key, ok := o.keyFunc(obj)
	if !ok {
		return
	}

	log.Info("msg", "Prometheus added", "key", key)
}
