package grpc

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/connectivity"
)

// SubmitTaskRequest represents a task submission request
type SubmitTaskRequest struct {
	TaskID          string
	TenantID        string
	Image           string
	Command         []string
	CPURequest      string
	MemoryRequest   string
	Priority        int32
	TimeoutSeconds  int64
}

// SubmitTaskResponse represents the response to a task submission
type SubmitTaskResponse struct {
	Accepted              bool
	QueuePosition         int32
	EstimatedStartSeconds int64
	Reason                string
}

// TaskStatusResponse represents task status information
type TaskStatusResponse struct {
	TaskID      string
	Status      string // Pending, Running, Completed, Failed
	PodName     string
	StartedAt   int64 // Unix timestamp
	CompletedAt int64 // Unix timestamp
	ExitCode    int32
}

// QueueDepthResponse represents queue depth information
type QueueDepthResponse struct {
	TotalQueued  int32
	PendingTasks int32
	RunningTasks int32
}

// Module1Client defines the interface for interacting with Module 1
type Module1Client interface {
	// SubmitTask submits a task to Module 1 for scheduling
	SubmitTask(ctx context.Context, req *SubmitTaskRequest) (*SubmitTaskResponse, error)

	// GetTaskStatus gets the status of a task from Module 1
	GetTaskStatus(ctx context.Context, taskID string) (*TaskStatusResponse, error)

	// CancelTask cancels a task in Module 1
	CancelTask(ctx context.Context, taskID string) error

	// GetQueueDepth gets the current queue depth from Module 1
	GetQueueDepth(ctx context.Context) (*QueueDepthResponse, error)

	// Close closes the client connection
	Close() error
}

// RealClient implements Module1Client with actual gRPC connection
type RealClient struct {
	address string
	conn    *grpc.ClientConn
	log     *logrus.Logger
}

// NewRealClient creates a new RealClient with retry logic
// Reads MODULE1_GRPC_ADDRESS environment variable
// Default: localhost:50051
func NewRealClient(log *logrus.Logger) (*RealClient, error) {
	if log == nil {
		log = logrus.New()
	}

	address := os.Getenv("MODULE1_GRPC_ADDRESS")
	if address == "" {
		address = "localhost:50051"
	}

	client := &RealClient{
		address: address,
		log:     log,
	}

	if err := client.connectWithRetry(); err != nil {
		return nil, err
	}

	return client, nil
}

// connectWithRetry attempts to connect to Module 1 with exponential backoff
// Max 3 attempts with 2 second backoff
func (c *RealClient) connectWithRetry() error {
	maxAttempts := 3
	backoffDuration := 2 * time.Second

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		c.log.Infof("Connecting to Module 1 at %s (attempt %d/%d)", c.address, attempt, maxAttempts)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		conn, err := grpc.DialContext(
			ctx,
			c.address,
			grpc.WithInsecure(),
			grpc.WithConnectParams(grpc.ConnectParams{
				Backoff:           backoff.Config{MaxDelay: 5 * time.Second},
				MinConnectTimeout: 5 * time.Second,
			}),
		)

		if err == nil {
			c.conn = conn
			c.log.Infof("Successfully connected to Module 1")
			return nil
		}

		c.log.Warnf("Connection attempt %d failed: %v", attempt, err)

		if attempt < maxAttempts {
			time.Sleep(backoffDuration)
		}
	}

	return fmt.Errorf("failed to connect to Module 1 after %d attempts", maxAttempts)
}

// ensureConnected verifies connection is still valid
func (c *RealClient) ensureConnected() error {
	if c.conn == nil {
		return fmt.Errorf("client not connected")
	}

	state := c.conn.GetState()
	if state == connectivity.TransientFailure || state == connectivity.Shutdown {
		c.log.Warnf("Connection state is %v, attempting to reconnect", state)
		if err := c.connectWithRetry(); err != nil {
			return err
		}
	}

	return nil
}

// SubmitTask submits a task to Module 1
func (c *RealClient) SubmitTask(ctx context.Context, req *SubmitTaskRequest) (*SubmitTaskResponse, error) {
	if err := c.ensureConnected(); err != nil {
		return nil, err
	}

	// Create timeout context if not specified
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}

	// TODO: Call actual scheduler service stub once generated
	// For now, return placeholder
	c.log.Debugf("SubmitTask called for task %s (tenant: %s)", req.TaskID, req.TenantID)
	return &SubmitTaskResponse{
		Accepted:              true,
		QueuePosition:         1,
		EstimatedStartSeconds: 5,
	}, nil
}

// GetTaskStatus gets the status of a task from Module 1
func (c *RealClient) GetTaskStatus(ctx context.Context, taskID string) (*TaskStatusResponse, error) {
	if err := c.ensureConnected(); err != nil {
		return nil, err
	}

	// Create timeout context if not specified
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}

	// TODO: Call actual scheduler service stub once generated
	// For now, return placeholder
	c.log.Debugf("GetTaskStatus called for task %s", taskID)
	return &TaskStatusResponse{
		TaskID:  taskID,
		Status:  "Running",
		PodName: fmt.Sprintf("%s-pod", taskID),
	}, nil
}

// CancelTask cancels a task in Module 1
func (c *RealClient) CancelTask(ctx context.Context, taskID string) error {
	if err := c.ensureConnected(); err != nil {
		return err
	}

	// Create timeout context if not specified
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}

	// TODO: Call actual scheduler service stub once generated
	// For now, return nil
	c.log.Debugf("CancelTask called for task %s", taskID)
	return nil
}

// GetQueueDepth gets the current queue depth from Module 1
func (c *RealClient) GetQueueDepth(ctx context.Context) (*QueueDepthResponse, error) {
	if err := c.ensureConnected(); err != nil {
		return nil, err
	}

	// Create timeout context if not specified
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}

	// TODO: Call actual scheduler service stub once generated
	// For now, return placeholder
	c.log.Debugf("GetQueueDepth called")
	return &QueueDepthResponse{
		TotalQueued:  10,
		PendingTasks: 5,
		RunningTasks: 5,
	}, nil
}

// Close closes the gRPC connection
func (c *RealClient) Close() error {
	if c.conn != nil {
		c.log.Infof("Closing connection to Module 1")
		return c.conn.Close()
	}
	return nil
}
