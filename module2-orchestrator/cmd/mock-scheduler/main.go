package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/orchestrator/module2-orchestrator/pkg/mock"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
)

func main() {
	// Setup logging
	log := logrus.New()
	log.SetFormatter(&logrus.JSONFormatter{
		FieldMap: logrus.FieldMap{
			logrus.FieldKeyTime:  "timestamp",
			logrus.FieldKeyLevel: "level",
			logrus.FieldKeyMsg:   "message",
		},
	})
	log.SetLevel(logrus.InfoLevel)

	logEntry := log.WithField("component", "mock-scheduler")

	// Parse configuration
	config := parseConfig(logEntry)
	logEntry.WithFields(logrus.Fields{
		"port":            config.Port,
		"delay_ms":        config.DelayMS,
		"failure_rate":    config.FailureRate,
		"initial_queue":   config.InitialQueueDepth,
		"redis_addr":      config.RedisAddr,
		"postgres_dsn":    config.PostgresDSN,
	}).Info("Starting Mock Module 1 Scheduler")

	// Create listener
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", config.Port))
	if err != nil {
		logEntry.WithError(err).Fatalf("Failed to listen on port %d", config.Port)
	}
	defer lis.Close()

	// Create gRPC server
	grpcServer := grpc.NewServer()

	// Create scheduler service
	service, err := mock.NewSchedulerService(
		logEntry,
		config.DelayMS,
		config.FailureRate,
		config.RedisAddr,
		config.PostgresDSN,
	)
	if err != nil {
		logEntry.WithError(err).Fatal("Failed to create scheduler service")
	}

	// Pre-fill queue if configured
	if config.InitialQueueDepth > 0 {
		logEntry.Infof("Pre-filling queue with %d tasks", config.InitialQueueDepth)
		for i := 0; i < config.InitialQueueDepth; i++ {
			service.AddPendingTask(fmt.Sprintf("queue-filler-%d", i))
		}
	}

	// Register service (we'll implement a generic handler)
	mock.RegisterSchedulerService(grpcServer, service)

	logEntry.Infof("Mock Module 1 Scheduler listening on port %d", config.Port)

	// Handle shutdown signals
	go handleShutdown(grpcServer, logEntry)

	// Start server
	if err := grpcServer.Serve(lis); err != nil {
		logEntry.WithError(err).Fatal("gRPC server error")
	}
}

// Config represents Mock Module 1 configuration
type Config struct {
	Port               int
	DelayMS            int
	FailureRate        int
	InitialQueueDepth  int
	RedisAddr          string
	PostgresDSN        string
	Scenario           string
}

// parseConfig reads configuration from environment variables
func parseConfig(log *logrus.Entry) *Config {
	config := &Config{
		Port:              50051,
		DelayMS:           100,
		FailureRate:       0,
		InitialQueueDepth: 0,
		RedisAddr:         "redis.module2-system:6379",
		PostgresDSN:       "postgres://postgres:postgres@postgres.module2-system:5432/cloudtask?sslmode=disable",
		Scenario:          "normal",
	}

	if p := os.Getenv("MOCK_PORT"); p != "" {
		if port, err := strconv.Atoi(p); err == nil {
			config.Port = port
		}
	}

	if d := os.Getenv("MOCK_DELAY_MS"); d != "" {
		if delay, err := strconv.Atoi(d); err == nil {
			config.DelayMS = delay
		}
	}

	if fr := os.Getenv("MOCK_FAILURE_RATE"); fr != "" {
		if rate, err := strconv.Atoi(fr); err == nil {
			config.FailureRate = rate
		}
	}

	if q := os.Getenv("MOCK_QUEUE_DEPTH"); q != "" {
		if depth, err := strconv.Atoi(q); err == nil {
			config.InitialQueueDepth = depth
		}
	}

	if r := os.Getenv("REDIS_ADDR"); r != "" {
		config.RedisAddr = r
	}

	if pg := os.Getenv("POSTGRES_DSN"); pg != "" {
		config.PostgresDSN = pg
	}

	if s := os.Getenv("MOCK_SCENARIO"); s != "" {
		config.Scenario = s
	}

	return config
}

// handleShutdown gracefully shuts down the server
func handleShutdown(server *grpc.Server, log *logrus.Entry) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)

	<-sigChan
	log.Info("Received shutdown signal, gracefully stopping gRPC server")

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Try graceful stop
	done := make(chan struct{})
	go func() {
		server.GracefulStop()
		close(done)
	}()

	select {
	case <-done:
		log.Info("gRPC server stopped gracefully")
	case <-ctx.Done():
		log.Warn("Graceful shutdown timeout, forcing stop")
		server.Stop()
	}
}
