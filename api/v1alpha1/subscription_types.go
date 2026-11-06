/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SubscriptionSpec says which events go where.
type SubscriptionSpec struct {
	Filter   Filter    `json:"filter"`
	Sink     Sink      `json:"sink"`
	Delivery *Delivery `json:"delivery,omitempty"`
}

// Filter selects events. An event matches if its type is one of Types and, if
// Orgs is set, its org (the pmorg extension) is one of Orgs.
type Filter struct {
	// +kubebuilder:validation:MinItems=1
	Types []string `json:"types"`
	// +optional
	Orgs []string `json:"orgs,omitempty"`
}

// Sink is where matching events are sent, as CloudEvents over HTTP.
type Sink struct {
	// +kubebuilder:validation:Pattern=`^https?://`
	URI string `json:"uri"`
}

// Delivery controls retries when the sink does not accept an event, and what
// happens to events the sink never accepted.
type Delivery struct {
	// Retry is the number of retries after the first failed attempt.
	// +kubebuilder:validation:Minimum=0
	// +optional
	Retry *int32 `json:"retry,omitempty"`
	// +kubebuilder:validation:Enum=linear;exponential
	// +optional
	BackoffPolicy *string `json:"backoffPolicy,omitempty"`
	// BackoffDelay is the base delay between retries as an ISO 8601 duration, e.g. PT0.5S.
	// +kubebuilder:validation:Pattern=`^P(\d+Y)?(\d+M)?(\d+W)?(\d+D)?(T(\d+H)?(\d+M)?(\d+(\.\d+)?S)?)?$`
	// +optional
	BackoffDelay *string `json:"backoffDelay,omitempty"`
	// Timeout is how long a single request to the sink may take, as an ISO 8601
	// duration, e.g. PT10S.
	// +kubebuilder:validation:Pattern=`^P(\d+Y)?(\d+M)?(\d+W)?(\d+D)?(T(\d+H)?(\d+M)?(\d+(\.\d+)?S)?)?$`
	// +optional
	Timeout *string `json:"timeout,omitempty"`
	// DeadLetterSink gets the events the sink did not accept after all retries.
	// +optional
	DeadLetterSink *Sink `json:"deadLetterSink,omitempty"`
}

type SubscriptionStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// Subscription delivers events of the whole platform to a sink. Platform
// admins create them in the eventing workspace.
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].reason`
type Subscription struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SubscriptionSpec   `json:"spec"`
	Status SubscriptionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type SubscriptionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Subscription `json:"items"`
}
