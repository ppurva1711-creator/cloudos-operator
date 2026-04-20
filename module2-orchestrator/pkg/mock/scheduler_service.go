package mock

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
)

// Task represents an internally tracked task
type Task struct {
	ID            string
	TenantID      string
	Status        string // Pending, Running, Completed, Failed
	QueuePosition int32
	StartedAt     int64
	CompletedAt   int64
	ExitCode      int32
	CPURequest    string
	MemoryRequest string
	PodName       string
	CreatedAt     time.Time
}

// SchedulerService implements the Module 1 scheduler service
type SchedulerService struct {
	log           *logrus.Entry
	delayMS       int
	failureRate   int
	redisClient   *redis.Client
	tasks         map[string]*Task
	queueDepth    int32
	mu            sync.RWMutex
	stopChan      chan struct{}
	taskUpdatesCh chan *Task
}

// NewSchedulerService creates a new scheduler service
func NewSchedulerService(
	log *logrus.Entry,
	delayMS int,
	failureRate int,
	redisAddr string,
	postgresDSN string,
) (*SchedulerService, error) {
	// Initialize Redis client
	redisClient := redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})

	// Test Redis connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := redisClient.Ping(ctx).Err(); err != nil {
		log.Warnf("Redis connection failed: %v (will continue without Redis)", err)
	}

	service := &SchedulerService{
		log:           log.WithField("service", "scheduler"),
		delayMS:       delayMS,
		failureRate:   failureRate,
		redisClient:   redisClient,
		tasks:         make(map[string]*Task),
		queueDepth:    0,
		stopChan:      make(chan struct{}),
		taskUpdatesCh: make(chan *Task, 100),
	}

	// Start background task processor
	go service.processTaskUpdates()

	return service, nil
}

