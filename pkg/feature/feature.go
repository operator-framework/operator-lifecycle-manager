package feature

import (
	"github.com/spf13/pflag"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/component-base/featuregate"
)

const (
	// MyFeature featuregate.Feature = "MyFeature"
	// owner: @username
	// alpha: v1.X
	// (see https://github.com/kubernetes/kubernetes/blob/master/pkg/features/kube_features.go)

	// OperatorLifecycleManagerV1 enables OLM's v1 APIs.
	// owner: @njhale
	// alpha: v0.15.0
	OperatorLifecycleManagerV1 featuregate.Feature = "OperatorLifecycleManagerV1"
)

var (
	mutableGate featuregate.MutableFeatureGate = featuregate.NewFeatureGate()

	// Gate holds the set of feature gates
	Gate featuregate.FeatureGate = mutableGate
)

func init() {
	utilruntime.Must(mutableGate.Add(featureGates))
}

// AddFlag adds the feature gates defined in this package to the to the given FlagSet.
func AddFlag(fs *pflag.FlagSet) {
	mutableGate.AddFlag(fs)
}

var featureGates = map[featuregate.Feature]featuregate.FeatureSpec{
	OperatorLifecycleManagerV1: {Default: true, LockToDefault: true, PreRelease: featuregate.GA},
}
