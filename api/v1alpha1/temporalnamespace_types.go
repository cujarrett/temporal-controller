package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// TemporalNamespaceSpec defines the desired state of TemporalNamespace.
// The Temporal namespace takes this object's metadata.name.
type TemporalNamespaceSpec struct {
	// retention is how long Temporal keeps the history of closed workflows.
	// Temporal requires one on every namespace.
	// +required
	Retention metav1.Duration `json:"retention"`

	// description is shown in the Temporal UI.
	// +optional
	Description string `json:"description,omitempty"`
}

// TemporalNamespaceStatus defines the observed state of TemporalNamespace.
type TemporalNamespaceStatus struct {
	// observedGeneration is the spec generation the conditions describe.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions holds Ready, which a Crossplane composition waits on.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Retention",type=string,JSONPath=`.spec.retention`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// TemporalNamespace is the Schema for the temporalnamespaces API
type TemporalNamespace struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of TemporalNamespace
	// +required
	Spec TemporalNamespaceSpec `json:"spec"`

	// status defines the observed state of TemporalNamespace
	// +optional
	Status TemporalNamespaceStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// TemporalNamespaceList contains a list of TemporalNamespace
type TemporalNamespaceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []TemporalNamespace `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &TemporalNamespace{}, &TemporalNamespaceList{})
		return nil
	})
}
