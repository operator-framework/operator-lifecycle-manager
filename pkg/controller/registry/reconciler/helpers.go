package reconciler

import (
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func DesiredRegistryNetworkPolicy(catalogSource client.Object, matchLabels map[string]string) *networkingv1.NetworkPolicy {
	np := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      catalogSource.GetName(),
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
	ownerutil.AddOwner(np, catalogSource, false, false)
	return np
}

func sanitizedDeepEqual(a client.Object, b client.Object) bool {
	a = a.DeepCopyObject().(client.Object)
	b = b.DeepCopyObject().(client.Object)
	if v := a.GetUID(); v == "" {
		b.SetUID(v)
	}
	if v := a.GetResourceVersion(); v == "" {
		b.SetResourceVersion(v)
	}
	if v := a.GetGeneration(); v == 0 {
		b.SetGeneration(v)
	}
	if v := a.GetManagedFields(); len(v) == 0 {
		b.SetManagedFields(v)
	}
	if v := a.GetCreationTimestamp(); v.IsZero() {
		b.SetCreationTimestamp(v)
	}
	return equality.Semantic.DeepEqual(a, b)
}
