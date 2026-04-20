package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/orchestrator/module2-orchestrator/api/v1"
	"github.com/orchestrator/module2-orchestrator/pkg/api"
	"github.com/orchestrator/module2-orchestrator/pkg/api/validators"
	"github.com/orchestrator/module2-orchestrator/pkg/auth"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TaskHandler handles task-related API requests
type TaskHandler struct {
	kubeClient client.Client
	log        *logrus.Logger
}

// NewTaskHandler creates a new TaskHandler
func NewTaskHandler(kubeClient client.Client, log *logrus.Logger) *TaskHandler {
	return &TaskHandler{
		kubeClient: kubeClient,
		log:        log,
	}
}

// CreateTask handles POST /v1/tasks
// Creates a new CloudTask in the tenant namespace
func (h *TaskHandler) CreateTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Extract tenant ID from JWT context
	tenantID, err := auth.GetTenantIDFromContext(ctx)
	if err != nil {
		h.writeError(w, http.StatusUnauthorized, "Tenant ID not found in token", err)
		return
	}

	// Parse request body
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "Failed to read request body", err)
		return
	}

	var req api.CreateCloudTaskRequest
	if err := json.Unmarshal(body, &req); err != nil {
		h.writeError(w, http.StatusBadRequest, "Invalid JSON format", err)
		return
	}

	// Validate request
	if validationErrors := validators.ValidateCreateTaskRequest(&req); len(validationErrors) > 0 {
		h.writeValidationErrors(w, http.StatusBadRequest, validationErrors)
		return
	}

	// Validate tenant ID from request matches token tenant ID
	if req.TenantID != tenantID {
		h.writeError(w, http.StatusForbidden, "Tenant ID mismatch: cannot create tasks for other tenants", nil)
		return
	}

	// Generate task ID (use name if provided, otherwise generate UUID-like)
	taskID := req.Name
	if taskID == "" {
		taskID = fmt.Sprintf("task-%s", randString(8))
	}

	if err := validators.ValidateTaskID(taskID); err != nil {
		h.writeError(w, http.StatusBadRequest, "Invalid task name", err)
		return
	}

	// Build CloudTask resource
	namespace := fmt.Sprintf("tenant-%s", tenantID)
	cloudTask := &v1.CloudTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      taskID,
			Namespace: namespace,
			Labels: map[string]string{
				"tenantID": tenantID,
				"app":      "cloudtask",
			},
		},
		Spec: v1.CloudTaskSpec{
			Image:    req.Image,
			Command:  req.Command,
			Args:     req.Args,
			TenantID: tenantID,
			Retries:  req.Retries,
			Timeout:  req.Timeout,
			Priority: req.Priority,
		},
	}

	// Convert resource requirements
	if req.Resources != nil {
		cloudTask.Spec.Resources = convertResourceRequirements(req.Resources)
	}

	// Create in Kubernetes
	if err := h.kubeClient.Create(ctx, cloudTask); err != nil {
		if errors.IsAlreadyExists(err) {
			h.writeError(w, http.StatusConflict, "Task with this name already exists", nil)
			return
		}
		h.log.Errorf("Failed to create CloudTask: %v", err)
		h.writeError(w, http.StatusInternalServerError, "Failed to create task", err)
		return
	}

	h.log.Infof("Created CloudTask %s/%s for tenant %s", namespace, taskID, tenantID)

	// Return created task
	response := convertCloudTaskToResponse(cloudTask)
	h.writeJSON(w, http.StatusCreated, response)
}

// ListTasks handles GET /v1/tasks
// Lists all CloudTasks for the tenant
func (h *TaskHandler) ListTasks(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Extract tenant ID from JWT context
	tenantID, err := auth.GetTenantIDFromContext(ctx)
	if err != nil {
		h.writeError(w, http.StatusUnauthorized, "Tenant ID not found in token", err)
		return
	}

	// Parse query parameters
	status := r.URL.Query().Get("status")
	if status != "" {
		if err := validators.ValidateStatus(status); err != nil {
			h.writeError(w, http.StatusBadRequest, "Invalid status parameter", err)
			return
		}
	}

	limit := 50 // default
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			if l > 500 {
				limit = 500 // max limit
			} else {
				limit = l
			}
		}
	}

	offset := 0
	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		if o, err := strconv.Atoi(offsetStr); err == nil && o >= 0 {
			offset = o
		}
	}

	// List CloudTasks in tenant namespace
	namespace := fmt.Sprintf("tenant-%s", tenantID)
	var taskList v1.CloudTaskList
	listOpts := []client.ListOption{
		client.InNamespace(namespace),
		client.Limit(int64(limit + offset)), // Fetch more to apply offset
	}

	if err := h.kubeClient.List(ctx, &taskList, listOpts...); err != nil {
		h.log.Errorf("Failed to list CloudTasks: %v", err)
		h.writeError(w, http.StatusInternalServerError, "Failed to list tasks", err)
		return
	}

	// Filter by status if provided
	tasks := taskList.Items
	if status != "" {
		filtered := []v1.CloudTask{}
		for _, task := range tasks {
			if string(task.Status.Phase) == status {
				filtered = append(filtered, task)
			}
		}
		tasks = filtered
	}

	// Apply pagination
	total := len(tasks)
	if offset > total {
		tasks = []v1.CloudTask{}
	} else if offset+limit > total {
		tasks = tasks[offset:]
	} else {
		tasks = tasks[offset : offset+limit]
	}

	// Convert to response format
	responses := make([]api.CloudTaskResponse, len(tasks))
	for i, task := range tasks {
		responses[i] = convertCloudTaskToResponse(&task)
	}

	response := map[string]interface{}{
		"items":  responses,
		"count":  len(responses),
		"total":  total,
		"limit":  limit,
		"offset": offset,
	}

	h.writeJSON(w, http.StatusOK, response)
}

