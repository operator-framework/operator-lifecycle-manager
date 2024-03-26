package overrides

import (
	"fmt"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/olm/overrides/inject"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/proxy"
	"github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

// NewDeploymentInitializer returns a function that accepts a Deployment object
// and initializes it with env variables specified in operator configuration.
func NewDeploymentInitializer(logger *logrus.Logger, querier proxy.Querier, lister operatorlister.OperatorLister) *DeploymentInitializer {
	return &DeploymentInitializer{
		logger:  logger,
		querier: querier,
		config: &operatorConfig{
			lister: lister,
			logger: logger,
		},
	}
}

type DeploymentInitializer struct {
	logger  *logrus.Logger
	querier proxy.Querier
	config  *operatorConfig
}

func (d *DeploymentInitializer) GetDeploymentInitializer(ownerCSV ownerutil.Owner) install.DeploymentInitializerFunc {
	return func(spec *appsv1.Deployment) error {
		err := d.initialize(ownerCSV, spec)
		return err
	}
}

// Initialize initializes a deployment object with appropriate global cluster
// level proxy env variable(s).
func (d *DeploymentInitializer) initialize(ownerCSV ownerutil.Owner, deployment *appsv1.Deployment) error {
	var envVarOverrides, proxyEnvVar, merged []corev1.EnvVar
	var err error

	envVarOverrides, envFromOverrides, volumeOverrides, volumeMountOverrides, tolerationOverrides, resourcesOverride, nodeSelectorOverride, affinity, annotations, err := d.config.GetConfigOverrides(ownerCSV)
	if err != nil {
		err = fmt.Errorf("failed to get subscription pod configuration - %v", err)
		return err
	}

	if !proxy.IsOverridden(envVarOverrides) {
		proxyEnvVar, err = d.querier.QueryProxyConfig()
		if err != nil {
			err = fmt.Errorf("failed to query cluster proxy configuration - %v", err)
			return err
		}

		proxyEnvVar = dropEmptyProxyEnv(proxyEnvVar)
	}

	merged = append(envVarOverrides, proxyEnvVar...)

	if len(merged) == 0 {
		d.logger.WithField("csv", ownerCSV.GetName()).Debug("no env var to inject into csv")
	}

	podSpec := &deployment.Spec.Template.Spec
	if err := inject.InjectEnvIntoDeployment(podSpec, merged); err != nil {
		return fmt.Errorf("failed to inject proxy env variable(s) into deployment spec name=%s - %v", deployment.Name, err)
	}

	if err := inject.InjectEnvFromIntoDeployment(podSpec, envFromOverrides); err != nil {
		return fmt.Errorf("failed to inject envFrom variable(s) into deployment spec name=%s - %v", deployment.Name, err)
	}

	if err = inject.InjectVolumesIntoDeployment(podSpec, volumeOverrides); err != nil {
		return fmt.Errorf("failed to inject volume(s) into deployment spec name=%s - %v", deployment.Name, err)
	}

	if err = inject.InjectVolumeMountsIntoDeployment(podSpec, volumeMountOverrides); err != nil {
		return fmt.Errorf("failed to inject volumeMounts(s) into deployment spec name=%s - %v", deployment.Name, err)
	}

	if err = inject.InjectTolerationsIntoDeployment(podSpec, tolerationOverrides); err != nil {
		return fmt.Errorf("failed to inject toleration(s) into deployment spec name=%s - %v", deployment.Name, err)
	}

	if err = inject.InjectResourcesIntoDeployment(podSpec, resourcesOverride); err != nil {
		return fmt.Errorf("failed to inject resources into deployment spec name=%s - %v", deployment.Name, err)
	}

	if err = inject.InjectNodeSelectorIntoDeployment(podSpec, nodeSelectorOverride); err != nil {
		return fmt.Errorf("failed to inject nodeSelector into deployment spec name=%s - %v", deployment.Name, err)
	}

	if err = inject.OverrideDeploymentAffinity(podSpec, affinity); err != nil {
		return fmt.Errorf("failed to inject affinity into deployment spec name=%s - %s", deployment.Name, err)
	}

	if err = inject.InjectAnnotationsIntoDeployment(deployment, annotations); err != nil {
		return fmt.Errorf("failed to inject annotations into deployment spec name=%s - %s", deployment.Name, err)
	}

	return nil
}

func dropEmptyProxyEnv(in []corev1.EnvVar) (out []corev1.EnvVar) {
	out = make([]corev1.EnvVar, 0)
	for i := range in {
		if in[i].Value == "" {
			continue
		}

		out = append(out, in[i])
	}

	return
}
