/*
Copyright 2021.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// BundleInstanceSpec defines the desired state of BundleInstance
type BundleInstanceSpec struct {
	// ProvisionerClassName sets the name of the provisioner that should reconcile this BundleInstance.
	ProvisionerClassName string `json:"provisionerClassName"`

	// BundleName is the name of the bundle that this instance is managing on the cluster.
	BundleName string `json:"bundleName"`
}

// BundleInstanceStatus defines the observed state of BundleInstance
type BundleInstanceStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	InstalledBundleName string `json:"installedBundleName,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:resource:scope=Cluster
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="Desired Bundle",type=string,JSONPath=`.spec.bundleName`
//+kubebuilder:printcolumn:name="Installed Bundle",type=string,JSONPath=`.status.installedBundleName`
//+kubebuilder:printcolumn:name=Age,type=date,JSONPath=`.metadata.creationTimestamp`

// BundleInstance is the Schema for the bundleinstances API
type BundleInstance struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BundleInstanceSpec   `json:"spec,omitempty"`
	Status BundleInstanceStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// BundleInstanceList contains a list of BundleInstance
type BundleInstanceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BundleInstance `json:"items"`
}

func init() {
	SchemeBuilder.Register(&BundleInstance{}, &BundleInstanceList{})
}
