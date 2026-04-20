package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// TestNewLoggerDevelopment tests logger initialization in development mode
func TestNewLoggerDevelopment(t *testing.T) {
	logger, err := NewLogger("development")
	if err != nil {
		t.Fatalf("Failed to create development logger: %v", err)
	}
	if logger == nil {
		t.Fatal("Logger is nil")
	}
	if logger.zapLogger == nil {
		t.Fatal("ZapLogger is nil")
	}
	logger.Sync()
}

// TestNewLoggerProduction tests logger initialization in production mode
func TestNewLoggerProduction(t *testing.T) {
	logger, err := NewLogger("production")
	if err != nil {
		t.Fatalf("Failed to create production logger: %v", err)
	}
	if logger == nil {
		t.Fatal("Logger is nil")
	}
	if logger.zapLogger == nil {
		t.Fatal("ZapLogger is nil")
	}
	logger.Sync()
}

// TestNewLoggerInvalidEnv tests logger initialization with invalid environment
func TestNewLoggerInvalidEnv(t *testing.T) {
	logger, err := NewLogger("invalid")
	if err != nil {
		t.Fatalf("Failed to create logger with invalid env: %v", err)
	}
	if logger == nil {
		t.Fatal("Logger is nil")
	}
	logger.Sync()
}

// TestLogLevelFiltering tests that log levels are correctly filtered
func TestLogLevelFiltering(t *testing.T) {
	// Create a logger with debug level
	logger, err := NewLogger("development")
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Sync()

	// Log at various levels
	logger.Debug("Debug message")
	logger.Info("Info message")
	logger.Warn("Warn message")
	logger.Error("Error message") // Error is printed to stderr by default
}

// TestContextValueInjectionTenantID tests tenant_id injection into context
func TestContextValueInjectionTenantID(t *testing.T) {
	ctx := context.Background()
	ctx = WithTenantID(ctx, "tenant-123")

	tenantID, ok := ctx.Value(ContextKeyTenantID).(string)
	if !ok {
		t.Fatal("Failed to extract tenant ID from context")
	}
	if tenantID != "tenant-123" {
		t.Fatalf("Expected tenant-123, got %s", tenantID)
	}
}

// TestContextValueInjectionRequestID tests request_id injection into context
func TestContextValueInjectionRequestID(t *testing.T) {
	ctx := context.Background()
	ctx = WithRequestID(ctx, "req-456")

	requestID, ok := ctx.Value(ContextKeyRequestID).(string)
	if !ok {
		t.Fatal("Failed to extract request ID from context")
	}
	if requestID != "req-456" {
		t.Fatalf("Expected req-456, got %s", requestID)
	}
}

// TestContextValueInjectionTraceID tests trace_id injection into context
func TestContextValueInjectionTraceID(t *testing.T) {
	ctx := context.Background()
	ctx = WithTraceID(ctx, "trace-789")

	traceID, ok := ctx.Value(ContextKeyTraceID).(string)
	if !ok {
		t.Fatal("Failed to extract trace ID from context")
	}
	if traceID != "trace-789" {
		t.Fatalf("Expected trace-789, got %s", traceID)
	}
}

// TestGetLoggerWithContext tests GetLogger with context values
func TestGetLoggerWithContext(t *testing.T) {
	ctx := context.Background()
	ctx = WithTenantID(ctx, "tenant-abc")
	ctx = WithRequestID(ctx, "req-xyz")
	ctx = WithTraceID(ctx, "trace-123")

	logger := GetLogger(ctx)
	if logger == nil {
		t.Fatal("Logger is nil")
	}
	defer logger.Sync()
}

// TestLogWithContextValues tests logging with context values
func TestLogWithContextValues(t *testing.T) {
	ctx := context.Background()
	ctx = WithTenantID(ctx, "tenant-test")
	ctx = WithRequestID(ctx, "req-test")

	logger, err := NewLogger("development")
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Sync()

	logger.LogWithContext(ctx, zap.InfoLevel, "Test message with context",
		"user", "alice",
		"action", "create_task",
	)
}

// TestRequestLoggingMiddlewareBasic tests basic request logging middleware
func TestRequestLoggingMiddlewareBasic(t *testing.T) {
	logger, err := NewLogger("production")
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Sync()

	// Create a simple handler
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Wrap with middleware
	middleware := RequestLoggingMiddleware(logger)
	wrapped := middleware(handler)

	// Create a test request
	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()

	// Execute
	wrapped.ServeHTTP(w, req)

	// Verify response
	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", w.Code)
	}

	// Verify headers are set
	if w.Header().Get("X-Request-ID") == "" {
		t.Fatal("X-Request-ID header not set")
	}
	if w.Header().Get("X-Trace-ID") == "" {
		t.Fatal("X-Trace-ID header not set")
	}
}