// GetTask handles GET /v1/tasks/:id
// Retrieves a single CloudTask by ID
func (h *TaskHandler) GetTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Extract tenant ID from JWT context
	tenantID, err := auth.GetTenantIDFromContext(ctx)
	if err != nil {
		h.writeError(w, http.StatusUnauthorized, "Tenant ID not found in token", err)
		return
	}

	// Extract task ID from URL path
	taskID := strings.TrimPrefix(r.URL.Path, "/v1/tasks/")
	taskID = strings.Split(taskID, "/")[0] // Handle paths like /v1/tasks/:id/logs

	if err := validators.ValidateTaskID(taskID); err != nil {
		h.writeError(w, http.StatusBadRequest, "Invalid task ID", err)
		return
	}

	// Get CloudTask from tenant namespace
	namespace := fmt.Sprintf("tenant-%s", tenantID)
	var cloudTask v1.CloudTask

	if err := h.kubeClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: taskID}, &cloudTask); err != nil {
		if errors.IsNotFound(err) {
			h.writeError(w, http.StatusNotFound, "Task not found", nil)
			return
		}
		h.log.Errorf("Failed to get CloudTask: %v", err)
		h.writeError(w, http.StatusInternalServerError, "Failed to get task", err)
		return
	}

	// Verify tenant scoping
	if cloudTask.Spec.TenantID != tenantID {
		h.writeError(w, http.StatusForbidden, "Cannot access tasks from other tenants", nil)
		return
	}

	response := convertCloudTaskToResponse(&cloudTask)
	h.writeJSON(w, http.StatusOK, response)
}

// DeleteTask handles DELETE /v1/tasks/:id
// Deletes a CloudTask by ID
func (h *TaskHandler) DeleteTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Extract tenant ID from JWT context
	tenantID, err := auth.GetTenantIDFromContext(ctx)
	if err != nil {
		h.writeError(w, http.StatusUnauthorized, "Tenant ID not found in token", err)
		return
	}

	// Extract task ID from URL path
	taskID := strings.TrimPrefix(r.URL.Path, "/v1/tasks/")
	taskID = strings.Split(taskID, "/")[0]

	if err := validators.ValidateTaskID(taskID); err != nil {
		h.writeError(w, http.StatusBadRequest, "Invalid task ID", err)
		return
	}

	// Get task first to verify ownership
	namespace := fmt.Sprintf("tenant-%s", tenantID)
	var cloudTask v1.CloudTask

	if err := h.kubeClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: taskID}, &cloudTask); err != nil {
		if errors.IsNotFound(err) {
			h.writeError(w, http.StatusNotFound, "Task not found", nil)
			return
		}
		h.log.Errorf("Failed to get CloudTask for deletion: %v", err)
		h.writeError(w, http.StatusInternalServerError, "Failed to delete task", err)
		return
	}

	// Verify tenant scoping
	if cloudTask.Spec.TenantID != tenantID {
		h.writeError(w, http.StatusForbidden, "Cannot delete tasks from other tenants", nil)
		return
	}

	// Delete the CloudTask
	if err := h.kubeClient.Delete(ctx, &cloudTask); err != nil {
		if errors.IsNotFound(err) {
			// Already deleted, return success
			h.log.Infof("Task %s/%s already deleted", namespace, taskID)
		} else {
			h.log.Errorf("Failed to delete CloudTask: %v", err)
			h.writeError(w, http.StatusInternalServerError, "Failed to delete task", err)
			return
		}
	}

	h.log.Infof("Deleted CloudTask %s/%s for tenant %s", namespace, taskID, tenantID)
	w.WriteHeader(http.StatusNoContent)
}

