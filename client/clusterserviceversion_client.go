package client

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/rest"

	"github.com/coreos-inc/alm/apis/clusterserviceversion/v1alpha1"
)

type ClusterServiceVersionInterface interface {
	TransitionPhase(csv *v1alpha1.ClusterServiceVersion, phase v1alpha1.ClusterServiceVersionPhase, reason v1alpha1.ConditionReason, message string) (result *v1alpha1.ClusterServiceVersion, err error)
	UpdateRequirementStatus(csv *v1alpha1.ClusterServiceVersion, phase v1alpha1.ClusterServiceVersionPhase, statuses []v1alpha1.RequirementStatus, reason v1alpha1.ConditionReason, message string) (result *v1alpha1.ClusterServiceVersion, err error)
}

type ClusterServiceVersionClient struct {
	*rest.RESTClient
}

var _ ClusterServiceVersionInterface = &ClusterServiceVersionClient{}

// NewClusterServiceVersionClient creates a client that can interact with the ClusterServiceVersion resource in k8s api
func NewClusterServiceVersionClient(kubeconfig string) (client *ClusterServiceVersionClient, err error) {
	config, err := getConfig(kubeconfig)
	if err != nil {
		return
	}

	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		return nil, err
	}

	config.GroupVersion = &v1alpha1.SchemeGroupVersion
	config.APIPath = "/apis"
	config.ContentType = runtime.ContentTypeJSON
	config.NegotiatedSerializer = serializer.DirectCodecFactory{CodecFactory: serializer.NewCodecFactory(scheme)}
	restClient, err := rest.RESTClientFor(config)
	if err != nil {
		return nil, err
	}
	return &ClusterServiceVersionClient{restClient}, nil
}

func (c *ClusterServiceVersionClient) TransitionPhase(csv *v1alpha1.ClusterServiceVersion, phase v1alpha1.ClusterServiceVersionPhase, reason v1alpha1.ConditionReason, message string) (result *v1alpha1.ClusterServiceVersion, err error) {
	csv.Status.Phase = phase
	csv.Status.LastTransitionTime = metav1.Now()
	csv.Status.LastUpdateTime = metav1.Now()
	csv.Status.Message = message
	csv.Status.Reason = reason

	if len(csv.Status.Conditions) > 0 {
		previousCondition := csv.Status.Conditions[len(csv.Status.Conditions)-1]
		if previousCondition.Phase != csv.Status.Phase || previousCondition.Reason != csv.Status.Reason {
			csv.Status.Conditions = append(csv.Status.Conditions, v1alpha1.ClusterServiceVersionCondition{
				Phase:              csv.Status.Phase,
				LastTransitionTime: csv.Status.LastTransitionTime,
				LastUpdateTime:     csv.Status.LastUpdateTime,
				Message:            message,
				Reason:             reason,
			})
		}
	} else {
		csv.Status.Conditions = append(csv.Status.Conditions, v1alpha1.ClusterServiceVersionCondition{
			Phase:              csv.Status.Phase,
			LastTransitionTime: csv.Status.LastTransitionTime,
			LastUpdateTime:     csv.Status.LastUpdateTime,
			Message:            message,
			Reason:             reason,
		})
	}

	result = &v1alpha1.ClusterServiceVersion{}
	err = c.RESTClient.Put().Context(context.TODO()).
		Namespace(csv.Namespace).
		Resource("clusterserviceversion-v1s").
		Name(csv.Name).
		Body(csv).
		Do().
		Into(result)
	if err != nil {
		err = fmt.Errorf("failed to update CR status: %v", err)
	}
	return
}

func (c *ClusterServiceVersionClient) UpdateRequirementStatus(csv *v1alpha1.ClusterServiceVersion, phase v1alpha1.ClusterServiceVersionPhase, statuses []v1alpha1.RequirementStatus, reason v1alpha1.ConditionReason, message string) (result *v1alpha1.ClusterServiceVersion, err error) {
	csv.Status.RequirementStatus = statuses
	csv.Status.LastUpdateTime = metav1.Now()
	if csv.Status.Phase != phase {
		csv.Status.Phase = phase
		csv.Status.LastTransitionTime = metav1.Now()
	}
	csv.Status.Message = message
	csv.Status.Reason = reason
	if len(csv.Status.Conditions) > 0 {
		previousCondition := csv.Status.Conditions[len(csv.Status.Conditions)-1]
		if previousCondition.Phase != csv.Status.Phase || previousCondition.Reason != csv.Status.Reason {
			csv.Status.Conditions = append(csv.Status.Conditions, v1alpha1.ClusterServiceVersionCondition{
				Phase:              csv.Status.Phase,
				LastTransitionTime: csv.Status.LastTransitionTime,
				LastUpdateTime:     csv.Status.LastUpdateTime,
				Message:            message,
				Reason:             reason,
			})
		}
	} else {
		csv.Status.Conditions = append(csv.Status.Conditions, v1alpha1.ClusterServiceVersionCondition{
			Phase:              csv.Status.Phase,
			LastTransitionTime: csv.Status.LastTransitionTime,
			LastUpdateTime:     csv.Status.LastUpdateTime,
			Message:            message,
			Reason:             reason,
		})
	}
	result = &v1alpha1.ClusterServiceVersion{}
	err = c.RESTClient.Put().Context(context.TODO()).
		Namespace(csv.Namespace).
		Resource("clusterserviceversion-v1s").
		Name(csv.Name).
		Body(csv).
		Do().
		Into(result)
	if err != nil {
		err = fmt.Errorf("failed to update CR status: %v", err)
	}
	return
}
