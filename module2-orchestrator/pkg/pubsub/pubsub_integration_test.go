package pubsub

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- Test Full Publish → Subscribe Cycle ----

func TestPubSubIntegration_PublishSubscribeCycle(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	ps, err := NewRedisPubSub(mr.Addr(), "", log)
	require.NoError(t, err)
	defer ps.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Subscribe to channel
	ch := make(chan interface{}, 10)
	ps.Subscribe(ctx, ChannelTaskCompleted, ch)

	// Publish a task completion event
	err = ps.PublishTaskCompletion(ctx, "task-001", "pod-001", "default", "tenant-a", 0, 5*time.Second)
	require.NoError(t, err)

	// Verify message received via local subscriber
	select {
	case msg := <-ch:
		event, ok := msg.(TaskCompletionEvent)
		require.True(t, ok, "Message should be TaskCompletionEvent")
		assert.Equal(t, "task-001", event.TaskID)
		assert.Equal(t, "pod-001", event.PodName)
		assert.Equal(t, "tenant-a", event.TenantID)
		assert.Equal(t, int32(0), event.ExitCode)
		assert.Equal(t, int64(5000), event.DurationMs)
	case <-ctx.Done():
		t.Fatal("Timeout waiting for published message")
	}

	// Also verify via miniredis that the message was published to Redis
	msgs := mr.PublishedMessages(ChannelTaskCompleted)
	assert.Equal(t, 1, len(msgs), "One message should be published to Redis")

	var publishedEvent TaskCompletionEvent
	err = json.Unmarshal([]byte(msgs[0]), &publishedEvent)
	require.NoError(t, err)
	assert.Equal(t, "task.completed", publishedEvent.EventType)
	assert.Equal(t, "task-001", publishedEvent.TaskID)
}

// ---- Test All 4 Channels Simultaneously ----

func TestPubSubIntegration_AllChannelsSimultaneously(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	ps, err := NewRedisPubSub(mr.Addr(), "", log)
	require.NoError(t, err)
	defer ps.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Subscribe to all 4 channels
	chCompleted := make(chan interface{}, 10)
	chFailed := make(chan interface{}, 10)
	chScaling := make(chan interface{}, 10)
	chPodState := make(chan interface{}, 10)

	ps.Subscribe(ctx, ChannelTaskCompleted, chCompleted)
	ps.Subscribe(ctx, ChannelTaskFailed, chFailed)
	ps.Subscribe(ctx, ChannelScalingEvent, chScaling)
	ps.Subscribe(ctx, ChannelPodStateChange, chPodState)

	// Publish to all 4 channels simultaneously
	var wg sync.WaitGroup

	wg.Add(4)
	go func() {
		defer wg.Done()
		ps.PublishTaskCompletion(ctx, "task-001", "pod-1", "ns", "t-a", 0, time.Second)
	}()
	go func() {
		defer wg.Done()
		ps.PublishTaskFailure(ctx, "task-002", "pod-2", "ns", "t-a", "OOM", "out of memory", 137, time.Second, 1, 3)
	}()
	go func() {
		defer wg.Done()
		ps.PublishScalingEvent(ctx, ScalingEvent{ScalingAction: "scale_up", CurrentReplicas: 2, DesiredReplicas: 4})
	}()
	go func() {
		defer wg.Done()
		ps.PublishPodStateChange(ctx, PodStateChangeEvent{PodName: "pod-1", OldState: "Pending", NewState: "Running"})
	}()

	wg.Wait()

	// Verify: Each channel received a message
	select {
	case msg := <-chCompleted:
		event := msg.(TaskCompletionEvent)
		assert.Equal(t, "task-001", event.TaskID)
	case <-ctx.Done():
		t.Fatal("Timeout on completed channel")
	}

	select {
	case msg := <-chFailed:
		event := msg.(TaskFailureEvent)
		assert.Equal(t, "task-002", event.TaskID)
		assert.Equal(t, "OOM", event.Reason)
	case <-ctx.Done():
		t.Fatal("Timeout on failed channel")
	}

	select {
	case msg := <-chScaling:
		event := msg.(ScalingEvent)
		assert.Equal(t, "scale_up", event.ScalingAction)
		assert.Equal(t, int32(4), event.DesiredReplicas)
	case <-ctx.Done():
		t.Fatal("Timeout on scaling channel")
	}

	select {
	case msg := <-chPodState:
		event := msg.(PodStateChangeEvent)
		assert.Equal(t, "Pending", event.OldState)
		assert.Equal(t, "Running", event.NewState)
	case <-ctx.Done():
		t.Fatal("Timeout on pod state channel")
	}

	// Verify Redis received all 4 messages
	assert.Equal(t, 1, len(mr.PublishedMessages(ChannelTaskCompleted)))
	assert.Equal(t, 1, len(mr.PublishedMessages(ChannelTaskFailed)))
	assert.Equal(t, 1, len(mr.PublishedMessages(ChannelScalingEvent)))
	assert.Equal(t, 1, len(mr.PublishedMessages(ChannelPodStateChange)))
}

