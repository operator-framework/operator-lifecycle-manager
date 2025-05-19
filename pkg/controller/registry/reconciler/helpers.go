package reconciler

import (
	"fmt"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/bundle"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
)

func DesiredGRPCServerNetworkPolicy(catalogSource *v1alpha1.CatalogSource, matchLabels map[string]string) *networkingv1.NetworkPolicy {
	np := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-grpc-server", catalogSource.GetName()),
			Namespace: catalogSource.GetNamespace(),
			Labels: map[string]string{
				install.OLMManagedLabelKey: install.OLMManagedLabelValue,
				CatalogSourceLabelKey:      catalogSource.GetName(),
			},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: matchLabels,
			},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{
				{
					Ports: []networkingv1.NetworkPolicyPort{
						{
							Protocol: ptr.To(corev1.ProtocolTCP),
							Port:     ptr.To(intstr.FromInt32(50051)),
						},
					},
				},
			},
		},
	}

	// Allow egress to kube-apiserver from configmap backed catalog sources
	if catalogSource.Spec.SourceType == v1alpha1.SourceTypeConfigmap || catalogSource.Spec.SourceType == v1alpha1.SourceTypeInternal {
		np.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{
			{
				Ports: []networkingv1.NetworkPolicyPort{
					{
						Protocol: ptr.To(corev1.ProtocolTCP),
						Port:     ptr.To(intstr.FromInt32(6443)),
					},
				},
			},
		}
	}

	ownerutil.AddOwner(np, catalogSource, false, false)
	return np
}

func DesiredUnpackBundlesNetworkPolicy(catalogSource client.Object) *networkingv1.NetworkPolicy {
	np := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-unpack-bundles", catalogSource.GetName()),
			Namespace: catalogSource.GetNamespace(),
			Labels: map[string]string{
				install.OLMManagedLabelKey: install.OLMManagedLabelValue,
				CatalogSourceLabelKey:      catalogSource.GetName(),
			},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{
						Key:      bundle.BundleUnpackRefLabel,
						Operator: metav1.LabelSelectorOpExists,
					},
					{
						Key:      install.OLMManagedLabelKey,
						Operator: metav1.LabelSelectorOpIn,
						Values:   []string{install.OLMManagedLabelValue},
					},
				},
			},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress},
			Egress: []networkingv1.NetworkPolicyEgressRule{
				{
					Ports: []networkingv1.NetworkPolicyPort{
						{
							Protocol: ptr.To(corev1.ProtocolTCP),
							Port:     ptr.To(intstr.FromInt32(6443)),
						},
					},
				},
			},
		},
	}
	ownerutil.AddOwner(np, catalogSource, false, false)
	return np
}

func isExpectedNetworkPolicy(expected, current *networkingv1.NetworkPolicy) bool {
	if !equality.Semantic.DeepEqual(expected.Spec, current.Spec) {
		return false
	}
	if !equality.Semantic.DeepDerivative(expected.ObjectMeta.Labels, current.ObjectMeta.Labels) {
		return false
	}
	return true
}

//
//func isExpectedNetworkPolicy(desired client.Object, current client.Object) bool {
//	desired = desired.DeepCopyObject().(client.Object)
//	current = current.DeepCopyObject().(client.Object)
//	if v := desired.GetUID(); v == "" {
//		current.SetUID(v)
//	}
//	if v := desired.GetResourceVersion(); v == "" {
//		current.SetResourceVersion(v)
//	}
//	if v := desired.GetGeneration(); v == 0 {
//		current.SetGeneration(v)
//	}
//	if v := desired.GetManagedFields(); len(v) == 0 {
//		current.SetManagedFields(v)
//	}
//	if v := desired.GetCreationTimestamp(); v.IsZero() {
//		current.SetCreationTimestamp(v)
//	}
//	return equality.Semantic.DeepEqual(desired, current)
//}
