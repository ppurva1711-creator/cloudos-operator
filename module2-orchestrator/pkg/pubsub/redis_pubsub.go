package pubsub

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

// RedisPubSub manages Redis pub/sub for task lifecycle events
type RedisPubSub struct {
	client         *redis.Client
	log            *logrus.Logger
	addr           string
	password       string
	mu             sync.RWMutex
	closed         bool
	reconnectCount int
	subscribers    map[string][]chan interface{}
}

// NewRedisPubSub creates a new Redis pub/sub client
func NewRedisPubSub(addr, password string, log *logrus.Logger) (*RedisPubSub, error) {
	if log == nil {
		log = logrus.New()
	}

	client := redis.NewClient(&redis.Options{
		Addr:         addr,
		Password:     password,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis at %s: %w", addr, err)
	}

	log.Infof("Connected to Redis PubSub at %s", addr)

	return &RedisPubSub{
		client:      client,
		log:         log,
		addr:        addr,
		password:    password,
		subscribers: make(map[string][]chan interface{}),
	}, nil
}

// PublishTaskCompletion publishes a task completion event
func (r *RedisPubSub) PublishTaskCompletion(ctx context.Context, taskID, podName, namespace, tenantID string, exitCode int32, duration time.Duration) error {
	event := TaskCompletionEvent{
		EventType:  "task.completed",
		TaskID:     taskID,
		PodName:    podName,
		Namespace:  namespace,
		TenantID:   tenantID,
		ExitCode:   exitCode,
		DurationMs: duration.Milliseconds(),
		Timestamp:  time.Now(),
	}

	return r.publish(ctx, ChannelTaskCompleted, event)
}

// PublishTaskFailure publishes a task failure event
func (r *RedisPubSub) PublishTaskFailure(ctx context.Context, taskID, podName, namespace, tenantID, reason, message string, exitCode int32, duration time.Duration, retryAttempt, maxRetries int) error {
	event := TaskFailureEvent{
		EventType:    "task.failed",
		TaskID:       taskID,
		PodName:      podName,
		Namespace:    namespace,
		TenantID:     tenantID,
		Reason:       reason,
		Message:      message,
		ExitCode:     exitCode,
		DurationMs:   duration.Milliseconds(),
		Timestamp:    time.Now(),
		RetryAttempt: retryAttempt,
		MaxRetries:   maxRetries,
	}

	return r.publish(ctx, ChannelTaskFailed, event)
}

// PublishScalingEvent publishes a scaling event
func (r *RedisPubSub) PublishScalingEvent(ctx context.Context, event ScalingEvent) error {
	event.EventType = "scaling.event"
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	return r.publish(ctx, ChannelScalingEvent, event)
}

// PublishPodStateChange publishes a pod state change event
func (r *RedisPubSub) PublishPodStateChange(ctx context.Context, event PodStateChangeEvent) error {
	event.EventType = "pod.state_change"
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	return r.publish(ctx, ChannelPodStateChange, event)
}

// PublishQueueDepth publishes the current queue depth for a tenant
func (r *RedisPubSub) PublishQueueDepth(ctx context.Context, tenantID string, depth int64) error {
	event := QueueDepthEvent{
		EventType: "queue.depth",
		TenantID:  tenantID,
		Depth:     depth,
		Timestamp: time.Now(),
	}

	// Store in Redis hash for latest retrieval
	key := fmt.Sprintf("queue:depth:%s", tenantID)
	if err := r.client.Set(ctx, key, depth, 5*time.Minute).Err(); err != nil {
		return fmt.Errorf("failed to set queue depth: %w", err)
	}

	return r.publish(ctx, "queue:depth", event)
}

// GetCurrentQueueDepth retrieves the latest queue depth for a tenant
func (r *RedisPubSub) GetCurrentQueueDepth(ctx context.Context, tenantID string) (int64, error) {
	key := fmt.Sprintf("queue:depth:%s", tenantID)
	depth, err := r.client.Get(ctx, key).Int64()
	if err != nil {
		if err == redis.Nil {
			return 0, nil
		}
		return 0, fmt.Errorf("failed to get queue depth: %w", err)
	}
	return depth, nil
}

// Subscribe registers a local subscriber for a channel
func (r *RedisPubSub) Subscribe(ctx context.Context, channel string, ch chan interface{}) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.subscribers[channel] == nil {
		r.subscribers[channel] = make([]chan interface{}, 0)
	}
	r.subscribers[channel] = append(r.subscribers[channel], ch)

	r.log.Debugf("Subscribed to channel %s (total: %d)", channel, len(r.subscribers[channel]))
}

// Unsubscribe removes a local subscriber from a channel
func (r *RedisPubSub) Unsubscribe(ctx context.Context, channel string, ch chan interface{}) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if subs, exists := r.subscribers[channel]; exists {
		for i, sub := range subs {
			if sub == ch {
				r.subscribers[channel] = append(subs[:i], subs[i+1:]...)
				break
			}
		}
	}

	r.log.Debugf("Unsubscribed from channel %s", channel)
}

// SetAddress updates the Redis address for reconnection
func (r *RedisPubSub) SetAddress(addr string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Close old client
	if r.client != nil {
		r.client.Close()
	}

	r.addr = addr
	r.reconnectCount++

	r.client = redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: r.password,
	})

	r.log.Infof("Reconnected to Redis at %s (reconnect count: %d)", addr, r.reconnectCount)
}

// GetReconnectCount returns number of reconnections
func (r *RedisPubSub) GetReconnectCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.reconnectCount
}

// Close closes the Redis client connection
func (r *RedisPubSub) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return nil
	}

	r.closed = true
	if r.client != nil {
		return r.client.Close()
	}
	return nil
}

// publish is the internal method that serializes and publishes to Redis
func (r *RedisPubSub) publish(ctx context.Context, channel string, message interface{}) error {
	r.mu.RLock()
	if r.closed {
		r.mu.RUnlock()
		return fmt.Errorf("pubsub client is closed")
	}
	r.mu.RUnlock()

	data, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	if err := r.client.Publish(ctx, channel, string(data)).Err(); err != nil {
		r.log.Errorf("Failed to publish to channel %s: %v", channel, err)
		return fmt.Errorf("failed to publish to %s: %w", channel, err)
	}

	// Also notify local in-process subscribers
	r.mu.RLock()
	subs := r.subscribers[channel]
	r.mu.RUnlock()

	for _, ch := range subs {
		select {
		case ch <- message:
		case <-ctx.Done():
			return ctx.Err()
		default:
			r.log.Warnf("Subscriber channel full for %s, dropping message", channel)
		}
	}

	r.log.Debugf("Published event to channel %s (%d bytes)", channel, len(data))
	return nil
}
