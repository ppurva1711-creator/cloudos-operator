package grpc

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/orchestrator/module2-orchestrator/pkg/grpc/proto/scheduler"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/keepalive"
)

// CircuitBreakerState defines the state of the circuit breaker
type CircuitBreakerState string

const (
	StateClosed   CircuitBreakerState = "closed"
	StateOpen     CircuitBreakerState = "open"
	StateHalfOpen CircuitBreakerState = "half-open"
)

// Module1RealClient implements Module1Client with actual gRPC connection
// It includes retry logic, circuit breaker, and health checks
type Module1RealClient struct {
	address        string
	conn           *grpc.ClientConn
	client         scheduler.SchedulerServiceClient
	log            *logrus.Logger
	mu             sync.RWMutex
	consecutiveErr int
	circuitState   CircuitBreakerState
	lastFailTime   time.Time
	config         *ClientConfig
	healthTicker   *time.Ticker
	stopChan       chan struct{}
}

// ClientConfig holds configuration for Module1RealClient
type ClientConfig struct {
	MaxRetries        int           // Maximum number of retries (default: 3)
	Timeout           time.Duration // Call timeout (default: 30s)
	RetryBackoff      time.Duration // Initial backoff duration (default: 1s)
	MaxConsecutiveErr int           // Max consecutive errors before circuit opens (default: 5)
	CircuitRecoverTime time.Duration // Time to attempt recovery after opening (default: 30s)
	HealthCheckInterval time.Duration // Connection health check interval (default: 30s)
}

// NewModule1RealClient creates a new Module1RealClient with configuration from environment variables
// Supported environment variables:
// - MODULE1_GRPC_ADDRESS: gRPC server address (default: module1-scheduler:50051)
// - MODULE1_GRPC_TIMEOUT: Call timeout in seconds (default: 30)
// - MODULE1_GRPC_MAX_RETRIES: Max retry attempts (default: 3)
func NewModule1RealClient(log *logrus.Logger) (*Module1RealClient, error) {
	if log == nil {
		log = logrus.New()
	}

	// Parse configuration from environment variables
	config := &ClientConfig{
		MaxRetries:        3,
		Timeout:           30 * time.Second,
		RetryBackoff:      1 * time.Second,
		MaxConsecutiveErr: 5,
		CircuitRecoverTime: 30 * time.Second,
		HealthCheckInterval: 30 * time.Second,
	}

	if timeout := os.Getenv("MODULE1_GRPC_TIMEOUT"); timeout != "" {
		if t, err := strconv.Atoi(timeout); err == nil {
			config.Timeout = time.Duration(t) * time.Second
		}
	}

	if maxRetries := os.Getenv("MODULE1_GRPC_MAX_RETRIES"); maxRetries != "" {
		if r, err := strconv.Atoi(maxRetries); err == nil {
			config.MaxRetries = r
		}
	}

	address := os.Getenv("MODULE1_GRPC_ADDRESS")
	if address == "" {
		address = "module1-scheduler:50051"
	}

	client := &Module1RealClient{
		address:        address,
		log:            log,
		circuitState:   StateClosed,
		config:         config,
		stopChan:       make(chan struct{}),
		consecutiveErr: 0,
	}

	log.Infof("Initializing Module1 gRPC client: address=%s, timeout=%s, max_retries=%d",
		address, config.Timeout, config.MaxRetries)

	if err := client.connectWithRetry(); err != nil {
		return nil, err
	}

	// Start health check goroutine
	go client.healthCheckLoop()

	return client, nil
}

// connectWithRetry attempts to establish a gRPC connection with exponential backoff retry logic
func (c *Module1RealClient) connectWithRetry() error {
	for attempt := 1; attempt <= c.config.MaxRetries; attempt++ {
		c.log.Infof("Connecting to Module 1 at %s (attempt %d/%d)", c.address, attempt, c.config.MaxRetries)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		conn, err := grpc.DialContext(
			ctx,
			c.address,
			grpc.WithInsecure(),
			grpc.WithConnectParams(grpc.ConnectParams{
				Backoff:           backoff.Config{MaxDelay: 5 * time.Second},
				MinConnectTimeout: 3 * time.Second,
			}),
			grpc.WithKeepaliveParams(keepalive.ClientParameters{
				Time:                10 * time.Second,
				Timeout:             5 * time.Second,
				PermitWithoutStream: true,
			}),
		)

		if err == nil {
			c.mu.Lock()
			c.conn = conn
			c.client = scheduler.NewSchedulerServiceClient(conn)
			c.consecutiveErr = 0
			c.circuitState = StateClosed
			c.mu.Unlock()

			c.log.Infof("Successfully connected to Module 1")
			return nil
		}

		c.log.Warnf("Connection attempt %d/%d failed: %v", attempt, c.config.MaxRetries, err)

		if attempt < c.config.MaxRetries {
			backoffDuration := c.config.RetryBackoff * time.Duration(1<<uint(attempt-1))
			c.log.Debugf("Backing off for %s before next attempt", backoffDuration)
			time.Sleep(backoffDuration)
		}
	}

	return fmt.Errorf("failed to connect to Module 1 after %d attempts", c.config.MaxRetries)
}

