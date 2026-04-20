package grpc

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// MockClient is a mock implementation of Module1Client for testing and development
type MockClient struct {
	queueDepth     int32
	podNameFormat  string
	log            *logrus.Logger
	mu             sync.RWMutex
	submitedTasks  map[string]*TaskStatusResponse
	failOnTasks    map[string]bool // Task IDs that should fail
}

// NewMockClient creates a new MockClient with default configuration
func NewMockClient(log *logrus.Logger) *MockClient {
	if log == nil {
		log = logrus.New()
	}

	return &MockClient{
		queueDepth:    5,
		podNameFormat: "%s-pod",
		log:           log,
		submitedTasks: make(map[string]*TaskStatusResponse),
		failOnTasks:   make(map[string]bool),
	}
}

// SubmitTask submits a task (mock: always returns accepted=true)
func (m *MockClient) SubmitTask(ctx context.Context, req *SubmitTaskRequest) (*SubmitTaskResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.log.Infof("MockClient.SubmitTask: task_id=%s, tenant_id=%s", req.TaskID, req.TenantID)

	// Check if this task should fail (configurable in tests)
	if m.failOnTasks[req.TaskID] {
		return &SubmitTaskResponse{
			Accepted: false,
			Reason:   "Task rejected for testing",
		}, nil
	}

	// Store submitted task info
	statusResp := &TaskStatusResponse{
		TaskID:    req.TaskID,
		Status:    "Pending",
		PodName:   fmt.Sprintf(m.podNameFormat, req.TaskID),
		StartedAt: 0,
	}
	m.submitedTasks[req.TaskID] = statusResp

	return &SubmitTaskResponse{
		Accepted:              true,
		QueuePosition:         1,
		EstimatedStartSeconds: 5,
	}, nil
}

// GetTaskStatus gets the status of a task (mock: returns Running status)
func (m *MockClient) GetTaskStatus(ctx context.Context, taskID string) (*TaskStatusResponse, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	m.log.Debugf("MockClient.GetTaskStatus: task_id=%s", taskID)

	// Return stored task if exists
	if status, exists := m.submitedTasks[taskID]; exists {
		// Simulate progression to Running after a bit
		if status.Status == "Pending" && time.Since(time.Unix(status.StartedAt, 0)) > 2*time.Second {
			status.Status = "Running"
			status.StartedAt = time.Now().Unix()
		}
		return status, nil
	}

	// Return default running status
	return &TaskStatusResponse{
		TaskID:    taskID,
		Status:    "Running",
		PodName:   fmt.Sprintf(m.podNameFormat, taskID),
		StartedAt: time.Now().Unix(),
	}, nil
}

// CancelTask cancels a task (mock: returns nil/success)
func (m *MockClient) CancelTask(ctx context.Context, taskID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.log.Infof("MockClient.CancelTask: task_id=%s", taskID)

	// Mark task as cancelled in our storage
	if status, exists := m.submitedTasks[taskID]; exists {
		status.Status = "Cancelled"
		status.CompletedAt = time.Now().Unix()
	}

	return nil
}

// GetQueueDepth gets the current queue depth (mock: returns configured depth)
func (m *MockClient) GetQueueDepth(ctx context.Context) (*QueueDepthResponse, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	m.log.Debugf("MockClient.GetQueueDepth: returning %d", m.queueDepth)

	// Calculate pending and running split
	pending := (m.queueDepth * 2) / 3
	running := m.queueDepth - pending

	return &QueueDepthResponse{
		TotalQueued:  m.queueDepth,
		PendingTasks: pending,
		RunningTasks: running,
	}, nil
}

// Close closes the mock client (no-op for mock)
func (m *MockClient) Close() error {
	m.log.Debugf("MockClient.Close() called")
	return nil
}

// SetQueueDepth sets the queue depth for testing
func (m *MockClient) SetQueueDepth(depth int32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queueDepth = depth
	m.log.Debugf("MockClient.SetQueueDepth: %d", depth)
}

// GetQueueDepth returns the current queue depth
func (m *MockClient) GetMockQueueDepth() int32 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.queueDepth
}

// SetPodNameFormat sets the format for pod names in responses
func (m *MockClient) SetPodNameFormat(format string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.podNameFormat = format
	m.log.Debugf("MockClient.SetPodNameFormat: %s", format)
}

// SetTaskFailure configures a task to fail submission
func (m *MockClient) SetTaskFailure(taskID string, shouldFail bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if shouldFail {
		m.failOnTasks[taskID] = true
		m.log.Debugf("MockClient: Task %s set to fail", taskID)
	} else {
		delete(m.failOnTasks, taskID)
		m.log.Debugf("MockClient: Task %s reset to succeed", taskID)
	}
}

// GetSubmittedTasks returns a copy of all submitted tasks
func (m *MockClient) GetSubmittedTasks() map[string]*TaskStatusResponse {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]*TaskStatusResponse)
	for k, v := range m.submitedTasks {
		result[k] = v
	}
	return result
}

// ResetState resets all internal state (for testing)
func (m *MockClient) ResetState() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queueDepth = 5
	m.submitedTasks = make(map[string]*TaskStatusResponse)
	m.failOnTasks = make(map[string]bool)
	m.log.Debugf("MockClient: State reset")
}