// ---- Test Reconnection After Redis Restart ----

func TestPubSubIntegration_ReconnectionAfterRedisRestart(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)

	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	ps, err := NewRedisPubSub(mr.Addr(), "", log)
	require.NoError(t, err)
	defer ps.Close()

	ctx := context.Background()

	// Publish successfully before restart
	err = ps.PublishTaskCompletion(ctx, "task-001", "pod-1", "ns", "tenant-a", 0, time.Second)
	require.NoError(t, err)

	msgs := mr.PublishedMessages(ChannelTaskCompleted)
	assert.Equal(t, 1, len(msgs), "Should have 1 message before restart")

	// Simulate Redis restart
	mr.Close()
	time.Sleep(100 * time.Millisecond)

	mr2, err := miniredis.Run()
	require.NoError(t, err)
	defer mr2.Close()

	// Reconnect
	ps.SetAddress(mr2.Addr())
	assert.Equal(t, 1, ps.GetReconnectCount(), "Reconnect count should be 1")

	// Publish after reconnection
	err = ps.PublishTaskCompletion(ctx, "task-002", "pod-2", "ns", "tenant-a", 0, 2*time.Second)
	require.NoError(t, err, "Should publish successfully after reconnection")

	msgs2 := mr2.PublishedMessages(ChannelTaskCompleted)
	assert.Equal(t, 1, len(msgs2), "Should have 1 message on new server")

	var event TaskCompletionEvent
	err = json.Unmarshal([]byte(msgs2[0]), &event)
	require.NoError(t, err)
	assert.Equal(t, "task-002", event.TaskID)
}

// ---- Test Message Ordering Guarantees ----

func TestPubSubIntegration_MessageOrderingGuarantees(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	log := logrus.New()
	ps, err := NewRedisPubSub(mr.Addr(), "", log)
	require.NoError(t, err)
	defer ps.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Subscribe
	ch := make(chan interface{}, 100)
	ps.Subscribe(ctx, ChannelTaskCompleted, ch)

	// Publish 20 messages in order
	numMessages := 20
	for i := 0; i < numMessages; i++ {
		taskID := fmt.Sprintf("task-%03d", i)
		err := ps.PublishTaskCompletion(ctx, taskID, "pod", "ns", "t-a", 0, time.Duration(i)*time.Millisecond)
		require.NoError(t, err)
	}

	// Receive and verify order
	received := make([]string, 0, numMessages)
	for i := 0; i < numMessages; i++ {
		select {
		case msg := <-ch:
			event := msg.(TaskCompletionEvent)
			received = append(received, event.TaskID)
		case <-ctx.Done():
			t.Fatalf("Timeout waiting for message %d", i)
		}
	}

	// Verify messages arrived in order
	for i, taskID := range received {
		expected := fmt.Sprintf("task-%03d", i)
		assert.Equal(t, expected, taskID, "Message %d should be in order", i)
	}
}

// ---- Test Multiple Subscribers ----

func TestPubSubIntegration_MultipleSubscribers(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	log := logrus.New()
	ps, err := NewRedisPubSub(mr.Addr(), "", log)
	require.NoError(t, err)
	defer ps.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Create 5 subscribers
	subscribers := make([]chan interface{}, 5)
	for i := 0; i < 5; i++ {
		ch := make(chan interface{}, 10)
		ps.Subscribe(ctx, ChannelTaskCompleted, ch)
		subscribers[i] = ch
	}

	// Publish single message
	err = ps.PublishTaskCompletion(ctx, "task-broadcast", "pod", "ns", "t-a", 0, time.Second)
	require.NoError(t, err)

	// All subscribers should receive the message
	for i, ch := range subscribers {
		select {
		case msg := <-ch:
			event := msg.(TaskCompletionEvent)
			assert.Equal(t, "task-broadcast", event.TaskID, "Subscriber %d should receive message", i)
		case <-ctx.Done():
			t.Fatalf("Timeout waiting for subscriber %d", i)
		}
	}
}

