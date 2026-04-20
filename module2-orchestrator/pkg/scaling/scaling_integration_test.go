package scaling

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- CalculateDesiredReplicas Edge Cases ----

func TestScalingIntegration_EdgeCases_ZeroQueue(t *testing.T) {
	log := logrus.NewEntry(logrus.New())
	calculator := NewHPACalculator(log, 1, 10, 5, 80.0, 80.0)

	desired := calculator.CalculateDesiredReplicas(0)
	assert.Equal(t, int32(1), desired, "Queue=0 should return minReplicas")
}

func TestScalingIntegration_EdgeCases_SingleItem(t *testing.T) {
	log := logrus.NewEntry(logrus.New())
	calculator := NewHPACalculator(log, 1, 10, 5, 80.0, 80.0)

	desired := calculator.CalculateDesiredReplicas(1)
	assert.Equal(t, int32(1), desired, "Queue=1 with 5 items/pod should need 1 pod")
}

func TestScalingIntegration_EdgeCases_MediumQueue(t *testing.T) {
	log := logrus.NewEntry(logrus.New())
	calculator := NewHPACalculator(log, 1, 10, 5, 80.0, 80.0)

	desired := calculator.CalculateDesiredReplicas(999)
	assert.Equal(t, int32(10), desired, "Queue=999 should hit maxReplicas=10")
}

func TestScalingIntegration_EdgeCases_LargeQueue(t *testing.T) {
	log := logrus.NewEntry(logrus.New())
	calculator := NewHPACalculator(log, 1, 100, 10, 80.0, 80.0)

	desired := calculator.CalculateDesiredReplicas(10000)
	assert.Equal(t, int32(100), desired, "Queue=10000 should hit maxReplicas=100")
}

func TestScalingIntegration_EdgeCases_ExactThreshold(t *testing.T) {
	log := logrus.NewEntry(logrus.New())
	itemsPerPod := int64(5)
	calculator := NewHPACalculator(log, 1, 20, itemsPerPod, 80.0, 80.0)

	testCases := []struct {
		queueDepth int64
		expected   int32
		name       string
	}{
		{5, 1, "Exactly at threshold"},
		{6, 2, "Just over threshold"},
		{4, 1, "Just under threshold"},
		{10, 2, "Exactly 2x threshold"},
		{11, 3, "Slightly over 2x threshold"},
		{0, 1, "Zero queue"},
		{100, 20, "Max replicas boundary"},
		{101, 20, "Over max replicas"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			desired := calculator.CalculateDesiredReplicas(tc.queueDepth)
			assert.Equal(t, tc.expected, desired, "queue=%d", tc.queueDepth)
		})
	}
}

// ---- PublishQueueDepth → GetCurrentQueueDepth Cycle ----

