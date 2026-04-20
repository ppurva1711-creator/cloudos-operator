package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/orchestrator/module2-orchestrator/pkg/api"
	"github.com/orchestrator/module2-orchestrator/pkg/auth"
	"github.com/orchestrator/module2-orchestrator/pkg/grpc"
	"github.com/orchestrator/module2-orchestrator/pkg/logger"
	"github.com/orchestrator/module2-orchestrator/pkg/utils"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"go.uber.org/zap"
)

func main() {
	var port int
	var logLevel string
	var env string

	flag.IntVar(&port, "port", 8000, "Port to listen on")
	flag.StringVar(&logLevel, "log-level", "info", "Log level (debug, info, warn, error)")
	flag.StringVar(&env, "env", "production", "Environment (development, production)")
	flag.Parse()

	// Initialize structured logger
	structuredLogger, err := logger.NewLogger(env)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer structuredLogger.Sync()

	// Legacy logger for backward compatibility
	log := utils.InitializeLogger(logLevel)

	// Log startup information
	hostname, _ := os.Hostname()
	structuredLogger.Infof("Starting API Gateway",
		zap.String("version", "1.0.0"),
		zap.String("env", env),
		zap.String("log_level", logLevel),
		zap.String("hostname", hostname),
		zap.Int("port", port),
	)

	// Load JWT configuration from environment
	jwtAlgorithm := os.Getenv("JWT_ALGORITHM")
	if jwtAlgorithm == "" {
		jwtAlgorithm = "HS256"
	}

	// Ensure JWT_SECRET is set for HS256
	if jwtAlgorithm == "HS256" && os.Getenv("JWT_SECRET") == "" {
		log.Warnf("JWT_SECRET not set; falling back to default (not recommended for production)")
		os.Setenv("JWT_SECRET", "change-me-in-production")
	}

	// Initialize JWTManager
	jwtManager, err := auth.NewJWTManager(jwtAlgorithm)
	if err != nil {
		log.Fatalf("Failed to initialize JWT manager: %v", err)
	}
	structuredLogger.Infof("JWT Manager initialized",
		zap.String("algorithm", jwtAlgorithm),
	)

	// Initialize Redis client for rate limiting
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}

	redisClient := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: os.Getenv("REDIS_PASSWORD"),
		DB:       0,
	})

	// Test Redis connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := redisClient.Ping(ctx).Err(); err != nil {
		log.Warnf("Failed to connect to Redis: %v (rate limiting will be unavailable)", err)
		redisClient = nil
	} else {
		log.Infof("Connected to Redis at %s", redisAddr)
		structuredLogger.Infof("Connected to Redis",
			zap.String("addr", redisAddr),
		)
	}

	// Initialize rate limiter (optional if Redis unavailable)
	var rateLimiter *auth.RateLimiter
	if redisClient != nil {
		rateLimiter = auth.NewRateLimiter(redisClient, log)
		log.Infof("Rate limiter configured: 100 req/min, 1000 req/hour per tenant")
		structuredLogger.Infof("Rate limiter configured",
			zap.Int("per_minute", 100),
			zap.Int("per_hour", 1000),
		)
	}

	// Create Kubernetes client
	cfg, err := config.GetConfig()
	if err != nil {
		log.Errorf("Failed to get Kubernetes config: %v", err)
		cfg = nil // Continue with nil client for now
	}

	if cfg != nil {
		log.Infof("Kubernetes client initialized")
		structuredLogger.Infof("Kubernetes client initialized")
	} else {
		structuredLogger.Warnf("Kubernetes client not available")
	}

	// Initialize Module 1 gRPC client
	module1Client, err := grpc.NewModule1RealClient(log)
	if err != nil {
		log.Errorf("Failed to initialize Module 1 gRPC client: %v (continuing without Module 1)", err)
		structuredLogger.Warnf("Module 1 gRPC client initialization failed",
			zap.Error(err),
		)
		module1Client = nil
	} else {
		log.Infof("Module 1 gRPC client initialized successfully")
		structuredLogger.Infof("Module 1 gRPC client initialized",
			zap.String("state", "connected"),
		)
		defer module1Client.Close()
	}

	// Initialize API server
	apiServer := api.NewAPIServer(nil, log)
	if module1Client != nil {
		apiServer.SetModule1Client(module1Client)
	}

	// Setup routes
	mux := http.NewServeMux()

	// Add central request logging middleware
	var handler http.Handler = mux
	handler = logger.RequestLoggingMiddleware(structuredLogger)(handler)

	// Health check (no auth required)
	mux.HandleFunc("/health", withLogging(apiServer.HandleHealth, log))
	mux.HandleFunc("/metrics", withLogging(apiServer.HandleMetrics, log))

	// Task management (with JWT middleware and rate limiting)
	authMiddleware := auth.AuthMiddleware(jwtManager, log)

	// Wrap task endpoints with auth and rate limiting
	taskListHandler := withLogging(apiServer.HandleListTasks, log)
	mux.Handle("/tasks", authMiddleware(withRateLimit(taskListHandler, rateLimiter, log)))

	taskCreateHandler := withLogging(apiServer.HandleCreateTask, log)
	mux.Handle("/tasks/create", authMiddleware(withRateLimit(taskCreateHandler, rateLimiter, log)))

	taskGetHandler := withLogging(apiServer.HandleGetTask, log)
	mux.Handle("/tasks/get", authMiddleware(withRateLimit(taskGetHandler, rateLimiter, log)))

	taskDeleteHandler := withLogging(apiServer.HandleDeleteTask, log)
	mux.Handle("/tasks/delete", authMiddleware(withRateLimit(taskDeleteHandler, rateLimiter, log)))

	// Create HTTP server
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Get local IP for logging
	localIP := getLocalIP()

	// Start server in a goroutine
	go func() {
		log.Infof("API Gateway listening on %s", server.Addr)
		structuredLogger.Infof("API Gateway started",
			zap.String("addr", server.Addr),
			zap.String("local_ip", localIP),
		)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Infof("Shutdown signal received, gracefully stopping...")
	structuredLogger.Infof("Shutdown signal received")

	// Graceful shutdown
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Errorf("Shutdown error: %v", err)
		structuredLogger.Errorf("Shutdown error: %v", err)
	}

	// Close Redis connection
	if redisClient != nil {
		redisClient.Close()
	}

	log.Infof("API Gateway stopped")
	structuredLogger.Infof("API Gateway shutdown complete")
}