// ---- Test Unsubscribe ----

func TestPubSubIntegration_Unsubscribe(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	log := logrus.New()
	ps, err := NewRedisPubSub(mr.Addr(), "", log)
	require.NoError(t, err)
	defer ps.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch := make(chan interface{}, 10)
	ps.Subscribe(ctx, ChannelTaskCompleted, ch)

	// First message should be received
	err = ps.PublishTaskCompletion(ctx, "task-before", "pod", "ns", "t-a", 0, time.Second)
	require.NoError(t, err)

	select {
	case msg := <-ch:
		event := msg.(TaskCompletionEvent)
		assert.Equal(t, "task-before", event.TaskID)
	case <-ctx.Done():
		t.Fatal("Timeout waiting for first message")
	}

	// Unsubscribe
	ps.Unsubscribe(ctx, ChannelTaskCompleted, ch)

	// After unsubscribe, should NOT receive local messages
	err = ps.PublishTaskCompletion(ctx, "task-after", "pod", "ns", "t-a", 0, time.Second)
	require.NoError(t, err)

	select {
	case <-ch:
		t.Fatal("Should NOT receive message after unsubscribe")
	case <-time.After(500 * time.Millisecond):
		// Expected: no message
	}
}

// ---- Test Queue Depth Publish/Get Cycle ----

func TestPubSubIntegration_QueueDepthCycle(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	log := logrus.New()
	ps, err := NewRedisPubSub(mr.Addr(), "", log)
	require.NoError(t, err)
	defer ps.Close()

	ctx := context.Background()

	// Publish queue depth
	err = ps.PublishQueueDepth(ctx, "tenant-a", 100)
	require.NoError(t, err)

	// Get queue depth
	depth, err := ps.GetCurrentQueueDepth(ctx, "tenant-a")
	require.NoError(t, err)
	assert.Equal(t, int64(100), depth)

	// Update queue depth
	err = ps.PublishQueueDepth(ctx, "tenant-a", 250)
	require.NoError(t, err)

	depth, err = ps.GetCurrentQueueDepth(ctx, "tenant-a")
	require.NoError(t, err)
	assert.Equal(t, int64(250), depth)

	// Non-existent tenant should return 0
	depth, err = ps.GetCurrentQueueDepth(ctx, "tenant-unknown")
	require.NoError(t, err)
	assert.Equal(t, int64(0), depth)
}

// ---- Test High-Throughput Publishing ----

func TestPubSubIntegration_HighThroughput(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	ps, err := NewRedisPubSub(mr.Addr(), "", log)
	require.NoError(t, err)
	defer ps.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	ch := make(chan interface{}, 500)
	ps.Subscribe(ctx, ChannelTaskCompleted, ch)

	numMessages := 200
	for i := 0; i < numMessages; i++ {
		taskID := fmt.Sprintf("task-%04d", i)
		err := ps.PublishTaskCompletion(ctx, taskID, "pod", "ns", "t-a", 0, time.Millisecond)
		require.NoError(t, err)
	}

	// Drain and count
	receivedCount := 0
	for i := 0; i < numMessages; i++ {
		select {
		case <-ch:
			receivedCount++
		case <-ctx.Done():
			t.Fatalf("Timeout at message %d/%d", i, numMessages)
		}
	}

	assert.Equal(t, numMessages, receivedCount, "Should receive all high-throughput messages")
	assert.Equal(t, numMessages, len(mr.PublishedMessages(ChannelTaskCompleted)))
}

// ---- Test Close Behavior ----

func TestPubSubIntegration_CloseBehavior(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	log := logrus.New()
	ps, err := NewRedisPubSub(mr.Addr(), "", log)
	require.NoError(t, err)

	err = ps.Close()
	require.NoError(t, err)

	// Double close should not error
	err = ps.Close()
	require.NoError(t, err)

	// Operations after close should fail
	err = ps.PublishTaskCompletion(context.Background(), "task", "pod", "ns", "t", 0, time.Second)
	assert.Error(t, err, "Publish after close should fail")
}

// ---- Test Connection Failure ----

func TestPubSubIntegration_ConnectionFailure(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	_, err := NewRedisPubSub("localhost:59999", "", log)
	assert.Error(t, err, "Connection to invalid address should fail")
}
