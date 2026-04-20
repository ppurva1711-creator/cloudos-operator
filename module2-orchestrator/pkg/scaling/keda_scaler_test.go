package scaling

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewKEDAScaler(t *testing.T) {
	// Start mini Redis server
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	log := logrus.NewEntry(logrus.New())

	tests := []struct {
		name    string
		addr    string
		pass    string
		listName string
		interval time.Duration
		shouldErr bool
	}{
		{
			name:       "valid Redis connection",
			addr:       mr.Addr(),
			pass:       "",
			listName:   "tasks:pending",
			interval:   10 * time.Second,
			shouldErr:  false,
		},
		{
			name:       "default list name",
			addr:       mr.Addr(),
			pass:       "",
			listName:   "",
			interval:   10 * time.Second,
			shouldErr:  false,
		},
		{
			name:       "invalid Redis address",
			addr:       "localhost:9999",
			pass:       "",
			listName:   "tasks:pending",
			interval:   10 * time.Second,
			shouldErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scaler, err := NewKEDAScaler(log, tt.addr, tt.pass, tt.listName, tt.interval)
			if tt.shouldErr {
				assert.Error(t, err)
				assert.Nil(t, scaler)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, scaler)
				if scaler != nil {
					scaler.Close()
				}
			}
		})
	}
}

func TestPublishQueueDepth(t *testing.T) {
	// Start mini Redis server
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	log := logrus.NewEntry(logrus.New())
	ctx := context.Background()

	scaler, err := NewKEDAScaler(log, mr.Addr(), "", "tasks:pending", 10*time.Second)
	require.NoError(t, err)
	defer scaler.Close()

	tests := []struct {
		name      string
		depth     int
		shouldErr bool
	}{
		{
			name:      "publish zero depth",
			depth:     0,
			shouldErr: false,
		},
		{
			name:      "publish positive depth",
			depth:     42,
			shouldErr: false,
		},
		{
			name:      "publish large depth",
			depth:     10000,
			shouldErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := scaler.PublishQueueDepth(ctx, tt.depth)
			if tt.shouldErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				
				// Verify the value was set in Redis
				val, err := scaler.redisClient.Get(ctx, "tasks:pending:depth").Int()
				assert.NoError(t, err)
				assert.Equal(t, tt.depth, val)
			}
		})
	}
}

func TestGetCurrentQueueDepth(t *testing.T) {
	// Start mini Redis server
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	log := logrus.NewEntry(logrus.New())
	ctx := context.Background()

	scaler, err := NewKEDAScaler(log, mr.Addr(), "", "tasks:pending", 10*time.Second)
	require.NoError(t, err)
	defer scaler.Close()

	tests := []struct {
		name          string
		setupData     func(*redis.Client)
		expectedDepth int
		shouldErr     bool
	}{
		{
			name: "empty list returns 0",
			setupData: func(client *redis.Client) {
				// Do nothing, list doesn't exist
			},
			expectedDepth: 0,
			shouldErr:     false,
		},
		{
			name: "list with items",
			setupData: func(client *redis.Client) {
				for i := 0; i < 5; i++ {
					client.RPush(ctx, "tasks:pending", fmt.Sprintf("task-%d", i))
				}
			},
			expectedDepth: 5,
			shouldErr:     false,
		},
		{
			name: "list with many items",
			setupData: func(client *redis.Client) {
				for i := 0; i < 100; i++ {
					client.RPush(ctx, "tasks:pending", fmt.Sprintf("task-%d", i))
				}
			},
			expectedDepth: 100,
			shouldErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear list
			scaler.redisClient.Del(ctx, "tasks:pending")

			// Setup test data
			tt.setupData(scaler.redisClient)

			depth, err := scaler.GetCurrentQueueDepth(ctx)
			if tt.shouldErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedDepth, depth)
			}
		})
	}
}