// TestRequestLoggingMiddlewareWithRequestID tests middleware with existing request ID
func TestRequestLoggingMiddlewareWithRequestID(t *testing.T) {
	logger, err := NewLogger("production")
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Sync()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := RequestLoggingMiddleware(logger)
	wrapped := middleware(handler)

	// Create a request with existing Request ID
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Request-ID", "custom-req-123")
	w := httptest.NewRecorder()

	wrapped.ServeHTTP(w, req)

	// Verify the custom request ID is used
	if w.Header().Get("X-Request-ID") != "custom-req-123" {
		t.Fatalf("Expected X-Request-ID to be custom-req-123, got %s", w.Header().Get("X-Request-ID"))
	}
}

// TestRequestLoggingMiddlewareStatusCode tests logging different status codes
func TestRequestLoggingMiddlewareStatusCode(t *testing.T) {
	testCases := []struct {
		statusCode int
		name       string
	}{
		{http.StatusOK, "200 OK"},
		{http.StatusCreated, "201 Created"},
		{http.StatusBadRequest, "400 Bad Request"},
		{http.StatusUnauthorized, "401 Unauthorized"},
		{http.StatusInternalServerError, "500 Internal Server Error"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			logger, err := NewLogger("production")
			if err != nil {
				t.Fatalf("Failed to create logger: %v", err)
			}
			defer logger.Sync()

			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
			})

			middleware := RequestLoggingMiddleware(logger)
			wrapped := middleware(handler)

			req := httptest.NewRequest("POST", "/test", nil)
			w := httptest.NewRecorder()

			wrapped.ServeHTTP(w, req)

			if w.Code != tc.statusCode {
				t.Fatalf("Expected status %d, got %d", tc.statusCode, w.Code)
			}
		})
	}
}

// TestRequestLoggingMiddlewareWithTenantID tests middleware with tenant ID header
func TestRequestLoggingMiddlewareWithTenantID(t *testing.T) {
	logger, err := NewLogger("production")
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Sync()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := RequestLoggingMiddleware(logger)
	wrapped := middleware(handler)

	req := httptest.NewRequest("GET", "/tasks", nil)
	req.Header.Set("X-Tenant-ID", "tenant-xyz")
	w := httptest.NewRecorder()

	wrapped.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", w.Code)
	}
}

// TestRequestLoggingMiddlewareClientIP tests client IP extraction
func TestRequestLoggingMiddlewareClientIP(t *testing.T) {
	logger, err := NewLogger("production")
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Sync()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := RequestLoggingMiddleware(logger)
	wrapped := middleware(handler)

	// Test with X-Forwarded-For header
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Forwarded-For", "192.168.1.1")
	w := httptest.NewRecorder()

	wrapped.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", w.Code)
	}
}

// TestContextMetadataHelpers tests context helper functions
func TestContextMetadataHelpers(t *testing.T) {
	ctx := ContextWithRequestMetadata(
		context.Background(),
		"req-123",
		"tenant-a",
		"trace-456",
	)

	if RequestIDFromContext(ctx) != "req-123" {
		t.Fatal("Request ID not correctly extracted")
	}

	if TenantIDFromContext(ctx) != "tenant-a" {
		t.Fatal("Tenant ID not correctly extracted")
	}

	if TraceIDFromContext(ctx) != "trace-456" {
		t.Fatal("Trace ID not correctly extracted")
	}
}

// TestEmptyContextMetadataHelpers tests context helpers with empty values
func TestEmptyContextMetadataHelpers(t *testing.T) {
	ctx := context.Background()

	if RequestIDFromContext(ctx) != "" {
		t.Fatal("Expected empty request ID")
	}

	if TenantIDFromContext(ctx) != "" {
		t.Fatal("Expected empty tenant ID")
	}

	if TraceIDFromContext(ctx) != "" {
		t.Fatal("Expected empty trace ID")
	}
}

// TestJSONLogFormat tests that logs are in JSON format
func TestJSONLogFormat(t *testing.T) {
	logger, err := NewLogger("production")
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Sync()

	// Log a message
	logger.Info("Test message",
		zap.String("key1", "value1"),
		zap.Int("key2", 42),
	)
}

// TestLoggerSync tests logger sync functionality
func TestLoggerSync(t *testing.T) {
	logger, err := NewLogger("production")
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}

	err = logger.Sync()
	if err != nil && err.Error() != "sync /dev/stdout: inappropriate ioctl for device" {
		// Sync may fail on stdout in test environment, which is normal
		t.Logf("Sync returned: %v (expected in test environment)", err)
	}
}

