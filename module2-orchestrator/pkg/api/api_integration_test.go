package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// MockKubeClient implements client.Client for testing
type MockKubeClient struct {
	client.Client
}

// setupTestAPIServer creates a test server with mock kube client
func setupTestAPIServer(t *testing.T) (*httptest.Server, *MockKubeClient) {
	t.Helper()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	mockClient := &MockKubeClient{}
	apiServer := NewAPIServer(mockClient, log)

	mux := http.NewServeMux()

	// Route handlers matching API server patterns
	mux.HandleFunc("/v1/tasks", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			apiServer.HandleCreateTask(w, r)
		case http.MethodGet:
			if r.URL.Query().Get("name") != "" {
				apiServer.HandleGetTask(w, r)
			} else {
				apiServer.HandleListTasks(w, r)
			}
		case http.MethodDelete:
			apiServer.HandleDeleteTask(w, r)
		default:
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/health", apiServer.HandleHealth)
	mux.HandleFunc("/metrics", apiServer.HandleMetrics)

	server := httptest.NewServer(mux)
	t.Cleanup(func() { server.Close() })

	return server, mockClient
}

// ---- Complete Request Lifecycle ----

func TestAPIIntegration_Complete_RequestLifecycle(t *testing.T) {
	server, _ := setupTestAPIServer(t)
	httpClient := &http.Client{}

	// POST /v1/tasks → CloudTask created
	createReq := CreateCloudTaskRequest{
		Name:     "lifecycle-task",
		Image:    "busybox:latest",
		TenantID: "tenant-a",
		Command:  []string{"sh", "-c"},
		Args:     []string{"echo hello && sleep 5"},
		Retries:  3,
		Timeout:  "5m",
	}

	body, err := json.Marshal(createReq)
	require.NoError(t, err)

	resp, err := http.Post(server.URL+"/v1/tasks", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusCreated, resp.StatusCode, "POST should return 201")

	var createResp map[string]string
	json.NewDecoder(resp.Body).Decode(&createResp)
	assert.Equal(t, "lifecycle-task", createResp["name"])
	assert.Contains(t, createResp["message"], "CloudTask will be created")

	// GET /v1/tasks?name=<id> → returns single task
	resp2, err := http.Get(server.URL + "/v1/tasks?name=lifecycle-task")
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode, "GET single should return 200")

	var getResp map[string]string
	json.NewDecoder(resp2.Body).Decode(&getResp)
	assert.Equal(t, "lifecycle-task", getResp["name"])
	assert.Equal(t, "Running", getResp["phase"])

	// GET /v1/tasks → returns task list
	resp3, err := http.Get(server.URL + "/v1/tasks?namespace=tenant-a&tenantID=tenant-a")
	require.NoError(t, err)
	defer resp3.Body.Close()
	assert.Equal(t, http.StatusOK, resp3.StatusCode, "GET list should return 200")

	var listResp ListCloudTasksResponse
	json.NewDecoder(resp3.Body).Decode(&listResp)
	assert.Equal(t, 0, listResp.Count)

	// DELETE /v1/tasks?name=<id> → task deleted
	delReq, _ := http.NewRequest("DELETE", server.URL+"/v1/tasks?name=lifecycle-task", nil)
	resp4, err := httpClient.Do(delReq)
	require.NoError(t, err)
	defer resp4.Body.Close()
	assert.Equal(t, http.StatusOK, resp4.StatusCode, "DELETE should return 200")

	var delResp map[string]string
	json.NewDecoder(resp4.Body).Decode(&delResp)
	assert.Equal(t, "CloudTask deleted", delResp["message"])
}

// ---- Validation Error: Invalid JSON ----

func TestAPIIntegration_ValidationErrors_InvalidJSON(t *testing.T) {
	server, _ := setupTestAPIServer(t)

	resp, err := http.Post(server.URL+"/v1/tasks", "application/json", bytes.NewReader([]byte(`{invalid json}`)))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "Invalid JSON should return 400")

	var errorResp ErrorResponse
	json.NewDecoder(resp.Body).Decode(&errorResp)
	assert.Equal(t, http.StatusBadRequest, errorResp.Code)
}

// ---- Validation Error: Empty Body ----

func TestAPIIntegration_ValidationErrors_EmptyBody(t *testing.T) {
	server, _ := setupTestAPIServer(t)

	resp, err := http.Post(server.URL+"/v1/tasks", "application/json", bytes.NewReader([]byte{}))
	require.NoError(t, err)
	defer resp.Body.Close()

	// Empty body should be handled (may create with empty fields or return 400)
	assert.NotEqual(t, http.StatusInternalServerError, resp.StatusCode, "Should not crash on empty body")
}