// GetTaskLogs handles GET /v1/tasks/:id/logs
// Retrieves logs from the task pod
func (h *TaskHandler) GetTaskLogs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Extract tenant ID from JWT context
	tenantID, err := auth.GetTenantIDFromContext(ctx)
	if err != nil {
		h.writeError(w, http.StatusUnauthorized, "Tenant ID not found in token", err)
		return
	}

	// Extract task ID from URL path
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/tasks/"), "/")
	if len(parts) < 2 {
		h.writeError(w, http.StatusBadRequest, "Invalid request path", nil)
		return
	}
	taskID := parts[0]

	if err := validators.ValidateTaskID(taskID); err != nil {
		h.writeError(w, http.StatusBadRequest, "Invalid task ID", err)
		return
	}

	// Get CloudTask to find associated pod
	namespace := fmt.Sprintf("tenant-%s", tenantID)
	var cloudTask v1.CloudTask

	if err := h.kubeClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: taskID}, &cloudTask); err != nil {
		if errors.IsNotFound(err) {
			h.writeError(w, http.StatusNotFound, "Task not found", nil)
			return
		}
		h.log.Errorf("Failed to get CloudTask: %v", err)
		h.writeError(w, http.StatusInternalServerError, "Failed to get task logs", err)
		return
	}

	// Verify tenant scoping
	if cloudTask.Spec.TenantID != tenantID {
		h.writeError(w, http.StatusForbidden, "Cannot access logs from other tenant tasks", nil)
		return
	}

	if cloudTask.Status.PodName == "" {
		response := map[string]interface{}{
			"taskID": taskID,
			"logs":   "Task pod not yet created",
			"status": "Not Started",
		}
		h.writeJSON(w, http.StatusOK, response)
		return
	}

	// Get pod logs
	podLogs, err := h.getPodLogs(ctx, namespace, cloudTask.Status.PodName)
	if err != nil {
		h.log.Warnf("Failed to fetch pod logs: %v", err)
		response := map[string]interface{}{
			"taskID": taskID,
			"logs":   fmt.Sprintf("Unable to fetch logs: %v", err),
			"status": string(cloudTask.Status.Phase),
		}
		h.writeJSON(w, http.StatusOK, response)
		return
	}

	response := map[string]interface{}{
		"taskID": taskID,
		"logs":   podLogs,
		"status": string(cloudTask.Status.Phase),
		"podName": cloudTask.Status.PodName,
	}

	h.writeJSON(w, http.StatusOK, response)
}

// Helper methods

// getPodLogs retrieves logs from a pod
// In a real implementation, this would use the Kubernetes API to stream logs
func (h *TaskHandler) getPodLogs(ctx context.Context, namespace, podName string) (string, error) {
	// TODO: Implement actual pod log fetching using k8s client
	// For now, return a placeholder
	return "Pod logs would be streamed here", nil
}

// convertCloudTaskToResponse converts a CloudTask to API response format
func convertCloudTaskToResponse(task *v1.CloudTask) api.CloudTaskResponse {
	resp := api.CloudTaskResponse{
		Name:       task.Name,
		Namespace:  task.Namespace,
		Phase:      string(task.Status.Phase),
		PodName:    task.Status.PodName,
		Message:    task.Status.Message,
		RetryCount: task.Status.RetryCount,
		CreatedAt:  task.CreationTimestamp.String(),
	}

	if task.Status.CompletionTime != nil {
		resp.CompletedAt = task.Status.CompletionTime.String()
	}

	resp.Spec = api.CreateCloudTaskRequest{
		Name:      task.Name,
		Image:     task.Spec.Image,
		Command:   task.Spec.Command,
		Args:      task.Spec.Args,
		TenantID:  task.Spec.TenantID,
		Retries:   task.Spec.Retries,
		Timeout:   task.Spec.Timeout,
		Priority:  task.Spec.Priority,
	}

	if task.Spec.Resources != nil {
		resp.Spec.Resources = convertResourceRequirements(task.Spec.Resources)
	}

	return resp
}

// convertResourceRequirements converts from API format to Kubernetes format
func convertResourceRequirements(req *api.ResourceRequirementsReq) *v1.ResourceRequirements {
	result := &v1.ResourceRequirements{}

	if req.Requests != nil {
		result.Requests = &v1.ResourceList{
			CPU:    req.Requests.CPU,
			Memory: req.Requests.Memory,
		}
	}

	if req.Limits != nil {
		result.Limits = &v1.ResourceList{
			CPU:    req.Limits.CPU,
			Memory: req.Limits.Memory,
		}
	}

	return result
}

// Response helpers

func (h *TaskHandler) writeJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		h.log.Errorf("Failed to encode response: %v", err)
	}
}

func (h *TaskHandler) writeError(w http.ResponseWriter, statusCode int, message string, err error) {
	errMsg := message
	if err != nil {
		errMsg = fmt.Sprintf("%s: %v", message, err)
		h.log.Warnf("API Error: %s", errMsg)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error":  message,
		"status": statusCode,
		"detail": err.Error(),
	})
}

func (h *TaskHandler) writeValidationErrors(w http.ResponseWriter, statusCode int, errors validators.ValidationErrors) {
	errorDetails := make([]map[string]string, len(errors))
	for i, e := range errors {
		errorDetails[i] = map[string]string{
			"field":   e.Field,
			"message": e.Message,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error":  "Validation failed",
		"status": statusCode,
		"fields": errorDetails,
	})
}

// randString generates a random string of length n
func randString(n int) string {
	const letters = "abcdef0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[i%len(letters)]
	}
	return string(b)
}
