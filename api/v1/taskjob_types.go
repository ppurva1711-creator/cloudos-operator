package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type TaskPriority string

const (
	PriorityCritical TaskPriority = "critical"
	PriorityHigh     TaskPriority = "high"
	PriorityMedium   TaskPriority = "medium"
	PriorityLow      TaskPriority = "low"
)

type SchedulingAlgo string

const (
	AlgoMLFQ SchedulingAlgo = "mlfq"
	AlgoRR   SchedulingAlgo = "rr"
	AlgoSJF  SchedulingAlgo = "sjf"
	AlgoFIFO SchedulingAlgo = "fifo"
)

type TaskPhase string

const (
	PhaseQueued    TaskPhase = "Queued"
	PhaseRunning   TaskPhase = "Running"
	PhaseCompleted TaskPhase = "Completed"
	PhaseFailed    TaskPhase = "Failed"
)

type EnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type TaskJobSpec struct {
	Name                 string         `json:"name"`
	Image                string         `json:"image"`
	Command              []string       `json:"command,omitempty"`
	Args                 []string       `json:"args,omitempty"`
	Priority             TaskPriority   `json:"priority,omitempty"`
	Algorithm            SchedulingAlgo `json:"algorithm,omitempty"`
	EstimatedDurationSec int64          `json:"estimatedDurationSec,omitempty"`
	CPURequest           string         `json:"cpuRequest,omitempty"`
	MemoryRequest        string         `json:"memoryRequest,omitempty"`
	DependsOn            []string       `json:"dependsOn,omitempty"`
	Schedule             string         `json:"schedule,omitempty"`
	Env                  []EnvVar       `json:"env,omitempty"`
}

type TaskJobStatus struct {
	Phase      TaskPhase          `json:"phase,omitempty"`
	PodName    string             `json:"podName,omitempty"`
	StartTime  string             `json:"startTime,omitempty"`
	EndTime    string             `json:"endTime,omitempty"`
	ExitCode   int32              `json:"exitCode,omitempty"`
	Message    string             `json:"message,omitempty"`
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Priority",type=string,JSONPath=`.spec.priority`
// +kubebuilder:printcolumn:name="Pod",type=string,JSONPath=`.status.podName`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type TaskJob struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              TaskJobSpec   `json:"spec,omitempty"`
	Status            TaskJobStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type TaskJobList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TaskJob `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TaskJob{}, &TaskJobList{})
}
