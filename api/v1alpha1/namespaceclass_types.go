package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// NamespaceClassSpec defines the desired state of NamespaceClass
type NamespaceClassSpec struct {
	// resources are complete, namespaced Kubernetes objects written like normal manifests
	// (any kind, including custom resources). Leave metadata.namespace empty: each object is
	// created in every Namespace labeled namespaceclass.akuity.io/name=<this class>.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=100
	// +kubebuilder:validation:items:XEmbeddedResource
	// +kubebuilder:validation:items:XPreserveUnknownFields
	// +kubebuilder:validation:items:XValidation:rule="has(self.metadata) && has(self.metadata.name) && size(self.metadata.name) > 0",message="metadata.name is required"
	// +kubebuilder:validation:items:XValidation:rule="!has(self.metadata) || !has(self.metadata.generateName)",message="metadata.generateName is not supported; set metadata.name"
	Resources []runtime.RawExtension `json:"resources,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=nsclass
// +kubebuilder:validation:XValidation:rule="size(self.metadata.name) <= 63",message="metadata.name must be at most 63 characters because it is used as a label value"

// NamespaceClass lists Kubernetes objects that the controller creates in every Namespace
// labeled namespaceclass.akuity.io/name=<name of this class>.
type NamespaceClass struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of NamespaceClass
	// +required
	Spec NamespaceClassSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// NamespaceClassList contains a list of NamespaceClass
type NamespaceClassList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []NamespaceClass `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &NamespaceClass{}, &NamespaceClassList{})
		return nil
	})
}
