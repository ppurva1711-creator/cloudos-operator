package utils

import (
	"os"

	"github.com/sirupsen/logrus"
)

// InitializeLogger creates and configures a logrus logger with JSON formatting
func InitializeLogger(logLevel string) *logrus.Logger {
	log := logrus.New()

	// Set log level
	level := logrus.InfoLevel
	if l, err := logrus.ParseLevel(logLevel); err == nil {
		level = l
	}
	log.SetLevel(level)

	// JSON formatter for structured logging
	log.SetFormatter(&logrus.JSONFormatter{
		TimestampFormat: "2006-01-02T15:04:05Z07:00",
	})

	log.SetOutput(os.Stdout)

	return log
}

// NewLogger returns a logger instance with a given context
func NewLogger(context string) *logrus.Logger {
	return logrus.New()
}