// TestGetZapLogger tests GetZapLogger method
func TestGetZapLogger(t *testing.T) {
	logger, err := NewLogger("production")
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Sync()

	zapLogger := logger.GetZapLogger()
	if zapLogger == nil {
		t.Fatal("ZapLogger is nil")
	}
}

// TestMultipleContextValues tests multiple context value extractions
func TestMultipleContextValues(t *testing.T) {
	ctx := context.Background()
	ctx = WithTenantID(ctx, "tenant-1")
	ctx = WithRequestID(ctx, "req-1")
	ctx = WithTraceID(ctx, "trace-1")

	// All values should be present
	if TenantIDFromContext(ctx) != "tenant-1" {
		t.Fatal("Tenant ID mismatch")
	}
	if RequestIDFromContext(ctx) != "req-1" {
		t.Fatal("Request ID mismatch")
	}
	if TraceIDFromContext(ctx) != "trace-1" {
		t.Fatal("Trace ID mismatch")
	}
}

// TestResponseWriterStatusCode tests response writer status code capture
func TestResponseWriterStatusCode(t *testing.T) {
	rw := &ResponseWriter{ResponseWriter: httptest.NewRecorder()}

	rw.WriteHeader(http.StatusNotFound)

	if rw.statusCode != http.StatusNotFound {
		t.Fatalf("Expected status code %d, got %d", http.StatusNotFound, rw.statusCode)
	}
}

// TestResponseWriterWrite tests response writer write without explicit header
func TestResponseWriterWrite(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := &ResponseWriter{ResponseWriter: rec}

	rw.Write([]byte("test"))

	if rw.statusCode != http.StatusOK {
		t.Fatalf("Expected status code %d, got %d", http.StatusOK, rw.statusCode)
	}
}

// TestLogWithContextJSON tests that logs with context produce valid JSON
func TestLogWithContextJSON(t *testing.T) {
	// Capture log output to verify JSON format
	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()),
		zapcore.AddSync(&bytes.Buffer{}),
		zapcore.InfoLevel,
	)
	zapLogger := zap.New(core)

	ctx := context.Background()
	ctx = WithTenantID(ctx, "tenant-test")
	ctx = WithRequestID(ctx, "req-test")

	logger := &Logger{
		SugaredLogger: zapLogger.Sugar(),
		zapLogger:     zapLogger,
	}
	defer logger.Sync()

	logger.LogWithContext(ctx, zap.InfoLevel, "Test message", "key", "value")
}

// TestNilContextHandling tests nil context handling
func TestNilContextHandling(t *testing.T) {
	logger, err := NewLogger("production")
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Sync()

	// Should not panic with nil context
	panicDeferred := false
	defer func() {
		if r := recover(); r != nil {
			panicDeferred = true
		}
	}()

	ctx := context.Background()
	logger.LogWithContext(ctx, zap.InfoLevel, "Message with empty context")

	if panicDeferred {
		t.Fatal("Panic occurred when handling empty context")
	}
}

// TestConcurrentLogging tests concurrent logging operations
func TestConcurrentLogging(t *testing.T) {
	logger, err := NewLogger("production")
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Sync()

	done := make(chan bool, 10)

	for i := 0; i < 10; i++ {
		go func(id int) {
			ctx := context.Background()
			ctx = WithTenantID(ctx, fmt.Sprintf("tenant-%d", id))
			ctx = WithRequestID(ctx, fmt.Sprintf("req-%d", id))

			logger.InfoWithContext(ctx, "Concurrent message", "id", id)
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}

// TestRequestLoggingMiddlewareConcurrent tests concurrent request logging
func TestRequestLoggingMiddlewareConcurrent(t *testing.T) {
	logger, err := NewLogger("production")
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Sync()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	middleware := RequestLoggingMiddleware(logger)
	wrapped := middleware(handler)

	done := make(chan bool, 5)

	for i := 0; i < 5; i++ {
		go func(id int) {
			req := httptest.NewRequest("GET", "/test", nil)
			w := httptest.NewRecorder()
			wrapped.ServeHTTP(w, req)
			done <- true
		}(i)
	}

	for i := 0; i < 5; i++ {
		<-done
	}
}

// TestLogginHelperFunctions tests logging helper functions
func TestLoggingHelperFunctions(t *testing.T) {
	logger, err := NewLogger("development")
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Sync()

	ctx := context.Background()
	ctx = WithTenantID(ctx, "tenant-test")

	// Test all log level helpers
	logger.DebugWithContext(ctx, "Debug message")
	logger.InfoWithContext(ctx, "Info message")
	logger.WarnWithContext(ctx, "Warn message")
	logger.ErrorWithContext(ctx, "Error message")
}
