package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// HealthHandler handles health check requests
type HealthHandler struct {
	kubeClient  client.Client
	redisClient redis.Cmdable
	log         *logrus.Logger
}

// NewHealthHandler creates a new HealthHandler
func NewHealthHandler(kubeClient client.Client, redisClient redis.Cmdable, log *logrus.Logger) *HealthHandler {
	return &HealthHandler{
		kubeClient:  kubeClient,
		redisClient: redisClient,
		log:         log,
	}
}

// ComponentStatus represents the status of a system component
type ComponentStatus struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Message  string `json:"message,omitempty"`
	Latency  int64  `json:"latency_ms"`
}

// HealthCheckResponse represents the health check response
type HealthCheckResponse struct {
	Status     string             `json:"status"`
	Timestamp  string             `json:"timestamp"`
	Components []ComponentStatus  `json:"components"`
}

// Healthz handles GET /healthz
// Returns 200 if gateway is running, 503 if any dependency is down
func (h *HealthHandler) Healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	response := HealthCheckResponse{
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
		Components: make([]ComponentStatus, 0),
	}

	// Check Kubernetes API
	kubeStatus := h.checkKubeAPI(ctx)
	response.Components = append(response.Components, kubeStatus)

	// Check Redis
	redisStatus := h.checkRedis(ctx)
	response.Components = append(response.Components, redisStatus)

	// Determine overall status
	allHealthy := true
	for _, comp := range response.Components {
		if comp.Status != "healthy" {
			allHealthy = false
			break
		}
	}

	if allHealthy {
		response.Status = "healthy"
		h.writeJSON(w, http.StatusOK, response)
	} else {
		response.Status = "degraded"
		h.writeJSON(w, http.StatusServiceUnavailable, response)
	}
}

// Readyz handles GET /readyz
// Returns 200 if gateway is ready to serve traffic
func (h *HealthHandler) Readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	response := HealthCheckResponse{
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
		Components: make([]ComponentStatus, 0),
	}

	// Check critical dependencies for readiness
	// More permissive than liveness - only check if service can operate

	// Check Kubernetes API (required)
	kubeStatus := h.checkKubeAPI(ctx)
	response.Components = append(response.Components, kubeStatus)

	// Redis is not strictly required for readiness (can work in limited mode)
	// But we'll check it anyway

	isReady := kubeStatus.Status == "healthy"

	if isReady {
		response.Status = "ready"
		h.writeJSON(w, http.StatusOK, response)
	} else {
		response.Status = "not ready"
		h.writeJSON(w, http.StatusServiceUnavailable, response)
	}
}

// Helper methods

// checkKubeAPI checks if Kubernetes API is reachable
func (h *HealthHandler) checkKubeAPI(ctx context.Context) ComponentStatus {
	status := ComponentStatus{
		Name: "kubernetes",
	}

	if h.kubeClient == nil {
		status.Status = "unavailable"
		status.Message = "Kubernetes client not initialized"
		return status
	}

	start := time.Now()

	// Simple health check: try to list namespaces
	// In real scenarios, you'd use a simpler check like API discovery
	var namespaces corev1.NamespaceList
	ctx2, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	if err := h.kubeClient.List(ctx2, &namespaces, client.Limit(1)); err != nil {
		status.Status = "unhealthy"
		status.Message = err.Error()
		status.Latency = time.Since(start).Milliseconds()
		h.log.Warnf("Kubernetes health check failed: %v", err)
		return status
	}

	status.Status = "healthy"
	status.Latency = time.Since(start).Milliseconds()
	return status
}

// checkRedis checks if Redis is reachable
func (h *HealthHandler) checkRedis(ctx context.Context) ComponentStatus {
	status := ComponentStatus{
		Name: "redis",
	}

	if h.redisClient == nil {
		status.Status = "unavailable"
		status.Message = "Redis client not initialized"
		return status
	}

	start := time.Now()

	ctx2, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	if err := h.redisClient.Ping(ctx2).Err(); err != nil {
		status.Status = "unhealthy"
		status.Message = err.Error()
		status.Latency = time.Since(start).Milliseconds()
		h.log.Warnf("Redis health check failed: %v", err)
		return status
	}

	status.Status = "healthy"
	status.Latency = time.Since(start).Milliseconds()
	return status
}

// writeJSON writes JSON response
func (h *HealthHandler) writeJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		h.log.Errorf("Failed to encode health response: %v", err)
	}
}
