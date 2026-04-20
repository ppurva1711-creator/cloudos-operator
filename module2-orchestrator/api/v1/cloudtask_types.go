/*
Copyright 2024.

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

// Package v1 contains API Schema definitions for the tasks v1 API group
//+kubebuilder:object:generate=true
package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// CloudTaskFinalizer is used to ensure proper cleanup of CloudTask resources
	CloudTaskFinalizer = "tasks.orchestrator.dev/finalizer"
)

// CloudTaskPhase represents the phase of a CloudTask
type CloudTaskPhase string

const (
	// PhasePending means the CloudTask is pending execution
	PhasePending CloudTaskPhase = "Pending"

	// PhaseRunning means the CloudTask is currently running
	PhaseRunning CloudTaskPhase = "Running"

	// PhaseCompleted means the CloudTask has completed successfully
	PhaseCompleted CloudTaskPhase = "Completed"

	// PhaseFailed means the CloudTask has failed
	PhaseFailed CloudTaskPhase = "Failed"
)

// CloudTaskSpec defines the desired state of CloudTask
type CloudTaskSpec struct {
	// Image is the container image to run
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Image string `json:"image"`

	// Command is the command to run in the container
	// +kubebuilder:validation:Optional
	Command []string `json:"command,omitempty"`

	// Args are the arguments to pass to the command
	// +kubebuilder:validation:Optional
	Args []string `json:"args,omitempty"`

	// Resources defines the compute resources for the task
	// +kubebuilder:validation:Optional
	Resources *ResourceRequirements `json:"resources,omitempty"`

	// Retries is the number of times to retry a failed task
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=10
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=3
	Retries int32 `json:"retries,omitempty"`

	// Timeout is the maximum duration for the task (e.g., "5m", "1h")
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Pattern=`^\d+[smh]$`
	Timeout string `json:"timeout,omitempty"`

	// TenantID is the tenant that owns this task
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	TenantID string `json:"tenantID"`

	// Priority is the priority level of the task (higher = more important)
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=50
	Priority int32 `json:"priority,omitempty"`

	// Env defines environment variables for the container
	// +kubebuilder:validation:Optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// VolumeMounts defines volume mounts for the container
	// +kubebuilder:validation:Optional
	VolumeMounts []corev1.VolumeMount `json:"volumeMounts,omitempty"`

	// Labels to apply to the Pod
	// +kubebuilder:validation:Optional
	Labels map[string]string `json:"labels,omitempty"`

	// Annotations to apply to the Pod
	// +kubebuilder:validation:Optional
	Annotations map[string]string `json:"annotations,omitempty"`
}

// ResourceRequirements describes the compute resource requirements
type ResourceRequirements struct {
	// Requests defines the minimum resources required
	// +kubebuilder:validation:Optional
	Requests *ResourceList `json:"requests,omitempty"`

	// Limits defines the maximum resources allowed
	// +kubebuilder:validation:Optional
	Limits *ResourceList `json:"limits,omitempty"`
}

// ResourceList defines a set of (resource, quantity) pairs
type ResourceList struct {
	// CPU in millicores
	// +kubebuilder:validation:Pattern=`^\d+m?$`
	// +kubebuilder:validation:Optional
	CPU string `json:"cpu,omitempty"`

	// Memory in megabytes
	// +kubebuilder:validation:Pattern=`^\d+Mi$`
	// +kubebuilder:validation:Optional
	Memory string `json:"memory,omitempty"`
}

// CloudTaskStatus defines the observed state of CloudTask
type CloudTaskStatus struct {
	// Phase is the current phase of the CloudTask
	Phase CloudTaskPhase `json:"phase,omitempty"`

	// PodName is the name of the associated Pod
	// +kubebuilder:validation:Optional
	PodName string `json:"podName,omitempty"`

	// StartTime is when the task started running
	// +kubebuilder:validation:Optional
	StartTime *metav1.Time `json:"startTime,omitempty"`

	// CompletionTime is when the task completed (success or failure)
	// +kubebuilder:validation:Optional
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`

	// RetryCount is the number of times the task has been retried
	RetryCount int32 `json:"retryCount,omitempty"`

	// Message provides additional information about the task status
	Message string `json:"message,omitempty"`

	// LastUpdateTime is when the status was last updated
	// +kubebuilder:validation:Optional
	LastUpdateTime *metav1.Time `json:"lastUpdateTime,omitempty"`

	// Conditions represent the latest available observations of the CloudTask's state
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=ct;cts
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Pod",type=string,JSONPath=`.status.podName`
// +kubebuilder:printcolumn:name="RetryCount",type=integer,JSONPath=`.status.retryCount`
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// CloudTask is the Schema for the cloudtasks API
type CloudTask struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CloudTaskSpec   `json:"spec,omitempty"`
	Status CloudTaskStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// CloudTaskList contains a list of CloudTask
type CloudTaskList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CloudTask `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CloudTask{}, &CloudTaskList{})
}
