package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/orchestrator/module2-orchestrator/api/v1"
	"github.com/orchestrator/module2-orchestrator/pkg/api"
	"github.com/orchestrator/module2-orchestrator/pkg/auth"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func setupTestHandler(t *testing.T) (*TaskHandler, *fake.ClientBuilder) {
	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	// Create fake Kubernetes client
	builder := fake.NewClientBuilder()
	kubeClient := builder.Build()

	handler := NewTaskHandler(kubeClient, log)
	return handler, builder
}

// Helper to add claims to request context
func addClaimsToContext(req *http.Request, tenantID, userID string) *http.Request {
	claims := &auth.Claims{
		TenantID: tenantID,
		UserID:   userID,
		Email:    "user@example.com",
		Roles:    []string{"admin"},
	}
	ctx := context.WithValue(req.Context(), auth.ClaimsContextKey, claims)
	return req.WithContext(ctx)
}

// Tests for CreateTask

func TestCreateTask_Valid(t *testing.T) {
	handler, _ := setupTestHandler(t)

	reqBody := api.CreateCloudTaskRequest{
		Name:     "test-task",
		Image:    "alpine:latest",
		Command:  []string{"echo"},
		Args:     []string{"hello"},
		TenantID: "tenant-1",
		Priority: 50,
		Retries:  3,
	}

	bodyBytes, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/tasks", bytes.NewReader(bodyBytes))
	req = addClaimsToContext(req, "tenant-1", "user-1")

	w := httptest.NewRecorder()
	handler.CreateTask(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var response api.CloudTaskResponse
	json.NewDecoder(w.Body).Decode(&response)
	assert.Equal(t, "test-task", response.Name)
	assert.Equal(t, "alpine:latest", response.Spec.Image)
}

func TestCreateTask_InvalidImage(t *testing.T) {
	handler, _ := setupTestHandler(t)

	reqBody := api.CreateCloudTaskRequest{
		Name:     "test-task",
		Image:    "", // Invalid: empty
		Command:  []string{"echo"},
		TenantID: "tenant-1",
	}

	bodyBytes, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/tasks", bytes.NewReader(bodyBytes))
	req = addClaimsToContext(req, "tenant-1", "user-1")

	w := httptest.NewRecorder()
	handler.CreateTask(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "Validation failed")
}

func TestCreateTask_EmptyCommand(t *testing.T) {
	handler, _ := setupTestHandler(t)

	reqBody := api.CreateCloudTaskRequest{
		Name:     "test-task",
		Image:    "alpine:latest",
		Command:  []string{}, // Invalid: empty
		TenantID: "tenant-1",
	}

	bodyBytes, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/tasks", bytes.NewReader(bodyBytes))
	req = addClaimsToContext(req, "tenant-1", "user-1")

	w := httptest.NewRecorder()
	handler.CreateTask(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "Validation failed")
}

func TestCreateTask_InvalidPriority(t *testing.T) {
	handler, _ := setupTestHandler(t)

	reqBody := api.CreateCloudTaskRequest{
		Name:     "test-task",
		Image:    "alpine:latest",
		Command:  []string{"echo"},
		TenantID: "tenant-1",
		Priority: 150, // Invalid: > 100
	}

	bodyBytes, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/tasks", bytes.NewReader(bodyBytes))
	req = addClaimsToContext(req, "tenant-1", "user-1")

	w := httptest.NewRecorder()
	handler.CreateTask(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateTask_MissingToken(t *testing.T) {
	handler, _ := setupTestHandler(t)

	reqBody := api.CreateCloudTaskRequest{
		Name:     "test-task",
		Image:    "alpine:latest",
		Command:  []string{"echo"},
		TenantID: "tenant-1",
	}

	bodyBytes, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/tasks", bytes.NewReader(bodyBytes))
	// No context claims added

	w := httptest.NewRecorder()
	handler.CreateTask(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestCreateTask_TenantMismatch(t *testing.T) {
	handler, _ := setupTestHandler(t)

	reqBody := api.CreateCloudTaskRequest{
		Name:     "test-task",
		Image:    "alpine:latest",
		Command:  []string{"echo"},
		TenantID: "tenant-2", // Different from token tenant
	}

	bodyBytes, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/tasks", bytes.NewReader(bodyBytes))
	req = addClaimsToContext(req, "tenant-1", "user-1") // Token has tenant-1

	w := httptest.NewRecorder()
	handler.CreateTask(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "Tenant ID mismatch")
}

func TestCreateTask_InvalidJSON(t *testing.T) {
	handler, _ := setupTestHandler(t)

	req := httptest.NewRequest("POST", "/v1/tasks", bytes.NewReader([]byte("invalid json")))
	req = addClaimsToContext(req, "tenant-1", "user-1")

	w := httptest.NewRecorder()
	handler.CreateTask(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "Invalid JSON")
}

// Tests for ListTasks

func TestListTasks_Valid(t *testing.T) {
	// Setup with a pre-existing task
	builder := fake.NewClientBuilder()
	task := &v1.CloudTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-1",
			Namespace: "tenant-tenant-1",
		},
		Spec: v1.CloudTaskSpec{
			Image:    "alpine:latest",
			TenantID: "tenant-1",
		},
		Status: v1.CloudTaskStatus{
			Phase: v1.PhasePending,
		},
	}
	kubeClient := builder.WithObjects(task).Build()
	log := logrus.New()
	handler := NewTaskHandler(kubeClient, log)

	req := httptest.NewRequest("GET", "/v1/tasks", nil)
	req = addClaimsToContext(req, "tenant-1", "user-1")

	w := httptest.NewRecorder()
	handler.ListTasks(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.NewDecoder(w.Body).Decode(&response)
	assert.Equal(t, float64(1), response["count"])
}

func TestListTasks_WithPagination(t *testing.T) {
	handler, _ := setupTestHandler(t)

	req := httptest.NewRequest("GET", "/v1/tasks?limit=10&offset=0", nil)
	req = addClaimsToContext(req, "tenant-1", "user-1")

	w := httptest.NewRecorder()
	handler.ListTasks(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.NewDecoder(w.Body).Decode(&response)
	assert.Equal(t, float64(10), response["limit"])
	assert.Equal(t, float64(0), response["offset"])
}

func TestListTasks_WithStatusFilter(t *testing.T) {
	handler, _ := setupTestHandler(t)

	req := httptest.NewRequest("GET", "/v1/tasks?status=Running", nil)
	req = addClaimsToContext(req, "tenant-1", "user-1")

	w := httptest.NewRecorder()
	handler.ListTasks(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestListTasks_InvalidStatus(t *testing.T) {
	handler, _ := setupTestHandler(t)

	req := httptest.NewRequest("GET", "/v1/tasks?status=InvalidStatus", nil)
	req = addClaimsToContext(req, "tenant-1", "user-1")

	w := httptest.NewRecorder()
	handler.ListTasks(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// Tests for GetTask

func TestGetTask_Valid(t *testing.T) {
	builder := fake.NewClientBuilder()
	task := &v1.CloudTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-1",
			Namespace: "tenant-tenant-1",
		},
		Spec: v1.CloudTaskSpec{
			Image:    "alpine:latest",
			TenantID: "tenant-1",
		},
	}
	kubeClient := builder.WithObjects(task).Build()
	log := logrus.New()
	handler := NewTaskHandler(kubeClient, log)

	req := httptest.NewRequest("GET", "/v1/tasks/task-1", nil)
	req = addClaimsToContext(req, "tenant-1", "user-1")

	w := httptest.NewRecorder()
	handler.GetTask(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response api.CloudTaskResponse
	json.NewDecoder(w.Body).Decode(&response)
	assert.Equal(t, "task-1", response.Name)
}

func TestGetTask_NotFound(t *testing.T) {
	handler, _ := setupTestHandler(t)

	req := httptest.NewRequest("GET", "/v1/tasks/nonexistent", nil)
	req = addClaimsToContext(req, "tenant-1", "user-1")

	w := httptest.NewRecorder()
	handler.GetTask(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetTask_TenantIsolation(t *testing.T) {
	// Create a task owned by a different tenant
	builder := fake.NewClientBuilder()
	task := &v1.CloudTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-1",
			Namespace: "tenant-tenant-2",
		},
		Spec: v1.CloudTaskSpec{
			Image:    "alpine:latest",
			TenantID: "tenant-2",
		},
	}
	kubeClient := builder.WithObjects(task).Build()
	log := logrus.New()
	handler := NewTaskHandler(kubeClient, log)

	// Try to access from different tenant
	req := httptest.NewRequest("GET", "/v1/tasks/task-1", nil)
	req = addClaimsToContext(req, "tenant-1", "user-1")

	w := httptest.NewRecorder()
	handler.GetTask(w, req)

	// Should not find task in wrong namespace
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// Tests for DeleteTask

func TestDeleteTask_Valid(t *testing.T) {
	builder := fake.NewClientBuilder()
	task := &v1.CloudTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-1",
			Namespace: "tenant-tenant-1",
		},
		Spec: v1.CloudTaskSpec{
			Image:    "alpine:latest",
			TenantID: "tenant-1",
		},
	}
	kubeClient := builder.WithObjects(task).Build()
	log := logrus.New()
	handler := NewTaskHandler(kubeClient, log)

	req := httptest.NewRequest("DELETE", "/v1/tasks/task-1", nil)
	req = addClaimsToContext(req, "tenant-1", "user-1")

	w := httptest.NewRecorder()
	handler.DeleteTask(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestDeleteTask_NotFound(t *testing.T) {
	handler, _ := setupTestHandler(t)

	req := httptest.NewRequest("DELETE", "/v1/tasks/nonexistent", nil)
	req = addClaimsToContext(req, "tenant-1", "user-1")

	w := httptest.NewRecorder()
	handler.DeleteTask(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestDeleteTask_TenantIsolation(t *testing.T) {
	builder := fake.NewClientBuilder()
	task := &v1.CloudTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-1",
			Namespace: "tenant-tenant-2",
		},
		Spec: v1.CloudTaskSpec{
			Image:    "alpine:latest",
			TenantID: "tenant-2",
		},
	}
	kubeClient := builder.WithObjects(task).Build()
	log := logrus.New()
	handler := NewTaskHandler(kubeClient, log)

	req := httptest.NewRequest("DELETE", "/v1/tasks/task-1", nil)
	req = addClaimsToContext(req, "tenant-1", "user-1")

	w := httptest.NewRecorder()
	handler.DeleteTask(w, req)

	// Should not find task in wrong namespace
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// Tests for GetTaskLogs

func TestGetTaskLogs_Valid(t *testing.T) {
	builder := fake.NewClientBuilder()
	task := &v1.CloudTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-1",
			Namespace: "tenant-tenant-1",
		},
		Spec: v1.CloudTaskSpec{
			Image:    "alpine:latest",
			TenantID: "tenant-1",
		},
		Status: v1.CloudTaskStatus{
			Phase:   v1.PhaseRunning,
			PodName: "task-1-pod",
		},
	}
	kubeClient := builder.WithObjects(task).Build()
	log := logrus.New()
	handler := NewTaskHandler(kubeClient, log)

	req := httptest.NewRequest("GET", "/v1/tasks/task-1/logs", nil)
	req = addClaimsToContext(req, "tenant-1", "user-1")

	w := httptest.NewRecorder()
	handler.GetTaskLogs(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.NewDecoder(w.Body).Decode(&response)
	assert.Equal(t, "task-1", response["taskID"])
}

func TestGetTaskLogs_TaskNotFound(t *testing.T) {
	handler, _ := setupTestHandler(t)

	req := httptest.NewRequest("GET", "/v1/tasks/nonexistent/logs", nil)
	req = addClaimsToContext(req, "tenant-1", "user-1")

	w := httptest.NewRecorder()
	handler.GetTaskLogs(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetTaskLogs_NoPodYet(t *testing.T) {
	builder := fake.NewClientBuilder()
	task := &v1.CloudTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-1",
			Namespace: "tenant-tenant-1",
		},
		Spec: v1.CloudTaskSpec{
			Image:    "alpine:latest",
			TenantID: "tenant-1",
		},
		Status: v1.CloudTaskStatus{
			Phase:   v1.PhasePending,
			PodName: "", // No pod yet
		},
	}
	kubeClient := builder.WithObjects(task).Build()
	log := logrus.New()
	handler := NewTaskHandler(kubeClient, log)

	req := httptest.NewRequest("GET", "/v1/tasks/task-1/logs", nil)
	req = addClaimsToContext(req, "tenant-1", "user-1")

	w := httptest.NewRecorder()
	handler.GetTaskLogs(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "not yet created")
}

func TestGetTaskLogs_TenantIsolation(t *testing.T) {
	builder := fake.NewClientBuilder()
	task := &v1.CloudTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-1",
			Namespace: "tenant-tenant-2",
		},
		Spec: v1.CloudTaskSpec{
			Image:    "alpine:latest",
			TenantID: "tenant-2",
		},
		Status: v1.CloudTaskStatus{
			Phase:   v1.PhaseRunning,
			PodName: "task-1-pod",
		},
	}
	kubeClient := builder.WithObjects(task).Build()
	log := logrus.New()
	handler := NewTaskHandler(kubeClient, log)

	req := httptest.NewRequest("GET", "/v1/tasks/task-1/logs", nil)
	req = addClaimsToContext(req, "tenant-1", "user-1")

	w := httptest.NewRecorder()
	handler.GetTaskLogs(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}
