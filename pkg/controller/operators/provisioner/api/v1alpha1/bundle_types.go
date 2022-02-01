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

type BundleConditionType string

const (
	TypeUnpacked = "Unpacked"

	ReasonUnpackPending    = "UnpackPending"
	ReasonUnpacking        = "Unpacking"
	ReasonUnpackSuccessful = "UnpackSuccessful"
	ReasonUnpackFailed     = "UnpackFailed"

	PhasePending   = "Pending"
	PhaseUnpacking = "Unpacking"
	PhaseFailing   = "Failing"
	PhaseUnpacked  = "Unpacked"
)

// BundleSpec defines the desired state of Bundle
type BundleSpec struct {
	// ProvisionerClassName sets the name of the provisioner that should reconcile this BundleInstance.
	ProvisionerClassName string `json:"provisionerClassName"`

	// Image is the bundle image that backs the content of this bundle.
	Image string `json:"image"`

	// ImagePullSecrets is a list of pull secrets to have available to
	// pull the referenced image.
	ImagePullSecrets []ImagePullSecret `json:"imagePullSecrets,omitempty"`
}

type ImagePullSecret struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// BundleStatus defines the observed state of Bundle
type BundleStatus struct {
	Info               *BundleInfo        `json:"info,omitempty"`
	Phase              string             `json:"phase,omitempty"`
	Digest             string             `json:"digest,omitempty"`
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
}

type BundleInfo struct {
	Package string         `json:"package"`
	Name    string         `json:"name"`
	Version string         `json:"version"`
	Objects []BundleObject `json:"objects,omitempty"`
}

type BundleObject struct {
	Group     string `json:"group"`
	Version   string `json:"version"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

//+kubebuilder:object:root=true
//+kubebuilder:resource:scope=Cluster
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name=Image,type=string,JSONPath=`.spec.image`
//+kubebuilder:printcolumn:name=Phase,type=string,JSONPath=`.status.phase`
//+kubebuilder:printcolumn:name=Age,type=date,JSONPath=`.metadata.creationTimestamp`

// Bundle is the Schema for the bundles API
type Bundle struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BundleSpec   `json:"spec,omitempty"`
	Status BundleStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// BundleList contains a list of Bundle
type BundleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Bundle `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Bundle{}, &BundleList{})
}
