package grpc

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/orchestrator/module2-orchestrator/pkg/grpc/proto/scheduler"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// MockSchedulerService implements the scheduler service for testing
type MockSchedulerService struct {
	scheduler.UnimplementedSchedulerServiceServer

	submitTaskFunc   func(context.Context, *scheduler.SubmitTaskRequest) (*scheduler.SubmitTaskResponse, error)
	getStatusFunc    func(context.Context, *scheduler.GetTaskStatusRequest) (*scheduler.TaskStatusResponse, error)
	cancelTaskFunc   func(context.Context, *scheduler.CancelTaskRequest) (*scheduler.CancelTaskResponse, error)
	getQueueDepthFunc func(context.Context, *scheduler.GetQueueDepthRequest) (*scheduler.QueueDepthResponse, error)

	callCount map[string]int
}

func (m *MockSchedulerService) SubmitTask(ctx context.Context, req *scheduler.SubmitTaskRequest) (*scheduler.SubmitTaskResponse, error) {
	m.callCount["SubmitTask"]++
	if m.submitTaskFunc != nil {
		return m.submitTaskFunc(ctx, req)
	}
	return &scheduler.SubmitTaskResponse{Accepted: true, QueuePosition: 1}, nil
}

func (m *MockSchedulerService) GetTaskStatus(ctx context.Context, req *scheduler.GetTaskStatusRequest) (*scheduler.TaskStatusResponse, error) {
	m.callCount["GetTaskStatus"]++
	if m.getStatusFunc != nil {
		return m.getStatusFunc(ctx, req)
	}
	return &scheduler.TaskStatusResponse{TaskId: req.TaskId, Status: "Running"}, nil
}

func (m *MockSchedulerService) CancelTask(ctx context.Context, req *scheduler.CancelTaskRequest) (*scheduler.CancelTaskResponse, error) {
	m.callCount["CancelTask"]++
	if m.cancelTaskFunc != nil {
		return m.cancelTaskFunc(ctx, req)
	}
	return &scheduler.CancelTaskResponse{Cancelled: true}, nil
}

func (m *MockSchedulerService) GetQueueDepth(ctx context.Context, req *scheduler.GetQueueDepthRequest) (*scheduler.QueueDepthResponse, error) {
	m.callCount["GetQueueDepth"]++
	if m.getQueueDepthFunc != nil {
		return m.getQueueDepthFunc(ctx, req)
	}
	return &scheduler.QueueDepthResponse{TotalQueued: 5, PendingTasks: 3, RunningTasks: 2}, nil
}

// startMockServer starts a mock gRPC server and returns its address
func startMockServer(t *testing.T, service *MockSchedulerService) string {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	server := grpc.NewServer()
	scheduler.RegisterSchedulerServiceServer(server, service)

	go func() {
		if err := server.Serve(listener); err != nil {
			t.Logf("Server error: %v", err)
		}
	}()

	t.Cleanup(func() {
		server.Stop()
	})

	return listener.Addr().String()
}

// TestSubmitTaskSuccess tests successful task submission
func TestSubmitTaskSuccess(t *testing.T) {
	mock := &MockSchedulerService{
		callCount: make(map[string]int),
		submitTaskFunc: func(ctx context.Context, req *scheduler.SubmitTaskRequest) (*scheduler.SubmitTaskResponse, error) {
			return &scheduler.SubmitTaskResponse{
				Accepted:              true,
				QueuePosition:         1,
				EstimatedStartSeconds: 5,
			}, nil
		},
	}

	addr := startMockServer(t, mock)
	client := &Module1RealClient{
		address:      addr,
		log:          logrus.New(),
		circuitState: StateClosed,
		config: &ClientConfig{
			MaxRetries:        3,
			Timeout:           5 * time.Second,
			RetryBackoff:      100 * time.Millisecond,
			MaxConsecutiveErr: 5,
			CircuitRecoverTime: 2 * time.Second,
		},
		stopChan:       make(chan struct{}),
		consecutiveErr: 0,
	}

	// Manually connect
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	conn, err := grpc.DialContext(ctx, addr, grpc.WithInsecure())
	cancel()
	require.NoError(t, err)

	client.conn = conn
	client.client = scheduler.NewSchedulerServiceClient(conn)

	t.Cleanup(func() {
		client.Close()
	})

	// Execute test
	resp, err := client.SubmitTask(context.Background(), &SubmitTaskRequest{
		TaskID:     "test-task-1",
		TenantID:   "tenant-1",
		Image:      "nginx:latest",
		Command:    []string{"nginx"},
		CPURequest: "100m",
		Priority:   50,
	})

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.True(t, resp.Accepted)
	assert.Equal(t, int32(1), resp.QueuePosition)
	assert.Equal(t, 1, mock.callCount["SubmitTask"])
}

