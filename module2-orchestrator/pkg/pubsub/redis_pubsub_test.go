package pubsub

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewRedisPubSub tests the creation of a new Redis pub/sub client
func TestNewRedisPubSub(t *testing.T) {
	// Start a mini Redis server
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	pubsub, err := NewRedisPubSub(mr.Addr(), "", log)
	require.NoError(t, err)
	require.NotNil(t, pubsub)

	err = pubsub.Close()
	require.NoError(t, err)
}

// TestNewRedisPubSub_InvalidAddress tests connection failure
func TestNewRedisPubSub_InvalidAddress(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	// Try to connect to an invalid address
	pubsub, err := NewRedisPubSub("localhost:9999", "", log)
	require.Error(t, err)
	require.Nil(t, pubsub)
}

// TestPublishTaskCompletion tests publishing a task completion event
func TestPublishTaskCompletion(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	log := logrus.New()
	pubsub, err := NewRedisPubSub(mr.Addr(), "", log)
	require.NoError(t, err)
	defer pubsub.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Publish event
	err = pubsub.PublishTaskCompletion(ctx, "task-123", "pod-abc", "default", "tenant-1", 0, 5*time.Second)
	require.NoError(t, err)

	// Verify message was published
	msgCount := mr.PublishedMessages(ChannelTaskCompleted)
	assert.Equal(t, 1, len(msgCount))

	// Verify message content
	var event TaskCompletionEvent
	err = json.Unmarshal([]byte(msgCount[0]), &event)
	require.NoError(t, err)
	assert.Equal(t, "task.completed", event.EventType)
	assert.Equal(t, "task-123", event.TaskID)
	assert.Equal(t, "pod-abc", event.PodName)
	assert.Equal(t, "default", event.Namespace)
	assert.Equal(t, "tenant-1", event.TenantID)
	assert.Equal(t, int32(0), event.ExitCode)
	assert.Equal(t, int64(5000), event.DurationMs)
}

// TestPublishTaskFailure tests publishing a task failure event
func TestPublishTaskFailure(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	log := logrus.New()
	pubsub, err := NewRedisPubSub(mr.Addr(), "", log)
	require.NoError(t, err)
	defer pubsub.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Publish event
	err = pubsub.PublishTaskFailure(ctx, "task-123", "pod-abc", "default", "tenant-1", "CrashLoopBackOff", "pod exited with code 1", 1, 3*time.Second, 1, 3)
	require.NoError(t, err)

	// Verify message was published
	msgCount := mr.PublishedMessages(ChannelTaskFailed)
	assert.Equal(t, 1, len(msgCount))

	// Verify message content
	var event TaskFailureEvent
	err = json.Unmarshal([]byte(msgCount[0]), &event)
	require.NoError(t, err)
	assert.Equal(t, "task.failed", event.EventType)
	assert.Equal(t, "task-123", event.TaskID)
	assert.Equal(t, "CrashLoopBackOff", event.Reason)
	assert.Equal(t, int32(1), event.ExitCode)
	assert.Equal(t, 1, event.RetryAttempt)
	assert.Equal(t, 3, event.MaxRetries)
}

// TestPublishScalingEvent tests publishing a scaling event
func TestPublishScalingEvent(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	log := logrus.New()
	pubsub, err := NewRedisPubSub(mr.Addr(), "", log)
	require.NoError(t, err)
	defer pubsub.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Publish event
	event := ScalingEvent{
		ScalingAction:   "scale_up",
		Namespace:       "default",
		DeploymentName:  "worker",
		CurrentReplicas: 2,
		DesiredReplicas: 4,
		Metric:          "cpu",
		MetricValue:     85.5,
		Threshold:       80.0,
		Reason:          "CPU usage above threshold",
	}

	err = pubsub.PublishScalingEvent(ctx, event)
	require.NoError(t, err)

	// Verify message was published
	msgCount := mr.PublishedMessages(ChannelScalingEvent)
	assert.Equal(t, 1, len(msgCount))

	// Verify message content
	var publishedEvent ScalingEvent
	err = json.Unmarshal([]byte(msgCount[0]), &publishedEvent)
	require.NoError(t, err)
	assert.Equal(t, "scaling.event", publishedEvent.EventType)
	assert.Equal(t, "scale_up", publishedEvent.ScalingAction)
	assert.Equal(t, int32(4), publishedEvent.DesiredReplicas)
}

// TestPublishPodStateChange tests publishing a pod state change event
func TestPublishPodStateChange(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	log := logrus.New()
	pubsub, err := NewRedisPubSub(mr.Addr(), "", log)
	require.NoError(t, err)
	defer pubsub.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Publish event
	event := PodStateChangeEvent{
		PodName:      "task-123-pod",
		Namespace:    "default",
		TaskID:       "task-123",
		OldState:     "Pending",
		NewState:     "Running",
		StateReason:  "pod started",
		RestartCount: 0,
	}

	err = pubsub.PublishPodStateChange(ctx, event)
	require.NoError(t, err)

	// Verify message was published
	msgCount := mr.PublishedMessages(ChannelPodStateChange)
	assert.Equal(t, 1, len(msgCount))

	// Verify message content
	var publishedEvent PodStateChangeEvent
	err = json.Unmarshal([]byte(msgCount[0]), &publishedEvent)
	require.NoError(t, err)
	assert.Equal(t, "pod.state_change", publishedEvent.EventType)
	assert.Equal(t, "Pending", publishedEvent.OldState)
	assert.Equal(t, "Running", publishedEvent.NewState)
}

