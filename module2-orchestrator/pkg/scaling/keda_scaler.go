package scaling

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

// KEDAScaler handles queue-depth based scaling using KEDA triggers
type KEDAScaler struct {
	log              *logrus.Entry
	redisClient      *redis.Client
	listName         string
	publishInterval  time.Duration
	stopCh           chan struct{}
	isRunning        bool
}

// NewKEDAScaler creates a new KEDA scaler instance
func NewKEDAScaler(log *logrus.Entry, redisAddr, redisPassword, listName string, publishInterval time.Duration) (*KEDAScaler, error) {
	// Validate inputs
	if listName == "" {
		listName = "tasks:pending"
	}
	if publishInterval == 0 {
		publishInterval = 10 * time.Second
	}

	// Create Redis client
	redisClient := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: redisPassword,
		DB:       0,
	})

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := redisClient.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis at %s: %w", redisAddr, err)
	}

	return &KEDAScaler{
		log:             log.WithField("component", "keda-scaler"),
		redisClient:     redisClient,
		listName:        listName,
		publishInterval: publishInterval,
		stopCh:          make(chan struct{}),
		isRunning:       false,
	}, nil
}

// PublishQueueDepth publishes the current queue depth to Redis
// This is used by KEDA to determine scaling decisions
func (ks *KEDAScaler) PublishQueueDepth(ctx context.Context, depth int) error {
	if ks.redisClient == nil {
		return fmt.Errorf("Redis client not initialized")
	}

	// KEDA uses list length to determine scaling, so we ensure the list has the right number of items
	// For queue depth tracking via KEDA, we'll use a counter approach
	// Set a metric key that represents queue depth
	depthKey := ks.listName + ":depth"
	
	if err := ks.redisClient.Set(ctx, depthKey, depth, 0).Err(); err != nil {
		ks.log.WithError(err).Warnf("Failed to publish queue depth %d to Redis", depth)
		return err
	}

	ks.log.WithField("queue_depth", depth).Debugf("Published queue depth to Redis key: %s", depthKey)
	return nil
}

// GetCurrentQueueDepth reads the current queue depth from Redis
// It uses the list length of the pending tasks queue
func (ks *KEDAScaler) GetCurrentQueueDepth(ctx context.Context) (int, error) {
	if ks.redisClient == nil {
		return 0, fmt.Errorf("Redis client not initialized")
	}

	// Get the length of the list (LLEN command)
	length, err := ks.redisClient.LLen(ctx, ks.listName).Result()
	if err != nil && err != redis.Nil {
		ks.log.WithError(err).Warnf("Failed to get queue depth from Redis list: %s", ks.listName)
		return 0, err
	}

	if err == redis.Nil {
		// List doesn't exist, queue depth is 0
		return 0, nil
	}

	ks.log.WithField("queue_depth", length).Debugf("Retrieved queue depth from Redis")
	return int(length), nil
}

// StartQueueDepthPublisher starts the background goroutine that publishes queue depth periodically
func (ks *KEDAScaler) StartQueueDepthPublisher(ctx context.Context, getQueueDepthFunc func(context.Context) (int, error)) error {
	if ks.isRunning {
		return fmt.Errorf("queue depth publisher is already running")
	}

	if getQueueDepthFunc == nil {
		return fmt.Errorf("getQueueDepthFunc cannot be nil")
	}

	ks.isRunning = true
	ks.log.Info("Starting queue depth publisher")

	go ks.publishLoop(ctx, getQueueDepthFunc)
	return nil
}

// publishLoop runs the periodic publishing of queue depth
func (ks *KEDAScaler) publishLoop(ctx context.Context, getQueueDepthFunc func(context.Context) (int, error)) {
	ticker := time.NewTicker(ks.publishInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ks.stopCh:
			ks.log.Info("Stopping queue depth publisher")
			ks.isRunning = false
			return
		case <-ctx.Done():
			ks.log.Info("Context cancelled, stopping queue depth publisher")
			ks.isRunning = false
			return
		case <-ticker.C:
			// Get current queue depth
			depth, err := getQueueDepthFunc(ctx)
			if err != nil {
				ks.log.WithError(err).Warn("Failed to get queue depth")
				continue
			}

			// Publish to Redis
			if err := ks.PublishQueueDepth(ctx, depth); err != nil {
				ks.log.WithError(err).Warn("Failed to publish queue depth")
				// Continue publishing even on error
			}
		}
	}
}

// Stop stops the queue depth publisher
func (ks *KEDAScaler) Stop() error {
	if !ks.isRunning {
		return fmt.Errorf("queue depth publisher is not running")
	}

	close(ks.stopCh)
	// Give it a moment to clean up
	time.Sleep(100 * time.Millisecond)
	return nil
}

// Close closes the Redis connection
func (ks *KEDAScaler) Close() error {
	ks.log.Info("Closing KEDA scaler")
	
	if ks.isRunning {
		if err := ks.Stop(); err != nil {
			ks.log.WithError(err).Warn("Error stopping publisher")
		}
	}

	if ks.redisClient != nil {
		return ks.redisClient.Close()
	}
	return nil
}

// CalculateDesiredReplicasFromQueueDepth calculates desired replicas based on queue depth
// using the configured items-per-pod ratio
func (ks *KEDAScaler) CalculateDesiredReplicasFromQueueDepth(queueDepth, itemsPerPod, minReplicas, maxReplicas int) int {
	if queueDepth == 0 {
		return minReplicas
	}

	// Calculate needed replicas: ceil(queueDepth / itemsPerPod)
	needed := (queueDepth + itemsPerPod - 1) / itemsPerPod

	// Apply constraints
	if needed < minReplicas {
		return minReplicas
	}
	if needed > maxReplicas {
		return maxReplicas
	}

	return needed
}