// TestSubmitTaskFailure tests task submission failure
func TestSubmitTaskFailure(t *testing.T) {
	mock := &MockSchedulerService{
		callCount: make(map[string]int),
		submitTaskFunc: func(ctx context.Context, req *scheduler.SubmitTaskRequest) (*scheduler.SubmitTaskResponse, error) {
			return &scheduler.SubmitTaskResponse{
				Accepted: false,
				Reason:   "Queue is full",
			}, nil
		},
	}

	addr := startMockServer(t, mock)
	client := &Module1RealClient{
		address:      addr,
		log:          logrus.New(),
		circuitState: StateClosed,
		config: &ClientConfig{
			MaxRetries:        3,
			Timeout:           5 * time.Second,
			RetryBackoff:      100 * time.Millisecond,
			MaxConsecutiveErr: 5,
			CircuitRecoverTime: 2 * time.Second,
		},
		stopChan:       make(chan struct{}),
		consecutiveErr: 0,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	conn, err := grpc.DialContext(ctx, addr, grpc.WithInsecure())
	cancel()
	require.NoError(t, err)

	client.conn = conn
	client.client = scheduler.NewSchedulerServiceClient(conn)

	t.Cleanup(func() {
		client.Close()
	})

	resp, err := client.SubmitTask(context.Background(), &SubmitTaskRequest{
		TaskID:     "test-task-2",
		TenantID:   "tenant-1",
		Image:      "nginx:latest",
	})

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.False(t, resp.Accepted)
	assert.Equal(t, "Queue is full", resp.Reason)
}

// TestRetryLogicTriggersOnFailure tests that retry logic kicks in on transient failures
func TestRetryLogicTriggersOnFailure(t *testing.T) {
	attempt := 0
	mock := &MockSchedulerService{
		callCount: make(map[string]int),
		submitTaskFunc: func(ctx context.Context, req *scheduler.SubmitTaskRequest) (*scheduler.SubmitTaskResponse, error) {
			attempt++
			if attempt < 3 {
				return nil, status.Error(codes.Unavailable, "service temporarily unavailable")
			}
			return &scheduler.SubmitTaskResponse{Accepted: true, QueuePosition: 1}, nil
		},
	}

	addr := startMockServer(t, mock)
	client := &Module1RealClient{
		address:      addr,
		log:          logrus.New(),
		circuitState: StateClosed,
		config: &ClientConfig{
			MaxRetries:        3,
			Timeout:           5 * time.Second,
			RetryBackoff:      50 * time.Millisecond,
			MaxConsecutiveErr: 5,
			CircuitRecoverTime: 2 * time.Second,
		},
		stopChan:       make(chan struct{}),
		consecutiveErr: 0,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	conn, err := grpc.DialContext(ctx, addr, grpc.WithInsecure())
	cancel()
	require.NoError(t, err)

	client.conn = conn
	client.client = scheduler.NewSchedulerServiceClient(conn)

	t.Cleanup(func() {
		client.Close()
	})

	// Should succeed after retries
	resp, err := client.SubmitTask(context.Background(), &SubmitTaskRequest{
		TaskID:   "test-task-3",
		TenantID: "tenant-1",
		Image:    "nginx:latest",
	})

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.True(t, resp.Accepted)
	assert.Greater(t, mock.callCount["SubmitTask"], 1) // Should have retried
}

// TestCircuitBreakerOpensAfter5Failures tests circuit breaker opens after max consecutive errors
func TestCircuitBreakerOpensAfterMaxFailures(t *testing.T) {
	mock := &MockSchedulerService{
		callCount: make(map[string]int),
		submitTaskFunc: func(ctx context.Context, req *scheduler.SubmitTaskRequest) (*scheduler.SubmitTaskResponse, error) {
			return nil, status.Error(codes.Internal, "internal server error")
		},
	}

	addr := startMockServer(t, mock)
	client := &Module1RealClient{
		address:      addr,
		log:          logrus.New(),
		circuitState: StateClosed,
		config: &ClientConfig{
			MaxRetries:        0, // No retries to trigger failures faster
			Timeout:           5 * time.Second,
			RetryBackoff:      1 * time.Millisecond,
			MaxConsecutiveErr: 3, // Lower threshold for testing
			CircuitRecoverTime: 2 * time.Second,
		},
		stopChan:       make(chan struct{}),
		consecutiveErr: 0,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	conn, err := grpc.DialContext(ctx, addr, grpc.WithInsecure())
	cancel()
	require.NoError(t, err)

	client.conn = conn
	client.client = scheduler.NewSchedulerServiceClient(conn)

	t.Cleanup(func() {
		client.Close()
	})

	// Trigger failures to open circuit
	for i := 0; i < 3; i++ {
		_, err := client.SubmitTask(context.Background(), &SubmitTaskRequest{
			TaskID:     fmt.Sprintf("test-task-%d", i),
			TenantID:   "tenant-1",
			Image:      "nginx:latest",
		})
		assert.Error(t, err)
	}

	// Circuit should now be open
	assert.Equal(t, string(StateOpen), client.GetCircuitBreakerState())

	// Next call should fail immediately without calling service
	callsBeforeMidState := mock.callCount["SubmitTask"]
	_, err := client.SubmitTask(context.Background(), &SubmitTaskRequest{
		TaskID:   "test-task-4",
		TenantID: "tenant-1",
		Image:    "nginx:latest",
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "circuit breaker is open")
	// No additional call should have been made (circuit prevented it)
	assert.Equal(t, callsBeforeMidState, mock.callCount["SubmitTask"])
}

// TestCircuitBreakerRecoveryAfterTimeout tests circuit breaker recovers after timeout
func TestCircuitBreakerRecoveryAfterTimeout(t *testing.T) {
	failCount := 0
	mock := &MockSchedulerService{
		callCount: make(map[string]int),
		submitTaskFunc: func(ctx context.Context, req *scheduler.SubmitTaskRequest) (*scheduler.SubmitTaskResponse, error) {
			if failCount < 3 {
				failCount++
				return nil, status.Error(codes.Internal, "internal server error")
			}
			return &scheduler.SubmitTaskResponse{Accepted: true, QueuePosition: 1}, nil
		},
	}

	addr := startMockServer(t, mock)
	recoverTime := 100 * time.Millisecond
	client := &Module1RealClient{
		address:      addr,
		log:          logrus.New(),
		circuitState: StateClosed,
		config: &ClientConfig{
			MaxRetries:        0,
			Timeout:           5 * time.Second,
			RetryBackoff:      1 * time.Millisecond,
			MaxConsecutiveErr: 3,
			CircuitRecoverTime: recoverTime,
		},
		stopChan:       make(chan struct{}),
		consecutiveErr: 0,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	conn, err := grpc.DialContext(ctx, addr, grpc.WithInsecure())
	cancel()
	require.NoError(t, err)

	client.conn = conn
	client.client = scheduler.NewSchedulerServiceClient(conn)

	t.Cleanup(func() {
		client.Close()
	})

	// Trigger failures to open circuit
	for i := 0; i < 3; i++ {
		client.SubmitTask(context.Background(), &SubmitTaskRequest{
			TaskID:   fmt.Sprintf("test-task-%d", i),
			TenantID: "tenant-1",
			Image:    "nginx:latest",
		})
	}

	assert.Equal(t, string(StateOpen), client.GetCircuitBreakerState())

	// Wait for recovery timeout
	time.Sleep(recoverTime + 50*time.Millisecond)

	// Circuit should transition to half-open, then succeed
	resp, err := client.SubmitTask(context.Background(), &SubmitTaskRequest{
		TaskID:   "test-task-recover",
		TenantID: "tenant-1",
		Image:    "nginx:latest",
	})

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.True(t, resp.Accepted)
	// Circuit should be closed again after successful call
	assert.Equal(t, string(StateClosed), client.GetCircuitBreakerState())
}

// TestTimeoutHandling tests timeout is respected
func TestTimeoutHandling(t *testing.T) {
	mock := &MockSchedulerService{
		callCount: make(map[string]int),
		submitTaskFunc: func(ctx context.Context, req *scheduler.SubmitTaskRequest) (*scheduler.SubmitTaskResponse, error) {
			// Simulate slow handler
			time.Sleep(2 * time.Second)
			return &scheduler.SubmitTaskResponse{Accepted: true}, nil
		},
	}

	addr := startMockServer(t, mock)
	client := &Module1RealClient{
		address:      addr,
		log:          logrus.New(),
		circuitState: StateClosed,
		config: &ClientConfig{
			MaxRetries:        0,
			Timeout:           100 * time.Millisecond, // Very short timeout
			RetryBackoff:      10 * time.Millisecond,
			MaxConsecutiveErr: 5,
			CircuitRecoverTime: 2 * time.Second,
		},
		stopChan:       make(chan struct{}),
		consecutiveErr: 0,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	conn, err := grpc.DialContext(ctx, addr, grpc.WithInsecure())
	cancel()
	require.NoError(t, err)

	client.conn = conn
	client.client = scheduler.NewSchedulerServiceClient(conn)

	t.Cleanup(func() {
		client.Close()
	})

	// Should timeout
	resp, err := client.SubmitTask(context.Background(), &SubmitTaskRequest{
		TaskID:   "test-task-timeout",
		TenantID: "tenant-1",
		Image:    "nginx:latest",
	})

	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "failed after")
}

// TestGetTaskStatus tests status retrieval
func TestGetTaskStatus(t *testing.T) {
	mock := &MockSchedulerService{
		callCount: make(map[string]int),
		getStatusFunc: func(ctx context.Context, req *scheduler.GetTaskStatusRequest) (*scheduler.TaskStatusResponse, error) {
			return &scheduler.TaskStatusResponse{
				TaskId:      req.TaskId,
				Status:      "Running",
				PodName:     "task-pod-1",
				StartedAt:   time.Now().Unix(),
				CompletedAt: 0,
				ExitCode:    0,
			}, nil
		},
	}

	addr := startMockServer(t, mock)
	client := &Module1RealClient{
		address:      addr,
		log:          logrus.New(),
		circuitState: StateClosed,
		config: &ClientConfig{
			MaxRetries:        3,
			Timeout:           5 * time.Second,
			RetryBackoff:      100 * time.Millisecond,
			MaxConsecutiveErr: 5,
			CircuitRecoverTime: 2 * time.Second,
		},
		stopChan:       make(chan struct{}),
		consecutiveErr: 0,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	conn, err := grpc.DialContext(ctx, addr, grpc.WithInsecure())
	cancel()
	require.NoError(t, err)

	client.conn = conn
	client.client = scheduler.NewSchedulerServiceClient(conn)

	t.Cleanup(func() {
		client.Close()
	})

	resp, err := client.GetTaskStatus(context.Background(), "task-123")

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, "task-123", resp.TaskID)
	assert.Equal(t, "Running", resp.Status)
	assert.Equal(t, "task-pod-1", resp.PodName)
}

// TestCancelTask tests task cancellation
func TestCancelTask(t *testing.T) {
	mock := &MockSchedulerService{
		callCount: make(map[string]int),
		cancelTaskFunc: func(ctx context.Context, req *scheduler.CancelTaskRequest) (*scheduler.CancelTaskResponse, error) {
			return &scheduler.CancelTaskResponse{Cancelled: true}, nil
		},
	}

	addr := startMockServer(t, mock)
	client := &Module1RealClient{
		address:      addr,
		log:          logrus.New(),
		circuitState: StateClosed,
		config: &ClientConfig{
			MaxRetries:        3,
			Timeout:           5 * time.Second,
			RetryBackoff:      100 * time.Millisecond,
			MaxConsecutiveErr: 5,
			CircuitRecoverTime: 2 * time.Second,
		},
		stopChan:       make(chan struct{}),
		consecutiveErr: 0,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	conn, err := grpc.DialContext(ctx, addr, grpc.WithInsecure())
	cancel()
	require.NoError(t, err)

	client.conn = conn
	client.client = scheduler.NewSchedulerServiceClient(conn)

	t.Cleanup(func() {
		client.Close()
	})

	err = client.CancelTask(context.Background(), "task-456")

	assert.NoError(t, err)
}

// TestGetQueueDepth tests queue depth retrieval
func TestGetQueueDepth(t *testing.T) {
	mock := &MockSchedulerService{
		callCount: make(map[string]int),
		getQueueDepthFunc: func(ctx context.Context, req *scheduler.GetQueueDepthRequest) (*scheduler.QueueDepthResponse, error) {
			return &scheduler.QueueDepthResponse{
				TotalQueued:  10,
				PendingTasks: 5,
				RunningTasks: 3,
			}, nil
		},
	}

	addr := startMockServer(t, mock)
	client := &Module1RealClient{
		address:      addr,
		log:          logrus.New(),
		circuitState: StateClosed,
		config: &ClientConfig{
			MaxRetries:        3,
			Timeout:           5 * time.Second,
			RetryBackoff:      100 * time.Millisecond,
			MaxConsecutiveErr: 5,
			CircuitRecoverTime: 2 * time.Second,
		},
		stopChan:       make(chan struct{}),
		consecutiveErr: 0,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	conn, err := grpc.DialContext(ctx, addr, grpc.WithInsecure())
	cancel()
	require.NoError(t, err)

	client.conn = conn
	client.client = scheduler.NewSchedulerServiceClient(conn)

	t.Cleanup(func() {
		client.Close()
	})

	resp, err := client.GetQueueDepth(context.Background())

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, int32(10), resp.TotalQueued)
	assert.Equal(t, int32(5), resp.PendingTasks)
	assert.Equal(t, int32(3), resp.RunningTasks)
}