// TestTaskCompletionEventMarshaling tests JSON marshaling/unmarshaling
func TestTaskCompletionEventMarshaling(t *testing.T) {
	now := time.Now()
	event := TaskCompletionEvent{
		EventType:  "task.completed",
		TaskID:     "task-123",
		PodName:    "pod-abc",
		Namespace:  "default",
		TenantID:   "tenant-1",
		ExitCode:   0,
		DurationMs: 5000,
		Timestamp:  now,
	}

	// Marshal
	data, err := json.Marshal(event)
	require.NoError(t, err)

	// Unmarshal
	var unmarshaled TaskCompletionEvent
	err = json.Unmarshal(data, &unmarshaled)
	require.NoError(t, err)

	assert.Equal(t, event.EventType, unmarshaled.EventType)
	assert.Equal(t, event.TaskID, unmarshaled.TaskID)
	assert.Equal(t, event.PodName, unmarshaled.PodName)
	assert.Equal(t, event.Namespace, unmarshaled.Namespace)
	assert.Equal(t, event.ExitCode, unmarshaled.ExitCode)
	assert.Equal(t, event.DurationMs, unmarshaled.DurationMs)
}

// TestTaskFailureEventMarshaling tests JSON marshaling/unmarshaling
func TestTaskFailureEventMarshaling(t *testing.T) {
	now := time.Now()
	event := TaskFailureEvent{
		EventType:    "task.failed",
		TaskID:       "task-123",
		PodName:      "pod-abc",
		Namespace:    "default",
		TenantID:     "tenant-1",
		Reason:       "CrashLoopBackOff",
		Message:      "pod exited with code 1",
		ExitCode:     1,
		DurationMs:   3000,
		Timestamp:    now,
		RetryAttempt: 1,
		MaxRetries:   3,
	}

	// Marshal
	data, err := json.Marshal(event)
	require.NoError(t, err)

	// Unmarshal
	var unmarshaled TaskFailureEvent
	err = json.Unmarshal(data, &unmarshaled)
	require.NoError(t, err)

	assert.Equal(t, event.EventType, unmarshaled.EventType)
	assert.Equal(t, event.TaskID, unmarshaled.TaskID)
	assert.Equal(t, event.Reason, unmarshaled.Reason)
	assert.Equal(t, event.ExitCode, unmarshaled.ExitCode)
	assert.Equal(t, event.RetryAttempt, unmarshaled.RetryAttempt)
}

// TestScalingEventMarshaling tests JSON marshaling/unmarshaling
func TestScalingEventMarshaling(t *testing.T) {
	now := time.Now()
	event := ScalingEvent{
		EventType:       "scaling.event",
		ScalingAction:   "scale_up",
		Namespace:       "default",
		DeploymentName:  "worker",
		CurrentReplicas: 2,
		DesiredReplicas: 4,
		Metric:          "cpu",
		MetricValue:     85.5,
		Threshold:       80.0,
		Timestamp:       now,
		Reason:          "CPU above threshold",
	}

	// Marshal
	data, err := json.Marshal(event)
	require.NoError(t, err)

	// Unmarshal
	var unmarshaled ScalingEvent
	err = json.Unmarshal(data, &unmarshaled)
	require.NoError(t, err)

	assert.Equal(t, event.EventType, unmarshaled.EventType)
	assert.Equal(t, event.ScalingAction, unmarshaled.ScalingAction)
	assert.Equal(t, event.CurrentReplicas, unmarshaled.CurrentReplicas)
	assert.Equal(t, event.DesiredReplicas, unmarshaled.DesiredReplicas)
	assert.Equal(t, event.MetricValue, unmarshaled.MetricValue)
}

// TestPodStateChangeEventMarshaling tests JSON marshaling/unmarshaling
func TestPodStateChangeEventMarshaling(t *testing.T) {
	now := time.Now()
	event := PodStateChangeEvent{
		EventType:    "pod.state_change",
		PodName:      "task-123-pod",
		Namespace:    "default",
		TaskID:       "task-123",
		OldState:     "Pending",
		NewState:     "Running",
		StateReason:  "pod started",
		Timestamp:    now,
		ContainerID:  "docker://abc123",
		RestartCount: 0,
	}

	// Marshal
	data, err := json.Marshal(event)
	require.NoError(t, err)

	// Unmarshal
	var unmarshaled PodStateChangeEvent
	err = json.Unmarshal(data, &unmarshaled)
	require.NoError(t, err)

	assert.Equal(t, event.EventType, unmarshaled.EventType)
	assert.Equal(t, event.PodName, unmarshaled.PodName)
	assert.Equal(t, event.OldState, unmarshaled.OldState)
	assert.Equal(t, event.NewState, unmarshaled.NewState)
	assert.Equal(t, event.RestartCount, unmarshaled.RestartCount)
}

// TestMultiplePublish tests publishing multiple events
func TestMultiplePublish(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	log := logrus.New()
	pubsub, err := NewRedisPubSub(mr.Addr(), "", log)
	require.NoError(t, err)
	defer pubsub.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Publish multiple completion events
	for i := 1; i <= 5; i++ {
		taskID := "task-" + string(rune(48+i))
		err = pubsub.PublishTaskCompletion(ctx, taskID, "pod-abc", "default", "tenant-1", 0, 5*time.Second)
		require.NoError(t, err)
	}

	// Verify messages were published
	msgCount := mr.PublishedMessages(ChannelTaskCompleted)
	assert.Equal(t, 5, len(msgCount))
}

// TestChannelConstants verifies channel name constants
func TestChannelConstants(t *testing.T) {
	assert.Equal(t, "tasks:completed", ChannelTaskCompleted)
	assert.Equal(t, "tasks:failed", ChannelTaskFailed)
	assert.Equal(t, "scaling:event", ChannelScalingEvent)
	assert.Equal(t, "pods:state-change", ChannelPodStateChange)
}
