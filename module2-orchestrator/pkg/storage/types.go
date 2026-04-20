package storage

import (
	"database/sql"
	"time"
)

// TaskStatus represents the status of a task execution
type TaskStatus string

const (
	TaskStatusPending   TaskStatus = "pending"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusCompleted TaskStatus = "completed"
	TaskStatusFailed    TaskStatus = "failed"
	TaskStatusCancelled TaskStatus = "cancelled"
	TaskStatusPaused    TaskStatus = "paused"
)

// TaskRecord represents a task execution record in the database
type TaskRecord struct {
	// Primary identifiers
	ID       string
	TaskID   string
	TenantID string

	// Pod information
	PodName     string
	Namespace   string
	ContainerID sql.NullString

	// Task status and result
	Status       TaskStatus
	ExitCode     sql.NullInt32
	RetryAttempt int
	MaxRetries   int

	// Timing information
	StartedAt   sql.NullTime
	CompletedAt sql.NullTime
	Duration    sql.NullInt64 // Duration in seconds

	// Metadata
	Reason  sql.NullString // e.g., "CrashLoopBackOff"
	Message sql.NullString // Detailed error message

	// Timestamps
	CreatedAt time.Time
	UpdatedAt time.Time
}

// TaskExecutionStats represents aggregated statistics for task executions
type TaskExecutionStats struct {
	TotalTasks      int64
	CompletedTasks  int64
	FailedTasks     int64
	SuccessRate     float64 // percentage
	AvgDuration     float64 // seconds
	MaxDuration     int64   // seconds
}

// TaskFilter represents query filters for task history retrieval
type TaskFilter struct {
	TenantID   string    // Required
	Status     TaskStatus
	StartDate  time.Time
	EndDate    time.Time
	PodName    string
	OnlyFailed bool
	Limit      int
	Offset     int
}

// RecentFailure represents a recent task failure
type RecentFailure struct {
	ID       string
	TaskID   string
	PodName  string
	Reason   string
	Message  string
	FailedAt time.Time
	CreatedAt time.Time
}