func TestCalculateDesiredReplicasFromQueueDepth(t *testing.T) {
	// Start mini Redis server
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	log := logrus.NewEntry(logrus.New())
	scaler, err := NewKEDAScaler(log, mr.Addr(), "", "tasks:pending", 10*time.Second)
	require.NoError(t, err)
	defer scaler.Close()

	tests := []struct {
		name           string
		queueDepth     int
		itemsPerPod    int
		minReplicas    int
		maxReplicas    int
		expectedResult int
		description    string
	}{
		{
			name:           "zero queue returns minimum",
			queueDepth:     0,
			itemsPerPod:    10,
			minReplicas:    2,
			maxReplicas:    100,
			expectedResult: 2,
			description:    "queue=0 → replicas=2 (minimum)",
		},
		{
			name:           "queue=10 returns 1 adjusted to min",
			queueDepth:     10,
			itemsPerPod:    10,
			minReplicas:    2,
			maxReplicas:    100,
			expectedResult: 2,
			description:    "queue=10 → replicas=1... adjusted to min=2",
		},
		{
			name:           "queue=100 returns 10",
			queueDepth:     100,
			itemsPerPod:    10,
			minReplicas:    2,
			maxReplicas:    100,
			expectedResult: 10,
			description:    "queue=100 → replicas=10",
		},
		{
			name:           "queue=1000 returns max",
			queueDepth:     1000,
			itemsPerPod:    10,
			minReplicas:    2,
			maxReplicas:    100,
			expectedResult: 100,
			description:    "queue=1000 → replicas=100 (maximum)",
		},
		{
			name:           "queue=50 returns 5",
			queueDepth:     50,
			itemsPerPod:    10,
			minReplicas:    2,
			maxReplicas:    100,
			expectedResult: 5,
			description:    "queue=50 → replicas=5",
		},
		{
			name:           "queue with 5 items per pod",
			queueDepth:     17,
			itemsPerPod:    5,
			minReplicas:    2,
			maxReplicas:    100,
			expectedResult: 4,
			description:    "queue=17, items/pod=5 → replicas=4 (ceil(17/5))",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := scaler.CalculateDesiredReplicasFromQueueDepth(
				tt.queueDepth,
				tt.itemsPerPod,
				tt.minReplicas,
				tt.maxReplicas,
			)
			assert.Equal(t, tt.expectedResult, result, tt.description)
		})
	}
}

func TestStartQueueDepthPublisher(t *testing.T) {
	// Start mini Redis server
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	log := logrus.NewEntry(logrus.New())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	scaler, err := NewKEDAScaler(log, mr.Addr(), "", "tasks:pending", 100*time.Millisecond)
	require.NoError(t, err)
	defer scaler.Close()

	// Mock queue depth function
	callCount := atomic.Int32{}
	currentDepth := 42

	getDepthFunc := func(ctx context.Context) (int, error) {
		callCount.Add(1)
		return currentDepth, nil
	}

	// Start publisher
	err = scaler.StartQueueDepthPublisher(ctx, getDepthFunc)
	require.NoError(t, err)
	assert.True(t, scaler.isRunning)

	// Wait for at least 2 publishes
	time.Sleep(300 * time.Millisecond)

	// Check that getDepthFunc was called multiple times
	assert.Greater(t, int(callCount.Load()), 1, "getDepthFunc should have been called multiple times")

	// Verify value was published to Redis
	val, err := scaler.redisClient.Get(ctx, "tasks:pending:depth").Int()
	assert.NoError(t, err)
	assert.Equal(t, currentDepth, val)

	// Stop publisher
	err = scaler.Stop()
	assert.NoError(t, err)
	assert.False(t, scaler.isRunning)
}

func TestPublisherErrorHandling(t *testing.T) {
	// Start mini Redis server
	mr, err := miniredis.Run()
	require.NoError(t, err)

	log := logrus.NewEntry(logrus.New())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	scaler, err := NewKEDAScaler(log, mr.Addr(), "", "tasks:pending", 50*time.Millisecond)
	require.NoError(t, err)
	defer scaler.Close()

	errorCount := atomic.Int32{}
	successCount := atomic.Int32{}

	getDepthFunc := func(ctx context.Context) (int, error) {
		// Alternate between error and success
		if errorCount.Load()%2 == 0 {
			errorCount.Add(1)
			return 0, fmt.Errorf("simulated error")
		}
		successCount.Add(1)
		return 42, nil
	}

	// Start publisher
	err = scaler.StartQueueDepthPublisher(ctx, getDepthFunc)
	require.NoError(t, err)

	// Let it run for a bit
	time.Sleep(300 * time.Millisecond)

	// Stop publisher
	scaler.Stop()

	// Should have recovered from errors and continued publishing
	assert.Greater(t, int(successCount.Load()), 0, "Should have successful publishes despite errors")
}

func TestKEDAScalerIntegration(t *testing.T) {
	// Start mini Redis server
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	log := logrus.NewEntry(logrus.New())
	ctx := context.Background()

	// Create scaler
	scaler, err := NewKEDAScaler(log, mr.Addr(), "", "tasks:pending", 10*time.Second)
	require.NoError(t, err)
	defer scaler.Close()

	// Simulate adding tasks to queue
	for i := 0; i < 25; i++ {
		scaler.redisClient.RPush(ctx, "tasks:pending", fmt.Sprintf("task-%d", i))
	}

	// Get current queue depth
	depth, err := scaler.GetCurrentQueueDepth(ctx)
	require.NoError(t, err)
	assert.Equal(t, 25, depth)

	// Publish queue depth
	err = scaler.PublishQueueDepth(ctx, depth)
	require.NoError(t, err)

	// Calculate desired replicas (1 pod per 10 items)
	desired := scaler.CalculateDesiredReplicasFromQueueDepth(depth, 10, 2, 100)
	assert.Equal(t, 3, desired, "25 items should require 3 replicas (ceil(25/10))")

	// Verify published value
	val, err := scaler.redisClient.Get(ctx, "tasks:pending:depth").Int()
	assert.NoError(t, err)
	assert.Equal(t, 25, val)
}
