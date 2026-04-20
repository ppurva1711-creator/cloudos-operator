package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/orchestrator/module2-orchestrator/pkg/api/handlers"
	"github.com/orchestrator/module2-orchestrator/pkg/auth"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Router sets up all API routes with middleware
type Router struct {
	taskHandler   *handlers.TaskHandler
	healthHandler *handlers.HealthHandler
	log           *logrus.Logger
	rateLimiter   *auth.RateLimiter
	jwtManager    *auth.JWTManager
}

// NewRouter creates a new Router
func NewRouter(kubeClient client.Client, redisClient redis.Cmdable, jwtManager *auth.JWTManager, 
	rateLimiter *auth.RateLimiter, log *logrus.Logger) *Router {
	return &Router{
		taskHandler:   handlers.NewTaskHandler(kubeClient, log),
		healthHandler: handlers.NewHealthHandler(kubeClient, redisClient, log),
		log:           log,
		rateLimiter:   rateLimiter,
		jwtManager:    jwtManager,
	}
}

// SetupRoutes configures all HTTP routes and middleware
func (r *Router) SetupRoutes(mux *http.ServeMux) {
	// Health check endpoints (no auth required)
	mux.HandleFunc("/healthz", r.chainMiddleware(
		r.healthHandler.Healthz,
		r.requestLoggingMiddleware,
		r.requestIDMiddleware,
	))

	mux.HandleFunc("/readyz", r.chainMiddleware(
		r.healthHandler.Readyz,
		r.requestLoggingMiddleware,
		r.requestIDMiddleware,
	))

	// V1 API endpoints (with auth and rate limiting)
	// POST /v1/tasks
	mux.HandleFunc("POST /v1/tasks", r.chainMiddleware(
		r.taskHandler.CreateTask,
		r.corsMiddleware,
		r.requestIDMiddleware,
		r.requestLoggingMiddleware,
		r.rateLimitMiddleware,
		r.authMiddleware,
	))

	// GET /v1/tasks
	mux.HandleFunc("GET /v1/tasks", r.chainMiddleware(
		r.taskHandler.ListTasks,
		r.corsMiddleware,
		r.requestIDMiddleware,
		r.requestLoggingMiddleware,
		r.rateLimitMiddleware,
		r.authMiddleware,
	))

	// GET /v1/tasks/:id
	mux.HandleFunc("GET /v1/tasks/{id}", r.chainMiddleware(
		r.taskHandler.GetTask,
		r.corsMiddleware,
		r.requestIDMiddleware,
		r.requestLoggingMiddleware,
		r.rateLimitMiddleware,
		r.authMiddleware,
	))

	// DELETE /v1/tasks/:id
	mux.HandleFunc("DELETE /v1/tasks/{id}", r.chainMiddleware(
		r.taskHandler.DeleteTask,
		r.corsMiddleware,
		r.requestIDMiddleware,
		r.requestLoggingMiddleware,
		r.rateLimitMiddleware,
		r.authMiddleware,
	))

	// GET /v1/tasks/:id/logs
	mux.HandleFunc("GET /v1/tasks/{id}/logs", r.chainMiddleware(
		r.taskHandler.GetTaskLogs,
		r.corsMiddleware,
		r.requestIDMiddleware,
		r.requestLoggingMiddleware,
		r.rateLimitMiddleware,
		r.authMiddleware,
	))
}

// Middleware functions

// chainMiddleware chains multiple middleware functions
// Middleware is applied in reverse order (last in list runs first)
func (r *Router) chainMiddleware(handler http.HandlerFunc, middlewares ...func(http.HandlerFunc) http.HandlerFunc) http.HandlerFunc {
	// Apply middlewares in reverse order
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}
	return handler
}

// requestIDMiddleware adds a unique request ID to context
func (r *Router) requestIDMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		requestID := fmt.Sprintf("%s-%d", req.Method, time.Now().UnixNano())
		ctx := context.WithValue(req.Context(), "request-id", requestID)
		w.Header().Set("X-Request-ID", requestID)
		next(w, req.WithContext(ctx))
	}
}

