package logger

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	// Context keys for structured logging
	ContextKeyTenantID  = "tenant_id"
	ContextKeyRequestID = "request_id"
	ContextKeyTraceID   = "trace_id"

	// Service name
	ServiceName = "cloudtask-orchestrator"
)

// Logger wraps zap.Logger for structured logging
type Logger struct {
	*zap.SugaredLogger
	zapLogger *zap.Logger
}

// NewLogger creates a new structured logger with the specified environment
func NewLogger(env string) (*Logger, error) {
	var config zap.Config

	switch env {
	case "development", "dev":
		config = zap.NewDevelopmentConfig()
		config.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	case "production", "prod":
		config = zap.NewProductionConfig()
	default:
		config = zap.NewProductionConfig()
	}

	// Customize encoding
	config.EncoderConfig.TimeKey = "timestamp"
	config.EncoderConfig.MessageKey = "message"
	config.EncoderConfig.LevelKey = "level"
	config.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	config.OutputPaths = []string{"stdout"}
	config.ErrorOutputPaths = []string{"stderr"}

	zapLogger, err := config.Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create logger: %w", err)
	}

	// Add service name to all logs
	zapLogger = zapLogger.With(zap.String("service", ServiceName))

	return &Logger{
		SugaredLogger: zapLogger.Sugar(),
		zapLogger:     zapLogger,
	}, nil
}

// WithTenantID adds tenant_id to the context
func WithTenantID(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, ContextKeyTenantID, tenantID)
}

// WithRequestID adds request_id to the context
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, ContextKeyRequestID, requestID)
}

// WithTraceID adds trace_id to the context
func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, ContextKeyTraceID, traceID)
}

// GetLogger retrieves the logger with context values injected
func GetLogger(ctx context.Context) *Logger {
	logger, err := NewLogger("production")
	if err != nil {
		panic(fmt.Sprintf("failed to create logger: %v", err))
	}

	// Extract and inject context values
	fields := []zap.Field{}

	if tenantID, ok := ctx.Value(ContextKeyTenantID).(string); ok && tenantID != "" {
		fields = append(fields, zap.String(ContextKeyTenantID, tenantID))
	}

	if requestID, ok := ctx.Value(ContextKeyRequestID).(string); ok && requestID != "" {
		fields = append(fields, zap.String(ContextKeyRequestID, requestID))
	}

	if traceID, ok := ctx.Value(ContextKeyTraceID).(string); ok && traceID != "" {
		fields = append(fields, zap.String(ContextKeyTraceID, traceID))
	}

	if len(fields) > 0 {
		logger.zapLogger = logger.zapLogger.With(fields...)
		logger.SugaredLogger = logger.zapLogger.Sugar()
	}

	return logger
}

// LogWithContext logs a message with context values
func (l *Logger) LogWithContext(ctx context.Context, level zapcore.Level, msg string, keysAndValues ...interface{}) {
	fields := []zap.Field{}

	if tenantID, ok := ctx.Value(ContextKeyTenantID).(string); ok && tenantID != "" {
		fields = append(fields, zap.String(ContextKeyTenantID, tenantID))
	}

	if requestID, ok := ctx.Value(ContextKeyRequestID).(string); ok && requestID != "" {
		fields = append(fields, zap.String(ContextKeyRequestID, requestID))
	}

	if traceID, ok := ctx.Value(ContextKeyTraceID).(string); ok && traceID != "" {
		fields = append(fields, zap.String(ContextKeyTraceID, traceID))
	}

	// Convert keysAndValues to zap fields
	for i := 0; i < len(keysAndValues); i += 2 {
		if i+1 < len(keysAndValues) {
			fields = append(fields, zap.Any(fmt.Sprintf("%v", keysAndValues[i]), keysAndValues[i+1]))
		}
	}

	l.zapLogger.Log(level, msg, fields...)
}

// DebugWithContext logs debug message with context
func (l *Logger) DebugWithContext(ctx context.Context, msg string, keysAndValues ...interface{}) {
	l.LogWithContext(ctx, zap.DebugLevel, msg, keysAndValues...)
}

// InfoWithContext logs info message with context
func (l *Logger) InfoWithContext(ctx context.Context, msg string, keysAndValues ...interface{}) {
	l.LogWithContext(ctx, zap.InfoLevel, msg, keysAndValues...)
}

// WarnWithContext logs warn message with context
func (l *Logger) WarnWithContext(ctx context.Context, msg string, keysAndValues ...interface{}) {
	l.LogWithContext(ctx, zap.WarnLevel, msg, keysAndValues...)
}

// ErrorWithContext logs error message with context
func (l *Logger) ErrorWithContext(ctx context.Context, msg string, keysAndValues ...interface{}) {
	l.LogWithContext(ctx, zap.ErrorLevel, msg, keysAndValues...)
}

// Sync flushes any buffered log entries
func (l *Logger) Sync() error {
	return l.zapLogger.Sync()
}

// GetZapLogger returns the underlying zap.Logger
func (l *Logger) GetZapLogger() *zap.Logger {
	return l.zapLogger
}
