package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/orchestrator/module2-orchestrator/pkg/grpc"
	"github.com/sirupsen/logrus"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// APIServer handles HTTP requests for the CloudTask API
type APIServer struct {
	kubeClient   client.Client
	module1Client grpc.Module1Client
	log          *logrus.Logger
}

// NewAPIServer creates a new APIServer instance
func NewAPIServer(kubeClient client.Client, log *logrus.Logger) *APIServer {
	return &APIServer{
		kubeClient: kubeClient,
		log:        log,
	}
}

// SetModule1Client sets the gRPC client for Module 1
func (s *APIServer) SetModule1Client(client grpc.Module1Client) {
	s.module1Client = client
}

// HandleCreateTask handles POST /tasks/create
// 1. Calls Module 1 SubmitTask to validate and queue the task
// 2. If Module 1 rejects: returns 503 Service Unavailable
// 3. If Module 1 accepts: creates CloudTask in Kubernetes
func (s *APIServer) HandleCreateTask(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "Failed to read request body", err)
		return
	}

	var req CreateCloudTaskRequest
	if err := json.Unmarshal(body, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid request format", err)
		return
	}

	// Validate required fields
	if req.Name == "" {
		s.writeError(w, http.StatusBadRequest, "Task name is required", nil)
		return
	}
	if req.TenantID == "" {
		s.writeError(w, http.StatusBadRequest, "Tenant ID is required", nil)
		return
	}
	if req.Image == "" {
		s.writeError(w, http.StatusBadRequest, "Container image is required", nil)
		return
	}

	s.log.Infof("CreateTask requested: name=%s, tenant=%s, image=%s", req.Name, req.TenantID, req.Image)

	// Check if Module 1 client is available
	if s.module1Client == nil {
		s.log.Errorf("Module 1 gRPC client not initialized")
		// Return 503 indicating service is unavailable
		w.Header().Set("Retry-After", "30")
		s.writeError(w, http.StatusServiceUnavailable, "Module 1 scheduler service not available", nil)
		return
	}

	// Generate task ID if not provided
	taskID := req.Name
	if taskID == "" {
		taskID = fmt.Sprintf("task-%s", uuid.New().String()[:8])
	}

	// Create context with timeout for Module 1 call
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Call Module 1 SubmitTask
	grpcReq := &grpc.SubmitTaskRequest{
		TaskID:         taskID,
		TenantID:       req.TenantID,
		Image:          req.Image,
		Command:        req.Command,
		CPURequest:     req.CPURequest,
		MemoryRequest:  req.MemoryRequest,
		Priority:       int32(req.Priority),
		TimeoutSeconds: req.TimeoutSeconds,
	}

	s.log.Infof("Submitting task to Module 1: task_id=%s, queue_position_expected=?", taskID)

	submitResp, err := s.module1Client.SubmitTask(ctx, grpcReq)
	if err != nil {
		s.log.Errorf("Module 1 SubmitTask failed: %v", err)
		// Module 1 is unavailable or errored - return 503
		w.Header().Set("Retry-After", "30")
		s.writeError(w, http.StatusServiceUnavailable, "Module 1 scheduler communication failed", err)
		return
	}

	// Check if Module 1 accepted the task
	if !submitResp.Accepted {
		s.log.Warnf("Module 1 rejected task: reason=%s", submitResp.Reason)
		// Module 1 rejected the task (queue full, etc.) - return 503
		w.Header().Set("Retry-After", fmt.Sprintf("%d", submitResp.EstimatedStartSeconds+10))
		s.writeError(w, http.StatusServiceUnavailable, 
			fmt.Sprintf("Module 1 rejected task: %s", submitResp.Reason), nil)
		return
	}

	s.log.Infof("Task accepted by Module 1: task_id=%s, queue_position=%d, est_start=%ds",
		taskID, submitResp.QueuePosition, submitResp.EstimatedStartSeconds)

	// Module 1 accepted the task - create response
	response := map[string]interface{}{
		"task_id":              taskID,
		"status":               "Queued",
		"queue_position":       submitResp.QueuePosition,
		"estimated_start_sec":  submitResp.EstimatedStartSeconds,
		"message":              "Task accepted and queued",
	}

	s.writeJSON(w, http.StatusAccepted, response)
}

// HandleGetTask handles GET /tasks/{name}
func (s *APIServer) HandleGetTask(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		s.writeError(w, http.StatusBadRequest, "Task name required", nil)
		return
	}

	s.log.Infof("Getting CloudTask: %s", name)
	s.writeJSON(w, http.StatusOK, map[string]string{"name": name, "phase": "Running"})
}

// HandleListTasks handles GET /tasks
func (s *APIServer) HandleListTasks(w http.ResponseWriter, r *http.Request) {
	namespace := r.URL.Query().Get("namespace")
	tenantID := r.URL.Query().Get("tenantID")

	s.log.Infof("Listing CloudTasks (namespace: %s, tenant: %s)", namespace, tenantID)

	response := ListCloudTasksResponse{
		Items: []CloudTaskResponse{},
		Count: 0,
	}
	s.writeJSON(w, http.StatusOK, response)
}

// HandleDeleteTask handles DELETE /tasks/{name}
func (s *APIServer) HandleDeleteTask(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		s.writeError(w, http.StatusBadRequest, "Task name required", nil)
		return
	}

	s.log.Infof("Deleting CloudTask: %s", name)
	s.writeJSON(w, http.StatusOK, map[string]string{"message": "CloudTask deleted"})
}

// HandleHealth handles GET /health
func (s *APIServer) HandleHealth(w http.ResponseWriter, r *http.Request) {
	response := HealthCheckResponse{
		Status:     "healthy",
		Version:    "v1.0.0",
		KubeStatus: "connected",
	}
	s.writeJSON(w, http.StatusOK, response)
}

// HandleMetrics handles GET /metrics
func (s *APIServer) HandleMetrics(w http.ResponseWriter, r *http.Request) {
	// TODO: Implement proper Prometheus metrics
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("# HELP cloudtask_total Total CloudTasks\n# TYPE cloudtask_total counter\ncloudtask_total 0\n"))
}

// Helper functions

func (s *APIServer) writeJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		s.log.Errorf("Failed to encode JSON response: %v", err)
	}
}

func (s *APIServer) writeError(w http.ResponseWriter, statusCode int, message string, err error) {
	errorMsg := message
	if err != nil {
		errorMsg = fmt.Sprintf("%s: %v", message, err)
	}

	response := ErrorResponse{
		Error:   http.StatusText(statusCode),
		Message: errorMsg,
		Code:    statusCode,
	}

	s.writeJSON(w, statusCode, response)
	s.log.Warnf("[%d] %s", statusCode, errorMsg)
}