// ensureConnected verifies connection is still valid, reconnect if needed
func (c *Module1RealClient) ensureConnected() error {
	c.mu.RLock()
	if c.conn == nil {
		c.mu.RUnlock()
		return fmt.Errorf("client not connected")
	}

	state := c.conn.GetState()
	c.mu.RUnlock()

	if state == connectivity.TransientFailure || state == connectivity.Shutdown {
		c.log.Warnf("Connection state is %v, attempting to reconnect", state)
		return c.connectWithRetry()
	}

	return nil
}

// checkCircuitBreaker checks if circuit should be opened/closed
func (c *Module1RealClient) checkCircuitBreaker() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.circuitState == StateClosed {
		return nil
	}

	if c.circuitState == StateOpen {
		// Check if recovery time has elapsed
		if time.Since(c.lastFailTime) > c.config.CircuitRecoverTime {
			c.log.Infof("Circuit breaker transitioning to half-open after recovery timeout")
			c.circuitState = StateHalfOpen
			c.consecutiveErr = 0
			return nil
		}
		return fmt.Errorf("circuit breaker is open (failed %d consecutive times, recover in %s)",
			c.config.MaxConsecutiveErr,
			c.config.CircuitRecoverTime-time.Since(c.lastFailTime))
	}

	return nil
}

// recordSuccess resets error tracking on successful call
func (c *Module1RealClient) recordSuccess() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.circuitState == StateHalfOpen {
		c.log.Infof("Circuit breaker transitioning to closed (call succeeded)")
		c.circuitState = StateClosed
	}

	c.consecutiveErr = 0
}

// recordFailure tracks consecutive errors and opens circuit if threshold exceeded
func (c *Module1RealClient) recordFailure() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.consecutiveErr++
	c.lastFailTime = time.Now()

	if c.consecutiveErr >= c.config.MaxConsecutiveErr && c.circuitState != StateOpen {
		c.log.Errorf("Circuit breaker opening: %d consecutive errors", c.consecutiveErr)
		c.circuitState = StateOpen
	}
}