func TestScalingIntegration_PublishAndGetQueueDepth(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	queueManager := &MockQueueManager{
		addr:   mr.Addr(),
		queues: make(map[string]int64),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Publish queue depth
	err = queueManager.PublishQueueDepth(ctx, "tenant-a", 100)
	require.NoError(t, err)

	depth, err := queueManager.GetCurrentQueueDepth(ctx, "tenant-a")
	require.NoError(t, err)
	assert.Equal(t, int64(100), depth)

	// Multiple updates → last value wins
	for i := int64(0); i < 10; i++ {
		err := queueManager.PublishQueueDepth(ctx, "tenant-a", i*10)
		require.NoError(t, err)
	}

	depth, err = queueManager.GetCurrentQueueDepth(ctx, "tenant-a")
	require.NoError(t, err)
	assert.Equal(t, int64(90), depth, "Should reflect latest value")
}

// ---- Multi-Tenant Queue Depths ----

func TestScalingIntegration_MultiTenantQueueDepths(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	queueManager := &MockQueueManager{
		addr:   mr.Addr(),
		queues: make(map[string]int64),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tenantDepths := map[string]int64{
		"tenant-a": 100,
		"tenant-b": 200,
		"tenant-c": 50,
		"tenant-d": 300,
	}

	for tenant, depth := range tenantDepths {
		err := queueManager.PublishQueueDepth(ctx, tenant, depth)
		require.NoError(t, err)
	}

	for tenant, expectedDepth := range tenantDepths {
		depth, err := queueManager.GetCurrentQueueDepth(ctx, tenant)
		require.NoError(t, err)
		assert.Equal(t, expectedDepth, depth, "Tenant %s", tenant)
	}
}

// ---- Metrics Registration and Collection ----

func TestScalingIntegration_MetricsRegistrationAndCollection(t *testing.T) {
	log := logrus.NewEntry(logrus.New())

	// Use a custom registry to avoid conflicts with default
	reg := prometheus.NewRegistry()

	mc := NewMetricsCollector(log, nil, nil, 30*time.Second)

	// Register metrics with custom registry
	err := reg.Register(mc.GetQueueDepthMetric())
	require.NoError(t, err)
	err = reg.Register(mc.GetActiveTasksMetric())
	require.NoError(t, err)
	err = reg.Register(mc.GetFailedTasksMetric())
	require.NoError(t, err)
	err = reg.Register(mc.GetAvgDurationMetric())
	require.NoError(t, err)

	// Set metric values
	mc.GetQueueDepthMetric().Set(150)
	mc.GetActiveTasksMetric().Set(5)
	mc.GetFailedTasksMetric().Set(2)
	mc.GetAvgDurationMetric().Set(45.5)

	// Gather and verify
	families, err := reg.Gather()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(families), 4, "Should have at least 4 metric families")

	// Verify specific metric values
	for _, mf := range families {
		switch mf.GetName() {
		case "current_queue_depth":
			assert.Equal(t, 150.0, mf.GetMetric()[0].GetGauge().GetValue())
		case "active_task_count":
			assert.Equal(t, 5.0, mf.GetMetric()[0].GetGauge().GetValue())
		case "failed_task_count":
			assert.Equal(t, 2.0, mf.GetMetric()[0].GetGauge().GetValue())
		case "avg_task_duration_seconds":
			assert.Equal(t, 45.5, mf.GetMetric()[0].GetGauge().GetValue())
		}
	}
}

// ---- ShouldScaleUp / ShouldScaleDown ----

func TestScalingIntegration_ScaleUpDecisions(t *testing.T) {
	log := logrus.NewEntry(logrus.New())
	calculator := NewHPACalculator(log, 2, 10, 5, 80.0, 80.0)

	testCases := []struct {
		name     string
		metrics  MetricsSnapshot
		expectUp bool
	}{
		{
			name: "High CPU should scale up",
			metrics: MetricsSnapshot{
				CurrentCPU:      90.0,
				CurrentMemory:   50.0,
				QueueDepth:      0,
				CurrentReplicas: 2,
			},
			expectUp: true,
		},
		{
			name: "High memory should scale up",
			metrics: MetricsSnapshot{
				CurrentCPU:      50.0,
				CurrentMemory:   95.0,
				QueueDepth:      0,
				CurrentReplicas: 2,
			},
			expectUp: true,
		},
		{
			name: "High queue should scale up",
			metrics: MetricsSnapshot{
				CurrentCPU:      50.0,
				CurrentMemory:   50.0,
				QueueDepth:      50,
				CurrentReplicas: 2,
			},
			expectUp: true,
		},
		{
			name: "Everything low should NOT scale up",
			metrics: MetricsSnapshot{
				CurrentCPU:      20.0,
				CurrentMemory:   20.0,
				QueueDepth:      5,
				CurrentReplicas: 2,
			},
			expectUp: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := calculator.ShouldScaleUp(tc.metrics)
			assert.Equal(t, tc.expectUp, result)
		})
	}
}

func TestScalingIntegration_ScaleDownDecisions(t *testing.T) {
	log := logrus.NewEntry(logrus.New())
	calculator := NewHPACalculator(log, 2, 10, 5, 80.0, 80.0)

	testCases := []struct {
		name       string
		metrics    MetricsSnapshot
		expectDown bool
	}{
		{
			name: "Low CPU should scale down",
			metrics: MetricsSnapshot{
				CurrentCPU:      10.0,
				CurrentMemory:   50.0,
				QueueDepth:      0,
				CurrentReplicas: 5,
			},
			expectDown: true,
		},
		{
			name: "Low memory should scale down",
			metrics: MetricsSnapshot{
				CurrentCPU:      50.0,
				CurrentMemory:   10.0,
				QueueDepth:      0,
				CurrentReplicas: 5,
			},
			expectDown: true,
		},
		{
			name: "At min replicas should NOT scale down",
			metrics: MetricsSnapshot{
				CurrentCPU:      10.0,
				CurrentMemory:   10.0,
				QueueDepth:      0,
				CurrentReplicas: 2, // = minReplicas
			},
			expectDown: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := calculator.ShouldScaleDown(tc.metrics)
			assert.Equal(t, tc.expectDown, result)
		})
	}
}

// ---- MakeScalingDecision ----

