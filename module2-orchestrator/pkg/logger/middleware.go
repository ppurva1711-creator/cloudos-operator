package logger

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// ResponseWriter wraps the standard ResponseWriter to capture status code
type ResponseWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

// WriteHeader captures the status code
func (rw *ResponseWriter) WriteHeader(statusCode int) {
	if !rw.written {
		rw.statusCode = statusCode
		rw.written = true
		rw.ResponseWriter.WriteHeader(statusCode)
	}
}

// Write ensures WriteHeader is called if not already
func (rw *ResponseWriter) Write(b []byte) (int, error) {
	if !rw.written {
		rw.statusCode = http.StatusOK
		rw.written = true
	}
	return rw.ResponseWriter.Write(b)
}

// RequestLoggingMiddleware logs every HTTP request with comprehensive details
func RequestLoggingMiddleware(logger *Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Generate or extract request ID
			requestID := r.Header.Get("X-Request-ID")
			if requestID == "" {
				requestID = uuid.New().String()
			}

			// Extract trace ID if present
			traceID := r.Header.Get("X-Trace-ID")
			if traceID == "" {
				traceID = uuid.New().String()
			}

			// Extract tenant ID from JWT claims or header
			tenantID := r.Header.Get("X-Tenant-ID")
			if tenantID == "" {
				// Try to extract from JWT context if available
				if claims, ok := r.Context().Value("jwt_claims").(map[string]interface{}); ok {
					if tid, ok := claims["tenant_id"].(string); ok {
						tenantID = tid
					}
				}
			}

			// Create context with request metadata
			ctx := r.Context()
			ctx = WithRequestID(ctx, requestID)
			ctx = WithTraceID(ctx, traceID)
			if tenantID != "" {
				ctx = WithTenantID(ctx, tenantID)
			}

			// Wrap response writer to capture status code
			wrappedWriter := &ResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}

			// Logger for this request
			requestLogger := GetLogger(ctx)
			defer requestLogger.Sync()

			// Extract client IP
			clientIP := getClientIP(r)
			userAgent := r.Header.Get("User-Agent")

			// Log request start
			requestLogger.InfoWithContext(ctx, "HTTP request started",
				"method", r.Method,
				"path", r.RequestURI,
				"client_ip", clientIP,
				"user_agent", userAgent,
			)

			// Call the next handler
			next.ServeHTTP(wrappedWriter, r.WithContext(ctx))

			// Calculate duration
			duration := time.Since(start)
			durationMs := float64(duration.Milliseconds())

			// Determine log level based on status code
			var logLevel zapcore.Level
			switch {
			case wrappedWriter.statusCode >= 500:
				logLevel = zapcore.ErrorLevel
			case wrappedWriter.statusCode >= 400:
				logLevel = zapcore.WarnLevel
			default:
				logLevel = zapcore.InfoLevel
			}

			// Log request end
			requestLogger.LogWithContext(ctx, logLevel, "HTTP request completed",
				"method", r.Method,
				"path", r.RequestURI,
				"status", wrappedWriter.statusCode,
				"duration_ms", durationMs,
				"client_ip", clientIP,
				"user_agent", userAgent,
			)

			// Add request ID and trace ID to response headers
			w.Header().Set("X-Request-ID", requestID)
			w.Header().Set("X-Trace-ID", traceID)
		})
	}
}

// getClientIP extracts the client IP from the request
func getClientIP(r *http.Request) string {
	// Check X-Forwarded-For header (set by reverse proxies/ingress)
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		// X-Forwarded-For can be comma-separated list
		ips := net.SplitHostPort(forwarded, -1)
		if len(ips) > 0 {
			return ips[0]
		}
	}

	// Check X-Real-IP header
	if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
		return realIP
	}

	// Fallback to RemoteAddr
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// RequestIDFromContext extracts request ID from context
func RequestIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(ContextKeyRequestID).(string); ok {
		return id
	}
	return ""
}

// TenantIDFromContext extracts tenant ID from context
func TenantIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(ContextKeyTenantID).(string); ok {
		return id
	}
	return ""
}

// TraceIDFromContext extracts trace ID from context
func TraceIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(ContextKeyTraceID).(string); ok {
		return id
	}
	return ""
}

// ContextWithRequestMetadata adds logging metadata to a context
func ContextWithRequestMetadata(ctx context.Context, requestID, tenantID, traceID string) context.Context {
	if requestID != "" {
		ctx = WithRequestID(ctx, requestID)
	}
	if tenantID != "" {
		ctx = WithTenantID(ctx, tenantID)
	}
	if traceID != "" {
		ctx = WithTraceID(ctx, traceID)
	}
	return ctx
}
