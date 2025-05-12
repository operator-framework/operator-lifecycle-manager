package migrations

import (
    "encoding/json"
    "fmt"
    "github.com/operator-framework/api/pkg/operators/v1alpha1"
    "github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/catalog"
    "github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver"
    "github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/projection"
    "github.com/operator-framework/operator-registry/pkg/configmap"
    errorwrap "github.com/pkg/errors"
    "github.com/sirupsen/logrus"
    v1 "k8s.io/client-go/listers/core/v1"
    "regexp"
    "slices"
)

const (
    rbacResourceGroup = "rbac.authorization.k8s.io"
)

var (
    rbacResources = []string{"Role", "RoleBinding", "ClusterRole", "ClusterRoleBinding"}
)

type OldStyleInstallPlanMigrator struct {
    configMapLister v1.ConfigMapLister
}

func (o *OldStyleInstallPlanMigrator) unpackedStepsForBundle(namespace string, ref *catalog.UnpackedBundleReference) ([]v1alpha1.StepResource, error) {
    cm, err := o.configMapLister.ConfigMaps(ref.Namespace).Get(ref.Name)
    if err != nil {
        return nil, errorwrap.Wrapf(err, "error finding unpacked bundle configmap for ref %v", *ref)
    }
    loader := configmap.NewBundleLoader()
    bundle, err := loader.Load(cm)
    if err != nil {
        return nil, errorwrap.Wrapf(err, "error loading unpacked bundle configmap for ref %v", *ref)
    }

    if ref.Properties != "" {
        props, err := projection.PropertyListFromPropertiesAnnotation(ref.Properties)
        if err != nil {
            return nil, fmt.Errorf("failed to load bundle properties for %q: %w", bundle.CsvName, err)
        }
        bundle.Properties = props
    }

    steps, err := resolver.NewStepResourceFromBundle(bundle, namespace, ref.Replaces, ref.CatalogSourceName, ref.CatalogSourceNamespace)
    if err != nil {
        return nil, errorwrap.Wrapf(err, "error calculating steps for ref %v", *ref)
    }
    return steps, nil
}

func refForStep(step *v1alpha1.Step, log logrus.FieldLogger) *catalog.UnpackedBundleReference {
    log = log.WithFields(logrus.Fields{"resolving": step.Resolving, "step": step.Resource.Name})
    var ref catalog.UnpackedBundleReference
    if err := json.Unmarshal([]byte(step.Resource.Manifest), &ref); err != nil {
        log.Debug("step is not a reference to an unpacked bundle (this is not an error if the step is a manifest)")
        return nil
    }
    log = log.WithField("ref", ref)
    if ref.Kind != "ConfigMap" || ref.Name == "" || ref.Namespace == "" || ref.CatalogSourceName == "" || ref.CatalogSourceNamespace == "" {
        log.Debug("step is not a reference to an unpacked bundle (this is not an error if the step is a manifest)")
        return nil
    }
    return &ref
}

func IsOldStyleInstallPlan(installPlan *v1alpha1.InstallPlan) bool {
    // Ignore auto-approve InstallPlans, approved manual-approve install plans and installplans without 4.14 style RBAC steps
    return installPlan.Spec.Approval == v1alpha1.ApprovalManual && installPlan.Spec.Approved == false && hasOldStyleRBACStep(installPlan.Status.Plan)
}

func hasOldStyleRBACStep(steps []*v1alpha1.Step) bool {
    for _, step := range steps {
        if step == nil || step.Resource.Group != rbacResourceGroup || !slices.Contains(rbacResources, step.Resource.Kind) {
            continue
        }
        if ok, _ := regexp.MatchString(fmt.Sprintf("^%s(-.*)?-[a-f0-9]+$", step.Resolving), step.Resource.Name); ok {
            return true
        }
    }
    return false
}