// callWithRetry executes a gRPC call with exponential backoff retry logic
func (c *Module1RealClient) callWithRetry(ctx context.Context, callFunc func(context.Context) error) error {
	// Check circuit breaker first
	if err := c.checkCircuitBreaker(); err != nil {
		return err
	}

	var lastErr error
	backoffDuration := c.config.RetryBackoff

	for attempt := 0; attempt <= c.config.MaxRetries; attempt++ {
		// Create a timeout context for this attempt
		attemptCtx, cancel := context.WithTimeout(ctx, c.config.Timeout)

		err := callFunc(attemptCtx)
		cancel()

		if err == nil {
			c.recordSuccess()
			c.log.Debugf("gRPC call succeeded on attempt %d", attempt+1)
			return nil
		}

		lastErr = err
		c.log.Warnf("gRPC call attempt %d/%d failed: %v", attempt+1, c.config.MaxRetries+1, err)

		if attempt < c.config.MaxRetries {
			c.log.Debugf("Retrying in %s (attempt %d/%d)", backoffDuration, attempt+2, c.config.MaxRetries+1)
			select {
			case <-time.After(backoffDuration):
				backoffDuration *= 2 // Exponential backoff
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}

	c.recordFailure()
	return fmt.Errorf("gRPC call failed after %d attempts: %w", c.config.MaxRetries+1, lastErr)
}

// SubmitTask submits a task to Module 1 for scheduling
func (c *Module1RealClient) SubmitTask(ctx context.Context, req *SubmitTaskRequest) (*SubmitTaskResponse, error) {
	if err := c.ensureConnected(); err != nil {
		return nil, err
	}

	c.log.Infof("SubmitTask: task_id=%s, tenant_id=%s, image=%s, priority=%d",
		req.TaskID, req.TenantID, req.Image, req.Priority)

	var response *scheduler.SubmitTaskResponse
	var callErr error

	err := c.callWithRetry(ctx, func(callCtx context.Context) error {
		pbReq := &scheduler.SubmitTaskRequest{
			TaskId:         req.TaskID,
			TenantId:       req.TenantID,
			Image:          req.Image,
			Command:        req.Command,
			CpuRequest:     req.CPURequest,
			MemoryRequest:  req.MemoryRequest,
			Priority:       req.Priority,
			TimeoutSeconds: req.TimeoutSeconds,
		}

		resp, err := c.client.SubmitTask(callCtx, pbReq)
		if err != nil {
			callErr = err
			return err
		}

		response = resp
		return nil
	})

	if err != nil {
		c.log.Errorf("SubmitTask failed: %v", err)
		return nil, err
	}

	result := &SubmitTaskResponse{
		Accepted:              response.Accepted,
		QueuePosition:         response.QueuePosition,
		EstimatedStartSeconds: response.EstimatedStartSeconds,
		Reason:                response.Reason,
	}

	if response.Accepted {
		c.log.Infof("SubmitTask succeeded: task_id=%s, queue_position=%d, est_start=%ds",
			req.TaskID, response.QueuePosition, response.EstimatedStartSeconds)
	} else {
		c.log.Warnf("SubmitTask rejected: task_id=%s, reason=%s", req.TaskID, response.Reason)
	}

	return result, nil
}

// GetTaskStatus gets the status of a task from Module 1
func (c *Module1RealClient) GetTaskStatus(ctx context.Context, taskID string) (*TaskStatusResponse, error) {
	if err := c.ensureConnected(); err != nil {
		return nil, err
	}

	c.log.Debugf("GetTaskStatus: task_id=%s", taskID)

	var response *scheduler.TaskStatusResponse

	err := c.callWithRetry(ctx, func(callCtx context.Context) error {
		pbReq := &scheduler.GetTaskStatusRequest{
			TaskId:   taskID,
			TenantId: "", // Will be handled by Module 1
		}

		resp, err := c.client.GetTaskStatus(callCtx, pbReq)
		if err != nil {
			return err
		}

		response = resp
		return nil
	})

	if err != nil {
		c.log.Errorf("GetTaskStatus failed: task_id=%s, error=%v", taskID, err)
		return nil, err
	}

	result := &TaskStatusResponse{
		TaskID:      response.TaskId,
		Status:      response.Status,
		PodName:     response.PodName,
		StartedAt:   response.StartedAt,
		CompletedAt: response.CompletedAt,
		ExitCode:    response.ExitCode,
	}

	c.log.Debugf("GetTaskStatus succeeded: task_id=%s, status=%s", taskID, response.Status)

	return result, nil
}

// CancelTask cancels a task in Module 1
func (c *Module1RealClient) CancelTask(ctx context.Context, taskID string) error {
	if err := c.ensureConnected(); err != nil {
		return err
	}

	c.log.Infof("CancelTask: task_id=%s", taskID)

	err := c.callWithRetry(ctx, func(callCtx context.Context) error {
		pbReq := &scheduler.CancelTaskRequest{
			TaskId:   taskID,
			TenantId: "",
		}

		resp, err := c.client.CancelTask(callCtx, pbReq)
		if err != nil {
			return err
		}

		if !resp.Cancelled {
			return fmt.Errorf("task cancellation rejected: %s", resp.Reason)
		}

		return nil
	})

	if err != nil {
		c.log.Errorf("CancelTask failed: task_id=%s, error=%v", taskID, err)
		return err
	}

	c.log.Infof("CancelTask succeeded: task_id=%s", taskID)
	return nil
}

// GetQueueDepth gets the current queue depth from Module 1
func (c *Module1RealClient) GetQueueDepth(ctx context.Context) (*QueueDepthResponse, error) {
	if err := c.ensureConnected(); err != nil {
		return nil, err
	}

	c.log.Debugf("GetQueueDepth called")

	var response *scheduler.QueueDepthResponse

	err := c.callWithRetry(ctx, func(callCtx context.Context) error {
		pbReq := &scheduler.GetQueueDepthRequest{}

		resp, err := c.client.GetQueueDepth(callCtx, pbReq)
		if err != nil {
			return err
		}

		response = resp
		return nil
	})

	if err != nil {
		c.log.Errorf("GetQueueDepth failed: %v", err)
		return nil, err
	}

	result := &QueueDepthResponse{
		TotalQueued:  response.TotalQueued,
		PendingTasks: response.PendingTasks,
		RunningTasks: response.RunningTasks,
	}

	c.log.Debugf("GetQueueDepth: total=%d, pending=%d, running=%d",
		response.TotalQueued, response.PendingTasks, response.RunningTasks)

	return result, nil
}

// healthCheckLoop periodically checks the health of the connection
func (c *Module1RealClient) healthCheckLoop() {
	ticker := time.NewTicker(c.config.HealthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopChan:
			c.log.Debugf("Health check loop stopped")
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, err := c.GetQueueDepth(ctx)
			cancel()

			if err != nil {
				c.log.Warnf("Health check failed: %v", err)
			} else {
				c.log.Debugf("Health check passed")
			}
		}
	}
}

// GetConnectionState returns the current gRPC connection state
func (c *Module1RealClient) GetConnectionState() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.conn == nil {
		return "disconnected"
	}

	return c.conn.GetState().String()
}

// GetCircuitBreakerState returns the current circuit breaker state
func (c *Module1RealClient) GetCircuitBreakerState() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return string(c.circuitState)
}

// Close closes the gRPC connection and stops health checks
func (c *Module1RealClient) Close() error {
	close(c.stopChan)

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		c.log.Infof("Closing connection to Module 1")
		return c.conn.Close()
	}

	return nil
}