// SubmitTask submits a new task for scheduling
func (s *SchedulerService) SubmitTask(ctx context.Context, req map[string]interface{}) (map[string]interface{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	taskID := fmt.Sprintf("%v", req["task_id"])
	tenantID := fmt.Sprintf("%v", req["tenant_id"])

	s.log.WithFields(logrus.Fields{
		"task_id":   taskID,
		"tenant_id": tenantID,
	}).Info("Received SubmitTask request")

	// Create task
	task := &Task{
		ID:            taskID,
		TenantID:      tenantID,
		Status:        "Pending",
		QueuePosition: s.queueDepth + 1,
		PodName:       fmt.Sprintf("%s-pod-%d", taskID, time.Now().Unix()),
		CreatedAt:     time.Now(),
		CPURequest:    fmt.Sprintf("%v", req["cpu_request"]),
		MemoryRequest: fmt.Sprintf("%v", req["memory_request"]),
	}

	// Store task
	s.tasks[taskID] = task
	s.queueDepth++

	// Schedule task transition (Pending -> Running -> Completed/Failed)
	go s.scheduleTaskTransition(task)

	return map[string]interface{}{
		"accepted":                true,
		"queue_position":          task.QueuePosition,
		"estimated_start_seconds": int64(s.delayMS / 1000),
		"reason":                  "",
	}, nil
}

// GetTaskStatus retrieves the current status of a task
func (s *SchedulerService) GetTaskStatus(ctx context.Context, taskID string, tenantID string) (map[string]interface{}, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	task, exists := s.tasks[taskID]
	if !exists {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}

	s.log.WithFields(logrus.Fields{
		"task_id": taskID,
		"status":  task.Status,
	}).Debug("Retrieved task status")

	return map[string]interface{}{
		"task_id":      task.ID,
		"status":       task.Status,
		"pod_name":     task.PodName,
		"started_at":   task.StartedAt,
		"completed_at": task.CompletedAt,
		"exit_code":    task.ExitCode,
	}, nil
}

// CancelTask cancels a pending or running task
func (s *SchedulerService) CancelTask(ctx context.Context, taskID string, tenantID string) (map[string]interface{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, exists := s.tasks[taskID]
	if !exists {
		return map[string]interface{}{
			"cancelled": false,
			"reason":    "task not found",
		}, nil
	}

	if task.Status == "Completed" || task.Status == "Failed" {
		return map[string]interface{}{
			"cancelled": false,
			"reason":    fmt.Sprintf("cannot cancel %s task", task.Status),
		}, nil
	}

	task.Status = "Failed"
	task.CompletedAt = time.Now().Unix()
	task.ExitCode = 137 // SIGKILL
	s.queueDepth--

	s.log.WithFields(logrus.Fields{
		"task_id": taskID,
	}).Info("Task cancelled")

	// Publish cancellation event
	s.publishEvent("tasks:cancelled", task)

	return map[string]interface{}{
		"cancelled": true,
		"reason":    "",
	}, nil
}

// GetQueueDepth returns the current queue depth
func (s *SchedulerService) GetQueueDepth(ctx context.Context) (map[string]interface{}, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	pending := int32(0)
	running := int32(0)

	for _, task := range s.tasks {
		if task.Status == "Pending" {
			pending++
		} else if task.Status == "Running" {
			running++
		}
	}

	return map[string]interface{}{
		"total_queued":   s.queueDepth,
		"pending_tasks":  pending,
		"running_tasks":  running,
	}, nil
}

// GetResourceAllocation returns resource allocation for a task
func (s *SchedulerService) GetResourceAllocation(ctx context.Context, taskID string) (map[string]interface{}, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	task, exists := s.tasks[taskID]
	if !exists {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}

	return map[string]interface{}{
		"task_id":         task.ID,
		"cpu_allocation":  task.CPURequest,
		"memory_allocation": task.MemoryRequest,
		"node_name":       "mock-node",
	}, nil
}

// AddPendingTask adds a task to the pending queue (for testing)
func (s *SchedulerService) AddPendingTask(taskID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	task := &Task{
		ID:            taskID,
		TenantID:      "system",
		Status:        "Pending",
		QueuePosition: s.queueDepth + 1,
		PodName:       fmt.Sprintf("%s-pod", taskID),
		CreatedAt:     time.Now(),
	}

	s.tasks[taskID] = task
	s.queueDepth++
}

// scheduleTaskTransition schedules transitions of a task through states
func (s *SchedulerService) scheduleTaskTransition(task *Task) {
	// Wait for delay before moving to Running
	time.Sleep(time.Duration(s.delayMS) * time.Millisecond)

	s.mu.Lock()
	task.Status = "Running"
	task.StartedAt = time.Now().Unix()
	s.mu.Unlock()

	s.log.WithFields(logrus.Fields{
		"task_id": task.ID,
	}).Info("Task transitioned to Running")

	// Simulate execution
	time.Sleep(time.Duration(s.delayMS) * time.Millisecond)

	s.mu.Lock()
	defer s.mu.Unlock()

	// Determine success or failure
	shouldFail := false
	if s.failureRate > 0 {
		shouldFail = rand.Intn(100) < s.failureRate
	}

	task.CompletedAt = time.Now().Unix()

	if shouldFail {
		task.Status = "Failed"
		task.ExitCode = 1
		s.log.WithFields(logrus.Fields{
			"task_id": task.ID,
		}).Info("Task completed with failure")
		s.publishEvent("tasks:failed", task)
	} else {
		task.Status = "Completed"
		task.ExitCode = 0
		s.log.WithFields(logrus.Fields{
			"task_id": task.ID,
		}).Info("Task completed successfully")
		s.publishEvent("tasks:completed", task)
	}

	s.queueDepth--
}

// processTaskUpdates processes task updates from the channel
func (s *SchedulerService) processTaskUpdates() {
	for {
		select {
		case <-s.stopChan:
			return
		case task := <-s.taskUpdatesCh:
			if task != nil {
				s.log.WithFields(logrus.Fields{
					"task_id": task.ID,
					"status":  task.Status,
				}).Debug("Processing task update")
			}
		}
	}
}

// publishEvent publishes a task event to Redis
func (s *SchedulerService) publishEvent(channel string, task *Task) {
	if s.redisClient == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	message := map[string]interface{}{
		"task_id":      task.ID,
		"tenant_id":    task.TenantID,
		"status":       task.Status,
		"exit_code":    task.ExitCode,
		"timestamp":    time.Now().Unix(),
		"pod_name":     task.PodName,
	}

	// Convert to JSON-like string (simple format)
	msgStr := fmt.Sprintf(`{"task_id":"%s","tenant_id":"%s","status":"%s","exit_code":%d,"timestamp":%d,"pod_name":"%s"}`,
		task.ID, task.TenantID, task.Status, task.ExitCode, time.Now().Unix(), task.PodName)

	if err := s.redisClient.Publish(ctx, channel, msgStr).Err(); err != nil {
		s.log.WithFields(logrus.Fields{
			"channel": channel,
			"task_id": task.ID,
		}).Warnf("Failed to publish event: %v", err)
	}
}

// RegisterSchedulerService registers the scheduler service with a gRPC server
// This is a generic implementation since we're not using generated gRPC code
func RegisterSchedulerService(server *grpc.Server, service *SchedulerService) {
	// Register a lightweight service that uses Struct for requests/responses
	svcDesc := &grpc.ServiceDesc{
		ServiceName: "scheduler.SchedulerService",
		HandlerType: (*SchedulerService)(nil),
		Methods: []grpc.MethodDesc{
			{
				MethodName: "SubmitTask",
				Handler: func(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
					var in structpb.Struct
					if err := dec(&in); err != nil {
						return nil, err
					}
					svc := srv.(*SchedulerService)
					respMap, err := svc.SubmitTask(ctx, in.AsMap())
					if err != nil {
						return nil, err
					}
					out, _ := structpb.NewStruct(respMap)
					return out, nil
				},
			},
			{
				MethodName: "GetTaskStatus",
				Handler: func(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
					var in structpb.Struct
					if err := dec(&in); err != nil {
						return nil, err
					}
					svc := srv.(*SchedulerService)
					// extract task_id and tenant_id
					m := in.AsMap()
					taskID := ""
					tenantID := ""
					if v, ok := m["task_id"]; ok {
						taskID = fmt.Sprintf("%v", v)
					}
					if v, ok := m["tenant_id"]; ok {
						tenantID = fmt.Sprintf("%v", v)
					}
					respMap, err := svc.GetTaskStatus(ctx, taskID, tenantID)
					if err != nil {
						return nil, err
					}
					out, _ := structpb.NewStruct(respMap)
					return out, nil
				},
			},
			{
				MethodName: "CancelTask",
				Handler: func(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
					var in structpb.Struct
					if err := dec(&in); err != nil {
						return nil, err
					}
					svc := srv.(*SchedulerService)
					m := in.AsMap()
					taskID := ""
					tenantID := ""
					if v, ok := m["task_id"]; ok {
						taskID = fmt.Sprintf("%v", v)
					}
					if v, ok := m["tenant_id"]; ok {
						tenantID = fmt.Sprintf("%v", v)
					}
					respMap, err := svc.CancelTask(ctx, taskID, tenantID)
					if err != nil {
						return nil, err
					}
					out, _ := structpb.NewStruct(respMap)
					return out, nil
				},
			},
			{
				MethodName: "GetQueueDepth",
				Handler: func(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
					svc := srv.(*SchedulerService)
					respMap, err := svc.GetQueueDepth(ctx)
					if err != nil {
						return nil, err
					}
					out, _ := structpb.NewStruct(respMap)
					return out, nil
				},
			},
		},
		Streams:  []grpc.StreamDesc{},
		Metadata: "",
	}

	server.RegisterService(svcDesc, service)
}

// Close closes the scheduler service
func (s *SchedulerService) Close() error {
	close(s.stopChan)
	if s.redisClient != nil {
		return s.redisClient.Close()
	}
	return nil
}
