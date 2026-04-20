package pubsub

import (
	"time"
)

// Channel name constants for Redis PubSub
const (
	ChannelTaskCompleted  = "tasks:completed"
	ChannelTaskFailed     = "tasks:failed"
	ChannelScalingEvent   = "scaling:event"
	ChannelPodStateChange = "pods:state-change"
)

// TaskCompletionEvent is published when a task completes successfully
type TaskCompletionEvent struct {
	EventType  string    `json:"event_type"`
	TaskID     string    `json:"task_id"`
	PodName    string    `json:"pod_name"`
	Namespace  string    `json:"namespace"`
	TenantID   string    `json:"tenant_id"`
	ExitCode   int32     `json:"exit_code"`
	DurationMs int64     `json:"duration_ms"`
	Timestamp  time.Time `json:"timestamp"`
}

// TaskFailureEvent is published when a task fails
type TaskFailureEvent struct {
	EventType    string    `json:"event_type"`
	TaskID       string    `json:"task_id"`
	PodName      string    `json:"pod_name"`
	Namespace    string    `json:"namespace"`
	TenantID     string    `json:"tenant_id"`
	Reason       string    `json:"reason"`
	Message      string    `json:"message"`
	ExitCode     int32     `json:"exit_code"`
	DurationMs   int64     `json:"duration_ms"`
	Timestamp    time.Time `json:"timestamp"`
	RetryAttempt int       `json:"retry_attempt"`
	MaxRetries   int       `json:"max_retries"`
}

// ScalingEvent is published when an autoscaling decision is made
type ScalingEvent struct {
	EventType       string    `json:"event_type"`
	ScalingAction   string    `json:"scaling_action"`
	Namespace       string    `json:"namespace"`
	DeploymentName  string    `json:"deployment_name"`
	CurrentReplicas int32     `json:"current_replicas"`
	DesiredReplicas int32     `json:"desired_replicas"`
	Metric          string    `json:"metric"`
	MetricValue     float64   `json:"metric_value"`
	Threshold       float64   `json:"threshold"`
	Timestamp       time.Time `json:"timestamp"`
	Reason          string    `json:"reason"`
}

// PodStateChangeEvent is published when a pod transitions between states
type PodStateChangeEvent struct {
	EventType    string    `json:"event_type"`
	PodName      string    `json:"pod_name"`
	Namespace    string    `json:"namespace"`
	TaskID       string    `json:"task_id"`
	OldState     string    `json:"old_state"`
	NewState     string    `json:"new_state"`
	StateReason  string    `json:"state_reason"`
	Timestamp    time.Time `json:"timestamp"`
	ContainerID  string    `json:"container_id,omitempty"`
	RestartCount int32     `json:"restart_count"`
}

// QueueDepthEvent is published when queue depth is updated
type QueueDepthEvent struct {
	EventType string    `json:"event_type"`
	TenantID  string    `json:"tenant_id"`
	Depth     int64     `json:"depth"`
	Timestamp time.Time `json:"timestamp"`
}
