/*
Copyright 2020 The Crossplane Authors.

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

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
)

// SchemaParameters are the configurable fields of a Schema.
type SchemaParameters struct {
	// Database this schemas belongs to.
	// +optional
	Database *string `json:"database,omitempty"`

	// DatabaseRef references the database object this schema belongs to.
	// +immutable
	// +optional
	DatabaseRef *xpv1.Reference `json:"databaseRef,omitempty"`

	// DatabaseSelector selects a reference to a Database this schema belongs to.
	// +immutable
	// +optional
	DatabaseSelector *xpv1.Selector `json:"databaseSelector,omitempty"`

	// Owner references the role name which will own the new Schema.
	// +optional
	Owner *string `json:"owner,omitempty"`

	// OwnerRef references the role object which will own the new Schema.
	// +optional
	OwnerRef *xpv1.Reference `json:"ownerRef,omitempty"`

	// OwnerSelector selects a reference to a Role object which will own the new Schema.
	// +optional
	OwnerSelector *xpv1.Selector `json:"ownerSelector,omitempty"`
}

// A SchemaSpec defines the desired state of a Schema.
type SchemaSpec struct {
	xpv1.ResourceSpec `json:",inline"`
	ForProvider       SchemaParameters `json:"forProvider"`
}

// A SchemaStatus represents the observed state of a Schema.
type SchemaStatus struct {
	xpv1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true

// A Schema represents the declarative state of a PostgreSQL Schema.
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:resource:scope=Cluster,categories={crossplane,managed,sql}
type Schema struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SchemaSpec   `json:"spec"`
	Status SchemaStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SchemaList contains a list of Schema
type SchemaList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Schema `json:"items"`
}
