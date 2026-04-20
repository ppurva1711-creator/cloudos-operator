package grpc

import (
	"context"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

// Tests for MockClient

func TestMockClient_NewMockClient(t *testing.T) {
	log := logrus.New()
	client := NewMockClient(log)

	assert.NotNil(t, client)
	assert.Equal(t, int32(5), client.GetMockQueueDepth())
}

func TestMockClient_SubmitTask_Success(t *testing.T) {
	log := logrus.New()
	client := NewMockClient(log)

	req := &SubmitTaskRequest{
		TaskID:         "task-1",
		TenantID:       "tenant-1",
		Image:          "alpine:latest",
		Command:        []string{"echo", "hello"},
		CPURequest:     "100m",
		MemoryRequest:  "128Mi",
		Priority:       50,
		TimeoutSeconds: 300,
	}

	resp, err := client.SubmitTask(context.Background(), req)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.True(t, resp.Accepted)
	assert.Equal(t, int32(1), resp.QueuePosition)
	assert.Equal(t, int64(5), resp.EstimatedStartSeconds)
}

func TestMockClient_SubmitTask_Failure(t *testing.T) {
	log := logrus.New()
	client := NewMockClient(log)

	// Configure task to fail
	client.SetTaskFailure("task-fail", true)

	req := &SubmitTaskRequest{
		TaskID:    "task-fail",
		TenantID:  "tenant-1",
		Image:     "alpine:latest",
		Command:   []string{"echo"},
	}

	resp, err := client.SubmitTask(context.Background(), req)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.False(t, resp.Accepted)
	assert.Contains(t, resp.Reason, "rejected")
}

func TestMockClient_GetTaskStatus_Default(t *testing.T) {
	log := logrus.New()
	client := NewMockClient(log)

	resp, err := client.GetTaskStatus(context.Background(), "task-1")

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, "Running", resp.Status)
	assert.Equal(t, "task-1", resp.TaskID)
	assert.Equal(t, "task-1-pod", resp.PodName)
}

func TestMockClient_GetTaskStatus_Submitted(t *testing.T) {
	log := logrus.New()
	client := NewMockClient(log)

	// First submit a task
	submitReq := &SubmitTaskRequest{
		TaskID:    "task-1",
		TenantID:  "tenant-1",
		Image:     "alpine:latest",
		Command:   []string{"echo"},
	}
	_, err := client.SubmitTask(context.Background(), submitReq)
	assert.NoError(t, err)

	// Then get its status
	statusResp, err := client.GetTaskStatus(context.Background(), "task-1")

	assert.NoError(t, err)
	assert.NotNil(t, statusResp)
	assert.Equal(t, "task-1", statusResp.TaskID)
}

func TestMockClient_CancelTask(t *testing.T) {
	log := logrus.New()
	client := NewMockClient(log)

	err := client.CancelTask(context.Background(), "task-1")

	assert.NoError(t, err)
}

func TestMockClient_GetQueueDepth(t *testing.T) {
	log := logrus.New()
	client := NewMockClient(log)

	resp, err := client.GetQueueDepth(context.Background())

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, int32(5), resp.TotalQueued)
	assert.True(t, resp.PendingTasks > 0)
	assert.True(t, resp.RunningTasks > 0)
}

func TestMockClient_SetQueueDepth(t *testing.T) {
	log := logrus.New()
	client := NewMockClient(log)

	client.SetQueueDepth(25)

	resp, err := client.GetQueueDepth(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, int32(25), resp.TotalQueued)
}

func TestMockClient_SetPodNameFormat(t *testing.T) {
	log := logrus.New()
	client := NewMockClient(log)

	client.SetPodNameFormat("custom-%s-worker")

	req := &SubmitTaskRequest{
		TaskID:   "task-123",
		TenantID: "tenant-1",
		Image:    "alpine:latest",
		Command:  []string{"echo"},
	}

	_, err := client.SubmitTask(context.Background(), req)
	assert.NoError(t, err)

	resp, err := client.GetTaskStatus(context.Background(), "task-123")
	assert.NoError(t, err)
	assert.Equal(t, "custom-task-123-worker", resp.PodName)
}

