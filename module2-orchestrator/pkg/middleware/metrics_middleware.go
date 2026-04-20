package middleware

import (
	"net/http"
	"strconv"
	"time"

	"github.com/orchestrator/module2-orchestrator/pkg/metrics"
)

// MetricsMiddleware wraps HTTP handlers to record metrics for the API gateway
type MetricsMiddleware struct {
	metrics *metrics.GatewayMetrics
}

// NewMetricsMiddleware creates a new metrics middleware
func NewMetricsMiddleware(m *metrics.GatewayMetrics) *MetricsMiddleware {
	return &MetricsMiddleware{
		metrics: m,
	}
}

// Handler wraps an HTTP handler to record metrics
func (mm *MetricsMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Don't record metrics endpoint itself
		if r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}

		// Track connection
		mm.metrics.IncActiveConnections()
		defer mm.metrics.DecActiveConnections()

		// Capture response writer status
		wrapped := &responseWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
		}

		// Record request duration
		start := time.Now()
		next.ServeHTTP(wrapped, r)
		duration := time.Since(start).Seconds()

		// Record metrics
		mm.metrics.RecordHTTPRequest(
			r.Method,
			normalizeEndpoint(r.URL.Path),
			wrapped.statusCode,
			duration,
		)
	})
}

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

// WriteHeader captures the status code
func (rw *responseWriter) WriteHeader(code int) {
	if !rw.written {
		rw.statusCode = code
		rw.written = true
		rw.ResponseWriter.WriteHeader(code)
	}
}

// Write ensures WriteHeader is called
func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.written {
		rw.WriteHeader(http.StatusOK)
	}
	return rw.ResponseWriter.Write(b)
}

// normalizeEndpoint normalizes URL paths for metrics
// Groups similar paths together to avoid high cardinality
func normalizeEndpoint(path string) string {
	// Map specific patterns
	switch {
	case path == "/healthz":
		return "/healthz"
	case path == "/readyz":
		return "/readyz"
	case path == "/metrics":
		return "/metrics"
	case len(path) > 3 && path[:3] == "/v1":
		// Group v1 API endpoints
		if len(path) > 10 {
			return path[:10]
		}
		return path
	default:
		return path
	}
}

// ChainMiddleware chains multiple middlewares
func ChainMiddleware(handler http.Handler, middlewares ...func(http.Handler) http.Handler) http.Handler {
	for _, middleware := range middlewares {
		handler = middleware(handler)
	}
	return handler
}

// RecoveryMiddleware recovers from panics
func RecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				// Log panic
				// Send error response
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte("Internal Server Error"))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// LoggingMiddleware logs HTTP requests
func LoggingMiddleware(log interface{ Printf(string, ...interface{}) }) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			wrapped := &responseWriter{
				ResponseWriter: w,
				statusCode:     http.StatusOK,
			}

			next.ServeHTTP(wrapped, r)

			duration := time.Since(start)
			log.Printf("%s %s %s %d %v",
				r.RemoteAddr,
				r.Method,
				r.RequestURI,
				wrapped.statusCode,
				duration,
			)
		})
	}
}

// Example usage in main.go:
//
// func setupRouter(m *metrics.GatewayMetrics) http.Handler {
//     mux := http.NewServeMux()
//
//     // Register handlers
//     mux.HandleFunc("/v1/tasks", handlers.ListTasks)
//     mux.HandleFunc("/v1/tasks/create", handlers.CreateTask)
//     mux.HandleFunc("/healthz", handlers.Health)
//     mux.HandleFunc("/readyz", handlers.Ready)
//
//     // Expose metrics endpoint
//     mux.Handle("/metrics", promhttp.Handler())
//
//     // Apply middleware
//     metricsMiddleware := NewMetricsMiddleware(m)
//     handler := ChainMiddleware(
//         mux,
//         metricsMiddleware.Handler,
//         RecoveryMiddleware,
//         LoggingMiddleware(log).Handler,
//     )
//
//     return handler
// }
//
// func main() {
//     m := metrics.NewGatewayMetrics()
//     handler := setupRouter(m)
//
//     if err := http.ListenAndServe(":8080", handler); err != nil {
//         panic(err)
//     }
// }