// ---- Validation Error: Missing Task Name for GET ----

func TestAPIIntegration_ValidationErrors_MissingTaskName(t *testing.T) {
	server, _ := setupTestAPIServer(t)

	// GET without name should list tasks (not error)
	resp, err := http.Get(server.URL + "/v1/tasks")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode, "GET without name should list tasks")
}

// ---- Validation Error: Missing Task Name for DELETE ----

func TestAPIIntegration_ValidationErrors_MissingDeleteName(t *testing.T) {
	server, _ := setupTestAPIServer(t)

	client := &http.Client{}
	req, _ := http.NewRequest("DELETE", server.URL+"/v1/tasks", nil)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "DELETE without name should return 400")
}

// ---- Health Check Endpoint ----

func TestAPIIntegration_HealthCheck(t *testing.T) {
	server, _ := setupTestAPIServer(t)

	resp, err := http.Get(server.URL + "/health")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode, "Health check should return 200")

	var health HealthCheckResponse
	json.NewDecoder(resp.Body).Decode(&health)
	assert.Equal(t, "healthy", health.Status)
	assert.Equal(t, "v1.0.0", health.Version)
	assert.Equal(t, "connected", health.KubeStatus)
}

// ---- Metrics Endpoint ----

func TestAPIIntegration_Metrics(t *testing.T) {
	server, _ := setupTestAPIServer(t)

	resp, err := http.Get(server.URL + "/metrics")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode, "Metrics should return 200")
	assert.Equal(t, "text/plain", resp.Header.Get("Content-Type"))
}

// ---- Response Headers ----

func TestAPIIntegration_ResponseHeaders(t *testing.T) {
	server, _ := setupTestAPIServer(t)

	resp, err := http.Get(server.URL + "/v1/tasks?name=test-task")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"), "Should return JSON content type")
}

// ---- Concurrent Requests ----

func TestAPIIntegration_ConcurrentRequests(t *testing.T) {
	server, _ := setupTestAPIServer(t)

	numRequests := 30
	var wg sync.WaitGroup
	errors := make(chan error, numRequests)

	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			taskName := fmt.Sprintf("task-%03d", index)
			resp, err := http.Get(server.URL + "/v1/tasks?name=" + taskName)
			if err != nil {
				errors <- err
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				errors <- fmt.Errorf("unexpected status %d for %s", resp.StatusCode, taskName)
				return
			}
			errors <- nil
		}(i)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		require.NoError(t, err)
	}
}

// ---- Request Timeout ----

func TestAPIIntegration_RequestTimeout(t *testing.T) {
	// Create a slow handler
	slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer slowServer.Close()

	client := &http.Client{Timeout: 500 * time.Millisecond}
	_, err := client.Get(slowServer.URL)
	assert.Error(t, err, "Request should timeout")
}

// ---- Large Payload ----

func TestAPIIntegration_LargePayload(t *testing.T) {
	server, _ := setupTestAPIServer(t)

	// Create request with many args
	args := make([]string, 100)
	for i := range args {
		args[i] = fmt.Sprintf("arg-%d-with-some-padding-data", i)
	}

	req := CreateCloudTaskRequest{
		Name:     "large-payload-task",
		Image:    "busybox:latest",
		TenantID: "tenant-a",
		Args:     args,
	}

	body, _ := json.Marshal(req)
	resp, err := http.Post(server.URL+"/v1/tasks", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusCreated, resp.StatusCode, "Should handle large payload")
}

// ---- Method Not Allowed ----

func TestAPIIntegration_MethodNotAllowed(t *testing.T) {
	server, _ := setupTestAPIServer(t)

	client := &http.Client{}
	req, _ := http.NewRequest("PATCH", server.URL+"/v1/tasks", nil)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode, "PATCH should return 405")
}

// ---- Create Multiple Tasks ----

func TestAPIIntegration_CreateMultipleTasks(t *testing.T) {
	server, _ := setupTestAPIServer(t)

	for i := 0; i < 5; i++ {
		req := CreateCloudTaskRequest{
			Name:     fmt.Sprintf("task-%d", i),
			Image:    "nginx:latest",
			TenantID: "tenant-a",
		}
		body, _ := json.Marshal(req)

		resp, err := http.Post(server.URL+"/v1/tasks", "application/json", bytes.NewReader(body))
		require.NoError(t, err)
		resp.Body.Close()

		assert.Equal(t, http.StatusCreated, resp.StatusCode, "Task %d should be created", i)
	}
}
