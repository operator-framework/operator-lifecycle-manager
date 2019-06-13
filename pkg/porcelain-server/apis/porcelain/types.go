package porcelain

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/version"
	operatorsv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
)

// TODO: Update with changes to v1alpha1.

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// InstalledOperatorList is a list of InstalledOperator objects.
type InstalledOperatorList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []InstalledOperator
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type InstalledOperator struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	// ClusterServiceVersionRef references the CSV which attempted to install the operator.
	ClusterServiceVersionRef *corev1.ObjectReference
	// SubscriptionRef references the Subscription that installed the referenced CSV, if the CSV was installed via Subscription.
	SubscriptionRef 		 *corev1.ObjectReference
	
	// Fields projected from the referenced CSV

	// CustomResourceDefinitions is the set of CustomResourceDefinitions provided and required by the referenced CSV.
	CustomResourceDefinitions operatorsv1alpha1.CustomResourceDefinitions
	// APIServiceDefinitions is the set of APIServices provided and required by the referenced CSV.
	APIServiceDefinitions     operatorsv1alpha1.APIServiceDefinitions
	// MinKubeVersion is the minimum kubernetes version the operator is compatible with.
	MinKubeVersion            string
	// Version is the semantic version of the operator.
	Version                   version.OperatorVersion
	// Maturity is a rating of how mature the operator is.
	Maturity                  string
	// DisplayName is the human-readable name used to represent the operator in client displays.
	DisplayName               string
	// Description is a brief description of the operator's purpose.
	Description               string
	// Keywords defines a set of keywords associated with the operator.
	Keywords                  []string
	// Maintainers defines a set of people and/or organizations responsible for maintaining the operator.
	Maintainers               []operatorsv1alpha1.Maintainer
	// Provider is a link to the site of the operator's provider.
	Provider                  operatorsv1alpha1.AppLink
	// Links is a set of associated links.
	Links                     []operatorsv1alpha1.AppLink
	// Icon is the operator's base64 encoded icon.
	Icon                      []operatorsv1alpha1.Icon
	// InstallModes specify supported installation types.
	InstallModes 			  []operatorsv1alpha1.InstallMode
	// The name of a CSV this one replaces. Should match the field of the old CSV.
	Replaces 				  string
	// Current condition of the ClusterServiceVersion
	Phase 					  operatorsv1alpha1.ClusterServiceVersionPhase
	// A human readable message indicating details about why the ClusterServiceVersion is in this condition.
	Message 				  string
	// A brief CamelCase message indicating details about why the ClusterServiceVersion is in this state.
	// e.g. 'RequirementsNotMet'
	Reason 				      operatorsv1alpha1.ConditionReason
	
	// Fields projected from the referenced Subscription

	// CatalogSource is the name of the catalog the referenced CSV was resolved from (if any).
	CatalogSourceName      string
	// CatalogSourceNamespace is the namespace of the catalog the referenced CSV was resolved from (if any).
	CatalogSourceNamespace string
	// Package is the package the referenced CSV belongs to.
	Package                string
	// Channel is the channel the referenced CSV was resolved from.
	Channel                string
}
