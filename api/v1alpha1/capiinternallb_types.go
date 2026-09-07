/*
Copyright 2026.

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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// IPTypeSelection defines which Machine status address type to target.
// +kubebuilder:validation:Enum=InternalIP;ExternalIP;Auto
type IPTypeSelection string

const (
	IPTypeInternal IPTypeSelection = "InternalIP"
	IPTypeExternal IPTypeSelection = "ExternalIP"
	IPTypeAuto     IPTypeSelection = "Auto"
)

// CapiInternalLbSpec defines the desired state of CapiInternalLb
type CapiInternalLbSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	// The following markers will use OpenAPI v3 schema to validate the value
	// More info: https://book.kubebuilder.io/reference/markers/crd-validation.html

	// ClusterRef points to the target CAPI Cluster resource (cluster.x-k8s.io).
	// Your controller uses this to discover Machine objects and extract control plane IPs.
	ClusterRef corev1.ObjectReference `json:"clusterRef"`

	// TargetPort is the API server port on control plane nodes (default: 6443).
	// +optional
	// +kubebuilder:default=6443
	TargetPort int32 `json:"targetPort,omitempty"`

	// IPType specifies whether to select InternalIP, ExternalIP, or Auto (prefer Internal, fallback External).
	// +optional
	// +kubebuilder:default="InternalIP"
	IPType IPTypeSelection `json:"ipType,omitempty"`

	// ServiceName is the name of the K8s Service created by this controller.
	// If empty, defaults to "<cr-name>-service".
	// +optional
	ServiceName string `json:"serviceName,omitempty"`

	// HealthCheck configures active probing against each control plane endpoint.
	// +optional
	HealthCheck *HealthCheckSpec `json:"healthCheck,omitempty"`
}

type HealthCheckSpec struct {
	// Path is the HTTP endpoint queried on control plane nodes (e.g., /readyz or /livez).
	// +optional
	// +kubebuilder:default="/readyz"
	Path string `json:"path,omitempty"`

	// IntervalSeconds is the delay between health probes per target IP.
	// +optional
	// +kubebuilder:default=10
	IntervalSeconds int32 `json:"intervalSeconds,omitempty"`

	// TimeoutSeconds is the probe request timeout.
	// +optional
	// +kubebuilder:default=3
	TimeoutSeconds int32 `json:"timeoutSeconds,omitempty"`

	// UnhealthyThreshold is the consecutive probe failures required to mark an IP unready in the EndpointSlice.
	// +optional
	// +kubebuilder:default=3
	UnhealthyThreshold int32 `json:"unhealthyThreshold,omitempty"`

	// HealthyThreshold is the consecutive probe successes required to mark an IP ready in the EndpointSlice.
	// +optional
	// +kubebuilder:default=1
	HealthyThreshold int32 `json:"healthyThreshold,omitempty"`
}

// CapiInternalLbStatus defines the observed state of CapiInternalLb.
type CapiInternalLbStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// conditions represent the current state of the CapiInternalLb resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// CapiInternalLb is the Schema for the capiinternallbs API
type CapiInternalLb struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of CapiInternalLb
	// +required
	Spec CapiInternalLbSpec `json:"spec"`

	// status defines the observed state of CapiInternalLb
	// +optional
	Status CapiInternalLbStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// CapiInternalLbList contains a list of CapiInternalLb
type CapiInternalLbList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []CapiInternalLb `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &CapiInternalLb{}, &CapiInternalLbList{})
		return nil
	})
}