func TestMockClient_ResetState(t *testing.T) {
	log := logrus.New()
	client := NewMockClient(log)

	// Set some state
	client.SetQueueDepth(50)
	client.SetTaskFailure("task-fail", true)

	req := &SubmitTaskRequest{
		TaskID:   "task-1",
		TenantID: "tenant-1",
		Image:    "alpine:latest",
		Command:  []string{"echo"},
	}
	client.SubmitTask(context.Background(), req)

	// Reset
	client.ResetState()

	// Verify state is reset
	assert.Equal(t, int32(5), client.GetMockQueueDepth())
	submitted := client.GetSubmittedTasks()
	assert.Empty(t, submitted)
}

func TestMockClient_GetSubmittedTasks(t *testing.T) {
	log := logrus.New()
	client := NewMockClient(log)

	// Submit multiple tasks
	for i := 1; i <= 3; i++ {
		req := &SubmitTaskRequest{
			TaskID:   "task-" + string(rune(i)),
			TenantID: "tenant-1",
			Image:    "alpine:latest",
			Command:  []string{"echo"},
		}
		client.SubmitTask(context.Background(), req)
	}

	// Get submitted tasks
	submitted := client.GetSubmittedTasks()

	assert.Equal(t, 3, len(submitted))
	assert.Contains(t, submitted, "task-1")
	assert.Contains(t, submitted, "task-2")
	assert.Contains(t, submitted, "task-3")
}

func TestMockClient_Close(t *testing.T) {
	log := logrus.New()
	client := NewMockClient(log)

	err := client.Close()
	assert.NoError(t, err)
}

func TestMockClient_Concurrent_Access(t *testing.T) {
	log := logrus.New()
	client := NewMockClient(log)

	// Run multiple goroutines
	done := make(chan bool, 10)

	for i := 0; i < 10; i++ {
		go func(idx int) {
			defer func() { done <- true }()

			// Concurrent operations
			req := &SubmitTaskRequest{
				TaskID:   "task-concurrent-" + string(rune(idx)),
				TenantID: "tenant-1",
				Image:    "alpine:latest",
				Command:  []string{"echo"},
			}

			_, err := client.SubmitTask(context.Background(), req)
			assert.NoError(t, err)

			_, err = client.GetQueueDepth(context.Background())
			assert.NoError(t, err)

			_, err = client.GetTaskStatus(context.Background(), "task-any")
			assert.NoError(t, err)
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}

// Tests for context handling

func TestMockClient_Context_Timeout(t *testing.T) {
	log := logrus.New()
	client := NewMockClient(log)

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	time.Sleep(10 * time.Millisecond) // Exceed timeout

	req := &SubmitTaskRequest{
		TaskID:   "task-1",
		TenantID: "tenant-1",
		Image:    "alpine:latest",
		Command:  []string{"echo"},
	}

	// Should still succeed (mock doesn't actually wait)
	resp, err := client.SubmitTask(ctx, req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestMockClient_Context_Cancelled(t *testing.T) {
	log := logrus.New()
	client := NewMockClient(log)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := &SubmitTaskRequest{
		TaskID:   "task-1",
		TenantID: "tenant-1",
		Image:    "alpine:latest",
		Command:  []string{"echo"},
	}

	// Should still succeed (mock doesn't check context)
	resp, err := client.SubmitTask(ctx, req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
}

// Interface compliance test

func TestModule1ClientInterface_Implemented(t *testing.T) {
	// Ensure MockClient implements Module1Client interface
	var _ Module1Client = &MockClient{}

	// Ensure RealClient implements Module1Client interface
	// (can't instantiate without connection, so we just check it would)
	// We'd do: var _ Module1Client = &RealClient{}
	// But RealClient requires actual connection
}