// requestLoggingMiddleware logs HTTP requests
func (r *Router) requestLoggingMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()
		requestID := req.Context().Value("request-id").(string)

		// Create a response writer wrapper to capture status
		wrapped := &responseWriterWrapper{ResponseWriter: w, statusCode: http.StatusOK}

		r.log.WithFields(logrus.Fields{
			"request_id": requestID,
			"method":     req.Method,
			"path":       req.RequestURI,
			"remote_addr": req.RemoteAddr,
		}).Infof("Request started")

		next(wrapped, req)

		duration := time.Since(start)
		r.log.WithFields(logrus.Fields{
			"request_id": requestID,
			"status":     wrapped.statusCode,
			"duration_ms": duration.Milliseconds(),
		}).Infof("Request completed")
	}
}

// corsMiddleware adds CORS headers
func (r *Router) corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, PATCH")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-ID")
		w.Header().Set("Access-Control-Max-Age", "3600")

		// Handle preflight requests
		if req.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next(w, req)
	}
}

// authMiddleware validates JWT token
func (r *Router) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		// Extract bearer token from Authorization header
		tokenString := extractBearerToken(req)

		if tokenString == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintf(w, `{"error":"Missing or invalid Authorization header","status":401}`)
			return
		}

		// Validate token
		claims, err := r.jwtManager.ValidateToken(tokenString)
		if err != nil {
			statusCode := http.StatusForbidden
			errorMsg := "Invalid token"

			switch err {
			case auth.ErrTokenExpired:
				errorMsg = "Token has expired"
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(statusCode)
			fmt.Fprintf(w, `{"error":"%s","status":%d}`, errorMsg, statusCode)
			return
		}

		// Inject claims into context
		ctx := context.WithValue(req.Context(), auth.ClaimsContextKey, claims)
		r.log.Debugf("User %s (tenant: %s) authenticated", claims.UserID, claims.TenantID)

		next(w, req.WithContext(ctx))
	}
}

// rateLimitMiddleware enforces per-tenant rate limiting
func (r *Router) rateLimitMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		// Skip rate limiting if not configured
		if r.rateLimiter == nil {
			next(w, req)
			return
		}

		// Extract tenant ID from context (set by auth middleware)
		tenantID, ok := req.Context().Value(auth.ClaimsContextKey).(*auth.Claims)
		if !ok {
			// No tenant context, skip rate limiting
			next(w, req)
			return
		}

		// Check rate limit
		allowed, retryAfter, err := r.rateLimiter.Allow(req.Context(), tenantID.TenantID)
		if err != nil {
			r.log.Errorf("Rate limit check failed: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, `{"error":"Internal server error","status":500}`)
			return
		}

		if !allowed {
			w.Header().Set("Retry-After", fmt.Sprintf("%.0f", retryAfter.Seconds()))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprintf(w, `{"error":"Rate limit exceeded","status":429}`)
			return
		}

		next(w, req)
	}
}

// Helper functions

// extractBearerToken extracts JWT from Authorization header
func extractBearerToken(req *http.Request) string {
	authHeader := req.Header.Get("Authorization")
	if authHeader == "" {
		return ""
	}

	const bearerScheme = "Bearer "
	if len(authHeader) > len(bearerScheme) && authHeader[:len(bearerScheme)] == bearerScheme {
		return authHeader[len(bearerScheme):]
	}

	return ""
}

// responseWriterWrapper wraps http.ResponseWriter to capture status code
type responseWriterWrapper struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

// WriteHeader captures the status code
func (w *responseWriterWrapper) WriteHeader(statusCode int) {
	if !w.written {
		w.statusCode = statusCode
		w.written = true
		w.ResponseWriter.WriteHeader(statusCode)
	}
}

// Write captures writes and updates status if not yet written
func (w *responseWriterWrapper) Write(b []byte) (int, error) {
	if !w.written {
		w.statusCode = http.StatusOK
		w.written = true
	}
	return w.ResponseWriter.Write(b)
}