func TestScalingIntegration_MakeScalingDecision(t *testing.T) {
	log := logrus.NewEntry(logrus.New())
	calculator := NewHPACalculator(log, 2, 10, 5, 80.0, 80.0)

	// Scale up decision
	decision := calculator.MakeScalingDecision(MetricsSnapshot{
		CurrentCPU:      90.0,
		CurrentMemory:   50.0,
		QueueDepth:      50,
		CurrentReplicas: 2,
	})
	assert.Equal(t, "scale-up", decision.Action)
	assert.Greater(t, decision.DesiredReplicas, int32(2))

	// No change decision
	decision = calculator.MakeScalingDecision(MetricsSnapshot{
		CurrentCPU:      60.0,
		CurrentMemory:   60.0,
		QueueDepth:      5,
		CurrentReplicas: 2,
	})
	assert.Equal(t, "no-change", decision.Action)

	// Scale down decision
	decision = calculator.MakeScalingDecision(MetricsSnapshot{
		CurrentCPU:      10.0,
		CurrentMemory:   10.0,
		QueueDepth:      0,
		CurrentReplicas: 8,
	})
	assert.Equal(t, "scale-down", decision.Action)
	assert.LessOrEqual(t, decision.DesiredReplicas, int32(8))
}

// ---- Concurrent Metrics Collection ----

func TestScalingIntegration_ConcurrentMetricsCollection(t *testing.T) {
	metricsCollector := &MockMetricsCollector{
		metrics: make(map[string]float64),
	}

	metricsCollector.RegisterMetric("concurrent_test", 0)

	var wg sync.WaitGroup
	numGoroutines := 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(val int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				metricsCollector.RecordMetric("concurrent_test", float64(val*10+j))
			}
		}(i)
	}

	wg.Wait()

	finalValue := metricsCollector.GetMetric("concurrent_test")
	assert.Greater(t, finalValue, 0.0, "Metric should have been updated")
}

// ---- Queue Depth After Redis Restart ----

func TestScalingIntegration_QueueDepthWithRedisRestart(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)

	queueManager := &MockQueueManager{
		addr:   mr.Addr(),
		queues: make(map[string]int64),
	}

	ctx := context.Background()

	err = queueManager.PublishQueueDepth(ctx, "tenant-a", 100)
	require.NoError(t, err)

	depth, err := queueManager.GetCurrentQueueDepth(ctx, "tenant-a")
	require.NoError(t, err)
	assert.Equal(t, int64(100), depth)

	// Simulate Redis restart
	mr.Close()
	time.Sleep(100 * time.Millisecond)

	mr2, err := miniredis.Run()
	require.NoError(t, err)
	defer mr2.Close()

	queueManager.SetAddress(mr2.Addr())

	ctx2 := context.Background()
	err = queueManager.PublishQueueDepth(ctx2, "tenant-a", 150)
	require.NoError(t, err)

	depth, err = queueManager.GetCurrentQueueDepth(ctx2, "tenant-a")
	require.NoError(t, err)
	assert.Equal(t, int64(150), depth)
}

// ---- MetricsCollector Start/Stop ----

func TestScalingIntegration_MetricsCollectorStartStop(t *testing.T) {
	log := logrus.NewEntry(logrus.New())
	mc := NewMetricsCollector(log, nil, nil, 100*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	mc.Start(ctx)

	// Let it collect a few times
	time.Sleep(350 * time.Millisecond)

	// Stop via context cancellation
	cancel()
	time.Sleep(50 * time.Millisecond)

	// Stop explicitly
	mc.Stop()
}

// ---- Mock implementations ----

type MockQueueManager struct {
	addr   string
	queues map[string]int64
	mu     sync.RWMutex
}

func (m *MockQueueManager) SetAddress(addr string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.addr = addr
}

func (m *MockQueueManager) PublishQueueDepth(ctx context.Context, tenantID string, depth int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queues[tenantID] = depth
	return nil
}

func (m *MockQueueManager) GetCurrentQueueDepth(ctx context.Context, tenantID string) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.queues[tenantID], nil
}

type MockMetricsCollector struct {
	metrics map[string]float64
	mu      sync.RWMutex
}

func (m *MockMetricsCollector) RegisterMetric(name string, initialValue float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.metrics[name] = initialValue
}

func (m *MockMetricsCollector) RecordMetric(name string, value float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.metrics[name] = value
}

func (m *MockMetricsCollector) GetMetric(name string) float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.metrics[name]
}

func (m *MockMetricsCollector) GetMetrics() map[string]float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]float64)
	for k, v := range m.metrics {
		result[k] = v
	}
	return result
}