// withLogging wraps an HTTP handler with request logging
func withLogging(handler http.HandlerFunc, log *logrus.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		log.Infof("%s %s from %s", r.Method, r.RequestURI, r.RemoteAddr)
		handler(w, r)
		log.Debugf("Request completed in %v", time.Since(start))
	}
}

// withRateLimit applies per-tenant rate limiting based on JWT claims
func withRateLimit(next http.HandlerFunc, rateLimiter *auth.RateLimiter, log *logrus.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip rate limiting if no rate limiter configured
		if rateLimiter == nil {
			next(w, r)
			return
		}

		// Extract tenant ID from context
		tenantID, err := auth.GetTenantIDFromContext(r.Context())
		if err != nil {
			log.Warnf("Failed to extract tenant ID from context: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		// Check rate limit
		allowed, retryAfter, err := rateLimiter.Allow(r.Context(), tenantID)
		if err != nil {
			log.Errorf("Rate limit check failed: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		if !allowed {
			// Rate limit exceeded
			w.Header().Set("Retry-After", fmt.Sprintf("%.0f", retryAfter.Seconds()))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write((&auth.ErrorResponse{
				Error:  "Rate limit exceeded",
				Status: http.StatusTooManyRequests,
			}).ToJSON())
			return
		}

		next(w, r)
	})
}

// getLocalIP returns the local IP address of the machine
func getLocalIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "localhost"
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String()
}
